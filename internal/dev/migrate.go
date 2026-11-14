package dev

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// A task saved by a version before 0.16 holds a phase, an optional second plan for the stage and base64
// snapshots of its files. Reading one translates it in memory: every viewer — the panel, the ownership check
// of another session, the Stop hook — can read it without touching anything. Its origin trees are built once,
// by the first operation of its own session, inside its own checkout (see upgrade).

// legacyFile is the content and mode of one file as older versions recorded it; nil means the file was absent.
type legacyFile struct {
	Bytes string `json:"bytes"`
	Mode  uint32 `json:"mode"`
}

func (f *legacyFile) content() []byte {
	raw, _ := base64.StdEncoding.DecodeString(f.Bytes)
	return raw
}

// legacyState is what a translated task still needs to have its origin trees built: the before-images of the
// files its present stage owns, and of the files of the whole task.
type legacyState struct {
	stage, task map[string]*legacyFile
}

type legacyGate struct {
	ID            string                 `json:"id"`
	Session       string                 `json:"session"`
	Cwd           string                 `json:"cwd"`
	Request       Request                `json:"request"`
	Mode          string                 `json:"mode"`
	Plan          *Plan                  `json:"plan"`
	Baseline      map[string]*legacyFile `json:"baseline"`
	HeadBefore    string                 `json:"head_before"`
	Phase         string                 `json:"phase"`
	ResumePhase   string                 `json:"resume_phase"`
	HoldReason    string                 `json:"hold_reason"`
	Iteration     int                    `json:"iteration"`
	Writers       []*Writer              `json:"writers"`
	AuthorModels  []string               `json:"author_models"`
	Reviews       []Review               `json:"reviews"`
	VisualRuns    []VisualRun            `json:"visual_runs"`
	VisualSkip    string                 `json:"visual_skip"`
	Dispositions  map[string]Disposition `json:"dispositions"`
	Resolutions   map[string]string      `json:"resolutions"`
	Checks        []CheckReceipt         `json:"checks"`
	Baselines     map[string]int         `json:"baselines"`
	Corrections   []Correction           `json:"corrections"`
	Round         *Round                 `json:"round"`
	ContextPack   string                 `json:"context_pack"`
	Boost         int                    `json:"boost"`
	Substitutions json.RawMessage        `json:"substitutions"`
	Final         *Final                 `json:"final"`
	Worktree      string                 `json:"worktree"`
	Branch        string                 `json:"branch"`
	Landed        string                 `json:"landed"`
	Conflicts     []string               `json:"conflicts"`
	Epic          *Plan                  `json:"epic"`
	StageIndex    int                    `json:"stage_index"`
	StageRecords  []StageRecord          `json:"stage_records"`
	EpicBaseline  map[string]*legacyFile `json:"epic_baseline"`
	EpicHead      string                 `json:"epic_head_before"`
	Integration   bool                   `json:"integration"`
	Signals       []Signal               `json:"slop_signals"`
	Reported      bool                   `json:"completion_reported"`
	CreatedAt     int64                  `json:"created_at"`
}

// migrateState translates a task of an earlier format. It reads nothing but the bytes it is given.
func migrateState(raw []byte) (*Gate, error) {
	old := legacyGate{}
	if err := json.Unmarshal(raw, &old); err != nil {
		return nil, err
	}
	if old.Plan == nil || old.Phase == "" {
		return nil, errors.New("missing plan or phase")
	}
	whole := old.Plan
	if old.Epic != nil {
		whole = old.Epic
	}
	plan := whole.normalized()
	if len(plan.Stages) == 0 {
		return nil, errors.New("the plan has no streams")
	}
	// Earlier versions took free text among the context files. It never was a path: it stays as a note.
	var notes []string
	plan.Context = slices.DeleteFunc(plan.Context, func(entry string) bool {
		if strings.TrimSpace(entry) != "" && !filepath.IsAbs(entry) && !strings.ContainsAny(entry, ":\x00\n") && !strings.Contains(entry, " — ") && !globMeta.MatchString(entry) {
			return false
		}
		notes = append(notes, oneLine(entry))
		return true
	})
	if len(notes) > 0 {
		plan.Brief = strings.TrimSpace(plan.Brief) + "\n\nNotes carried over from the plan's context:\n- " + strings.Join(notes, "\n- ")
	}
	gate := &Gate{Version: stateVersion, ID: old.ID, Session: old.Session, Cwd: old.Cwd, Request: old.Request, Plan: plan, Mode: old.Mode,
		Accepted: old.StageRecords, Round: old.Round, Iteration: old.Iteration, Writers: old.Writers, AuthorModels: old.AuthorModels,
		Reviews: old.Reviews, VisualRuns: old.VisualRuns, VisualSkip: old.VisualSkip, Dispositions: old.Dispositions, Resolutions: old.Resolutions,
		Checks: old.Checks, Baselines: old.Baselines, Corrections: old.Corrections, ContextPack: old.ContextPack, Boost: old.Boost,
		Worktree: old.Worktree, Branch: old.Branch, Landed: old.Landed, Conflicts: old.Conflicts, Signals: old.Signals, Reported: old.Reported,
		CreatedAt: old.CreatedAt}
	_ = json.Unmarshal(old.Substitutions, &gate.Substitutions)
	gate.Revisions = []Revision{{Number: 1, At: old.CreatedAt, Digest: digest(plan), Reason: "migrated from an earlier version of the tool"}}
	if old.Epic != nil {
		gate.Stage = old.StageIndex
	}
	if old.Integration {
		gate.Stage, gate.Whole = len(plan.Stages), true
	}
	if gate.Stage < 0 || gate.Stage > len(plan.Stages) || (gate.Stage == len(plan.Stages) && !gate.Whole) {
		return nil, errors.New("invalid stage position")
	}
	// Earlier versions decided the mode of a stage when it opened. Accepted stages keep theirs, the present
	// position keeps its own, and a stage that has not opened is decided by the first run (see Run).
	gate.Modes = make([]string, len(plan.Stages))
	for index := range gate.Modes {
		switch {
		case index < len(old.StageRecords) && old.StageRecords[index].Mode != "":
			gate.Modes[index] = old.StageRecords[index].Mode
		case index <= gate.Stage || len(plan.Stages) == 1:
			gate.Modes[index] = old.Mode
		}
	}
	phase := old.Phase
	if phase == "held" {
		phase = old.ResumePhase
	}
	switch phase {
	case "implementing", "correcting", "staged":
		gate.Step = StepWrite
	case "awaiting-review", "reviewing", "reviewed":
		gate.Step = StepVerify
	case "complete":
		gate.Step = StepDone
	case "cancelled":
		gate.Step = StepCancelled
	default:
		return nil, fmt.Errorf("unknown phase %q", old.Phase)
	}
	if gate.Round != nil {
		gate.Rounds = old.Iteration + 1
	}
	if gate.Step == StepWrite && gate.Round == nil && len(gate.Writers) > 0 && phase != "staged" {
		gate.Step = StepVerify // a task that only reviews existing work never had a round
	}
	if old.Phase == "held" && gate.active() {
		// A hold used to stand for five different things. Only waiting for a person is still a pause; a blocked
		// or failed writer and an interrupted operation are read from the streams themselves.
		reason := old.HoldReason
		switch {
		case strings.Contains(reason, "the user stopped"):
			gate.Pause = &Paused{Kind: PauseUser, Reason: reason}
		case strings.Contains(reason, " is blocked: "), strings.Contains(reason, " failed: "), strings.Contains(reason, " cancelled: "),
			strings.Contains(reason, "was interrupted"), strings.Contains(reason, "answer the blocked writers"):
		case strings.Contains(reason, "the user asked to confirm each stage"):
			gate.Pause = &Paused{Kind: PauseStep, Reason: reason}
		default:
			gate.Pause = &Paused{Kind: PauseDecision, Reason: reason}
		}
	}
	if gate.Step == StepDone {
		if old.Final == nil {
			return nil, errors.New("completed task has no completion receipt")
		}
		gate.Final = old.Final
		gate.Reported = gate.Reported || gate.Worktree == "" // its content is no longer watched; a landing still checks its own
	}
	state := &legacyState{stage: old.Baseline, task: old.EpicBaseline}
	if old.Epic == nil || state.task == nil {
		state.task = old.Baseline
	}
	if gate.StartCommit = old.EpicHead; gate.StartCommit == "" {
		gate.StartCommit = old.HeadBefore
	}
	gate.legacy = state
	return gate, nil
}

// upgrade builds the origin trees of a translated task from the file snapshots it carried, and saves it in
// the present format. It runs under the task's lock, inside its checkout. The original file is kept.
func (g *Gate) upgrade() error {
	if g == nil || g.legacy == nil {
		return nil
	}
	state := g.legacy
	dir := sessionDir(g.Cwd, g.Session)
	source := filepath.Join(dir, "state.json")
	if raw, err := os.ReadFile(source); err == nil {
		if _, exists := os.Stat(filepath.Join(dir, "state.v1.json")); errors.Is(exists, os.ErrNotExist) {
			if err := os.WriteFile(filepath.Join(dir, "state.v1.json"), raw, 0o600); err != nil {
				return fmt.Errorf("keep the original task state: %w", err)
			}
		}
	}
	// An origin is the working tree as it is now with the task's files put back to their before-images. Earlier
	// versions recorded nothing else, and held, as this does, that only those files are the task's change.
	unlanded := g.Step == StepDone && g.Worktree != "" && g.Landed == ""
	if g.active() || unlanded {
		now, err := g.snapshot()
		if err != nil {
			return err
		}
		if g.BaseTree, err = overlayTree(g.objects(), now, state.task); err != nil {
			return err
		}
		g.StageTree = g.BaseTree
		if !g.Whole && len(g.Accepted) > 0 && g.active() {
			if g.StageTree, err = overlayTree(g.objects(), now, state.stage); err != nil {
				return err
			}
		}
		if unlanded {
			// A finished isolated task that was never landed: what is in its worktree now is what was accepted.
			key, err := g.taskKey(now)
			if err != nil {
				return err
			}
			g.Final.Tree, g.Final.Key = now, key
		}
	}
	g.legacy = nil
	return g.save()
}
