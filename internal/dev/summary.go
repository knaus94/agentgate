package dev

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/update"
)

// Every command that touches a task answers with the same compact picture: where the task is, what this call
// did, what only a person can settle and which actions are legal now. What a command has of its own is under
// `result`. The lead's context is re-sent on every turn, so an answer holds what a decision needs and no
// more; `status --full` and `show` have the rest.

// needs is what only a person can settle at the present position.
func (g *Gate) needs(cfg *config.Config) []Need {
	out := []Need{}
	if !g.active() {
		return out
	}
	if g.DesignFailure != "" {
		return append(out, Need{Kind: "setup", What: g.DesignFailure, Command: "run",
			Resolve: "Repair what it names — a Figma sign-in (`agentgate figma status`), a link to a frame, the stand — then `run` tries again. " +
				"A design that cannot be had is taken out of the plan with `amend`, with the user's word in `authorized`."})
	}
	if g.Step == StepWrite {
		if g.Round == nil {
			return out
		}
		latest := g.latest()
		for _, stream := range g.Round.Streams {
			writer := latest[stream.Name]
			if writer == nil || writer.Batch != g.Round.Batch {
				continue
			}
			switch writer.Status {
			case "BLOCKED":
				out = append(out, Need{Kind: "answer", Stream: stream.Name, What: clipText(strings.Join(writer.Blockers, " | "), 1200), Detail: clipText(oneLine(writer.Summary), 400),
					Resolve: "answer {stream, answer}: the writer continues in its own session. Ask the user first when the decision is theirs; `amend` when the answer is a different plan.",
					Command: "answer", Input: map[string]any{"stream": stream.Name, "answer": "<the decision>"}})
			case "FAILED", "CANCELLED":
				out = append(out, Need{Kind: "failed", Stream: stream.Name, What: strings.ToLower(writer.Status) + ": " + clipText(oneLine(writer.Error), 400), Command: "run",
					Resolve: "`run` retries the unfinished streams and keeps the finished ones. If every account is out of quota, `agentgate accounts` says when it returns: `pause` with that reason."})
			}
		}
		return out
	}
	verdict, needs, err := g.classify(cfg)
	switch {
	case err != nil:
		return append(out, Need{Kind: "setup", What: err.Error(), Resolve: "Repair what the message names, then `run`.", Command: "run"})
	case verdict == verdictLead:
		return needs
	}
	return out
}

// next is one sentence on what to do now.
func (g *Gate) next(needs []Need) string {
	notes := ""
	if g.Landed == "" && len(g.leftNotes()) > 0 {
		notes = " The task left notes (`notes`): tell the user; on their word decide {findings: [{id, decision: accepted, evidence}]} fixes one in this task."
	}
	switch {
	case g.Dropped:
		return "The task was dropped. `forget` removes it from the record, on the user's word."
	case g.Step == StepDone && g.Worktree != "" && g.Landed == "":
		return "Done in an isolated worktree; the checkout is untouched. Report the work and the diff (`git diff HEAD..." + g.Branch +
			"` in the checkout), then ask the user how to bring it in and `land` {how}: apply (uncommitted, default), commit, branch or drop. Never land without the user's word." + notes
	case g.Step == StepDone:
		return "Done. Report the work from `receipt`: the models that ran, the checks and their results, substitutions and remaining risks." + notes
	case g.Step == StepCancelled:
		return "The task was dropped."
	case g.Pause != nil && g.Pause.Kind == PauseUser:
		return "Stopped by the user. Report INCOMPLETE. `resume` only on the user's word."
	case g.Pause != nil && g.Pause.Kind == PauseStep:
		return "Report the accepted stage to the user and wait for their word; `run` then starts the next stage."
	case g.Pause != nil:
		return "Paused: " + g.Pause.Reason + " — when it is resolved, `run` (or answer, decide, amend) continues."
	case len(needs) > 0 && g.Step == StepWrite:
		return "Settle `needs`: `answer` a blocked writer; `run` retries a failed one."
	case len(needs) > 0:
		return "Settle `needs`: each names its `command` and the `input` to send it; `<…>` is yours to write. `decide` and `answer` continue the task by themselves."
	case g.Step == StepWrite:
		return "`run` continues: writers, then declared checks, then independent review."
	default:
		return "`run` verifies: declared checks first, then independent review."
	}
}

// position names where the task is among its stages; nil for a task of one stage.
func (g *Gate) position() map[string]any {
	if len(g.Plan.Stages) < 2 {
		return nil
	}
	if g.Whole || g.Stage >= len(g.Plan.Stages) {
		return map[string]any{"name": "the whole change", "accepted": len(g.Accepted), "of": len(g.Plan.Stages)}
	}
	return map[string]any{"name": g.Plan.Stages[g.Stage].Name, "index": g.Stage + 1, "of": len(g.Plan.Stages), "accepted": len(g.Accepted)}
}

func (g *Gate) writerLines() []map[string]any {
	var names []string
	latest := g.latest()
	for name := range latest {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []map[string]any{}
	for _, name := range names {
		writer := latest[name]
		line := map[string]any{"stream": name, "status": writer.Status, "route": writer.Route.String(), "summary": clipText(firstLine(writer.Summary), 220)}
		if writer.Stale {
			line["status"] = "AMENDED" // reported under the plan before the amendment
		}
		if writer.Status == "RUNNING" {
			for key, value := range workerProgress(writer.Dir) {
				line[key] = value
			}
		}
		out = append(out, line)
	}
	return out
}

// workerProgress says how a running worker is doing: how long it runs, how long it has been silent and what
// it did last. It reads the event stream `watch` shows.
func workerProgress(dir string) map[string]any {
	out := map[string]any{}
	var receipt struct {
		StartedAt int64 `json:"started_at"`
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "process.json")); err == nil && json.Unmarshal(raw, &receipt) == nil && receipt.StartedAt > 0 {
		out["running_s"] = time.Now().Unix() - receipt.StartedAt
	}
	if info, err := os.Stat(filepath.Join(dir, "events.jsonl")); err == nil {
		out["quiet_s"] = int64(time.Since(info.ModTime()).Seconds())
	}
	if events := laneEvents(dir); len(events) > 0 {
		last := events[len(events)-1]
		out["last_event"] = clipText(last.Glyph()+" "+oneLine(last.Text), 160)
	}
	return out
}

// tally counts the open findings of the present position.
func (g *Gate) tally(cfg *config.Config) (open, blocking, notes []Finding) {
	for _, finding := range g.Findings() {
		switch _, resolved := g.Resolutions[finding.ID]; {
		case resolved:
		case g.Dispositions[finding.ID].Decision == "rejected":
		case g.isNote(cfg, finding):
			notes = append(notes, finding)
		default:
			open = append(open, finding)
			if g.blocking(cfg, finding) {
				blocking = append(blocking, finding)
			}
		}
	}
	return open, blocking, notes
}

// noteLine is a finding in one line, as a person reads it in a list.
func noteLine(finding Finding) string {
	where := finding.Path
	if finding.Line > 0 {
		where = fmt.Sprintf("%s:%d", finding.Path, finding.Line)
	}
	return clipText(fmt.Sprintf("%s %s %s — %s", finding.Severity, finding.Axis, where, oneLine(finding.Evidence)), 240)
}

// notes are what the task leaves unfixed, one line each: the present position's under their ids, which
// decide takes, and the earlier stages' under their stage.
func (g *Gate) notes() []string {
	out := []string{}
	for index, record := range g.Accepted {
		if index == len(g.Accepted)-1 && !g.Whole {
			break // the last stage is the present position: its notes are listed with their ids below
		}
		for _, note := range record.Notes {
			out = append(out, "["+record.Name+"] "+note)
		}
	}
	for _, finding := range g.leftNotes() {
		out = append(out, finding.ID+": "+noteLine(finding))
	}
	return out
}

// checkLines are the declared checks of the present position with their last results, as they were recorded.
func (g *Gate) checkLines() []string {
	latest := map[string]*CheckReceipt{}
	for index := range g.Checks {
		latest[checkID(g.Checks[index].Argv)] = &g.Checks[index]
	}
	out := []string{}
	for _, check := range g.checks() {
		receipt := latest[checkID(check.Argv)]
		result := "not run"
		switch {
		case receipt == nil:
		case passed(receipt) && receipt.Reused:
			result = "passed (unchanged inputs)"
		case passed(receipt):
			result = "passed"
		default:
			result = fmt.Sprintf("failed (exit %d)", receipt.ReturnCode)
		}
		out = append(out, checkLabel(check.Argv)+" — "+result)
	}
	return out
}

// reviewers are the routes that reviewed the present position.
func (g *Gate) reviewers() []string {
	out := []string{}
	for _, review := range g.Reviews {
		for _, lane := range review.Lanes {
			name := lane.Route.String()
			if lane.Route.Alias == "lead" {
				name = "the lead"
			}
			if !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	return out
}

// receipt is what the lead reports at the end: who did the work on which model, what verified it, what is
// left, and what each stage took.
func (g *Gate) receipt() map[string]any {
	writers, reviewers := []string{}, []string{}
	add := func(list *[]string, items ...string) {
		for _, item := range items {
			if !slices.Contains(*list, item) {
				*list = append(*list, item)
			}
		}
	}
	minutes := func(seconds int64) float64 { return math.Round(float64(seconds)/6) / 10 }
	stages := []map[string]any{}
	start := g.CreatedAt
	for _, record := range g.Accepted {
		var names []string
		for name := range record.Writers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			add(&writers, name+" "+record.Writers[name].Route.String())
		}
		add(&reviewers, record.Reviewers...)
		stage := map[string]any{"name": record.Name, "mode": record.Mode, "correction_rounds": record.Rounds, "findings": record.Findings,
			"notes": len(record.Notes), "minutes": minutes(record.AcceptedAt - start)}
		if record.Browser != "" {
			stage["browser"] = record.Browser
		}
		stages = append(stages, stage)
		start = record.AcceptedAt
	}
	for name, writer := range g.latest() {
		add(&writers, name+" "+writer.Route.String())
	}
	sort.Strings(writers)
	add(&reviewers, g.reviewers()...)
	end := time.Now().Unix()
	if g.Final != nil {
		end = g.Final.At
	}
	if g.Whole {
		stages = append(stages, map[string]any{"name": "the whole change", "mode": g.Mode, "correction_rounds": len(g.Corrections),
			"findings": len(g.Findings()), "notes": len(g.leftNotes()), "minutes": minutes(end - start)})
	}
	out := map[string]any{"writers": writers, "reviewers": reviewers, "checks": g.checkLines(), "notes": len(g.notes()),
		"stages": stages, "minutes": minutes(end - g.CreatedAt)}
	if preparation := loadPreparation(g.Preparation); preparation != nil {
		out["preparation"] = preparation.Route.String()
	}
	if len(g.Substitutions) > 0 {
		out["substitutions"] = g.Substitutions
	}
	if line := g.browserLine(); line != "" {
		out["browser"] = line
	}
	if g.Worktree != "" {
		out["isolated"] = g.landing()
	}
	return out
}

// report is the compact answer of a command: did is what this call changed.
func (g *Gate) report(cfg *config.Config, did []string) (out map[string]any) {
	g.reading(func() { out = g.picture(cfg, did) })
	return out
}

// picture is the compact answer as one record of the working tree shows the task.
func (g *Gate) picture(cfg *config.Config, did []string) map[string]any {
	out := map[string]any{"task": g.ID[:8], "action": g.Request.Action, "mode": g.Mode, "step": g.Step}
	if g.toolBefore != "" {
		out["notice"] = fmt.Sprintf("agentgate was %s at this task's last command and is %s now: read `agentgate guide` in full before the next command.",
			g.toolBefore, update.Version)
	}
	if position := g.position(); position != nil {
		out["stage"] = position
	}
	if g.Pause != nil {
		out["paused"] = map[string]string{"kind": g.Pause.Kind, "reason": g.Pause.Reason}
	}
	if len(did) > 0 {
		out["did"] = did
	}
	if lines := g.writerLines(); len(lines) > 0 && g.active() {
		out["writers"] = lines
	}
	needs := g.needs(cfg)
	if len(needs) > 0 {
		out["needs"] = needs
	}
	if checks := g.checks(); len(checks) > 0 && g.active() {
		current := g.currentReceipts()
		failed := []string{}
		for _, failure := range g.failingReceipts(current) {
			failed = append(failed, checkLabel(failure.Argv))
		}
		waiting := len(g.outstandingReceipts(current)) - len(failed)
		out["checks"] = map[string]any{"declared": len(checks), "passing": len(checks) - len(failed) - waiting, "failing": failed, "not_run": waiting}
	}
	if open, blocking, notes := g.tally(cfg); len(open)+len(notes) > 0 && g.active() {
		out["findings"] = map[string]int{"open": len(open), "blocking": len(blocking), "notes": len(notes)}
	}
	if len(g.Foreign) > 0 && g.active() {
		out["foreign_changes"] = g.Foreign
	}
	if len(g.Substitutions) > 0 && g.active() {
		out["substitutions"] = g.Substitutions
	}
	if g.ReviewError != "" && g.active() {
		out["review_error"] = g.ReviewError
	}
	if g.Worktree != "" {
		out["isolated"] = g.landing()
	}
	if tree := g.presentTree(); tree != "" {
		if unchecked := g.uncheckedTests(tree); len(unchecked) > 0 {
			out["unchecked_tests"] = unchecked
		}
	}
	if g.Final != nil {
		out["summary"] = clipText(g.Final.Summary, 1500)
		out["receipt"] = g.receipt()
		if !g.Request.Writes() {
			// A review of existing work ends with its findings: they are the outcome.
			open, _, notes := g.tally(cfg)
			out["outcome"] = map[string]any{"findings": open, "notes": notes}
		}
	}
	if notes := g.notes(); len(notes) > 0 && g.Step == StepDone && g.Landed == "" {
		out["notes"] = notes
	}
	actions := g.actions()
	if actions == nil {
		actions = []Action{}
	}
	out["actions"], out["next"] = actions, g.next(needs)
	return out
}

// presentTree is the working tree the answer speaks of: the one a finished task was completed at, or the one
// there is now once the writers of the round have reported. "" while writers work.
func (g *Gate) presentTree() string {
	switch {
	case g.Final != nil:
		return g.Final.Tree
	case g.Step == StepVerify || g.Step == StepDecide:
		tree, _ := g.snapshot()
		return tree
	}
	return ""
}

// respond is the answer of a command that has data of its own: the common object, that data under result.
// A next the data carries replaces the common one.
func (g *Gate) respond(cfg *config.Config, did []string, result map[string]any) map[string]any {
	out := g.report(cfg, did)
	if next, ok := result["next"].(string); ok {
		out["next"] = next
		delete(result, "next")
	}
	out["result"] = result
	return out
}

// Summary is the full read-only picture of a task: everything report has, and under result the details it
// leaves out.
func (g *Gate) Summary(cfg *config.Config) map[string]any {
	open, blocking, notes := g.tally(cfg)
	ids := func(findings []Finding) []string {
		list := []string{}
		for _, finding := range findings {
			list = append(list, finding.ID)
		}
		return list
	}
	var writers []*Writer
	for _, writer := range g.latest() {
		writers = append(writers, writer)
	}
	sort.Slice(writers, func(a, b int) bool { return writers[a].Stream < writers[b].Stream })
	dir := sessionDir(g.Cwd, g.Session)
	result := map[string]any{"id": g.ID, "writers": writers, "iteration": g.Iteration, "findings": open, "blocking": ids(blocking), "notes": notes,
		"dispositions": g.Dispositions, "disputes": g.Disputes, "checks": g.checkLines(), "checks_failed": g.failing(), "revisions": g.Revisions,
		"operation": ops.Status(dir), "timings_s": ops.Timings(dir, g.ID), "board": g.board(), "state": dir}
	if g.Round != nil {
		result["round"] = g.Round.Batch
	}
	if g.ContextPack != "" {
		result["context_pack"] = g.ContextPack
	}
	if len(g.Attachments) > 0 {
		result["attachments"] = g.Attachments
	}
	if review := g.reviewDiversity(); review != nil {
		result["review_diversity"] = review
	}
	if g.Plan.Visual != nil {
		result["visual"] = g.visualSummary()
	}
	if len(g.Signals) > 0 {
		result["slop_signals"] = g.Signals
	}
	if g.active() {
		if quality := g.qualityStatus(cfg); quality != nil {
			result["quality"] = quality
		}
	}
	if len(g.Plan.Stages) > 1 {
		result["stages"] = g.stagesSummary()
	}
	return g.respond(cfg, nil, result)
}

// stagesSummary is the progress of a task of several stages, for the full summary and the panel.
func (g *Gate) stagesSummary() []map[string]any {
	var out []map[string]any
	for index, stage := range g.Plan.Stages {
		item := map[string]any{"name": stage.Name, "goal": oneLine(stage.Goal), "status": "pending", "mode": g.Modes[index]}
		switch {
		case index < len(g.Accepted):
			record := g.Accepted[index]
			item["status"], item["mode"], item["summary"], item["commit"] = "accepted", record.Mode, oneLine(record.Summary), record.Commit
			item["correction_rounds"], item["findings"] = record.Rounds, record.Findings
		case index == g.Stage && !g.Whole:
			item["status"] = "current"
		}
		out = append(out, item)
	}
	return out
}

// showParts are what `show` takes.
var showParts = []string{"findings", "plan", "stage", "checks", "review", "architecture", "receipt", "report", "all"}

// Show prints one part of the task in full, for when the compact answer is not enough.
func Show(cfg *config.Config, gate *Gate, what string) (any, error) {
	var result map[string]any
	switch what {
	case "", "all":
		return gate.Summary(cfg), nil
	case "findings":
		open, blocking, notes := gate.tally(cfg)
		blocks := map[string]bool{}
		for _, finding := range blocking {
			blocks[finding.ID] = true
		}
		list := []map[string]any{}
		for _, finding := range open {
			item := map[string]any{"finding": finding, "blocks": blocks[finding.ID]}
			if disposition, judged := gate.Dispositions[finding.ID]; judged {
				item["decision"] = disposition
			}
			if dispute, disputed := gate.Disputes[finding.ID]; disputed {
				item["dispute"] = dispute
			}
			list = append(list, item)
		}
		result = map[string]any{"open": list, "notes": notes, "resolved": gate.Resolutions}
	case "plan":
		result = map[string]any{"plan": gate.Plan, "revisions": gate.Revisions, "name": gate.PlanName}
	case "stage", "stages":
		result = map[string]any{"position": gate.position(), "stages": gate.stagesSummary(), "accepted": gate.Accepted, "brief": gate.brief()}
	case "checks":
		result = map[string]any{"checks": gate.checkEvidence(), "failing": gate.failing()}
	case "review":
		result = gate.reviewView(cfg)
	case "architecture":
		view, err := ArchitectureView(gate.Plan)
		if err != nil {
			return nil, err
		}
		result = view
	case "receipt":
		result = gate.receipt()
	case "report", "quality":
		result = map[string]any{"quality": gate.qualityStatus(cfg), "review_diversity": gate.reviewDiversity(), "writers": gate.latest()}
	default:
		return nil, fmt.Errorf("show takes %s, not %q", strings.Join(showParts, ", "), what)
	}
	return gate.respond(cfg, nil, result), nil
}

// reviewView is what a person judges in a review and the input to answer it with: the evidence the lead owes
// a reading of, or what the reviewers found.
func (g *Gate) reviewView(cfg *config.Config) map[string]any {
	if reading := g.Reading; reading != nil {
		var packet struct {
			Changes  []Change         `json:"changes"`
			Signals  []Signal         `json:"slop_signals"`
			Quality  *qualityInputs   `json:"quality"`
			Accepted []Finding        `json:"accepted_findings"`
			Disputes []map[string]any `json:"disputes"`
		}
		raw, _ := os.ReadFile(reading.Packet)
		_ = json.Unmarshal(raw, &packet)
		changes := []string{}
		for _, change := range packet.Changes {
			line := fmt.Sprintf("%s %s +%d -%d", change.Status, change.Path, change.Added, change.Removed)
			if change.Collapsed != "" {
				line += " (not in the patch: " + change.Collapsed + ")"
			}
			changes = append(changes, line)
		}
		signals := []string{}
		for _, signal := range packet.Signals {
			raw, _ := json.Marshal(signal)
			signals = append(signals, clipText(string(raw), 200))
		}
		criteria, checks := []string{}, []string{}
		if packet.Quality != nil {
			for _, requirement := range packet.Quality.Requirements {
				due := "due now"
				if !requirement.Due {
					due = "due at " + requirement.DueAt
				}
				method := ""
				if requirement.Method != "" {
					method = ", proof by " + requirement.Method
				}
				criteria = append(criteria, fmt.Sprintf("%s (%s%s): %s", requirement.ID, due, method, clipText(oneLine(requirement.Text), 300)))
			}
			for _, rule := range packet.Quality.Rules {
				criteria = append(criteria, fmt.Sprintf("rule %s (%s): %s", rule.ID, rule.Mode, strings.Join(rule.Files, ", ")))
			}
			for id, check := range packet.Quality.Checks {
				checks = append(checks, fmt.Sprintf("%s %s — exit %d", id, checkLabel(check.Receipt.Argv), check.Receipt.ReturnCode))
			}
			sort.Strings(checks)
		}
		verify := []string{}
		for _, finding := range packet.Accepted {
			verify = append(verify, finding.ID+": "+noteLine(finding))
		}
		return map[string]any{"owed_by": "the lead", "patch": reading.Patch, "changes": changes, "slop_signals": signals, "criteria": criteria,
			"checks": checks, "verify_fixed": verify, "disputes": packet.Disputes,
			"how": "Read the patch. Judge every added line and every slop signal: does the task need it? Put each defect in findings with a path, line and remedy; " +
				"give every criterion a status, and a cause when it is not verified. An empty findings list is a valid result.",
			"command": "decide", "input": map[string]any{"review": g.reviewSkeleton(packet.Quality)}}
	}
	if len(g.Reviews) == 0 {
		return map[string]any{"reviews": 0, "next": "Nothing was reviewed yet at this position: `run` reviews once the writers have reported."}
	}
	last := g.Reviews[len(g.Reviews)-1]
	findings := []string{}
	var undecided []map[string]any
	for _, lane := range last.Lanes {
		for _, report := range lane.Reports {
			for _, finding := range report.Findings {
				state := "note"
				switch {
				case g.Resolutions[finding.ID] != "":
					state = "resolved"
				case g.Dispositions[finding.ID].Decision != "":
					state = g.Dispositions[finding.ID].Decision
				case g.blocking(cfg, finding):
					state = "blocks"
				}
				findings = append(findings, fmt.Sprintf("%s [%s] %s", finding.ID, state, noteLine(finding)))
				if state == "note" || state == "blocks" && g.Disputes[finding.ID].Upheld {
					undecided = append(undecided, map[string]any{"id": finding.ID, "decision": "<accepted|rejected>", "evidence": "<what in the code decides it>"})
				}
			}
		}
	}
	criteria := []string{}
	if report := g.qualityReport(); report != nil {
		for _, result := range report.Requirements {
			line := result.ID + " " + result.Status
			if result.Cause != "" {
				line += " (cause: " + result.Cause + ")"
			}
			criteria = append(criteria, line+": "+clipText(oneLine(result.Reason), 300))
		}
		for _, result := range report.Rules {
			criteria = append(criteria, "rule "+result.ID+" "+result.Status+": "+clipText(oneLine(result.Evidence), 300))
		}
	}
	out := map[string]any{"reviewed_by": laneRoutes(last.Lanes), "findings": findings, "criteria": criteria, "patch": filepath.Join(last.Dir, "change.patch")}
	if len(undecided) > 0 {
		out["command"], out["input"] = "decide", map[string]any{"findings": undecided}
	}
	return out
}

// Status is the compact picture of the task, or one part of it in full.
func Status(cfg *config.Config, gate *Gate, what string) (any, error) {
	if gate.legacy != nil {
		out := map[string]any{"task": gate.ID[:8], "action": gate.Request.Action, "mode": gate.Mode, "step": gate.Step,
			"saved_by_an_earlier_version": true, "actions": gate.actions()}
		next := "This task was saved by an earlier version of the tool. The next command of its own session brings it to the present format; " +
			"the original state is kept beside it as state.v1.json. Checks and review run again once, because their receipts were bound differently. "
		if gate.Pause != nil {
			out["paused"] = map[string]string{"kind": gate.Pause.Kind, "reason": gate.Pause.Reason}
		}
		switch {
		case gate.Pause != nil && gate.Pause.Kind == PauseUser:
			next += "The user stopped it: `resume` continues it, on the user's word only; then `run`."
		case gate.active():
			next += "`run` continues it."
		}
		out["next"] = next
		return out, nil
	}
	var out map[string]any
	var err error
	gate.reading(func() {
		if what == "" {
			out = gate.report(cfg, nil)
			return
		}
		var value any
		if value, err = Show(cfg, gate, what); err == nil {
			out = value.(map[string]any)
		}
	})
	if err != nil {
		return nil, err
	}
	gate.operation(out)
	return out, nil
}

// operation adds what the session's operations say: the one running now, or how the last one ended.
func (g *Gate) operation(out map[string]any) {
	record := ops.Status(sessionDir(g.Cwd, g.Session))
	if record == nil {
		return
	}
	since := time.Unix(int64(record.StartedAt), 0)
	if record.Live && record.PID != os.Getpid() {
		running := map[string]any{"action": record.Action, "since": since.Format(time.RFC3339), "running_s": int64(time.Since(since).Seconds())}
		if record.Doing != "" {
			running["now"], running["now_s"] = record.Doing, int64(time.Since(time.Unix(int64(record.DoingSince), 0)).Seconds())
		}
		out["running"] = running
		out["next"] = "`" + record.Action + "` is running; its answer comes when it ends. Meanwhile `tell` leaves the writers a note and `stop` ends it."
		return
	}
	if !record.Live {
		last := map[string]any{"action": record.Action, "state": record.State}
		if record.Error != "" {
			last["error"] = clipText(oneLine(record.Error), 400)
		}
		out["last"] = last
	}
}

// Respond answers a command about the task with the common object and the command's own data under result.
func Respond(cfg *config.Config, gate *Gate, result map[string]any) map[string]any {
	return gate.respond(cfg, nil, result)
}

// Refused answers a command that is not legal now: the task as it is, why, and what is possible instead.
func Refused(cfg *config.Config, gate *Gate, refusal *Refusal) map[string]any {
	out := gate.report(cfg, nil)
	out["refused"] = map[string]any{"action": refusal.Action, "because": refusal.Because}
	return out
}

// Failed answers a command that could not do what it was asked: the task as it is now, and why.
func Failed(cfg *config.Config, gate *Gate, err error) map[string]any {
	out := gate.report(cfg, nil)
	out["error"] = err.Error()
	return out
}
