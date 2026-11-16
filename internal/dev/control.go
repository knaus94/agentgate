package dev

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
)

// PauseInput parks the task with the true reason: a missing decision, an authorization, or the end of it.
type PauseInput struct {
	Reason    string `json:"reason"`
	Cancelled bool   `json:"cancelled,omitempty"` // the user dropped the task for good
}

// Pause parks the task. The position is kept; whatever moves the task later lifts the pause.
func Pause(cfg *config.Config, gate *Gate, input PauseInput) (any, error) {
	if err := gate.can(ActPause); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Reason) == "" {
		return nil, errors.New("a pause needs its real reason")
	}
	if input.Cancelled {
		if err := gate.setStep(StepCancelled); err != nil {
			return nil, err
		}
		gate.Pause = &Paused{Kind: PauseUser, Reason: input.Reason, At: time.Now().Unix()}
		return gate.report(cfg, []string{"task cancelled: " + input.Reason}), gate.save()
	}
	gate.pause(PauseDecision, input.Reason)
	return gate.report(cfg, []string{"paused: " + input.Reason}), gate.save()
}

// Resume lifts a pause. Only the lead resumes, never a status call; a user stop only on the user's word.
func Resume(cfg *config.Config, gate *Gate) (any, error) {
	if err := gate.can(ActResume); err != nil {
		return nil, err
	}
	gate.Pause = nil
	return gate.report(cfg, []string{"resumed"}), gate.save()
}

// Stop asks the running operation to stop, or parks an idle task as stopped by the user. It does not take the
// operation lock.
func Stop(cfg *config.Config) (any, error) {
	if err := requireLead(nil); err != nil {
		return nil, err
	}
	if ops.RequestStop(home(), "the user stopped /dev") {
		result := map[string]any{"cancellation_requested": true, "operation": ops.Status(home()).Action,
			"next": "The running operation is terminating its processes and will park the task. `status` confirms it."}
		if gate, err := LoadCurrent(); err == nil && gate != nil {
			var out map[string]any
			gate.Inside(func() { out = gate.respond(cfg, nil, result) })
			return out, nil
		}
		return result, nil
	}
	unlock, err := lock()
	if err != nil {
		// Another operation may have started between the first observation and the lock attempt. Ask that
		// owner to stop; never overwrite its state.
		if ops.RequestStop(home(), "the user stopped /dev") {
			return map[string]any{"cancellation_requested": true, "operation": ops.Status(home())}, nil
		}
		return nil, err
	}
	defer unlock()
	gate, err := LoadCurrent()
	if err != nil {
		return nil, err
	}
	if gate == nil || !gate.active() {
		return map[string]any{"stopped": true}, nil
	}
	gate.pause(PauseUser, "the user stopped /dev")
	return gate.report(cfg, []string{"stopped by the user"}), gate.save()
}

// StopHook is the lead client's Stop hook: it keeps the lead from ending a turn on an unfinished task unless
// an operation is still running or the task is honestly paused.
func StopHook(cwd, sessionID string) map[string]any {
	gate, err := loadGate(cwd, sessionID)
	if err != nil {
		return map[string]any{"decision": "block", "reason": "AgentGate cannot verify the task because its saved state is unreadable: " + err.Error()}
	}
	if gate == nil {
		return map[string]any{}
	}
	unlock, err := flock(filepath.Join(sessionDir(cwd, sessionID), "lock"), syscall.LOCK_EX|syscall.LOCK_NB, "operation running")
	if err != nil {
		return map[string]any{} // the operation owner reports its own completion
	}
	defer unlock()
	gate, err = loadGate(cwd, sessionID)
	if err != nil {
		return map[string]any{"decision": "block", "reason": "AgentGate cannot verify the saved task: " + err.Error()}
	}
	if gate == nil || gate.Session != sessionID || gate.Pause != nil || gate.Step == StepCancelled || gate.Landed != "" || gate.legacy != nil {
		return map[string]any{}
	}
	if gate.Step == StepDone {
		if gate.Reported {
			return map[string]any{}
		}
		previous, _ := os.Getwd()
		where := gate.Cwd
		if gate.Worktree != "" {
			where = gate.Worktree
		}
		if os.Chdir(where) == nil {
			key := ""
			now, err := gate.snapshot()
			if err == nil {
				key, err = gate.taskKey(now)
			}
			_ = os.Chdir(previous)
			// A finished task is bound to its own files as they were completed, not to HEAD and not to the rest of
			// the checkout: committing it, or editing something else, must not reopen it.
			if err == nil && key == gate.Final.Key {
				gate.Reported = true
				_ = gate.save()
				return map[string]any{}
			}
		}
		if gate.reopen() == nil {
			_ = gate.save()
		}
	}
	// A live operation reports its own completion; the lead may end the turn and wait for it.
	record := ops.Status(sessionDir(cwd, sessionID))
	if record != nil && record.Live {
		return map[string]any{}
	}
	reason := "/dev task is at " + gate.Step + "."
	// The lead often believes a command is still running when it was refused within a second.
	if record != nil && record.State == "failed" && record.Error != "" {
		reason += " Your last command, `" + record.Action + "`, was refused: " + clipText(oneLine(record.Error), 400) + "."
	}
	if kinds := gate.needKinds(cwd); len(kinds) > 0 {
		reason += " It needs: " + strings.Join(kinds, ", ") + "."
	}
	return map[string]any{"decision": "block", "reason": reason + " Settle it with one of the `actions` that `agentgate status` lists. " +
		"If you are waiting for the user's decision, `agentgate pause` with the true reason and report INCOMPLETE. Do not claim it is done."}
}

// needKinds lists the kinds of what the task needs, read in its own checkout; nothing when they cannot be read.
func (g *Gate) needKinds(cwd string) []string {
	previous, _ := os.Getwd()
	where := cwd
	if g.Worktree != "" {
		where = g.Worktree
	}
	if where != "" && os.Chdir(where) != nil {
		return nil
	}
	defer os.Chdir(previous)
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	var kinds []string
	for _, need := range g.needs(cfg) {
		if !slices.Contains(kinds, need.Kind) {
			kinds = append(kinds, need.Kind)
		}
	}
	return kinds
}

func clipText(text string, limit int) string {
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}

// reopen returns a finished task to verification: its content changed after it was completed, or a landing
// has to catch up with the checkout first. A task of several stages is then judged as a whole.
func (g *Gate) reopen() error {
	if err := g.setStep(StepVerify); err != nil {
		return err
	}
	g.Final, g.Reported, g.Pause = nil, false, nil
	if len(g.Plan.Stages) > 1 {
		g.Stage, g.Whole, g.Mode, g.StageTree = len(g.Plan.Stages), true, g.overallMode(), g.BaseTree
	}
	return nil
}
