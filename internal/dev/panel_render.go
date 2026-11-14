package dev

import (
	"fmt"
	"github.com/knaus94/agentgate/internal/config"
	"strings"
	"time"
)

func span(started, ended, now int64) string {
	switch {
	case started == 0:
		return "—"
	case ended == 0:
		return clock(float64(now - started))
	}
	return clock(float64(ended - started))
}

func (m *panel) mark(status string, live bool) string {
	p := m.paint
	switch {
	case live:
		return p.cyan("▶")
	case status == "COMPLETED" || status == "PASS" || status == "done" || status == "passed" || status == "complete" ||
		status == "reviewed" || status == "ready to land" || (strings.HasPrefix(status, "landed: ") && status != "landed: drop"):
		return p.green("✔")
	case status == "BLOCKED" || status == "NEEDS_FIX" || status == "held" || status == "stale" || strings.HasPrefix(status, "failed before"):
		return p.yellow("!")
	case status == "FAILED" || status == "CANCELLED" || status == "stopped" || status == "cancelled" || strings.HasPrefix(status, "failed"):
		return p.red("✖")
	case status == "state error":
		return p.red("!")
	}
	return p.dim("•")
}

// frame draws the whole screen as lines.
func (m *panel) frame() []string {
	p, width := m.paint, max(m.width, 1)
	now := time.Now()
	rule := p.dim(strings.Repeat("─", width))
	scope := "this checkout"
	if m.global {
		scope = "every checkout"
	}
	left := " agentgate · " + scope
	right := now.Format("15:04:05") + " "
	if m.limits != "" {
		right = m.limits + "  " + right
	}
	if width < 70 {
		right = now.Format("15:04:05") + " "
	}
	if width < 40 {
		left, right = " agentgate", ""
	}
	head := p.bold(left) + strings.Repeat(" ", max(1, width-cells(left)-cells(right))) + p.dim(right)
	if m.help {
		lines := []string{head, rule, p.bold(" KEYBOARD"), "", " ↑ ↓ / j k      Select a task or row", " tab / ← →      Switch task list and details", " enter          Open selected row", " esc / h        Go back", " PgUp PgDn      Move by a page", " Home End / g G First / last row", " f              Follow worker activity", " a              Toggle every checkout", " r              Refresh now", " ?              Toggle this help", " q / Ctrl-C     Close dashboard"}
		return m.finish(lines, "Any key returns · q quit")
	}
	if m.view != nil {
		return m.finish(append([]string{head, rule}, m.detail(width, m.height-3)...), "↑↓ PgUp PgDn scroll · f follow · esc back · q quit")
	}
	lines := []string{head, rule}
	if len(m.tasks) == 0 {
		lines = append(lines, "")
		for _, line := range wrap("No /dev task here yet. Start in Claude Code with /dev <task> or in Codex with $dev <task>. Press a for every checkout.", width-2) {
			lines = append(lines, "  "+p.dim(line))
		}
		return m.finish(lines, "a every checkout · q quit")
	}
	// Task list: a window around the selection.
	room := min(len(m.tasks), max(3, (m.height-6)/3))
	if m.unlimited {
		room = len(m.tasks)
	}
	first := 0
	for index, task := range m.tasks {
		if task.Dir == m.chosen && index >= room {
			first = index - room + 1
		}
	}
	lines = append(lines, p.bold(fmt.Sprintf(" TASKS %d", len(m.tasks))))
	repoWidth := 0
	if m.global {
		repoWidth = 14
	}
	for _, task := range m.tasks[first:min(len(m.tasks), first+room)] {
		cursor := "  "
		if task.Dir == m.chosen {
			cursor = p.cyan("▸ ")
			if m.focus != 0 {
				cursor = p.dim("▸ ")
			}
		}
		mode := "—"
		if task.Gate != nil {
			mode = task.Gate.Mode
		}
		doing := ""
		if task.live() {
			doing = "▶ " + task.Op.Action + " " + clock(float64(now.UnixNano())/1e9-task.Op.StartedAt)
		} else if task.Gate != nil && task.Gate.Worktree != "" && task.Gate.Landed == "" {
			doing = "isolated"
		}
		if width < 90 {
			titleWidth := max(1, width-5)
			lines = append(lines, cursor+m.mark(task.phase(), task.live())+" "+p.bold(fit(oneLine(task.Title), titleWidth)))
			meta := mode + " · " + task.phase() + " · " + clock(task.elapsed(now.Unix()))
			if m.global {
				meta += " · " + task.Repo
			}
			if doing != "" {
				meta += " · " + doing
			}
			lines = append(lines, "    "+p.dim(fit(meta, width-4)))
			continue
		}
		fixed := 2 + 2 + 7 + 17 + 8 + repoWidth + 18
		row := cursor + m.mark(task.phase(), task.live()) + " " + fit(mode, 7) + p.status(fit(task.phase(), 17)) +
			fit(clock(task.elapsed(now.Unix())), 8)
		if m.global {
			row += p.dim(fit(task.Repo, repoWidth))
		}
		title := fit(oneLine(task.Title), max(10, width-fixed))
		if task.Dir == m.chosen {
			title = p.bold(title)
		}
		lines = append(lines, row+title+" "+p.cyan(fit(doing, 17)))
	}
	lines = append(lines, rule)
	task := m.selected()
	gate := task.Gate
	meta := "no task yet"
	if gate != nil {
		meta = fmt.Sprintf("%s · round %d · session %s", gate.Mode, gate.Iteration, string([]rune(gate.Session)[:min(8, len([]rune(gate.Session)))]))
		if gate.Worktree != "" {
			meta += " · " + gate.Branch
		}
	}
	for index, line := range wrap(oneLine(task.Title), width-2) {
		if index < 2 {
			lines = append(lines, " "+p.bold(line))
		}
	}
	lines = append(lines, " "+p.dim(meta))
	if task.Warning != "" {
		for _, line := range wrap(task.Warning, width-2) {
			lines = append(lines, " "+p.red(line))
		}
	}
	if gate != nil && gate.Phase == "held" {
		for _, line := range wrap("on hold: "+gate.HoldReason, width-2) {
			lines = append(lines, " "+p.yellow(line))
		}
	}
	if gate != nil && gate.Epic != nil {
		// The parts of a staged task: accepted, the present one, still ahead.
		lines = append(lines, "", p.bold(" STAGES")+p.dim(fmt.Sprintf("  %d of %d accepted", len(gate.StageRecords), len(gate.Epic.Stages))))
		for index, stage := range gate.Epic.Stages {
			mark, mode, note := p.dim("○"), "", oneLine(stage.Goal)
			switch {
			case index < len(gate.StageRecords):
				record := gate.StageRecords[index]
				mark, mode, note = p.green("✔"), record.Mode, oneLine(record.Summary)
				if record.Commit != "" {
					note = record.Commit + " " + note
				}
			case index == gate.StageIndex && !gate.Integration:
				mark, mode = p.cyan("▶"), gate.Mode
			}
			lines = append(lines, "   "+mark+" "+fit(fmt.Sprintf("%d. %s", index+1, stage.Name), 28)+p.dim(fit(mode, 8))+fit(note, max(10, width-42)))
		}
		if gate.Integration {
			lines = append(lines, "   "+p.cyan("▶")+" "+fit("the whole change", 28)+p.dim(fit(gate.Mode, 8))+"review of the seams between the stages")
		}
	}
	if len(task.Stages) > 0 {
		var parts []string
		for _, stage := range task.Stages {
			text := stage.Action + " " + clock(stage.Seconds)
			switch {
			case stage.Live:
				text = "▶ " + text
			case stage.State == "failed":
				text += " ✖"
			case stage.State == "cancelled":
				text += " (stopped)"
			}
			parts = append(parts, text)
		}
		for index, line := range wrap(strings.Join(parts, " › "), width-12) {
			label := "          "
			if index == 0 {
				label = " steps    "
			}
			lines = append(lines, p.dim(label)+line)
		}
	}
	items := m.items()
	cursor := func(kind string, index int) string {
		if m.focus == 1 && m.row < len(items) && items[m.row].Kind == kind && items[m.row].Index == index {
			return p.cyan(" ▸ ")
		}
		return "   "
	}
	if len(task.Lanes) > 0 {
		lines = append(lines, "", p.bold(" WORKERS"))
		for index, lane := range task.Lanes {
			note := lane.Note
			if note == "" {
				note = strings.ToLower(lane.Status)
			}
			runs := ""
			if lane.Runs > 1 {
				runs = fmt.Sprintf(" ×%d", lane.Runs)
			}
			if width < 100 {
				text := lane.Kind + " " + lane.Name + " · " + lane.Route
				lines = append(lines, cursor("lane", index)+m.mark(lane.Status, lane.Live)+" "+fit(text, width-5))
				lines = append(lines, "     "+p.dim(fit(span(lane.Started, lane.Ended, now.Unix())+runs+" · "+note, width-5)))
				continue
			}
			text := fit(lane.Kind, 8) + fit(lane.Name, 20) + p.dim(fit(lane.Route, 24)) + fit(span(lane.Started, lane.Ended, now.Unix())+runs, 11)
			lines = append(lines, cursor("lane", index)+m.mark(lane.Status, lane.Live)+" "+text+fit(note, max(8, width-4-63)))
		}
	}
	if len(task.Checks) > 0 {
		lines = append(lines, "", p.bold(" CHECKS"))
		for index, check := range task.Checks {
			if width < 70 {
				lines = append(lines, cursor("check", index)+m.mark(check.State, false)+" "+fit(check.Argv, width-5), "     "+p.dim(fit(check.State, width-5)))
				continue
			}
			lines = append(lines, cursor("check", index)+m.mark(check.State, false)+" "+fit(check.Argv, max(20, width-36))+" "+p.dim(check.State))
		}
	}
	if len(task.Open) > 0 || task.Notes > 0 {
		counts := map[string]int{}
		for _, finding := range task.Open {
			counts[finding.Severity]++
		}
		state := p.green("nothing blocks")
		if task.Blocked > 0 {
			state = p.yellow(fmt.Sprintf("%d blocking", task.Blocked))
		}
		lines = append(lines, "", cursor("findings", 0)+p.bold("FINDINGS")+fmt.Sprintf("  P0 %d · P1 %d · P2 %d · notes %d · ", counts["P0"], counts["P1"], counts["P2"], task.Notes)+state)
	}
	if gate != nil && len(gate.Signals) > 0 {
		lines = append(lines, cursor("signals", 0)+p.bold("SIGNALS")+p.dim(fmt.Sprintf("  %d leftovers found mechanically on added lines", len(gate.Signals))))
	}
	if gate != nil {
		lines = append(lines, cursor("plan", 0)+p.bold("PLAN")+p.dim(fmt.Sprintf("  %d streams · %d files · %d checks", len(gate.Plan.Streams), len(gate.Files), len(gate.Plan.Checks))))
	}
	if task.Next != "" {
		lines = append(lines, "")
		for index, line := range wrap(task.Next, width-9) {
			label := "        "
			if index == 0 {
				label = " next   "
			}
			if index < 3 {
				lines = append(lines, p.dim(label)+line)
			}
		}
	}
	return m.finish(lines, "↑↓ move · tab switch list · enter open · a every checkout · ? help · q quit")
}

// finish cuts the frame to the screen and puts the key hints on the last line.
func (m *panel) finish(lines []string, hints string) []string {
	if !m.unlimited {
		room := max(0, m.height-1)
		if m.view == nil && !m.help && room > 2 && len(lines) > room {
			// Keep the header visible and scroll the overview around the selected
			// row. Previously navigation could select an item below the screen.
			anchor := 2
			for index, line := range lines {
				plain := terminalText(line)
				if m.focus == 1 && strings.HasPrefix(plain, " ▸ ") || m.focus == 0 && strings.HasPrefix(plain, "▸ ") {
					anchor = index
				}
			}
			visible := room - 2
			if anchor-2 < m.offset {
				m.offset = max(0, anchor-2)
			}
			if anchor-2 >= m.offset+visible {
				m.offset = anchor - 2 - visible + 1
			}
			m.offset = min(max(0, m.offset), max(0, len(lines)-2-visible))
			lines = append(append([]string{}, lines[:2]...), lines[2+m.offset:min(len(lines), 2+m.offset+visible)]...)
		}
		if len(lines) > room {
			lines = lines[:room]
		}
		for len(lines) < room {
			lines = append(lines, "")
		}
	}
	if m.width < 70 {
		hints = "↑↓ move · enter open · ? help · q quit"
	}
	if m.width < 40 {
		hints = "↑↓  enter  ? help  q quit"
	}
	lines = append(lines, m.paint.dim(" "+fit(hints, max(0, m.width-2))))
	for index := range lines {
		lines[index] = clipStyled(lines[index], m.width)
	}
	return lines
}

// detail draws the opened view: a worker's activity, a check's output, the findings or the plan.
func (m *panel) detail(width, room int) []string {
	p := m.paint
	task := m.selected()
	var head, body []string
	text := func(label, value string) {
		for index, line := range wrap(value, width-12) {
			prefix := "          "
			if index == 0 {
				prefix = fit(" "+label, 10)
			}
			body = append(body, p.dim(prefix)+line)
		}
	}
	switch {
	case task == nil:
		body = []string{p.dim(" The task is gone.")}
	case m.view.Kind == "lane" && m.view.Index < len(task.Lanes):
		lane := task.Lanes[m.view.Index]
		tidy := task.tidy()
		head = []string{" " + m.mark(lane.Status, lane.Live) + " " + p.bold(lane.Kind+" "+lane.Name) + "  " + p.dim(lane.Route) + "  " +
			span(lane.Started, lane.Ended, time.Now().Unix()) + "  " + p.status(lane.Status), p.dim(" " + fit(tidy.Replace(lane.Dir)+runsLine(lane.Dir), width-2))}
		events := laneEvents(lane.Dir)
		for index := range events {
			events[index].Text, events[index].Detail = tidy.Replace(events[index].Text), tidy.Replace(events[index].Detail)
		}
		if len(events) == 0 {
			body = append(body, "", p.dim(" No activity recorded for this run yet."))
		}
		for _, event := range events {
			glyph := event.Glyph()
			paintLine := func(value string) string { return value }
			switch {
			case event.Failed:
				paintLine = p.red
			case event.Kind == "think":
				paintLine = p.dim
			case event.Kind == "run":
				paintLine = p.cyan
			case event.Kind == "edit":
				paintLine = p.yellow
			case event.Kind == "done":
				paintLine = p.green
			}
			// A command keeps its own lines; prose is wrapped. Either way a long one is cut, not hidden.
			var shown []string
			for _, paragraph := range strings.Split(event.Text, "\n") {
				if event.Kind == "run" {
					shown = append(shown, fit(strings.ReplaceAll(paragraph, "\t", "  "), width-5))
				} else {
					shown = append(shown, wrap(paragraph, width-5)...)
				}
			}
			limit := 12
			if event.Kind == "run" {
				limit = 6
			}
			if len(shown) > limit {
				shown = append(shown[:limit], fmt.Sprintf("… (%d more lines)", len(shown)-limit))
			}
			for index, line := range shown {
				prefix := "   "
				if index == 0 {
					prefix = " " + glyph + " "
				}
				body = append(body, paintLine(strings.TrimRight(prefix+line, " ")))
			}
			if event.Detail != "" {
				for _, line := range strings.Split(event.Detail, "\n") {
					body = append(body, p.dim("   │ "+fit(strings.ReplaceAll(line, "\t", "  "), width-6)))
				}
			}
		}
	case m.view.Kind == "check" && m.view.Index < len(task.Checks):
		check := task.Checks[m.view.Index]
		head = []string{" " + m.mark(check.State, false) + " " + p.bold(fit(check.Argv, width-30)) + "  " + p.dim(check.State), p.dim(" " + fit(check.Log, width-2))}
		if check.Log == "" {
			body = append(body, "", p.dim(" This check has not run yet."))
		}
		for _, line := range strings.Split(strings.TrimRight(string(readTail(check.Log, 256<<10)), "\n"), "\n") {
			body = append(body, " "+fit(strings.ReplaceAll(line, "\t", "  "), width-2))
		}
	case m.view.Kind == "findings":
		head = []string{" " + p.bold("FINDINGS") + p.dim(fmt.Sprintf("  %d open · %d notes", len(task.Open), task.Notes))}
		for _, finding := range task.Open {
			decision := task.Gate.Dispositions[finding.ID].Decision
			if decision == "" {
				decision = "not triaged"
			}
			where := finding.Path
			if finding.Line > 0 {
				where += fmt.Sprintf(":%d", finding.Line)
			}
			title := fmt.Sprintf(" %s %s  %s  %s", finding.Severity, finding.Axis, where, decision)
			if task.Gate.blocking(m.cfg, finding) {
				title = p.yellow(title + "  · blocks")
			} else {
				title = p.bold(title)
			}
			body = append(body, "", title)
			text("what", finding.Evidence)
			text("impact", finding.Impact)
			text("remedy", finding.Remedy)
		}
	case m.view.Kind == "signals" && task.Gate != nil:
		head = []string{" " + p.bold("SIGNALS") + p.dim("  leads for the reviewers, not verdicts")}
		for _, signal := range task.Gate.Signals {
			body = append(body, " "+p.yellow(fit(signal.Kind, 18))+p.dim(fmt.Sprintf("%s:%d", signal.Path, signal.Line)), "   "+fit(signal.Text, width-5))
		}
	case m.view.Kind == "plan" && task.Gate != nil:
		plan := task.Gate.Plan
		head = []string{" " + p.bold("PLAN") + p.dim("  "+task.Gate.Mode+" · "+oneLine(plan.Request))}
		text("brief", plan.Brief)
		for _, stream := range plan.Streams {
			body = append(body, "", " "+p.bold(stream.Name)+p.dim("  "+stream.Domain))
			if stream.Brief != "" {
				text("", stream.Brief)
			}
			for _, file := range stream.Files {
				body = append(body, p.dim("          ")+terminalText(file))
			}
		}
		if accepted := plan.Architecture; accepted != nil {
			body = append(body, "", " "+p.bold("accepted design"))
			text("", accepted.Acceptance)
		}
	}
	room = max(1, room-len(head)-1)
	if m.follow || m.scroll > len(body)-room {
		m.scroll = max(0, len(body)-room)
	}
	m.scroll = max(0, m.scroll)
	visible := body[m.scroll:min(len(body), m.scroll+room)]
	position := ""
	if len(body) > room {
		position = fmt.Sprintf("  %d–%d of %d", m.scroll+1, m.scroll+len(visible), len(body))
		if m.follow {
			position += " · following"
		}
	}
	if len(head) > 0 {
		head[0] += p.dim(position)
	}
	return append(append(head, ""), visible...)
}

// PanelText is the panel as text, for `watch --once` and for output that is not a terminal. Height 0 means
// as tall as the content.
func PanelText(cfg *config.Config, global, color bool, width, height int) string {
	m := &panel{cfg: cfg, global: global, paint: paint{color}, width: width, height: height, focus: -1, unlimited: height == 0}
	if height == 0 {
		m.height = 40
	}
	if !global && len(loadTasks(false)) == 0 && len(loadTasks(true)) > 0 {
		m.global = true
	}
	m.refresh()
	lines := m.frame()
	lines = lines[:len(lines)-1] // key hints belong to the interactive panel
	if height == 0 {
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
