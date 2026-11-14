package dev

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
)

// Names are used as directory components, board filenames and stable stream
// references. Display labels belong in briefs, never in filesystem paths.
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var gateIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func knownPhase(phase string) bool {
	return slices.Contains([]string{"implementing", "correcting", "awaiting-review", "reviewing", "reviewed", "held", "staged", "complete", "cancelled"}, phase)
}

// validateGateState checks the relationships that the runtime and terminal UI
// rely on. Invalid state is reported instead of being interpreted as no task.
func validateGateState(gate *Gate) error {
	if gate == nil || gate.Plan == nil || !gateIDPattern.MatchString(gate.ID) || !knownPhase(gate.Phase) || !filepath.IsAbs(gate.Cwd) {
		return errors.New("missing task identity, plan, checkout or phase")
	}
	if len(gate.Plan.Streams) == 0 {
		return errors.New("task plan has no streams")
	}
	if gate.Worktree != "" {
		if err := gate.validateWorktreePath(); err != nil {
			return err
		}
	}
	for _, writer := range gate.Writers {
		if writer == nil || !safeName.MatchString(writer.Stream) {
			return errors.New("task has an invalid writer record")
		}
	}
	if gate.StageIndex < 0 || gate.Epic != nil && gate.StageIndex >= len(gate.Epic.Stages) || gate.Phase == "staged" && gate.Epic == nil {
		return errors.New("task has an invalid stage index or no staged plan")
	}
	if gate.Phase == "complete" && gate.Final == nil {
		return errors.New("completed task has no completion receipt")
	}
	if gate.Phase == "reviewed" && (len(gate.Reviews) == 0 || gate.ReviewDigest == "") {
		return errors.New("reviewed task has no review receipt")
	}
	if gate.Phase == "held" && (!knownPhase(gate.ResumePhase) || gate.ResumePhase == "held") {
		return errors.New("held task has no valid resume phase")
	}
	return nil
}

// recoverInterrupted runs only under the session lock. A process that died
// without writing its final receipt must not leave immortal RUNNING writers.
// Recovery records the interruption; only an explicit resume restarts work.
func (g *Gate) recoverInterrupted() error {
	if g == nil || !g.active() || g.Phase == "held" {
		return nil
	}
	operation := ops.Status(home())
	if operation == nil || operation.State != "running" || operation.Live {
		return nil
	}
	for _, writer := range g.Writers {
		if writer.Status == "RUNNING" {
			writer.Status, writer.Error = "FAILED", "the owning operation was interrupted"
		}
	}
	g.ResumePhase, g.Phase = g.Phase, "held"
	g.HoldReason = "the previous operation was interrupted; inspect the task and explicitly resume it"
	return g.save()
}

// readyForReview is a state-machine guard, not a trust check of a model's prose.
// Every scheduled stream must have completed its own record in this round.
func (g *Gate) readyForReview() error {
	if g.Phase != "awaiting-review" && g.Phase != "reviewed" {
		return fmt.Errorf("cannot review a gate that is %s; finish or resume its writer round first", g.Phase)
	}
	if g.Round == nil { // review-only tasks and the final integration pass
		return nil
	}
	latest := g.latest()
	for _, stream := range g.Round.Streams {
		writer := latest[stream.Name]
		if writer == nil || writer.Batch != g.Round.Batch || writer.Status != "COMPLETED" {
			return fmt.Errorf("stream %q has not completed the current writer round", stream.Name)
		}
	}
	return nil
}

func (g *Gate) canResumeRound() error {
	if g.Round == nil || (g.Phase != "implementing" && g.Phase != "correcting") {
		return errors.New("there is no unfinished writer round to continue")
	}
	for _, writer := range g.latest() {
		if writer.Batch == g.Round.Batch && writer.Status == "BLOCKED" {
			return fmt.Errorf("stream %q needs an answer before its writer round can continue", writer.Stream)
		}
	}
	return nil
}

// Continue retries the saved round after an explicit resume. Completed streams
// retain their receipts and sessions; only unfinished streams are restarted.
func Continue(cfg *config.Config, gate *Gate) (any, error) {
	if gate == nil {
		return nil, errors.New("there is no task to continue")
	}
	if err := gate.canResumeRound(); err != nil {
		return nil, err
	}
	return gate.write(cfg)
}
