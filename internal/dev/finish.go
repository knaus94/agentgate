package dev

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
)

type FixInput struct {
	Brief        string        `json:"brief"` // the root cause and the coherent remedy
	Reassessment *Reassessment `json:"reassessment,omitempty"`
	Escalate     bool          `json:"escalate,omitempty"` // raise the writers' effort one step for this correction
	Streams      []string      `json:"streams,omitempty"`  // which streams repair a failing check; default: all
}

// Progress says whether the last correction moved the task forward.
type Progress struct {
	Status     string   `json:"status"`
	Reassess   bool     `json:"reassessment_required"`
	Resolved   []string `json:"resolved,omitempty"`
	Unresolved []string `json:"unresolved,omitempty"`
	Recurring  []string `json:"recurring,omitempty"`
}

// progress detects corrections that go nowhere: unchanged or cycling content, or a defect that keeps coming back.
// There is no fixed round limit; a stalled task needs a new diagnosis, not another identical attempt.
func (g *Gate) progress() Progress {
	if len(g.Corrections) == 0 {
		return Progress{Status: "first-correction"}
	}
	if g.Phase != "reviewed" {
		return Progress{Status: "awaiting-review"}
	}
	last := g.Corrections[len(g.Corrections)-1]
	out := Progress{}
	for _, id := range last.FindingIDs {
		if _, ok := g.Resolutions[id]; ok {
			out.Resolved = append(out.Resolved, id)
			continue
		}
		out.Unresolved = append(out.Unresolved, id)
		if len(g.Corrections) > 1 && slices.Contains(g.Corrections[len(g.Corrections)-2].FindingIDs, id) && g.Dispositions[id].Decision == "accepted" {
			out.Recurring = append(out.Recurring, id)
		}
	}
	cycle := slices.ContainsFunc(g.Corrections, func(c Correction) bool { return c.StartDigest == g.ReviewDigest })
	switch {
	case g.ReviewDigest == last.StartDigest:
		out.Status = "unchanged-content"
	case cycle:
		out.Status = "content-cycle"
	case len(out.Recurring) > 0:
		out.Status = "recurring-findings"
	case len(out.Resolved) == 0:
		out.Status = "no-confirmed-fix"
	default:
		out.Status = "progress"
	}
	out.Reassess = out.Status != "progress"
	return out
}

// Fix sends the accepted findings back to the writers that own the affected files, in their own sessions.
func Fix(cfg *config.Config, gate *Gate, input FixInput) (any, error) {
	if !gate.Request.Writes() {
		return nil, errors.New("a review without --fix has no authority to change code")
	}
	content, _, err := gate.contentDigest()
	if err != nil {
		return nil, err
	}
	// A correction is driven by accepted review findings, by failing declared checks, or by both.
	failing := gate.failing()
	if slices.ContainsFunc(failing, func(f CheckFailure) bool { return f.Setup }) {
		return nil, errors.New("layout verification could not run: repair the stand or measurement contract, then replan/check; do not send a tooling failure to a CSS writer")
	}
	conflicts := gate.unresolved()
	if len(failing) == 0 && len(conflicts) == 0 {
		if gate.Phase != "reviewed" || content != gate.ReviewDigest {
			return nil, errors.New("a correction needs a review and triage of the current code, or a failing declared check")
		}
		if gate.visualRequired() && gate.currentVisual(content) == nil {
			return nil, errors.New("collect the opted-in browser report before spending a correction round")
		}
		if pending := gate.untriaged(); len(pending) > 0 {
			return nil, fmt.Errorf("triage these findings first: %s", strings.Join(pending, ", "))
		}
	} else if gate.Phase != "awaiting-review" && gate.Phase != "reviewed" {
		return nil, fmt.Errorf("cannot correct a task that is %s", gate.Phase)
	}
	accepted := gate.accepted()
	if len(accepted) == 0 && len(failing) == 0 && len(conflicts) == 0 {
		return nil, errors.New("there are no accepted findings and no failing checks to correct")
	}
	if strings.TrimSpace(input.Brief) == "" {
		input.Brief = "Diagnose and correct the accepted findings and failing checks in this assignment. Repair the root cause, preserve the contract, and report the diagnosis and actual checks."
	}
	var checkIDs []string
	for _, failure := range failing {
		checkIDs = append(checkIDs, checkID(failure.Argv))
	}
	progress := gate.progress()
	// The same check failing again after a correction aimed at it is a stall, just like a finding that returns.
	if len(gate.Corrections) > 0 && slices.ContainsFunc(gate.Corrections[len(gate.Corrections)-1].CheckIDs, func(id string) bool {
		return slices.Contains(checkIDs, id)
	}) {
		progress = Progress{Status: "check-still-failing", Reassess: true}
	}
	if progress.Reassess && (input.Reassessment == nil || strings.TrimSpace(input.Reassessment.Evidence) == "" ||
		strings.TrimSpace(input.Reassessment.ChangedApproach) == "") {
		return nil, fmt.Errorf("the last correction made no verified progress (%s): delegate diagnosis through consult and provide reassessment "+
			"{evidence, changed_approach}, or hold and ask the user if a decision is missing", progress.Status)
	}
	// Each finding goes to the stream that owns its file; files named only by the lead join that stream.
	touched := map[string]bool{}
	var extra []string
	for _, finding := range accepted {
		target := gate.streamOf(finding.Path)
		files := gate.Dispositions[finding.ID].Files
		for _, file := range files {
			if target == nil {
				target = gate.streamOf(file)
			}
		}
		if target == nil {
			target = &gate.Plan.Streams[0]
		}
		touched[target.Name] = true
		for _, file := range files {
			if gate.streamOf(file) == nil {
				target.Files = append(target.Files, file)
				extra = append(extra, file)
			}
		}
	}
	if len(extra) > 0 {
		unlock, err := scopeLock()
		if err != nil {
			return nil, err
		}
		defer unlock()
		if gate.Worktree == "" {
			if err := checkOwnership(extra); err != nil {
				return nil, err
			}
		}
		added, err := capture(extra)
		if err != nil {
			return nil, err
		}
		for name, state := range added {
			gate.Baseline[name] = state
		}
		gate.Files = append(gate.Files, extra...)
		sort.Strings(gate.Files)
		gate.Context = subtract(gate.Context, extra)
		if err := gate.save(); err != nil {
			return nil, err
		}
		unlock()
		// Unlock is idempotent; do not retain the checkout-wide claim lock while
		// writers run. The saved state now owns the added files.
	}
	// A failing check does not say which stream broke it: every stream gets it unless the lead names the owners.
	if len(failing) > 0 {
		for _, stream := range gate.Plan.Streams {
			if !stream.Done && (len(input.Streams) == 0 || slices.Contains(input.Streams, stream.Name)) {
				touched[stream.Name] = true
			}
		}
	}
	for _, file := range conflicts {
		if stream := gate.streamOf(file); stream != nil {
			touched[stream.Name] = true
		}
	}
	if len(touched) == 0 {
		return nil, errors.New("streams names no stream of the plan")
	}
	var streams []Stream
	var ids []string
	for _, stream := range gate.Plan.Streams {
		if touched[stream.Name] {
			stream.After, stream.Done = nil, false
			streams = append(streams, stream)
		}
	}
	for _, finding := range accepted {
		ids = append(ids, finding.ID)
	}
	// More effort is earned, not assumed: a correction that made no verified progress, or the lead's explicit
	// request, raises the writers one step, up to the mode's ceiling.
	if progress.Reassess || input.Escalate {
		gate.Boost++
	}
	gate.Iteration++
	gate.Corrections = append(gate.Corrections, Correction{Iteration: gate.Iteration, FindingIDs: ids, StartDigest: content,
		CheckIDs: checkIDs, Brief: input.Brief, Reassessment: input.Reassessment})
	gate.Round = &Round{Batch: fmt.Sprintf("v%d.c%d", gate.PlanVersion, gate.Iteration), Streams: streams, Correction: true,
		Findings: accepted, Remedy: input.Brief, Boost: gate.Boost, Failures: failing, Conflicts: conflicts}
	gate.Phase, gate.WriteFiles = "correcting", gate.Files
	return gate.write(cfg)
}

type FinishInput struct {
	Summary              string     `json:"summary"`
	ArchitectureEvidence string     `json:"architecture_evidence,omitempty"`
	VisualExclusions     []Excluded `json:"visual_exclusions,omitempty"`
}

type Final struct {
	FinishInput
	At int64 `json:"at"`
}

// Finish closes the task. It refuses while anything the mode requires is missing or stale.
func Finish(cfg *config.Config, gate *Gate, input FinishInput) (any, error) {
	if err := gate.auditScope(); err != nil {
		return nil, err
	}
	content, _, err := gate.contentDigest()
	if err != nil {
		return nil, err
	}
	if gate.Phase != "reviewed" || content != gate.ReviewDigest {
		return nil, errors.New("the code has no current independent review")
	}
	if err := gate.readyForReview(); err != nil {
		return nil, err
	}
	if len(gate.Reviews) == 0 {
		return nil, errors.New("the code has no current independent review")
	}
	if !gate.independentReview(cfg, gate.Reviews[len(gate.Reviews)-1]) {
		return nil, errors.New("the current reviewer policy or author history requires a fresh independent review")
	}
	if gate.Reviews[len(gate.Reviews)-1].Contract != gate.contract() {
		return nil, errors.New("the task contract changed since the review")
	}
	if quality := gate.qualityStatus(cfg); quality != nil {
		if problems := quality["problems"].([]string); len(problems) > 0 {
			return nil, fmt.Errorf("quality criteria are not satisfied: %s", strings.Join(problems, "; "))
		}
	}
	if pending := gate.untriaged(); len(pending) > 0 {
		return nil, fmt.Errorf("triage these findings first: %s", strings.Join(pending, ", "))
	}
	var blocking []string
	for _, finding := range gate.Findings() {
		if gate.blocking(cfg, finding) {
			blocking = append(blocking, finding.ID)
		}
	}
	if len(blocking) > 0 {
		return nil, fmt.Errorf("these findings still block completion: %s", strings.Join(blocking, ", "))
	}
	if gate.visualRequired() {
		run := gate.currentVisual(content)
		if run == nil {
			return nil, errors.New("the opted-in browser verification is unfinished; run it or record why it is skipped")
		}
		if missing := gate.unobserved(run, input.VisualExclusions); len(missing) > 0 {
			return nil, fmt.Errorf("browser scenarios were not observed: %s; check them or list visual_exclusions with reasons", strings.Join(missing, ", "))
		}
	}
	if gate.Request.Writes() && len(gate.Plan.Checks) == 0 && gate.Mode != "light" {
		return nil, errors.New("this mode needs at least one declared check with a current result")
	}
	for _, check := range gate.outstanding() {
		return nil, fmt.Errorf("check has no current passing result: %s", strings.Join(check.Argv, " "))
	}
	needsEvidence := len(cfg.Modes[gate.Mode].Architecture) > 0 && gate.Request.Writes()
	if strings.TrimSpace(input.Summary) == "" || (needsEvidence && strings.TrimSpace(input.ArchitectureEvidence) == "") {
		return nil, errors.New("record summary: what was delivered and verified; this mode also needs architecture_evidence: " +
			"how the result matches the accepted design")
	}
	if err := ops.Checkpoint(); err != nil {
		return nil, err
	}
	// A stage of a staged task is accepted, not finished: the task goes on to what follows it.
	if gate.stage() != nil {
		return gate.acceptStage(cfg, input)
	}
	gate.Final, gate.Phase = &Final{FinishInput: input, At: time.Now().Unix()}, "complete"
	if gate.Worktree != "" {
		if err := gate.commitTask("dev: " + firstLine(input.Summary)); err != nil {
			return nil, err
		}
		if current, err := gate.qualityInputs(cfg); err != nil {
			return nil, err
		} else {
			// A private commit records the same verified bytes under a new HEAD.
			// Check logs and verdicts are unchanged; only their repository stamp moves.
			gate.Reviews[len(gate.Reviews)-1].QualityDigest = current.fingerprint()
		}
	}
	return gate.Summary(cfg), gate.save()
}

func (g *Gate) holdNow(reason string) {
	if g.Phase != "held" {
		g.ResumePhase = g.Phase
	}
	g.Phase, g.HoldReason = "held", reason
	_ = g.save()
}

type HoldInput struct {
	Reason    string `json:"reason"`
	Cancelled bool   `json:"cancelled,omitempty"` // the user dropped the task for good
}

// Hold parks the task with the true reason: a missing decision, an authorization, or a user stop.
func Hold(cfg *config.Config, gate *Gate, input HoldInput) (any, error) {
	if strings.TrimSpace(input.Reason) == "" {
		return nil, errors.New("a hold needs its real reason")
	}
	gate.holdNow(input.Reason)
	if input.Cancelled {
		gate.Phase = "cancelled"
	}
	return gate.Summary(cfg), gate.save()
}

// Resume returns a held task to the phase it was in. Only the lead resumes, never a status call.
func Resume(cfg *config.Config, gate *Gate) (any, error) {
	if gate.Phase != "held" {
		return nil, errors.New("only a held task can be resumed")
	}
	gate.Phase, gate.ResumePhase, gate.HoldReason = gate.ResumePhase, "", ""
	if gate.Phase == "reviewing" {
		gate.Phase = "awaiting-review"
	}
	return gate.Summary(cfg), gate.save()
}

// Stop asks the running operation to stop, or holds an idle task. It does not take the operation lock.
func Stop(cfg *config.Config) (any, error) {
	if err := requireLead(nil); err != nil {
		return nil, err
	}
	if ops.RequestStop(home(), "the user stopped /dev") {
		return map[string]any{"cancellation_requested": true, "operation": ops.Status(home()),
			"next": "The running operation is terminating its processes and will hold the task. Check status to confirm."}, nil
	}
	unlock, err := lock()
	if err != nil {
		// Another operation may have started between the first observation and
		// the lock attempt. Ask that owner to stop; never overwrite its state.
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
	return Hold(cfg, gate, HoldInput{Reason: "the user stopped /dev"})
}

// Summary is the read-only picture of a task that every operation returns.
func (g *Gate) Summary(cfg *config.Config) map[string]any {
	var open, notes, blocking []Finding
	for _, finding := range g.Findings() {
		switch _, resolved := g.Resolutions[finding.ID]; {
		case resolved:
		case g.isNote(finding):
			notes = append(notes, finding)
		default:
			open = append(open, finding)
			if g.blocking(cfg, finding) {
				blocking = append(blocking, finding)
			}
		}
	}
	ids := func(findings []Finding) []string {
		out := []string{}
		for _, finding := range findings {
			out = append(out, finding.ID)
		}
		return out
	}
	var writers []*Writer
	for _, writer := range g.latest() {
		writers = append(writers, writer)
	}
	sort.Slice(writers, func(a, b int) bool { return writers[a].Stream < writers[b].Stream })
	var outstanding [][]string
	current := g.currentReceipts()
	for _, check := range g.outstandingReceipts(current) {
		outstanding = append(outstanding, check.Argv)
	}
	dir := sessionDir(g.Cwd, g.Session)
	out := map[string]any{"id": g.ID, "mode": g.Mode, "action": g.Request.Action, "phase": g.Phase, "iteration": g.Iteration,
		"writers": writers, "findings": open, "blocking": ids(blocking), "untriaged": g.untriaged(), "notes": notes,
		"checks_outstanding": outstanding, "checks_failed": g.failingReceipts(current), "progress": g.progress(), "foreign_changes": g.Foreign,
		"substitutions": g.Substitutions, "operation": ops.Status(dir), "timings_s": ops.Timings(dir, g.ID),
		"board": g.board(), "state": dir, "next": g.nextWithReceipts(cfg, len(blocking) > 0, current)}
	if g.ContextPack != "" {
		out["context_pack"] = g.ContextPack
	}
	if review := g.reviewDiversity(); review != nil {
		out["review_diversity"] = review
	}
	if g.ScoutNote != "" {
		out["scout_note"] = g.ScoutNote
	}
	if g.Phase == "held" {
		out["hold_reason"], out["resume_phase"] = g.HoldReason, g.ResumePhase
	}
	if g.ReviewError != "" {
		out["review_error"] = g.ReviewError
	}
	if g.Plan.Visual != nil {
		out["visual"] = g.visualSummary()
	}
	if g.Worktree != "" {
		out["isolated"] = g.landing()
	}
	if len(g.Signals) > 0 {
		out["slop_signals"] = g.Signals
	}
	if quality := g.qualityStatus(cfg); quality != nil {
		out["quality"] = quality
	}
	if g.Epic != nil {
		out["stages"] = g.stagesSummary()
		switch {
		case !g.active():
		case g.Integration:
			out["stage"] = "integration: the whole change, after every stage was accepted"
		case g.stage() != nil:
			out["stage"] = fmt.Sprintf("%s (%d of %d)", g.stage().Name, g.StageIndex+1, len(g.Epic.Stages))
		}
	}
	return out
}

// next tells the lead the one thing to do now, so the workflow does not live only in a document.
func (g *Gate) next(cfg *config.Config, blocked bool) string {
	var current map[string]*CheckReceipt
	if g.Phase == "awaiting-review" || g.Phase == "reviewed" {
		current = g.currentReceipts()
	}
	return g.nextWithReceipts(cfg, blocked, current)
}

func (g *Gate) nextWithReceipts(cfg *config.Config, blocked bool, current map[string]*CheckReceipt) string {
	switch g.Phase {
	case "staged":
		stage := g.stage()
		return fmt.Sprintf("Stage %d of %d, %s (%s), is ready to start: `launch` with the same plan. The stages before it are accepted and "+
			"committed to memory; if what you learned changes the stages still ahead, revise them in the plan with `update` first.",
			g.StageIndex+1, len(g.Epic.Stages), stage.Name, g.Mode)
	case "implementing", "correcting":
		return "Writers have not finished this round: `agentgate task continue` retries unfinished streams (or answer a blocked stream)."
	case "awaiting-review":
		if left := g.unresolved(); len(left) > 0 {
			return "Catching up with the checkout left conflict markers in " + strings.Join(left, ", ") +
				". `fix` {brief}: say how the two changes combine; the owning writers resolve them in their sessions."
		}
		failed := g.failingReceipts(current)
		if g.Request.Writes() && len(failed) > 0 {
			if slices.ContainsFunc(failed, func(f CheckFailure) bool { return f.Setup }) {
				return "A layout tool could not observe the page (checks_failed.setup). Repair the stand or use prepare with the failure context, then replan the corrected contract and review. Do not tune CSS, baselines or tolerances to bypass it."
			}
			return "A declared check fails, so no reviewer was started. Run `fix`: the owning writers receive the output and diagnose the defect in their own sessions. The lead need not inspect source or pictures."
		}
		if g.Integration && len(g.Reviews) == 0 {
			return "Every stage is accepted. Run `review` for the pass over the whole change: it looks at the seams between the stages."
		}
		if len(cfg.Modes[g.Mode].Review) == 0 {
			return "Run `review`: it returns the diffs for you to read, then call it again with lead_review."
		}
		return "Run `review`: declared checks run first; independent reviewers inspect the code and design. The lead need not duplicate their investigation."
	case "held":
		for _, writer := range g.latest() {
			if writer.Status == "BLOCKED" {
				return "Decide the blocker, then `answer` {stream, answer}: the writer continues in its own session."
			}
		}
		return "Resolve the hold reason, then `resume`. Never resume a user stop on your own."
	case "reviewed":
		switch {
		case len(g.untriaged()) > 0:
			return "Assess the reviewers' cited evidence and `triage`: accepted, rejected, deferred or needs-approval. Delegate disputed technical details through consult."
		case len(g.accepted()) > 0:
			return "Run `fix`; the owning writers diagnose and correct accepted findings in their sessions. Supply brief only for an additional decision or constraint."
		case blocked:
			return "Blocking findings remain: accept and fix them, or reject them with contrary evidence."
		case len(g.outstandingReceipts(current)) > 0:
			return "Run the outstanding checks with `check`."
		}
		if quality := g.qualityStatus(cfg); quality != nil && len(quality["problems"].([]string)) > 0 {
			return "Quality criteria are not satisfied: inspect quality.problems, collect missing evidence or fix accepted findings, then review again. " +
				"If future behavior was assigned to the current stage by mistake, prepare an amended whole plan retaining the original requirements and apply it with replan; do not waive the criterion. " +
				"Use review {recheck: true} to reconsider unchanged evidence. Hold when a required proof source is unavailable."
		}
		return "Everything required is current: `finish` with a summary."
	case "complete":
		if g.Worktree != "" && g.Landed == "" {
			return "Done in an isolated worktree; the checkout is untouched. Report the work and the diff (`git diff HEAD..." + g.Branch +
				"` in the checkout), then ask the user how to bring it in and run `land` {how}: apply (uncommitted, default), " +
				"commit, branch (keep for a pull request) or drop. Never land without the user's word."
		}
		return "Done. Report the work, the checks that ran and any limits to the user."
	}
	return ""
}

// StopHook is the Claude Code Stop hook: it keeps the lead from ending a turn on an unfinished task
// unless an operation is still running or the task is honestly on hold.
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
	if gate == nil || gate.Session != sessionID || gate.Phase == "held" || gate.Phase == "cancelled" || gate.Landed != "" {
		return map[string]any{}
	}
	if gate.Phase == "complete" {
		if gate.Reported {
			return map[string]any{}
		}
		previous, _ := os.Getwd()
		where := gate.Cwd
		if gate.Worktree != "" {
			where = gate.Worktree
		}
		if os.Chdir(where) == nil {
			content, _, err := gate.contentDigest()
			_ = os.Chdir(previous)
			// A finished task is bound to the reviewed content, not to HEAD: committing it must not reopen it.
			if err == nil && content == gate.ReviewDigest {
				gate.Reported = true
				_ = gate.save()
				return map[string]any{}
			}
		}
		gate.Phase, gate.Final = "awaiting-review", nil
		_ = gate.save()
	}
	// A live operation reports its own completion; the lead may end the turn and wait for it.
	if record := ops.Status(sessionDir(cwd, sessionID)); record != nil && record.Live {
		return map[string]any{}
	}
	return map[string]any{"decision": "block", "reason": "/dev task is " + gate.Phase + ". Run `agentgate status` and follow its `next`. " +
		"If you are waiting for the user's decision or were stopped, `agentgate hold` with the true reason and report INCOMPLETE. Do not claim it is done."}
}
