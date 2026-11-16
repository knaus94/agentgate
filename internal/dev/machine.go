package dev

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/ops"
)

// A task's position is a step, named after what has to happen next. This file is the one definition of what
// is legal where: the guard of every command, the actions offered to the lead and the tests that look for
// dead ends are all derived from the two tables below.

const (
	StepWrite     = "write"     // the present round of writers is not finished
	StepVerify    = "verify"    // every stream reported; checks and review must run or be reconciled
	StepDecide    = "decide"    // verification produced something only the lead can settle
	StepDone      = "done"      // complete
	StepCancelled = "cancelled" // dropped for good
)

// Paused is waiting for a person. It lies on top of the position and does not change it.
type Paused struct {
	Kind   string `json:"kind"` // user, decision or step
	Reason string `json:"reason"`
	At     int64  `json:"at"`
}

const (
	PauseUser     = "user"     // the user stopped the task: only an explicit resume lifts it
	PauseDecision = "decision" // the lead waits for the user's decision
	PauseStep     = "step"     // the user confirms every stage
)

// Action is something the lead can ask for.
type Action string

const (
	ActRun    Action = "run"    // take every determined step until a person is needed
	ActAnswer Action = "answer" // reply to a blocked writer in its own session
	ActDecide Action = "decide" // rule on findings, give the lead's own review, send an unmet criterion to a writer
	ActAmend  Action = "amend"  // replace the plan, with a reason
	ActPause  Action = "pause"  // park the task with the true reason
	ActResume Action = "resume" // lift a pause
	ActStop   Action = "stop"   // cancel the running operation, or park an idle task as stopped by the user
	ActLand   Action = "land"   // bring a finished isolated task into the checkout, or drop it
	ActCheck  Action = "check"  // run declared checks
	ActVisual Action = "visual" // record the browser choice or a skip, or run a browser pass
	// Drop and forget are the user's to ask for; they are never offered among the actions.
	ActDrop   Action = "drop"   // end the task and take back what it changed
	ActForget Action = "forget" // remove a finished or dropped task from the record
)

// legal lists the actions of each step, the one that moves the task first.
var legal = map[string][]Action{
	StepWrite:     {ActRun, ActAnswer, ActDecide, ActAmend, ActCheck, ActVisual, ActPause, ActStop},
	StepVerify:    {ActRun, ActDecide, ActAmend, ActCheck, ActVisual, ActPause, ActStop},
	StepDecide:    {ActDecide, ActRun, ActAmend, ActCheck, ActVisual, ActPause, ActStop},
	StepDone:      {ActLand, ActDecide}, // decide fixes the notes a finished task left
	StepCancelled: {ActLand},
}

// transitions lists the steps each step may be followed by.
var transitions = map[string][]string{
	StepWrite:     {StepWrite, StepVerify, StepCancelled},
	StepVerify:    {StepVerify, StepWrite, StepDecide, StepDone, StepCancelled},
	StepDecide:    {StepDecide, StepWrite, StepVerify, StepDone, StepCancelled},
	StepDone:      {StepVerify, StepCancelled}, // reopened, or dropped
	StepCancelled: {},
}

// Refusal says why an action is not legal now and what is.
type Refusal struct {
	Action  Action   `json:"action"`
	Because string   `json:"because"`
	Instead []Action `json:"instead,omitempty"`
}

func (r *Refusal) Error() string {
	if len(r.Instead) == 0 {
		return r.Because
	}
	names := make([]string, len(r.Instead))
	for index, action := range r.Instead {
		names[index] = string(action)
	}
	return r.Because + " (possible now: " + strings.Join(names, ", ") + ")"
}

func (g *Gate) active() bool { return g.Step != StepDone && g.Step != StepCancelled }

// blocked lists the streams of the present round whose writer returned questions.
func (g *Gate) blocked() []*Writer {
	var out []*Writer
	if g.Round == nil {
		return nil
	}
	latest := g.latest()
	for _, stream := range g.Round.Streams {
		if writer := latest[stream.Name]; writer != nil && writer.Batch == g.Round.Batch && writer.Status == "BLOCKED" {
			out = append(out, writer)
		}
	}
	return out
}

// can is the guard every command passes before it changes the task.
func (g *Gate) can(action Action) error {
	refuse := func(format string, values ...any) error {
		return &Refusal{Action: action, Because: fmt.Sprintf(format, values...), Instead: g.actions()}
	}
	switch action {
	case ActLand:
		switch {
		case g.Worktree == "":
			return refuse("this task was not isolated: its changes are already in the checkout")
		case g.Landed != "":
			return refuse("this task is already closed (%s)", g.Landed)
		}
		return nil
	case ActDrop:
		switch {
		case g.Landed != "":
			return refuse("this task is already closed (%s): its changes are the checkout's now", g.Landed)
		case g.Dropped:
			return refuse("this task was already dropped")
		}
		return nil
	case ActForget:
		switch {
		case g.active():
			return refuse("only a finished or dropped task is forgotten; this one is at %s", g.Step)
		case g.Worktree != "" && g.Landed == "":
			return refuse("this task's worktree is not landed or dropped yet: `land` it, or `drop` it")
		}
		return nil
	case ActDecide:
		if g.Step == StepDone && g.Landed != "" {
			return refuse("this task is landed (%s): its changes are the checkout's now; a new task fixes what it left", g.Landed)
		}
	}
	if !slices.Contains(legal[g.Step], action) && action != ActResume {
		if !g.active() {
			return refuse("the task is %s", g.Step)
		}
		return refuse("%s is not possible while the task is at %s", action, g.Step)
	}
	if g.Pause != nil && g.Pause.Kind == PauseUser && action != ActResume && action != ActStop && action != ActPause {
		return refuse("the user stopped this task (%s); it continues only on their word, with `resume`", g.Pause.Reason)
	}
	switch action {
	case ActResume:
		if g.Pause == nil {
			return refuse("only a paused task can be resumed")
		}
	case ActAnswer:
		if len(g.blocked()) == 0 {
			return refuse("there is no blocked writer to answer")
		}
	}
	return nil
}

// actions is what the lead can do now, the action that moves the task first. Everything listed passes can.
func (g *Gate) actions() []Action {
	if !g.active() {
		var out []Action
		if g.Worktree != "" && g.Landed == "" {
			out = append(out, ActLand)
		}
		if g.Step == StepDone && g.Landed == "" && len(g.leftNotes()) > 0 {
			out = append(out, ActDecide)
		}
		return out
	}
	if g.Pause != nil && g.Pause.Kind == PauseUser {
		return []Action{ActResume, ActStop}
	}
	out := []Action{}
	for _, action := range legal[g.Step] {
		switch action {
		case ActAnswer:
			if len(g.blocked()) == 0 {
				continue
			}
		case ActRun:
			if g.Step == StepWrite && len(g.blocked()) > 0 && len(g.ready(nil)) == 0 {
				continue // a blocked writer is answered, not retried; `run` is for the streams that do not wait for it
			}
		case ActCheck:
			if len(g.checks()) == 0 {
				continue
			}
		case ActVisual:
			if g.Plan.Visual == nil {
				continue
			}
		case ActDecide:
			if g.Step != StepDecide && len(g.Findings()) == 0 {
				continue
			}
		}
		out = append(out, action)
	}
	if g.Pause != nil {
		out = append([]Action{ActResume}, out...)
	}
	if g.Worktree != "" && g.Landed == "" {
		out = append(out, ActLand) // an unfinished isolated task can still be dropped
	}
	return out
}

// leftNotes are the findings a position closed without fixing: not resolved and not rejected.
func (g *Gate) leftNotes() []Finding {
	var out []Finding
	for _, finding := range g.Findings() {
		if _, resolved := g.Resolutions[finding.ID]; !resolved && g.Dispositions[finding.ID].Decision != "rejected" {
			out = append(out, finding)
		}
	}
	return out
}

// setStep is the only assignment of the step. A transition the table does not contain is a defect of the
// tool, reported instead of saved.
func (g *Gate) setStep(next string) error {
	if g.Step == next {
		return nil
	}
	if !slices.Contains(transitions[g.Step], next) {
		return fmt.Errorf("internal: a task cannot go from %s to %s; its state was left unchanged", g.Step, next)
	}
	g.Step = next
	return nil
}

// pause parks the task. A user stop is never replaced by a weaker pause.
func (g *Gate) pause(kind, reason string) {
	if g.Pause != nil && g.Pause.Kind == PauseUser && kind != PauseUser {
		return
	}
	g.Pause = &Paused{Kind: kind, Reason: reason, At: time.Now().Unix()}
}

// proceed lifts a pause that waited for a decision: taking an action that moves the task is that decision.
func (g *Gate) proceed() {
	if g.Pause != nil && g.Pause.Kind != PauseUser {
		g.Pause = nil
	}
	g.AutoRounds = 0
}

// Names are used as directory components, board filenames and stable stream references. Display labels
// belong in briefs, never in filesystem paths.
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var gateIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// validateGateState checks the relationships the runtime and the panel rely on. Invalid state is reported
// instead of being interpreted as no task.
func validateGateState(gate *Gate) error {
	if gate == nil || gate.Plan == nil || !gateIDPattern.MatchString(gate.ID) || !filepath.IsAbs(gate.Cwd) {
		return errors.New("missing task identity, plan or checkout")
	}
	if _, known := legal[gate.Step]; !known {
		return fmt.Errorf("unknown step %q", gate.Step)
	}
	if len(gate.Plan.Stages) == 0 {
		return errors.New("task plan has no stages")
	}
	for _, stage := range gate.Plan.Stages {
		if len(stage.Streams) == 0 {
			return errors.New("task plan has a stage without streams")
		}
	}
	if gate.Stage < 0 || gate.Stage > len(gate.Plan.Stages) || len(gate.Accepted) > len(gate.Plan.Stages) ||
		len(gate.Modes) != len(gate.Plan.Stages) {
		return errors.New("task has an invalid stage position")
	}
	if gate.Stage == len(gate.Plan.Stages) && gate.active() && !gate.Whole {
		return errors.New("every stage is accepted but the task is neither finished nor in its final pass")
	}
	// A landed task has no worktree left to clean up; the path is only a record of where it was.
	if gate.Worktree != "" && gate.Landed == "" {
		if err := gate.validateWorktreePath(); err != nil {
			return err
		}
	}
	for _, writer := range gate.Writers {
		if writer == nil || !safeName.MatchString(writer.Stream) {
			return errors.New("task has an invalid writer record")
		}
	}
	if gate.Step == StepDone && gate.Final == nil {
		return errors.New("completed task has no completion receipt")
	}
	if gate.Pause != nil && !slices.Contains([]string{PauseUser, PauseDecision, PauseStep}, gate.Pause.Kind) {
		return errors.New("task has an unknown kind of pause")
	}
	if gate.active() && gate.BaseTree == "" && gate.legacy == nil {
		return errors.New("task has no recorded origin")
	}
	return nil
}

// recoverInterrupted runs only under the session lock. A process that died without writing its final receipt
// must not leave immortal RUNNING writers: they are recorded as failed, and the next run retries them.
func (g *Gate) recoverInterrupted() error {
	if g == nil || !g.active() {
		return nil
	}
	operation := ops.Status(home())
	if operation == nil || operation.State != "running" || operation.Live {
		return nil
	}
	changed := false
	for _, writer := range g.Writers {
		if writer.Status == "RUNNING" {
			writer.Status, writer.Error, changed = "FAILED", "the owning operation was interrupted", true
		}
	}
	if !changed {
		return nil
	}
	return g.save()
}
