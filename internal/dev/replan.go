package dev

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/knaus94/agentgate/internal/config"
)

// Replan replaces the contract at a stable boundary. It preserves source snapshots, findings and author
// sessions; it never writes code or captures a new before-image of an implementation already in progress.
func Replan(cfg *config.Config, gate *Gate, plan *Plan) (any, error) {
	if !slices.Contains([]string{"implementing", "correcting", "awaiting-review", "reviewed"}, gate.Phase) {
		return nil, errors.New("replan needs an idle active task; explicitly resume a held task first")
	}
	if (gate.Epic != nil) != plan.staged() {
		return nil, errors.New("replan preserves the task's staged or single-pass shape")
	}
	for _, writer := range gate.latest() {
		if writer.Status == "RUNNING" {
			return nil, errors.New("cannot replan while a writer is running")
		}
	}
	resolved, err := Resolve(cfg, plan)
	if err != nil {
		return nil, err
	}
	if resolved.Request.Action != gate.Request.Action || resolved.Request.Fix != gate.Request.Fix {
		return nil, errors.New("replan preserves the task's action and write authority")
	}
	if err := resolved.requireArchitecture(); err != nil {
		return nil, err
	}
	if _, err := loadReviewRules(cfg, plan.Files()); err != nil {
		return nil, err
	}
	// Stage changes and their invalidation are persisted together by extend. A refusal must not leave
	// either the in-memory gate or its saved Epic halfway through the amendment.
	updated := *gate
	updated.Baseline = maps.Clone(gate.Baseline)
	updated.EpicBaseline = maps.Clone(gate.EpicBaseline)
	if gate.Epic != nil {
		if updated.EpicBaseline == nil {
			return nil, errors.New("staged amendment needs the original task baseline")
		}
		if updated.EpicHead == "" && gate.StageIndex == 0 && len(gate.StageRecords) == 0 && !gate.Integration {
			updated.EpicHead, updated.EpicManifest = gate.HeadBefore, maps.Clone(gate.Manifest)
		}
		if gate.Integration {
			return nil, errors.New("all stages are accepted; the integration pass cannot amend their plan")
		}
		for _, requirement := range gate.Epic.Requirements {
			if !slices.Contains(plan.Requirements, requirement) {
				return nil, fmt.Errorf("stage amendment must retain requirement %q unchanged; move its coverage rather than remove or weaken it", requirement.ID)
			}
		}
		if err := updated.replan(plan); err != nil {
			return nil, err
		}
		if gate.StageIndex >= len(plan.Stages) || plan.Stages[gate.StageIndex].Name != gate.Epic.Stages[gate.StageIndex].Name {
			return nil, errors.New("stage amendment keeps the current stage's identity")
		}
		resolved, err = Resolve(cfg, plan.stageView(gate.StageIndex, gate.StageRecords))
		if err != nil {
			return nil, err
		}
		if len(resolved.Plan.Streams) != len(gate.Plan.Streams) {
			return nil, errors.New("stage amendment keeps its existing writer streams")
		}
		for _, old := range gate.Plan.Streams {
			index := slices.IndexFunc(resolved.Plan.Streams, func(s Stream) bool { return s.Name == old.Name && s.Domain == old.Domain })
			if index < 0 || len(subtract(old.Files, resolved.Plan.Streams[index].Files)) > 0 {
				return nil, fmt.Errorf("stage amendment keeps stream %q and its owned files", old.Name)
			}
		}
		for _, file := range subtract(resolved.Plan.Files(), gate.Files) {
			before, original, err := updated.amendmentBaselines(file)
			if err != nil {
				return nil, err
			}
			updated.Baseline[file] = before
			if _, known := updated.EpicBaseline[file]; !known {
				updated.EpicBaseline[file] = original
			}
		}
	}
	unfinished := false
	blocked := false
	if gate.Round != nil {
		for _, stream := range gate.Round.Streams {
			writer := gate.latest()[stream.Name]
			unfinished = unfinished || writer == nil || writer.Batch != gate.Round.Batch || writer.Status != "COMPLETED"
			blocked = blocked || writer != nil && writer.Batch == gate.Round.Batch && writer.Status == "BLOCKED"
		}
	}
	if unfinished && digest(resolved.Plan.Streams) != digest(gate.Plan.Streams) {
		return nil, errors.New("cannot change writer streams inside an unfinished round; finish it or launch an explicitly updated implementation plan")
	}
	if err := updated.auditScope(); err != nil {
		return nil, err
	}
	if resolved.ModeConfig().Scout != "" {
		rules, err := loadReviewRules(cfg, resolved.Plan.Files())
		if err != nil {
			return nil, err
		}
		path, err := scoutPath(resolved, rules)
		if err != nil {
			return nil, err
		}
		if fileExists(path) {
			updated.ContextPack, updated.ScoutNote = path, ""
		}
	}
	updated.ReviewDigest, updated.ReviewError = "", ""
	if !unfinished && (len(gate.Writers) > 0 || gate.Phase != "implementing") {
		updated.Phase = "awaiting-review"
	}
	if unfinished {
		updated.Phase = "implementing"
		if gate.Round.Correction {
			updated.Phase = "correcting"
		}
	}
	if blocked {
		updated.ResumePhase, updated.Phase = updated.Phase, "held"
		updated.HoldReason = "The plan was amended; answer the blocked writers to continue their existing sessions."
	}
	if err := updated.extend(resolved); err != nil {
		return nil, err
	}
	*gate = updated
	return gate.Summary(cfg), nil
}

// An output already produced during the stage must remain a change, not become its own before-image.
func (g *Gate) amendmentBaselines(file string) (*FileState, *FileState, error) {
	original, known := g.EpicBaseline[file]
	var err error
	if !known {
		if before, exists := g.Baseline[file]; exists && len(g.StageRecords) == 0 {
			original = before
		} else {
			original, err = recoverBefore(file, g.EpicHead, g.EpicManifest)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	if before, known := g.Baseline[file]; known {
		return before, original, nil
	}
	if len(g.StageRecords) == 0 {
		return original, original, nil
	}
	before, err := recoverBefore(file, g.HeadBefore, g.Manifest)
	return before, original, err
}

func recoverBefore(file, head string, manifest map[string]string) (*FileState, error) {
	if head == "" {
		return nil, fmt.Errorf("cannot recover the original baseline of %s without its recorded source origin", file)
	}
	if original, dirty := manifest[file]; dirty {
		snapshot, err := capture([]string{file})
		if err != nil {
			return nil, err
		}
		now := "deleted"
		if state := snapshot[file]; state != nil {
			now = fmt.Sprintf("%x:%o", sha256.Sum256([]byte(state.text())), state.Mode)
		}
		if now != original {
			return nil, fmt.Errorf("cannot recover original uncommitted bytes of %s; current output cannot become its baseline", file)
		}
		return snapshot[file], nil
	}
	tree, err := git("ls-tree", "-z", head, "--", file)
	if err != nil {
		return nil, fmt.Errorf("read original tree for %s: %w", file, err)
	}
	if tree == "" {
		return nil, nil
	}
	header, _, _ := strings.Cut(tree, "\t")
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
		return nil, fmt.Errorf("original %s was not a regular file", file)
	}
	raw, err := git("cat-file", "blob", fields[2])
	if err != nil {
		return nil, fmt.Errorf("read original bytes for %s: %w", file, err)
	}
	mode := uint32(0o644)
	if fields[0] == "100755" {
		mode = 0o755
	}
	return &FileState{Bytes: base64.StdEncoding.EncodeToString([]byte(raw)), Mode: mode}, nil
}
