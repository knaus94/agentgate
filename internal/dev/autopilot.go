package dev

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
)

// The lead is asked for decisions, not for keystrokes. `run` takes every step that the state determines —
// write, check, review, send findings back to their authors, accept a stage, open the next — and stops where
// a person is needed. Called again with nothing decided, it reports the same needs and starts no model.

// Need is one thing only a person can settle. Every kind has one way to resolve it, named in Resolve.
type Need struct {
	Kind    string `json:"kind"` // answer, failed, review, finding, requirement, rule, stall, limit, setup, scenarios, conflict, scope
	ID      string `json:"id,omitempty"`
	Stream  string `json:"stream,omitempty"`
	What    string `json:"what"`
	Detail  any    `json:"detail,omitempty"`
	Resolve string `json:"resolve"`
	// environment marks a need that comes from a failing check, where the cause may lie outside the code: a
	// stand, a service. Running the task again runs that check again.
	environment bool
}

const (
	verdictVerify = "verify" // the evidence is not current
	verdictFix    = "fix"    // writers have something to correct and nothing stops them
	verdictLead   = "lead"   // only a person can settle what is open
	verdictClean  = "clean"  // everything the position requires is current and satisfied
)

// autoRounds is how many corrections the tool opens in a row before the lead looks. It is not a limit on
// rounds: `run` goes on.
const autoRounds = 3

// Progress says whether the last correction moved the task forward.
type Progress struct {
	Status     string   `json:"status"`
	Reassess   bool     `json:"reassessment_required"`
	Resolved   []string `json:"resolved,omitempty"`
	Unresolved []string `json:"unresolved,omitempty"`
	Recurring  []string `json:"recurring,omitempty"`
}

// progress detects corrections that go nowhere: unchanged or cycling content, or a defect that keeps coming
// back. There is no fixed round limit; a stalled task needs a new diagnosis, not another identical attempt.
func (g *Gate) progress(key string) Progress {
	if len(g.Corrections) == 0 {
		return Progress{Status: "first-correction"}
	}
	last := g.Corrections[len(g.Corrections)-1]
	out := Progress{}
	// Only what the last correction carried can have stalled. An accepted finding it did not carry is new work
	// for the writers, whatever the content looks like.
	for _, finding := range g.accepted() {
		if !slices.Contains(last.FindingIDs, finding.ID) {
			return Progress{Status: "new-work"}
		}
	}
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
	cycle := slices.ContainsFunc(g.Corrections, func(c Correction) bool { return c.StartKey == key })
	switch {
	case key == last.StartKey:
		out.Status = "unchanged-content"
	case cycle:
		out.Status = "content-cycle"
	case len(out.Recurring) > 0:
		out.Status = "recurring-findings"
	case len(out.Resolved) == 0 && len(last.FindingIDs) > 0:
		out.Status = "no-confirmed-fix"
	default:
		out.Status = "progress"
	}
	out.Reassess = out.Status != "progress"
	return out
}

// classify reads the evidence of the present position and says what follows from it. It starts nothing.
func (g *Gate) classify(cfg *config.Config) (string, []Need, error) {
	writes := g.Request.Writes()
	if conflicts := g.unresolved(); len(conflicts) > 0 {
		if writes {
			return verdictFix, nil, nil
		}
		return verdictLead, []Need{{Kind: "conflict", What: "conflict markers remain in " + strings.Join(conflicts, ", "),
			Resolve: "A review without --fix cannot change code: report it, or `stop`."}}, nil
	}
	now, err := g.snapshot()
	if err != nil {
		return "", nil, err
	}
	owned, foreign, err := g.changes(now)
	if err != nil {
		return "", nil, err
	}
	// Nobody but the task writes in its own worktree, and what it holds is what lands: a file no stream owns
	// would land unreviewed. It is settled before anything is paid to review the rest.
	if g.Worktree != "" && len(foreign) > 0 {
		return verdictLead, []Need{{Kind: "scope", What: "the isolated task changed files outside its plan: " + strings.Join(foreign, ", "),
			Resolve: "`amend` the plan to give them to a stream, so they are reviewed with the rest; or remove them from the worktree. Then `run`."}}, nil
	}
	receipts := g.currentReceipts()
	failing := g.failingReceipts(receipts)
	for _, failure := range failing {
		if failure.Setup {
			return verdictLead, []Need{{Kind: "setup", What: "layout check of " + layoutScreen(failure.Argv) + " could not observe the page: " + toolMessage(failure.Tail),
				Detail: map[string]string{"log": failure.Log, "check": checkLabel(failure.Argv)}, environment: true,
				Resolve: "Repair the stand, or have `prepare` correct the measurement contract and `amend` it; then `run` measures again. Do not tune CSS, baselines or tolerances to get past it."}}, nil
		}
	}
	// A check that passed on these same files and fails now failed outside the code. Writers cannot repair a
	// stopped database: the lead hears of it, unless it already sent the failure to the writers.
	if writes && len(failing) > 0 && g.Remedy == "" && len(g.FixStreams) == 0 && !slices.ContainsFunc(failing, func(f CheckFailure) bool { return !f.Environment }) {
		var out []Need
		for _, failure := range failing {
			out = append(out, Need{Kind: "setup", environment: true, Detail: lastLines(failure.Tail, 12),
				What:    "check `" + checkLabel(failure.Argv) + "` passed on these same files before and fails now: the cause is outside the code — a service, a stand, the machine",
				Resolve: "Repair what its log names, then `run` runs the check again. If the code is at fault after all, decide {brief} sends the failure to the writers."})
		}
		return verdictLead, out, nil
	}
	if writes && len(failing) > 0 {
		var ids []string
		for _, failure := range failing {
			ids = append(ids, checkID(failure.Argv))
		}
		// The same check failing again after a correction aimed at it is a stall, just like a finding that returns.
		stalled := len(g.Corrections) > 0 && slices.ContainsFunc(g.Corrections[len(g.Corrections)-1].CheckIDs, func(id string) bool { return slices.Contains(ids, id) })
		if stalled && g.Reassessed == nil {
			need := g.stall("check-still-failing", failing[0].Tail)
			need.environment = true
			need.Resolve += " If the cause was outside the code and you repaired it, `run` runs the failing checks again."
			return verdictLead, []Need{need}, nil
		}
		if need, limited := g.limited(); limited {
			return verdictLead, []Need{need}, nil
		}
		return verdictFix, nil, nil
	}
	if writes && len(g.outstandingReceipts(receipts)) > 0 {
		return verdictVerify, nil, nil
	}
	key, err := g.reviewKey(now)
	if err != nil {
		return "", nil, err
	}
	quality, err := g.qualityInputs(cfg)
	if err != nil {
		return "", nil, err
	}
	contract := g.codeContract()
	if len(owned) > 0 || quality != nil {
		var last *Review
		if len(g.Reviews) > 0 {
			last = &g.Reviews[len(g.Reviews)-1]
		}
		// A correction opened after the last review is not verified by it, also when it changed no byte: the
		// reviewer still owes its answer to what the writers did or disputed.
		if last == nil || last.Key != key || last.Contract != contract || !g.independentReview(cfg, *last) || last.QualityDigest != quality.fingerprint() ||
			last.Iteration < g.Iteration {
			if reading := g.Reading; reading != nil && reading.Key == key && reading.Contract == contract && reading.QualityDigest == quality.fingerprint() {
				need := Need{Kind: "review", What: "this mode has no reviewer process: read the patch and judge it yourself",
					Detail:  map[string]string{"patch": reading.Patch, "packet": reading.Packet},
					Resolve: "decide {review: {summary, findings, quality}} — quality only when the packet has one; a code ref is path:line or path:first-last. Judge every slop_signal and every added line: does the task need it? An empty findings list is a valid result."}
				if disputed := len(g.openDisputes(g.accepted())); disputed > 0 {
					need.What = fmt.Sprintf("the writer disputes %d of your findings; its evidence is under `disputes` in the packet. Read the patch and judge again", disputed)
					need.Resolve += " A disputed finding you leave out of findings is withdrawn; one you report again under its id is upheld and goes back to be fixed."
				}
				return verdictLead, []Need{need}, nil
			}
			return verdictVerify, nil, nil
		}
	} else if g.Request.ReviewFirst() {
		return verdictVerify, nil, nil
	}
	if g.visualRequired() {
		run := g.currentVisual(key)
		switch {
		case run == nil:
			return verdictLead, []Need{{Kind: "scenarios", What: "the opted-in browser scenarios were not observed on the present code",
				Resolve: "run {visual: {url, origins}} observes them; visual {skip_reason} or {exclusions} records why they are not."}}, nil
		case len(g.unobserved(run, nil)) > 0:
			return verdictLead, []Need{{Kind: "scenarios", What: "browser scenarios were not observed: " + strings.Join(g.unobserved(run, nil), ", "),
				Resolve: "run {visual: {url, origins}} again, or list them in visual {exclusions} with reasons."}}, nil
		}
	}
	if !writes {
		return verdictClean, nil, nil // a review of existing work ends with its report: the findings are the result
	}
	if len(g.accepted()) > 0 {
		if progress := g.progress(key); progress.Reassess && g.Reassessed == nil && !g.Ruled {
			return verdictLead, []Need{g.stall(progress.Status, strings.Join(append(progress.Recurring, progress.Unresolved...), ", "))}, nil
		}
		if need, limited := g.limited(); limited {
			return verdictLead, []Need{need}, nil
		}
		return verdictFix, nil, nil
	}
	var needs []Need
	for _, finding := range g.undecided(cfg) {
		need := Need{Kind: "finding", ID: finding.ID, What: fmt.Sprintf("%s %s at %s:%d — %s", finding.Severity, finding.Axis, finding.Path, finding.Line, oneLine(finding.Evidence)),
			Resolve: "decide {findings: [{id, decision: accepted|rejected, evidence}]}: rejected needs contrary evidence from the code; accepted sends it back to be fixed."}
		detail := map[string]any{"impact": finding.Impact, "remedy": finding.Remedy}
		if dispute, disputed := g.Disputes[finding.ID]; disputed {
			need.What = "disputed by its author and upheld by the reviewer: " + need.What
			detail["author_evidence"] = dispute.Evidence
		}
		switch g.Dispositions[finding.ID].Decision {
		case "needs-approval":
			need.What = "waits for an approval: " + need.What
		case "deferred":
			need.What = "deferred, and its severity still blocks: " + need.What
		}
		need.Detail = detail
		needs = append(needs, need)
	}
	if quality != nil {
		if report := g.qualityReport(); report != nil {
			needs = append(needs, g.unmet(report, quality)...)
		}
	}
	if len(needs) > 0 {
		return verdictLead, needs, nil
	}
	return verdictClean, nil, nil
}

// unmet turns the criteria that keep the position from being accepted into needs.
func (g *Gate) unmet(report *QualityReport, quality *qualityInputs) []Need {
	var needs []Need
	for _, result := range report.Requirements {
		index := slices.IndexFunc(quality.Requirements, func(r criterion) bool { return r.ID == result.ID })
		if index < 0 || result.Status == "verified" || !quality.Requirements[index].Due {
			continue
		}
		needs = append(needs, Need{Kind: "requirement", ID: result.ID, What: fmt.Sprintf("%s is %s: %s", result.ID, result.Status, oneLine(result.Reason)),
			Detail: quality.Requirements[index].Text,
			Resolve: "If code is missing: decide {requirements: [{id, action: \"fix\", note}]} sends it to the writers. If proof is missing: collect it (`check`, a browser pass) and `run`. " +
				"If the requirement belongs to a later stage: `amend` the plan so that stage covers it. Never weaken a requirement to pass."})
	}
	for _, result := range report.Rules {
		index := slices.IndexFunc(quality.Rules, func(r reviewRule) bool { return r.ID == result.ID })
		if index < 0 || quality.Rules[index].Mode != "blocking" || result.Status == "pass" || result.Status == "not_applicable" {
			continue
		}
		needs = append(needs, Need{Kind: "rule", ID: result.ID, What: fmt.Sprintf("rule %s is %s: %s", result.ID, result.Status, oneLine(result.Evidence)),
			Resolve: "A defect belongs in a finding: decide {requirements: [{id, action: \"fix\", note}]} sends the rule's failure to the writers. " +
				"Missing evidence: make it available and `run`, or `pause` when it cannot be had."})
	}
	return needs
}

func (g *Gate) stall(status, detail string) Need {
	return Need{Kind: "stall", What: "the last correction made no verified progress (" + status + ")", Detail: detail,
		Resolve: "Diagnose again instead of repeating: `consult` can investigate. Then decide {reassessment: {evidence, changed_approach}}; " +
			"the writers get it and one more step of effort. `pause` and ask the user when a decision is missing."}
}

func (g *Gate) limited() (Need, bool) {
	if g.AutoRounds < autoRounds {
		return Need{}, false
	}
	return Need{Kind: "limit", What: fmt.Sprintf("%d corrections ran in a row in this stage without you looking", g.AutoRounds),
		Resolve: "`show findings` shows what keeps coming back. `run` continues; `amend` or `pause` when the plan or a decision is what is wrong."}, true
}

// Start validates a plan, opens a task for it and runs it.
func Start(cfg *config.Config, plan *Plan, name string, implement bool, input RunInput) (any, error) {
	resolved, err := Resolve(cfg, plan)
	if err != nil {
		return nil, err
	}
	switch request := &resolved.Request; {
	case request.Action == "plan" && !implement:
		return nil, errors.New("this plan is a proposal from a planning request. Start it with `--implement` once the user authorized the implementation; the flag is your statement that they did")
	case request.Action == "plan":
		request.Action = "implement"
	case !request.Writes() && !request.ReviewFirst():
		return nil, fmt.Errorf("%s is read-only; use `agentgate consult`", request.Action)
	}
	if _, err := loadReviewRules(cfg, resolved.Plan.Files()); err != nil {
		return nil, err
	}
	if err := requireTools(resolved.Plan); err != nil {
		return nil, err
	}
	if !resolved.Request.ReviewFirst() {
		if err := resolved.requireArchitecture(); err != nil {
			return nil, err
		}
		if _, err := loadUI(resolved.Plan); err != nil {
			return nil, err
		}
	}
	if err := resolved.independent(); err != nil {
		return nil, err
	}
	gate, err := begin(resolved, name)
	if err != nil {
		return nil, err
	}
	if proposal := loadProposal(name); proposal != nil && proposal.Digest == digest(plan) {
		gate.Preparation = proposal.Preparation
		if fileExists(proposal.ContextPack) {
			gate.ContextPack = proposal.ContextPack
		}
	}
	if err := gate.save(); err != nil {
		return nil, err
	}
	return Run(cfg, gate, input)
}

// independent refuses a plan whose reviewers could not be independent of its writers: the model that writes a
// stage and every permitted substitute of a review lane would be the same.
func (r *Resolved) independent() error {
	if !r.Request.Writes() {
		return nil
	}
	cfg := r.Config
	for index, stage := range r.Plan.Stages {
		mode := cfg.Modes[r.Modes[index]]
		var authors []string
		for _, stream := range stage.Streams {
			authors = append(authors, writerRoute(cfg, r.Modes[index], stream, 0).Model)
		}
		for _, lane := range mode.Review {
			route := cfg.MustRoute(lane.Route)
			free := func(route config.Route) bool {
				return !slices.Contains(authors, route.Model) && !slices.Contains(cfg.ReviewersExcluded, route.Alias)
			}
			if free(route) || slices.ContainsFunc(cfg.Alternates(route), free) {
				continue
			}
			return fmt.Errorf("stage %q: no reviewer independent of the writer is configured for %s; change modes.%s.review or its fallbacks before starting",
				stage.Name, lane.Route, r.Modes[index])
		}
	}
	return nil
}

// Run takes every determined step of the task and stops where a person is needed.
func Run(cfg *config.Config, gate *Gate, input RunInput) (any, error) {
	if err := gate.can(ActRun); err != nil {
		return nil, err
	}
	gate.proceed()
	// A task of an earlier format left the modes of the stages it had not opened undecided.
	if slices.Contains(gate.Modes, "") {
		resolved, err := gate.resolve(cfg)
		if err != nil {
			return nil, err
		}
		for index, mode := range gate.Modes {
			if mode == "" {
				gate.Modes[index] = resolved.Modes[index]
			}
		}
	}
	did := []string{}
	stale := 0
	first := true
	for {
		if err := ops.Checkpoint(); err != nil {
			return nil, err
		}
		switch gate.Step {
		case StepWrite:
			if gate.Round == nil {
				if err := gate.openStage(cfg); err != nil {
					if failure := (*designError)(nil); errors.As(err, &failure) {
						return gate.report(cfg, did), gate.save()
					}
					return nil, err
				}
			}
			complete, ran, completed, err := gate.write(cfg)
			if err != nil {
				return nil, err
			}
			if ran > 0 {
				did = append(did, fmt.Sprintf("writers: %d of %d streams completed (%s)", completed, ran, gate.Round.Batch))
			}
			if !complete {
				return gate.report(cfg, did), gate.save()
			}
			if err := gate.setStep(StepVerify); err != nil {
				return nil, err
			}
			if err := gate.save(); err != nil {
				return nil, err
			}
		case StepVerify, StepDecide:
			verdict, needs, err := gate.classify(cfg)
			if err != nil {
				return nil, err
			}
			// The lead runs the task again while a check fails for a reason that may lie outside the code: the
			// one thing a second look can change. It costs no model.
			again := first && verdict == verdictLead && slices.ContainsFunc(needs, func(need Need) bool { return need.environment })
			first = false
			if verdict == verdictVerify || input.Recheck || input.Visual != nil || again || gate.DesignFailure != "" {
				// The evidence is not current: declared checks, then independent review.
				if err := gate.verify(cfg, input, &did); err != nil {
					if failure := (*designError)(nil); errors.As(err, &failure) {
						if err := gate.setStep(StepDecide); err != nil {
							return nil, err
						}
						return gate.report(cfg, did), gate.save()
					}
					return nil, err
				}
				input.Visual, input.Recheck = nil, false
				if verdict, _, err = gate.classify(cfg); err != nil {
					return nil, err
				}
				if verdict == verdictVerify {
					// Something changed between reading the evidence and judging it. Read it again, a bounded
					// number of times: evidence that never holds still is reported, not chased.
					if stale++; stale > 2 {
						return nil, errors.New("the files or the criteria keep changing while they are verified; stop what is changing them, then `run`")
					}
					continue
				}
			}
			stale = 0
			switch verdict {
			case verdictFix:
				note, err := gate.openCorrection(cfg, true)
				if err != nil {
					return nil, err
				}
				did = append(did, note)
			case verdictClean:
				note, err := gate.accept(cfg)
				if err != nil {
					return nil, err
				}
				did = append(did, note)
				if gate.Pause != nil || !gate.active() {
					return gate.report(cfg, did), nil
				}
			default:
				if err := gate.setStep(StepDecide); err != nil {
					return nil, err
				}
				return gate.report(cfg, did), gate.save()
			}
		default:
			return gate.report(cfg, did), nil
		}
	}
}

// openStage prepares the present stage for its writers: what the declared checks say before any writing, the
// designs it builds, and its first round.
func (g *Gate) openStage(cfg *config.Config) error {
	if _, err := g.admit(cfg); err != nil {
		return err
	}
	if len(g.Baselines) == 0 {
		if err := g.baselineChecks(); err != nil {
			return err
		}
	}
	// Measuring a page before the work is only a before-image while nothing was written yet. A stage reopened
	// after a failed preparation must not take written code as its baseline.
	now, err := g.snapshot()
	if err != nil {
		return err
	}
	if err := g.prepareDesigns(len(g.Writers) == 0 && now == g.StageTree); err != nil {
		return err
	}
	g.openRound()
	return g.save()
}

// openCorrection sends what verification found back to the writers that own it, in their own sessions.
func (g *Gate) openCorrection(cfg *config.Config, auto bool) (string, error) {
	if !g.Request.Writes() {
		return "", errors.New("a review without --fix has no authority to change code")
	}
	now, err := g.snapshot()
	if err != nil {
		return "", err
	}
	key, err := g.reviewKey(now)
	if err != nil {
		return "", err
	}
	failing, conflicts, accepted := g.failing(), g.unresolved(), g.accepted()
	streams := g.streams()
	touched := map[string]bool{}
	var ids, checkIDs []string
	for _, finding := range accepted {
		ids = append(ids, finding.ID)
		target := g.target(finding)
		if target == nil {
			target = &streams[0]
		}
		touched[target.Name] = true
	}
	// A failing check does not say which stream broke it: every stream gets it unless the lead named the owners.
	for _, failure := range failing {
		checkIDs = append(checkIDs, checkID(failure.Argv))
	}
	if len(failing) > 0 {
		for _, stream := range streams {
			if !stream.Done && (len(g.FixStreams) == 0 || slices.Contains(g.FixStreams, stream.Name)) {
				touched[stream.Name] = true
			}
		}
	}
	for _, file := range conflicts {
		if stream := g.owner(file); stream != nil {
			touched[stream.Name] = true
		} else {
			touched[streams[0].Name] = true
		}
	}
	var round []Stream
	for _, stream := range streams {
		if touched[stream.Name] {
			stream.After, stream.Done = nil, false
			round = append(round, stream)
		}
	}
	if len(round) == 0 {
		return "", errors.New("there is nothing to correct: no accepted finding, failing check or conflict names a stream of the present stage")
	}
	// More effort is earned, not assumed: a correction that made no verified progress, or the lead's explicit
	// request, raises the writers one step, up to the mode's ceiling.
	if g.Reassessed != nil || g.Escalate {
		g.Boost++
	}
	remedy := g.Remedy
	if strings.TrimSpace(remedy) == "" {
		remedy = "Diagnose and correct the accepted findings and failing checks in this assignment. Repair the root cause, preserve the contract, and report the diagnosis and actual checks."
	}
	if g.Reassessed != nil {
		remedy += "\n\nNew diagnosis from the lead: " + g.Reassessed.Evidence + "\nChanged approach: " + g.Reassessed.ChangedApproach
	}
	g.Iteration++
	g.Rounds++
	g.Corrections = append(g.Corrections, Correction{Iteration: g.Iteration, FindingIDs: ids, StartKey: key, CheckIDs: checkIDs, Brief: remedy, Reassessment: g.Reassessed})
	g.Round = &Round{Batch: g.batch(), Streams: round, Correction: true, Auto: auto, Findings: accepted, Remedy: remedy, Boost: g.Boost, Failures: failing, Conflicts: conflicts}
	g.Reassessed, g.Escalate, g.Ruled, g.Remedy, g.FixStreams, g.Reading = nil, false, false, "", nil, nil
	if auto {
		g.AutoRounds++
	}
	if err := g.setStep(StepWrite); err != nil {
		return "", err
	}
	names := make([]string, len(round))
	for index, stream := range round {
		names[index] = stream.Name
	}
	what := []string{}
	if len(accepted) > 0 {
		what = append(what, fmt.Sprintf("%d findings", len(accepted)))
	}
	if len(failing) > 0 {
		what = append(what, fmt.Sprintf("%d failing checks", len(failing)))
	}
	if len(conflicts) > 0 {
		what = append(what, fmt.Sprintf("%d conflicted files", len(conflicts)))
	}
	return fmt.Sprintf("sent %s back to %s", strings.Join(what, ", "), strings.Join(names, ", ")), g.save()
}

// workSummary is what the writers of the present position reported, assembled into one record: the summary of
// an accepted stage and its hand-off to the next are the writers' own words, not prose the lead writes. A
// stream that was corrected says what it built first and what it did after the review: its last report alone
// would describe the correction and lose the work.
func (g *Gate) workSummary() string {
	first, last := map[string]*Writer{}, map[string]*Writer{}
	var names []string
	for _, writer := range g.Writers {
		if writer.Status != "COMPLETED" {
			continue
		}
		if first[writer.Stream] == nil {
			first[writer.Stream] = writer
			names = append(names, writer.Stream)
		}
		last[writer.Stream] = writer
	}
	sort.Strings(names)
	clip := func(text string, limit int) string {
		text = oneLine(text)
		if runes := []rune(text); len(runes) > limit {
			return string(runes[:limit]) + "…"
		}
		return text
	}
	var parts []string
	for _, name := range names {
		summary := clip(first[name].Summary, 500)
		if last[name] != first[name] {
			summary += " After review: " + clip(last[name].Summary, 300)
		}
		if len(names) > 1 {
			summary = name + ": " + summary
		}
		parts = append(parts, summary)
	}
	if len(parts) == 0 {
		return "reviewed as it was; nothing was written"
	}
	return strings.Join(parts, " | ")
}

// accept closes the present position and opens what follows: the next stage, the pass over the whole change,
// or the end of the task.
func (g *Gate) accept(cfg *config.Config) (string, error) {
	if err := ops.Checkpoint(); err != nil {
		return "", err
	}
	now, err := g.snapshot()
	if err != nil {
		return "", err
	}
	// The last position was already recorded when its completion could not be committed: only finish again.
	if g.Whole || g.Stage >= len(g.Plan.Stages) || len(g.Accepted) > g.Stage {
		return g.finish(now)
	}
	stage := g.stage()
	record := StageRecord{Name: stage.Name, Mode: g.Mode, Summary: g.workSummary(), AcceptedAt: time.Now().Unix(),
		Rounds: len(g.Corrections), Findings: len(g.Findings()), Tree: now, Handoffs: map[string]string{}, Writers: map[string]WriterRef{}}
	for name, writer := range g.latest() {
		if writer.Handoff != "" {
			record.Handoffs[name] = writer.Handoff
		}
		if writer.Session != "" {
			record.Writers[name] = WriterRef{Route: writer.Route, Dir: writer.Dir, Session: writer.Session}
		}
	}
	record.Archive = filepath.Join(g.dir(), "stages", fmt.Sprintf("%d-%s.json", g.Stage+1, stage.Name))
	if err := os.MkdirAll(filepath.Dir(record.Archive), 0o700); err != nil {
		return "", err
	}
	if err := ops.WriteJSON(record.Archive, map[string]any{"stage": stage, "mode": g.Mode, "summary": record.Summary, "writers": g.Writers, "reviews": g.Reviews,
		"dispositions": g.Dispositions, "resolutions": g.Resolutions, "disputes": g.Disputes, "checks": g.Checks, "corrections": g.Corrections,
		"slop_signals": g.Signals, "foreign_changes": g.Foreign}); err != nil {
		return "", err
	}
	single := len(g.Plan.Stages) == 1
	if g.Worktree != "" && !single {
		if err := g.commitTask("dev: " + stage.Name + ": " + firstLine(record.Summary)); err != nil {
			return "", err
		}
		commit, _ := git("rev-parse", "--short", "HEAD")
		record.Commit = strings.TrimSpace(commit)
	}
	g.Accepted = append(g.Accepted, record)
	last := g.Stage+1 == len(g.Plan.Stages)
	if last && !integrationNeeded(g.overallMode(), len(g.Plan.Stages)) {
		// The task ends here: what the last stage's workers did stays readable in the finished task.
		return g.finish(now)
	}
	g.AuthorModels = g.authors()
	g.Writers, g.Reviews, g.ReviewError, g.Reading = nil, nil, "", nil
	g.VisualRuns, g.VisualSkip = nil, ""
	g.Dispositions, g.Resolutions, g.Baselines, g.Disputes = map[string]Disposition{}, map[string]string{}, map[string]int{}, map[string]Dispute{}
	g.Checks, g.Corrections, g.Round, g.Demands = nil, nil, nil, nil
	g.Rounds, g.Iteration, g.Boost, g.AutoRounds = 0, 0, 0, 0
	g.Signals, g.Conflicts, g.Foreign = nil, nil, nil
	g.Stage++
	// The lead reports an accepted stage to the user: what its writers did travels with the news.
	done := oneLine(record.Summary)
	if runes := []rune(done); len(runes) > 300 {
		done = string(runes[:300]) + "…"
	}
	note := fmt.Sprintf("stage %s accepted: %s", record.Name, done)
	if g.Stage < len(g.Plan.Stages) {
		g.Mode, g.StageTree = g.Modes[g.Stage], now
		if err := g.setStep(StepWrite); err != nil {
			return "", err
		}
		if g.Request.Step {
			g.pause(PauseStep, "stage "+record.Name+" is accepted; the user asked to confirm each stage before the next one starts")
		}
		return note, g.save()
	}
	// Per-stage reviews do not see the seams. The whole change gets one more pass, measured from where the
	// task began.
	g.Whole, g.Mode, g.StageTree = true, g.overallMode(), g.BaseTree
	return note + "; every stage is accepted, the whole change is reviewed next", g.save()
}

// overallMode is the most thorough mode of a stage.
func (g *Gate) overallMode() string {
	mode := g.Mode
	for _, candidate := range g.Modes {
		if modeRank[candidate] > modeRank[mode] {
			mode = candidate
		}
	}
	return mode
}

// finish completes the task.
func (g *Gate) finish(now string) (string, error) {
	summary := g.workSummary()
	if len(g.Plan.Stages) > 1 {
		var parts []string
		for _, record := range g.Accepted {
			parts = append(parts, record.Name+": "+oneLine(record.Summary))
		}
		summary = strings.Join(parts, " | ")
	}
	key, err := g.taskKey(now)
	if err != nil {
		return "", err
	}
	if g.Worktree != "" {
		if err := g.commitTask("dev: " + firstLine(g.Request.Task)); err != nil {
			return "", err
		}
	}
	if err := g.setStep(StepDone); err != nil {
		return "", err
	}
	g.Final, g.Pause = &Final{Summary: summary, Tree: now, Key: key, At: time.Now().Unix()}, nil
	return "task complete", g.save()
}
