package dev

import (
	"fmt"
	"sort"
	"strings"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
)

// Every command answers with the same compact picture: where the task is, what this call did, what only a
// person can settle and which actions are legal now. The lead's context is re-sent on every turn, so an
// answer holds what a decision needs and no more; `status --full` and `show` have the rest.

// needs is what only a person can settle at the present position.
func (g *Gate) needs(cfg *config.Config) []Need {
	out := []Need{}
	if !g.active() {
		return out
	}
	if g.DesignFailure != "" {
		return append(out, Need{Kind: "setup", What: g.DesignFailure,
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
				out = append(out, Need{Kind: "answer", Stream: stream.Name, What: strings.Join(writer.Blockers, " | "), Detail: oneLine(writer.Summary),
					Resolve: "answer {stream, answer}: the writer continues in its own session. Ask the user first when the decision is theirs; `amend` when the answer is a different plan."})
			case "FAILED", "CANCELLED":
				out = append(out, Need{Kind: "failed", Stream: stream.Name, What: strings.ToLower(writer.Status) + ": " + oneLine(writer.Error),
					Resolve: "`run` retries the unfinished streams and keeps the finished ones. If every account is out of quota, `agentgate accounts` says when it returns: `pause` with that reason."})
			}
		}
		return out
	}
	verdict, needs, err := g.classify(cfg)
	switch {
	case err != nil:
		return append(out, Need{Kind: "setup", What: err.Error(), Resolve: "Repair what the message names, then `run`."})
	case verdict == verdictLead:
		return needs
	}
	return out
}

// next is one sentence on what to do now.
func (g *Gate) next(needs []Need) string {
	switch {
	case g.Step == StepDone && g.Worktree != "" && g.Landed == "":
		return "Done in an isolated worktree; the checkout is untouched. Report the work and the diff (`git diff HEAD..." + g.Branch +
			"` in the checkout), then ask the user how to bring it in and `land` {how}: apply (uncommitted, default), commit, branch or drop. Never land without the user's word."
	case g.Step == StepDone:
		return "Done. Report the work, the checks that ran, substitutions and any limits to the user."
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
		return "Settle `needs`, each as its `resolve` says. `decide` and `answer` continue the task by themselves."
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
		summary := firstLine(writer.Summary)
		if runes := []rune(summary); len(runes) > 220 {
			summary = string(runes[:220]) + "…"
		}
		line := map[string]any{"stream": name, "status": writer.Status, "route": writer.Route.String(), "summary": summary}
		if writer.Stale {
			line["status"] = "AMENDED" // reported under the plan before the amendment
		}
		out = append(out, line)
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

// report is the compact answer of a command: did is what this call changed.
func (g *Gate) report(cfg *config.Config, did []string) map[string]any {
	out := map[string]any{"task": g.ID[:8], "action": g.Request.Action, "mode": g.Mode, "step": g.Step}
	if position := g.position(); position != nil {
		out["stage"] = position
	}
	if g.Pause != nil {
		out["paused"] = map[string]string{"kind": g.Pause.Kind, "reason": g.Pause.Reason}
	}
	if len(did) > 0 {
		out["did"] = did
	}
	if lines := g.writerLines(); len(lines) > 0 {
		out["writers"] = lines
	}
	needs := g.needs(cfg)
	if len(needs) > 0 {
		out["needs"] = needs
	}
	if checks := g.checks(); len(checks) > 0 && g.active() {
		current := g.currentReceipts()
		failed := [][]string{}
		for _, failure := range g.failingReceipts(current) {
			failed = append(failed, failure.Argv)
		}
		waiting := len(g.outstandingReceipts(current)) - len(failed)
		out["checks"] = map[string]any{"declared": len(checks), "passing": len(checks) - len(failed) - waiting, "failing": failed, "not_run": waiting}
	}
	if open, blocking, notes := g.tally(cfg); len(open)+len(notes) > 0 {
		out["findings"] = map[string]int{"open": len(open), "blocking": len(blocking), "notes": len(notes)}
	}
	if len(g.Foreign) > 0 {
		out["foreign_changes"] = g.Foreign
	}
	if len(g.Substitutions) > 0 {
		out["substitutions"] = g.Substitutions
	}
	if g.ReviewError != "" {
		out["review_error"] = g.ReviewError
	}
	if g.Worktree != "" {
		out["isolated"] = g.landing()
	}
	if g.Final != nil {
		out["summary"] = g.Final.Summary
		if !g.Request.Writes() {
			// A review of existing work ends with its findings: they are the result.
			open, _, notes := g.tally(cfg)
			out["result"] = map[string]any{"findings": open, "notes": notes}
		}
	}
	actions := g.actions()
	if actions == nil {
		actions = []Action{}
	}
	out["actions"], out["next"] = actions, g.next(needs)
	return out
}

// Summary is the full read-only picture of a task: everything report has, and the details it leaves out.
func (g *Gate) Summary(cfg *config.Config) map[string]any {
	out := g.report(cfg, nil)
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
	out["id"], out["writers"], out["iteration"] = g.ID, writers, g.Iteration
	out["findings"], out["blocking"], out["notes"] = open, ids(blocking), notes
	out["dispositions"], out["disputes"] = g.Dispositions, g.Disputes
	out["checks_failed"] = g.failing()
	out["revisions"] = g.Revisions
	out["operation"], out["timings_s"] = ops.Status(dir), ops.Timings(dir, g.ID)
	out["board"], out["state"] = g.board(), dir
	if g.Round != nil {
		out["round"] = g.Round.Batch
	}
	if g.ContextPack != "" {
		out["context_pack"] = g.ContextPack
	}
	if review := g.reviewDiversity(); review != nil {
		out["review_diversity"] = review
	}
	if g.Plan.Visual != nil {
		out["visual"] = g.visualSummary()
	}
	if len(g.Signals) > 0 {
		out["slop_signals"] = g.Signals
	}
	if g.active() {
		if quality := g.qualityStatus(cfg); quality != nil {
			out["quality"] = quality
		}
	}
	if len(g.Plan.Stages) > 1 {
		out["stages"] = g.stagesSummary()
	}
	return out
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

// Show prints one part of the task in full, for when the compact answer is not enough.
func Show(cfg *config.Config, gate *Gate, what string) (any, error) {
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
		return map[string]any{"open": list, "notes": notes, "resolved": gate.Resolutions}, nil
	case "plan":
		return map[string]any{"plan": gate.Plan, "revisions": gate.Revisions, "name": gate.PlanName}, nil
	case "stage", "stages":
		return map[string]any{"position": gate.position(), "stages": gate.stagesSummary(), "accepted": gate.Accepted, "brief": gate.brief()}, nil
	case "checks":
		return map[string]any{"checks": gate.checkEvidence(), "failing": gate.failing()}, nil
	case "report", "quality":
		return map[string]any{"quality": gate.qualityStatus(cfg), "review_diversity": gate.reviewDiversity(), "writers": gate.latest()}, nil
	}
	return nil, fmt.Errorf("show takes findings, plan, stage, checks, report or all, not %q", what)
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
	if what == "" {
		return gate.report(cfg, nil), nil
	}
	return Show(cfg, gate, what)
}

// Refused answers a command that is not legal now: the task as it is, why, and what is possible instead.
func Refused(cfg *config.Config, gate *Gate, refusal *Refusal) map[string]any {
	out := gate.report(cfg, nil)
	out["refused"] = map[string]any{"action": refusal.Action, "because": refusal.Because}
	return out
}
