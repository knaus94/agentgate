package dev

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
)

// A large specification is not written in one breath. The plan splits it into stages: each stage is one
// logical outcome that leaves the declared checks green, and it goes through the whole gate — write, check,
// review, correct, accept — before the next one starts. A reviewer of a stage reads that stage's change, not
// everything before it; a last pass over the whole change looks at what only the whole can show.
//
// A stage is run by the ordinary gate: the gate holds the whole plan (Epic) and, as its Plan, a view of the
// current stage that looks like any single-stage plan.

// Requirement is one thing the specification asks for. Every requirement belongs to a stage, so nothing is
// dropped on the way from the specification to the work.
type Requirement struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Stage is one part of a staged plan.
type Stage struct {
	Name       string      `json:"name"`
	Goal       string      `json:"goal"`                 // the one outcome of this stage
	Acceptance []string    `json:"acceptance,omitempty"` // how one tells the stage is done
	Covers     []string    `json:"covers,omitempty"`     // requirement ids this stage delivers
	Streams    []Stream    `json:"streams"`
	Checks     []Check     `json:"checks,omitempty"`     // in addition to the plan's checks
	Assessment *Assessment `json:"assessment,omitempty"` // this stage's own facts; default: the plan's
}

// StageRecord is what is kept of an accepted stage.
type StageRecord struct {
	Name       string            `json:"name"`
	Mode       string            `json:"mode"`
	Summary    string            `json:"summary"`
	AcceptedAt int64             `json:"accepted_at"`
	Rounds     int               `json:"correction_rounds"`
	Findings   int               `json:"findings"`
	Commit     string            `json:"commit,omitempty"`
	Handoffs   map[string]string `json:"handoffs,omitempty"` // stream -> the contract later stages build on
	Archive    string            `json:"archive"`            // the stage's reviews, findings and receipts
}

var modeRank = map[string]int{"light": 0, "medium": 1, "heavy": 2, "ultra": 3}

func (p *Plan) staged() bool { return len(p.Stages) > 0 }

func (p *Plan) allStreams() []Stream {
	if !p.staged() {
		return p.Streams
	}
	var out []Stream
	for _, stage := range p.Stages {
		out = append(out, stage.Streams...)
	}
	return out
}

// stageView is the plan of one stage as the gate runs it: the stage's streams and checks under a brief that
// places the stage in the whole — what came before and was accepted, what this stage delivers, what is left
// to later stages and therefore not to be done now.
func (p *Plan) stageView(index int, records []StageRecord) *Plan {
	stage := p.Stages[index]
	var brief strings.Builder
	brief.WriteString(strings.TrimSpace(p.Brief))
	fmt.Fprintf(&brief, "\n\n## This stage: %s (%d of %d)\n\n%s\n", stage.Name, index+1, len(p.Stages), strings.TrimSpace(stage.Goal))
	if len(stage.Acceptance) > 0 {
		brief.WriteString("\nAccepted when:\n")
		for _, item := range stage.Acceptance {
			brief.WriteString("- " + item + "\n")
		}
	}
	if len(stage.Covers) > 0 {
		brief.WriteString("\nRequirements this stage delivers:\n")
		for _, requirement := range p.Requirements {
			if slices.Contains(stage.Covers, requirement.ID) {
				fmt.Fprintf(&brief, "- %s: %s\n", requirement.ID, requirement.Text)
			}
		}
	}
	if len(records) > 0 {
		brief.WriteString("\n## Earlier stages, accepted — build on them, do not redo them\n")
		for _, record := range records {
			fmt.Fprintf(&brief, "- %s: %s\n", record.Name, oneLine(record.Summary))
			for stream, handoff := range record.Handoffs {
				fmt.Fprintf(&brief, "  - contract of %s: %s\n", stream, oneLine(handoff))
			}
		}
	}
	if index+1 < len(p.Stages) {
		brief.WriteString("\n## Later stages — not part of this one\n")
		for _, later := range p.Stages[index+1:] {
			fmt.Fprintf(&brief, "- %s: %s\n", later.Name, oneLine(later.Goal))
		}
	}
	view := *p
	view.Stages, view.View = nil, true
	view.Brief, view.Streams = brief.String(), slices.Clone(stage.Streams)
	view.Checks = mergeChecks(p.Checks, stage.Checks)
	if stage.Assessment != nil {
		view.Assessment = stage.Assessment
	}
	return &view
}

// integrationView is the whole change as one plan, for the pass that follows the last stage. A file belongs
// to the last stage that owned it.
func (p *Plan) integrationView(records []StageRecord) *Plan {
	owner := map[string]int{}
	var streams []Stream
	checks := slices.Clone(p.Checks)
	for index := len(p.Stages) - 1; index >= 0; index-- {
		stage := p.Stages[index]
		checks = mergeChecks(checks, stage.Checks)
		for _, stream := range stage.Streams {
			kept := Stream{Name: stage.Name + "." + stream.Name, Domain: stream.Domain} // the name becomes part of a path
			for _, file := range stream.Files {
				if _, taken := owner[file]; !taken {
					owner[file] = index
					kept.Files = append(kept.Files, file)
				}
			}
			if len(kept.Files) > 0 {
				streams = append(streams, kept)
			}
		}
	}
	slices.Reverse(streams)
	var brief strings.Builder
	brief.WriteString(strings.TrimSpace(p.Brief))
	brief.WriteString("\n\n## Integration pass\n\nThe work was written and reviewed stage by stage. This pass reads the whole change for what a " +
		"review of one stage cannot see: seams between stages, a contract that drifted from one stage to the next, leftovers of an earlier " +
		"stage, a requirement that no stage met end to end.\n\nStages:\n")
	for _, record := range records {
		fmt.Fprintf(&brief, "- %s: %s\n", record.Name, oneLine(record.Summary))
	}
	if len(p.Requirements) > 0 {
		brief.WriteString("\nRequirements of the whole task:\n")
		for _, requirement := range p.Requirements {
			fmt.Fprintf(&brief, "- %s: %s\n", requirement.ID, requirement.Text)
		}
	}
	view := *p
	view.Stages, view.View = nil, true
	view.Brief, view.Streams, view.Checks = brief.String(), streams, checks
	return &view
}

func mergeChecks(base, extra []Check) []Check {
	out := slices.Clone(base)
	for _, check := range extra {
		if !slices.ContainsFunc(out, func(other Check) bool { return slices.Equal(other.Argv, check.Argv) }) {
			out = append(out, check)
		}
	}
	return out
}

// resolveStaged validates a staged plan: every stage is a sound plan of its own, and every requirement of
// the specification is delivered by some stage. The mode of the whole is the most thorough mode of a stage.
func resolveStaged(cfg *config.Config, plan *Plan, request Request) (*Resolved, error) {
	if request.Single {
		return nil, errors.New("--single asks for one pass, and the plan has stages")
	}
	if !request.Writes() || request.ReviewFirst() {
		return nil, fmt.Errorf("stages are for work that writes code; %s reads what is already there", request.Action)
	}
	if len(plan.Streams) > 0 {
		return nil, errors.New("a staged plan keeps its streams inside the stages")
	}
	if len(plan.Stages) < 2 {
		return nil, errors.New("one stage is not a split: give at least two, or drop stages")
	}
	if len(plan.Requirements) == 0 {
		return nil, errors.New("a staged plan lists requirements [{id, text}] and says which stage covers each, so nothing in the specification is lost")
	}
	known, covered, names := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, requirement := range plan.Requirements {
		if strings.TrimSpace(requirement.ID) == "" || strings.TrimSpace(requirement.Text) == "" || known[requirement.ID] {
			return nil, fmt.Errorf("requirements need a unique id and a text: %q", requirement.ID)
		}
		known[requirement.ID] = true
	}
	resolved := &Resolved{Plan: plan, Request: request, Config: cfg}
	for index, stage := range plan.Stages {
		if !planName.MatchString(stage.Name) || names[stage.Name] {
			return nil, fmt.Errorf("stage names are unique, lowercase letters, digits and dashes: %q", stage.Name)
		}
		names[stage.Name] = true
		if strings.TrimSpace(stage.Goal) == "" {
			return nil, fmt.Errorf("stage %q needs a goal: the one outcome it delivers", stage.Name)
		}
		for _, id := range stage.Covers {
			if !known[id] {
				return nil, fmt.Errorf("stage %q covers an unknown requirement %q", stage.Name, id)
			}
			covered[id] = true
		}
		view, err := Resolve(cfg, plan.stageView(index, nil))
		if err != nil {
			return nil, fmt.Errorf("stage %q: %w", stage.Name, err)
		}
		resolved.Stages = append(resolved.Stages, view)
		if resolved.Mode == "" || modeRank[view.Mode] > modeRank[resolved.Mode] {
			resolved.Mode = view.Mode
		}
	}
	var missing []string
	for _, requirement := range plan.Requirements {
		if !covered[requirement.ID] {
			missing = append(missing, requirement.ID)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("no stage covers these requirements: %s", strings.Join(missing, ", "))
	}
	resolved.Decision = Decision{resolved.Mode, "stages", []string{fmt.Sprintf("%d stages; the most thorough of them is %s", len(plan.Stages), resolved.Mode)}}
	return resolved, resolved.validateUI()
}

// integrationNeeded says whether the whole change gets its own pass after the last stage: always in the
// thorough modes, and whenever there are enough stages for seams to matter.
func integrationNeeded(mode string, stages int) bool {
	return modeRank[mode] >= modeRank["heavy"] || stages >= 3
}

// isolateStaged says whether a staged task works in its own worktree without being asked. It does when the
// repository told how to prepare one: a bare worktree without dependencies would only fail the checks.
func isolateStaged(cfg *config.Config) bool {
	switch cfg.Staging.Isolate {
	case "always":
		return true
	case "never":
		return false
	}
	return len(cfg.Worktree.Setup) > 0 || len(cfg.Worktree.Copy) > 0
}

func (g *Gate) stage() *Stage {
	if g.Epic == nil || g.Integration || g.StageIndex >= len(g.Epic.Stages) {
		return nil
	}
	return &g.Epic.Stages[g.StageIndex]
}

// enterStage makes a stage (or the integration pass) the gate's present work. Whatever the stage's files hold
// now is its starting point, so its reviewers see the stage's change and nothing earlier.
func (g *Gate) enterStage(resolved *Resolved, baseline Snapshot) error {
	plan := resolved.Plan
	files := plan.Files()
	if g.Worktree == "" {
		unlock, err := scopeLock()
		if err != nil {
			return err
		}
		clash := owners(files)
		unlock()
		if len(clash) > 0 {
			return fmt.Errorf("files are owned by another session's gate: %s", strings.Join(clash, "; "))
		}
	}
	context := subtract(append(slices.Clone(plan.Context), guidance(files)...), files)
	now, err := capture(append(slices.Clone(files), context...))
	if err != nil {
		return err
	}
	for name, state := range now {
		if _, known := g.EpicBaseline[name]; !known {
			g.EpicBaseline[name] = state // a file a later stage brought in: this is how the task found it
		}
		if baseline != nil {
			if original, known := baseline[name]; known {
				now[name] = original
			}
		}
	}
	head, _ := git("rev-parse", "HEAD")
	g.Plan, g.Mode, g.PlanVersion = plan, resolved.Mode, 1
	g.Files, g.Context, g.WriteFiles, g.Baseline = files, context, files, now
	g.HeadBefore = strings.TrimSpace(head)
	if g.Manifest, err = manifest(); err != nil {
		return err
	}
	g.Writers, g.Reviews, g.ReviewDigest, g.ReviewError = nil, nil, "", ""
	g.VisualRuns, g.VisualSkip = nil, ""
	g.Dispositions, g.Resolutions, g.Baselines = map[string]Disposition{}, map[string]string{}, map[string]int{}
	g.Checks, g.Corrections, g.Round, g.Iteration, g.Boost = nil, nil, nil, 0, 0
	g.Signals, g.Conflicts, g.Foreign = nil, nil, nil
	g.ContextPack, g.ScoutNote, g.Final, g.Reported = "", "", nil, false
	return nil
}

// acceptStage closes the present stage and opens what follows: the next stage, the pass over the whole
// change, or the end of the task.
func (g *Gate) acceptStage(cfg *config.Config, input FinishInput) (any, error) {
	stage := g.stage()
	record := StageRecord{Name: stage.Name, Mode: g.Mode, Summary: input.Summary, AcceptedAt: time.Now().Unix(),
		Rounds: len(g.Corrections), Findings: len(g.Findings()), Handoffs: map[string]string{}}
	for name, writer := range g.latest() {
		if writer.Handoff != "" {
			record.Handoffs[name] = writer.Handoff
		}
	}
	record.Archive = filepath.Join(g.dir(), "stages", fmt.Sprintf("%d-%s.json", g.StageIndex+1, stage.Name))
	if err := os.MkdirAll(filepath.Dir(record.Archive), 0o700); err != nil {
		return nil, err
	}
	if err := ops.WriteJSON(record.Archive, map[string]any{"stage": stage, "mode": g.Mode, "final": input, "writers": g.Writers, "reviews": g.Reviews,
		"dispositions": g.Dispositions, "resolutions": g.Resolutions, "checks": g.Checks, "corrections": g.Corrections,
		"slop_signals": g.Signals, "foreign_changes": g.Foreign}); err != nil {
		return nil, err
	}
	if g.Worktree != "" {
		if err := g.commitTask("dev: " + stage.Name + ": " + firstLine(input.Summary)); err != nil {
			return nil, err
		}
		commit, _ := git("rev-parse", "--short", "HEAD")
		record.Commit = strings.TrimSpace(commit)
	}
	g.StageRecords = append(g.StageRecords, record)
	whole, err := Resolve(cfg, g.Epic)
	if err != nil {
		return nil, err
	}
	switch {
	case g.StageIndex+1 < len(g.Epic.Stages):
		g.StageIndex++
		next, err := Resolve(cfg, g.Epic.stageView(g.StageIndex, g.StageRecords))
		if err != nil {
			return nil, err
		}

		if err := g.enterStage(next, nil); err != nil {
			return nil, err
		}
		g.Phase = "staged"
		if g.Request.Step {
			g.holdNow("stage " + record.Name + " is accepted; the user asked to confirm each stage before the next one starts")
		}
	case integrationNeeded(whole.Mode, len(g.Epic.Stages)):
		whole.Plan = g.Epic.integrationView(g.StageRecords)
		g.Integration = true
		if err := g.enterStage(whole, g.EpicBaseline); err != nil {
			return nil, err
		}
		g.Phase = "awaiting-review"
	default:
		g.Final, g.Phase = &Final{FinishInput: input, At: time.Now().Unix()}, "complete"
	}
	return g.Summary(cfg), g.save()
}

// stagesSummary is the progress of a staged task, for the summary and the panel.
func (g *Gate) stagesSummary() []map[string]any {
	var out []map[string]any
	for index, stage := range g.Epic.Stages {
		item := map[string]any{"name": stage.Name, "goal": oneLine(stage.Goal), "status": "pending"}
		switch {
		case index < len(g.StageRecords):
			record := g.StageRecords[index]
			item["status"], item["mode"], item["summary"], item["commit"] = "accepted", record.Mode, oneLine(record.Summary), record.Commit
			item["correction_rounds"], item["findings"] = record.Rounds, record.Findings
		case index == g.StageIndex && !g.Integration:
			item["status"], item["mode"] = "current", g.Mode
		}
		out = append(out, item)
	}
	return out
}

// present resolves what the gate is working on now: the present stage of a staged task, with what the
// accepted stages left behind, or the plan itself.
func (g *Gate) present(cfg *config.Config, resolved *Resolved) (*Resolved, error) {
	if g.Epic == nil {
		return resolved, nil
	}
	return Resolve(cfg, g.Epic.stageView(g.StageIndex, g.StageRecords))
}

// replan takes a revised plan for a staged task. What was accepted stays as it was; the stages still ahead
// may change, with a reason.
func (g *Gate) replan(plan *Plan) error {
	if len(plan.Stages) < len(g.StageRecords)+1 {
		return errors.New("the revised plan has fewer stages than are already accepted")
	}
	changed := len(plan.Stages) != len(g.Epic.Stages)
	for index := range plan.Stages {
		if index >= len(g.Epic.Stages) {
			break
		}
		same := digest(plan.Stages[index]) == digest(g.Epic.Stages[index])
		if !same && index < len(g.StageRecords) {
			return fmt.Errorf("stage %q is accepted and cannot change; revise the stages after it", g.Epic.Stages[index].Name)
		}
		changed = changed || !same
	}
	if changed && strings.TrimSpace(plan.Update) == "" {
		return errors.New("the remaining stages changed: say what and why in update")
	}
	g.Epic = plan
	return nil
}
