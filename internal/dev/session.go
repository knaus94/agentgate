package dev

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/knaus94/agentgate/internal/config"
)

// A task the session writes keeps the gate and changes the author: the lead's own session — which holds the
// conversation, the user's decisions and the whole repository — writes every stage, and the tool does what it
// did for sandboxed writers: it audits the scope, runs the declared checks, places the readings and accepts.

// executor is who writes the task. A task of 0.21 or earlier has none recorded: sandboxed writers wrote it.
func (g *Gate) executor() string {
	if g.Executor == "" {
		return "workers"
	}
	return g.Executor
}

// sessionWrites says that the lead's own session writes this task.
func (g *Gate) sessionWrites() bool { return g.Executor == "session" }

// process is the mode of the present position as this task runs it.
func (g *Gate) process(cfg *config.Config) config.Mode { return cfg.Process(g.executor(), g.Mode) }

// sessionRoute names the session as the author of a stage.
func sessionRoute() config.Route {
	return config.Route{Alias: "session", Provider: config.Lead(), Model: leadModel()}
}

// routeName is a route as a person reads it: the session and the lead have no effort the tool chose.
func routeName(route config.Route) string {
	switch route.Alias {
	case "session":
		if route.Model != "" {
			return "the session (" + route.Model + ")"
		}
		return "the session"
	case "lead":
		return "the lead"
	}
	return route.String()
}

// risky says that a stage itself changes something a named risk is about. The plan's assessment does not
// count: a risk named once for a plan of twenty stages says nothing about any one of them.
func risky(stage Stage) bool { return stage.Assessment != nil && len(stage.Assessment.Risks) > 0 }

// checkpoint says whether the mode's reader reads when this stage is written: a stage the plan marks because
// later ones build on it, and in heavy and ultra a stage that changes something risky. Light reads once, at
// the end.
func (g *Gate) checkpoint(stage Stage) bool {
	switch {
	case g.Mode == "light":
		return false
	case stage.Checkpoint:
		return true
	}
	return modeRank[g.Mode] >= modeRank["heavy"] && risky(stage)
}

// reviewDue says whether the present position is read before it is accepted. Sandboxed writers are read at
// every stage. A task the session writes is read at its checkpoints and, after the last stage, as a whole.
func (g *Gate) reviewDue() bool {
	if !g.sessionWrites() || g.Request.ReviewFirst() || g.Whole || len(g.Plan.Stages) < 2 || g.Stage >= len(g.Plan.Stages) {
		return true
	}
	if g.Stage == len(g.Plan.Stages)-1 {
		return false // the last stage is read with the whole change
	}
	return g.checkpoint(g.Plan.Stages[g.Stage])
}

// spanStart is the first stage of what the present reading covers: the stage after the last one that was
// read. For sandboxed writers that is the present stage.
func (g *Gate) spanStart() int {
	index := g.stageIndex()
	if index < 0 || !g.sessionWrites() || !g.reviewDue() {
		return index
	}
	for position := min(index, len(g.Accepted)) - 1; position >= 0; position-- {
		if len(g.Accepted[position].Reviewers) > 0 {
			return position + 1
		}
	}
	return 0
}

// wholePass says whether the whole change gets its own pass after the last stage.
func (g *Gate) wholePass() bool {
	if g.sessionWrites() {
		return len(g.Plan.Stages) > 1
	}
	return integrationNeeded(g.overallMode(), len(g.Plan.Stages))
}

// unsettled are the changes outside the plan that have to be settled before anything is judged: all of them
// in a task's own worktree, and in a task the session writes those the session has not said are somebody
// else's. In a checkout shared with other sessions' writers a foreign change is only reported.
func (g *Gate) unsettled(foreign []string) []string {
	switch {
	case g.Worktree != "":
		return foreign
	case g.sessionWrites() && g.Request.Writes():
		return subtract(foreign, g.Disowned)
	}
	return nil
}

// scopeNeed asks the session whose the changes outside its plan are.
func (g *Gate) scopeNeed(foreign []string) Need {
	return Need{Kind: "scope", What: "files changed outside the plan since this position opened: " + strings.Join(foreign, ", "),
		Resolve: "Yours: `run {adopt: [paths]}` gives them to the open stage, so they are read with the rest. Somebody else's — the user's edit, " +
			"another session's task: `run {disown: [paths]}` leaves them out of this task.",
		Command: "run", Input: map[string]any{"adopt": foreign}}
}

// adopt takes files the session changed outside the plan into the open stage.
func (g *Gate) adopt(cfg *config.Config, paths []string) (string, error) {
	if !g.sessionWrites() {
		return "", errors.New("adopt is for a task the session writes; `amend` {scope} gives a writer's stream more files")
	}
	stage := g.Plan.Stages[len(g.Plan.Stages)-1]
	if index := g.stageIndex(); index >= 0 {
		stage = g.Plan.Stages[index]
	}
	next, err := Operations{Update: "the session changed these files outside the plan and takes them into it: " + strings.Join(paths, ", "),
		Scope: []ScopeChange{{Stream: stage.Streams[0].Name, Stage: stage.Name, Add: paths}}}.apply(g.Plan, g.stageIndex())
	if err != nil {
		return "", err
	}
	if _, err := Amend(cfg, g, next); err != nil {
		return "", err
	}
	return "adopted into " + stage.Name + ": " + strings.Join(paths, ", "), nil
}

// disown records changes outside the plan that are not this task's.
func (g *Gate) disown(paths []string) (string, error) {
	if !g.sessionWrites() {
		return "", errors.New("disown is for a task the session writes")
	}
	clean, err := cleanPaths(paths)
	if err != nil {
		return "", err
	}
	for _, path := range clean {
		if !slices.Contains(g.Disowned, path) {
			g.Disowned = append(g.Disowned, path)
		}
	}
	return "left out of the task as somebody else's: " + strings.Join(clean, ", "), nil
}

// sessionWrite closes the writing of the present round with the session's own report, and scans the lines the
// position added for literal leftovers. It reports false while the session still owes the writing.
func (g *Gate) sessionWrite(cfg *config.Config, written *Written) (string, bool, error) {
	if written == nil {
		return "", false, nil
	}
	if strings.TrimSpace(written.Summary) == "" {
		return "", false, errors.New("written needs summary: what you changed, and what you left")
	}
	if _, err := g.admit(cfg); err != nil {
		return "", false, err
	}
	route := sessionRoute()
	for index, stream := range g.pending() {
		// The directory is only a name: the panel reads a writer's stream from it, and no process ever ran there.
		record := &Writer{Stream: stream.Name, Route: route, Dir: filepath.Join(g.dir(), "writers", stream.Name+"-session"), Status: "COMPLETED",
			Summary: written.Summary, Blockers: []string{}, Deviations: written.Deviations, Batch: g.Round.Batch}
		if index == 0 && g.Round.Correction {
			g.takeDisputes(record, written.Disputes)
		}
		g.Writers = append(g.Writers, record)
	}
	g.recordLeft()
	note := "the session's writing is recorded (" + g.Round.Batch + ")"
	if owned, _, err := g.changes(g.Left); err == nil && g.Left != "" {
		paths := make([]string, len(owned))
		for index, change := range owned {
			paths[index] = change.Path
		}
		if g.Signals = g.signals(g.Left, paths); len(g.Signals) > 0 {
			var lines []string
			for _, signal := range g.Signals[:min(len(g.Signals), 8)] {
				lines = append(lines, fmt.Sprintf("%s:%d %s", signal.Path, signal.Line, signal.Kind))
			}
			note += fmt.Sprintf("; the scan of the added lines found %d possible leftovers (leads, not verdicts): %s", len(g.Signals), strings.Join(lines, ", "))
		}
	}
	return note, true, g.save()
}

// takeDisputes records the author's objections to findings of the present correction. A dispute counts only
// for a finding the correction carries, and only once: a finding the reviewer or the lead upheld is to be fixed.
func (g *Gate) takeDisputes(record *Writer, disputes []Dispute) {
	record.Disputes = nil
	for _, dispute := range disputes {
		known := slices.ContainsFunc(g.Round.Findings, func(finding Finding) bool { return finding.ID == dispute.ID })
		if previous, again := g.Disputes[dispute.ID]; !known || strings.TrimSpace(dispute.Evidence) == "" || (again && previous.Upheld) {
			continue
		}
		dispute.Stream = record.Stream
		record.Disputes = append(record.Disputes, dispute)
		g.Disputes[dispute.ID] = dispute
	}
}

// writeNeed is what the session owes at a round: the stage to write, or the fixes a verification asked for.
func (g *Gate) writeNeed() Need {
	if g.Round.Correction {
		return g.fixNeed()
	}
	stage := g.stage()
	detail := map[string]any{"checks": checkLines(g.Plan), "requirements": g.requirementsFor(),
		"rules": "Write this stage and nothing of a later one. Follow `like`: read it before you write, and take from it where files go, how they are " +
			"named, how errors, data access and configuration are handled. Make the smallest correct change: no extra options, no comments that " +
			"narrate, no checks for states the code cannot reach, no leftovers. Read your own diff once as its reviewer before you report."}
	what := "write the change; the tool then runs the declared checks"
	if stage != nil {
		detail["goal"], detail["streams"] = stage.Goal, stage.Streams
		detail["checks"] = checkLabels(g.checks())
		if len(stage.Like) > 0 {
			detail["like"] = stage.Like
		}
		if stage.LikeWaiver != "" {
			detail["like_waiver"] = stage.LikeWaiver
		}
		if len(stage.Acceptance) > 0 {
			detail["acceptance"] = stage.Acceptance
		}
		what = "write the change: " + oneLine(stage.Goal)
		if position := g.position(); position != nil {
			detail["stage"] = stage.Name
			what = fmt.Sprintf("write stage %s (%d of %d): %s", stage.Name, g.Stage+1, len(g.Plan.Stages), oneLine(stage.Goal))
		}
	}
	if g.ContextPack != "" {
		detail["grounding"] = g.ContextPack
	}
	if designs := g.designs(); len(designs) > 0 {
		detail["design"] = designs
	}
	return Need{Kind: "write", What: what, Detail: detail,
		Resolve: "Write it in this session, then `run {written: {summary, deviations}}`: the tool audits the scope, runs the checks and scans the added lines. " +
			"A question only the user can answer: `pause` with it, ask, then go on.",
		Command: "run", Input: map[string]any{"written": map[string]any{"summary": "<what you changed, and what you left>", "deviations": []string{}}}}
}

// fixNeed is a correction as the session reads it: the failing checks and the findings to fix or dispute.
func (g *Gate) fixNeed() Need {
	detail := map[string]any{}
	var parts []string
	if len(g.Round.Failures) > 0 {
		failing := []map[string]string{}
		for _, failure := range g.Round.Failures {
			failing = append(failing, map[string]string{"check": checkLabel(commandArgv(failure.Argv)), "output_tail": lastLines(failure.Tail, 20), "log": failure.Log})
		}
		detail["failing_checks"] = failing
		parts = append(parts, fmt.Sprintf("%d failing checks", len(failing)))
	}
	if len(g.Round.Findings) > 0 {
		findings := []map[string]any{}
		for _, finding := range g.Round.Findings {
			item := map[string]any{"id": finding.ID, "severity": finding.Severity, "axis": finding.Axis, "where": fmt.Sprintf("%s:%d", finding.Path, finding.Line),
				"evidence": finding.Evidence, "remedy": finding.Remedy}
			if dispute, disputed := g.Disputes[finding.ID]; disputed && dispute.Upheld {
				item["upheld"] = "you disputed it and the reader kept it: fix it"
			}
			findings = append(findings, item)
		}
		detail["findings"] = findings
		parts = append(parts, fmt.Sprintf("%d findings", len(findings)))
	}
	if len(g.Round.Work) > 0 {
		detail["work"] = g.Round.Work
		parts = append(parts, "your own diagnosis")
	}
	if len(g.Round.Conflicts) > 0 {
		detail["conflicts"] = g.Round.Conflicts
		parts = append(parts, "conflict markers")
	}
	resolve := "Make the failing checks pass by repairing the cause in your code; never weaken or skip a check. Then `run {written: {summary}}`."
	input := map[string]any{"summary": "<what you fixed>"}
	if len(g.Round.Findings) > 0 {
		resolve = "Open the code each finding cites and verify it. Right: fix it with its remedy. Wrong: leave the code and return a dispute {id, evidence} " +
			"— the reader that raised it answers. Make any failing check pass by repairing the cause; never weaken or skip one. Then `run {written: {summary, disputes}}`."
		input["disputes"] = []any{}
	}
	what := "fix " + strings.Join(parts, ", ")
	if len(parts) == 0 {
		// The round was opened by an amendment: its remedy says what the plan now asks of the stage.
		what, detail["streams"] = oneLine(g.Round.Remedy), g.Round.Streams
		resolve = "Bring the stage in line with the amended plan, then `run {written: {summary}}`."
	}
	return Need{Kind: "fix", What: what, Detail: detail, Resolve: resolve,
		Command: "run", Input: map[string]any{"written": input}}
}

// checkLabels are checks as one line each.
func checkLabels(checks []Check) []string {
	out := []string{}
	for _, check := range checks {
		if !generatedDesignCheck(check) {
			out = append(out, checkLabel(check.Argv))
		}
	}
	return out
}

// spend is what the task consumed, in tokens: the lead's session from the moment the request was handed over,
// and every model process the tool started for it. It is read from the clients' own records, so it is a
// measurement of what happened, not an estimate.
func (g *Gate) spend(until int64) map[string]any {
	since := g.CreatedAt
	if g.Opened > 0 {
		since = g.Opened
	}
	session, models := leadSpend(since, until)
	roots := []string{g.dir()}
	if g.Plan.Architecture != nil {
		roots = append(roots, filepath.Join(designsDir(), g.Plan.Architecture.ID))
	}
	if g.Plan.UI != nil && g.Plan.UI.Design != "" {
		roots = append(roots, filepath.Join(designsDir(), g.Plan.UI.Design))
	}
	out := map[string]any{"runs": runSpend(roots...), "unit": "tokens: input read fresh, input read from the cache, output"}
	if len(models) > 0 {
		out["session"], out["session_models"] = session, models
	} else {
		out["session"] = "the client's record of this session could not be read"
	}
	return out
}
