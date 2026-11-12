package dev

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/knaus94/claude-dev/internal/accounts"
	"github.com/knaus94/claude-dev/internal/config"
	"github.com/knaus94/claude-dev/internal/ops"
)

// The panel is the interactive `claude-dev watch`: every task of the checkout (or of every checkout), the
// stages each went through with their timings, its workers, checks and findings, and — one keypress deeper —
// what a worker is doing right now. It only reads state; the owning Claude Code session drives the task.

// laneView is one worker process of a task.
type laneView struct {
	Kind, Name, Dir, Route, Status, Note string
	Live                                 bool
	Started, Ended                       int64
	Runs                                 int
}

type checkView struct {
	Argv, State, Log string
}

type stageView struct {
	Action  string
	Seconds float64
	State   string
	Live    bool
}

// taskView is one row of the task list, with its details when it is the selected one.
type taskView struct {
	Gate    *Gate
	Dir     string // session directory
	Repo    string
	Op      *ops.Record
	Title   string
	Lanes   []laneView
	Checks  []checkView
	Stages  []stageView
	Open    []Finding
	Notes   int
	Blocked int
	Next    string
}

func (t *taskView) live() bool { return t.Op != nil && t.Op.Live }

func (t *taskView) phase() string {
	switch {
	case t.Gate == nil:
		return "preparing"
	case t.Gate.Worktree != "" && t.Gate.Landed == "" && t.Gate.Phase == "complete":
		return "ready to land"
	case t.Gate.Landed != "":
		return "landed: " + t.Gate.Landed
	}
	return t.Gate.Phase
}

func (t *taskView) elapsed(now int64) float64 {
	if t.Gate == nil {
		if t.Op != nil {
			return float64(now) - t.Op.StartedAt
		}
		return 0
	}
	end := now
	if !t.Gate.active() && t.Gate.Final != nil {
		end = t.Gate.Final.At
	}
	return float64(end - t.Gate.CreatedAt)
}

// rank orders the list: what is running, then what waits for someone, then what is finished.
func (t *taskView) rank() int {
	switch {
	case t.live():
		return 0
	case t.Gate != nil && t.Gate.active():
		return 1
	case t.Gate != nil && t.Gate.Worktree != "" && t.Gate.Landed == "":
		return 2
	}
	return 3
}

// loadTasks reads the tasks of one checkout, or of every checkout on the machine.
func loadTasks(global bool) []*taskView {
	pattern := filepath.Join(checkoutDir(""), "*")
	if global {
		pattern = filepath.Join(Root(), "*", "*")
	}
	dirs, _ := filepath.Glob(pattern)
	var out []*taskView
	for _, dir := range dirs {
		task := &taskView{Dir: dir, Op: ops.Status(dir)}
		if raw, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
			gate := &Gate{}
			if json.Unmarshal(raw, gate) == nil && gate.ID != "" {
				task.Gate, task.Title, task.Repo = gate, gate.Request.Task, filepath.Base(gate.Cwd)
			}
		}
		if task.Gate == nil {
			if !task.live() {
				continue
			}
			task.Title = "(" + task.Op.Action + " before launch)"
		} else if task.live() && task.Op.GateID != "" && task.Op.GateID != task.Gate.ID {
			task.Title = "(" + task.Op.Action + " for the next task) · last: " + task.Title
		}
		out = append(out, task)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].rank() != out[b].rank() {
			return out[a].rank() < out[b].rank()
		}
		return created(out[a]) > created(out[b])
	})
	finished := 0
	kept := out[:0]
	for _, task := range out {
		if task.rank() == 3 {
			if finished++; finished > 12 {
				continue
			}
		}
		kept = append(kept, task)
	}
	return kept
}

func created(t *taskView) int64 {
	if t.Gate != nil {
		return t.Gate.CreatedAt
	}
	return int64(t.Op.StartedAt)
}

// readLane reads the receipt of a worker's present run.
func readLane(dir string) (laneView, bool) {
	var receipt struct {
		State string `json:"state"`
		PID   int    `json:"pid"`
		Route struct {
			Alias, Effort string
		} `json:"route"`
		Account    string `json:"account"`
		StartedAt  int64  `json:"started_at"`
		ObservedAt int64  `json:"observed_at"`
		ReturnCode *int   `json:"returncode"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "process.json"))
	if err != nil || json.Unmarshal(raw, &receipt) != nil {
		return laneView{}, false
	}
	lane := laneView{Dir: dir, Route: receipt.Route.Alias + ":" + receipt.Route.Effort, Started: receipt.StartedAt, Status: "done"}
	if receipt.Account != "" {
		lane.Route += " @" + receipt.Account
	}
	if receipt.State == "starting" {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", receipt.PID)); err == nil {
			lane.Live, lane.Status = true, "RUNNING"
		} else {
			lane.Status = "stopped"
		}
		if lane.Started == 0 {
			lane.Started = receipt.ObservedAt
		}
	} else {
		lane.Ended = receipt.ObservedAt
		if receipt.ReturnCode != nil && *receipt.ReturnCode != 0 {
			lane.Status = "FAILED"
		}
	}
	if history, err := os.ReadFile(filepath.Join(dir, "runs.jsonl")); err == nil {
		lane.Runs = strings.Count(string(history), "\n")
	}
	return lane, true
}

// findLanes walks a directory for worker receipts, skipping the bulky directories that never hold one.
func findLanes(root string) []laneView {
	var out []laneView
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "home", "evidence", "delta", "checks", "board", "screenshots", "review-cache", "cache", "worktrees":
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "process.json" {
			if lane, ok := readLane(filepath.Dir(path)); ok {
				out = append(out, lane)
			}
		}
		return nil
	})
	return out
}

func routeText(route config.Route) string {
	if route.Account != "" {
		return route.String() + " @" + route.Account
	}
	return route.String()
}

// commandText is a check as a person names it: without the environment assignments in front.
func commandText(argv []string) string {
	if len(argv) > 1 && argv[0] == "env" {
		argv = argv[1:]
		for len(argv) > 1 && strings.Contains(argv[0], "=") && !strings.HasPrefix(argv[0], "-") {
			argv = argv[1:]
		}
	}
	return strings.Join(argv, " ")
}

func axesOf(laneDir string) string {
	var packet struct {
		Axes []string `json:"axes"`
	}
	raw, _ := os.ReadFile(filepath.Join(filepath.Dir(laneDir), "packet.json"))
	_ = json.Unmarshal(raw, &packet)
	return strings.Join(packet.Axes, "+")
}

// describe fills in the parts of a task that only the selected one needs.
func (t *taskView) describe(cfg *config.Config) {
	t.Lanes, t.Checks, t.Stages, t.Open, t.Notes, t.Blocked, t.Next = nil, nil, nil, nil, 0, 0, ""
	gateID := ""
	if t.Gate != nil {
		gateID = t.Gate.ID
	}
	// Stages: every finished operation of the task, in order, then the running one.
	paths, _ := filepath.Glob(filepath.Join(t.Dir, "operations", "*.json"))
	sort.Strings(paths)
	for _, path := range paths {
		record := ops.Record{}
		if raw, err := os.ReadFile(path); err == nil && json.Unmarshal(raw, &record) == nil && record.GateID == gateID {
			// Bookkeeping that ran no model and no check (a triage, a hold, a review call that only recorded
			// the lead's reading) is not a stage worth a place in the chain.
			if record.DurationS < 1 && record.State == "complete" && slices.Contains([]string{"triage", "hold", "resume", "review", "finish"}, record.Action) {
				continue
			}
			t.Stages = append(t.Stages, stageView{Action: record.Action, Seconds: record.DurationS, State: record.State})
		}
	}
	if t.live() {
		t.Stages = append(t.Stages, stageView{Action: t.Op.Action, Seconds: float64(time.Now().UnixNano())/1e9 - t.Op.StartedAt, Live: true})
	}
	var lanes []laneView
	if t.Gate != nil {
		lanes = findLanes(t.Gate.dir())
		// Every writer and every lane of the last review is listed, also one whose process left no receipt.
		known := map[string]bool{}
		for _, lane := range lanes {
			known[lane.Dir] = true
		}
		for _, writer := range t.Gate.latest() {
			if !known[writer.Dir] {
				lanes = append(lanes, laneView{Dir: writer.Dir, Route: routeText(writer.Route), Status: writer.Status})
			}
		}
		if count := len(t.Gate.Reviews); count > 0 {
			for _, run := range t.Gate.Reviews[count-1].Lanes {
				if !known[run.Dir] && run.Dir != "" {
					lanes = append(lanes, laneView{Dir: run.Dir, Route: routeText(run.Route), Status: "done"})
				}
			}
		}
	}
	if t.live() {
		// Scouts, architects and consultants work outside the task's own directory.
		checkout := filepath.Dir(t.Dir)
		for _, shared := range []string{filepath.Join(checkout, "scout"), filepath.Join(checkout, "designs"), filepath.Join(t.Dir, "consults")} {
			for _, lane := range findLanes(shared) {
				if lane.Live {
					lanes = append(lanes, lane)
				}
			}
		}
	}
	newestReview := ""
	for _, lane := range lanes {
		if rel, err := filepath.Rel(t.Dir, lane.Dir); err == nil {
			if part := strings.Split(rel, string(filepath.Separator)); len(part) > 1 && strings.HasPrefix(part[1], "review-") && part[1] > newestReview {
				newestReview = part[1]
			}
		}
	}
	for _, lane := range lanes {
		parts := strings.Split(filepath.ToSlash(lane.Dir), "/")
		lane.Kind, lane.Name = parts[len(parts)-2], parts[len(parts)-1]
		for index, part := range parts {
			switch {
			case part == "writers" && index+1 < len(parts):
				lane.Kind, lane.Name = "writer", parts[index+1]
				if cut := strings.LastIndex(lane.Name, "-"); cut > 0 {
					lane.Name = lane.Name[:cut]
				}
			case strings.HasPrefix(part, "review-") && index+1 < len(parts) && strings.HasPrefix(parts[index+1], "lane-"):
				lane.Kind, lane.Name = "review", axesOf(lane.Dir)
				if lane.Name == "" {
					lane.Name = parts[index+1]
				}
				if part != newestReview && !lane.Live {
					lane.Kind = ""
				}
			case part == "scout":
				lane.Kind, lane.Name = "scout", "context pack"
			case part == "designs" && index+2 < len(parts):
				lane.Kind, lane.Name = "design", parts[len(parts)-1]
			case part == "consults":
				lane.Kind, lane.Name = "consult", parts[len(parts)-2]
			}
		}
		if lane.Kind == "" {
			continue // an earlier review round: its lanes were superseded
		}
		if t.Gate != nil {
			for _, writer := range t.Gate.Writers {
				if writer.Dir == lane.Dir && !lane.Live {
					lane.Status, lane.Note = writer.Status, oneLine(writer.Summary)
					if len(writer.Blockers) > 0 {
						lane.Note = "? " + strings.Join(writer.Blockers, " | ")
					}
					if writer.Error != "" {
						lane.Note = oneLine(writer.Error)
					}
				}
			}
			for _, review := range t.Gate.Reviews {
				for _, run := range review.Lanes {
					if run.Dir == lane.Dir && !lane.Live {
						status, count := "PASS", 0
						for _, report := range run.Reports {
							count += len(report.Findings)
							if report.Status != "PASS" {
								status = report.Status
							}
						}
						lane.Status, lane.Note = status, fmt.Sprintf("%d findings", count)
					}
				}
			}
		}
		if lane.Live {
			if events := laneEvents(lane.Dir); len(events) > 0 {
				last := events[len(events)-1]
				lane.Note = last.Glyph() + " " + firstLine(last.Text)
			}
		}
		t.Lanes = append(t.Lanes, lane)
	}
	order := map[string]int{"scout": 0, "design": 1, "consult": 2, "writer": 3, "review": 4}
	sort.SliceStable(t.Lanes, func(a, b int) bool {
		left, right := t.Lanes[a], t.Lanes[b]
		if order[left.Kind] != order[right.Kind] {
			return order[left.Kind] < order[right.Kind]
		}
		return left.Name < right.Name
	})
	if t.Gate == nil {
		return
	}
	gate := t.Gate
	for _, finding := range gate.Findings() {
		if _, resolved := gate.Resolutions[finding.ID]; resolved {
			continue
		}
		if gate.isNote(finding) {
			t.Notes++
			continue
		}
		t.Open = append(t.Open, finding)
		if gate.blocking(cfg, finding) {
			t.Blocked++
		}
	}
	// Check receipts are judged against the files of the task, so the files must be where the process is.
	here, _ := os.Getwd()
	where := gate.Cwd
	if gate.Worktree != "" && gate.Landed == "" {
		where = gate.Worktree
	}
	inPlace := os.Chdir(where) == nil
	for index := range gate.Plan.Checks {
		check := &gate.Plan.Checks[index]
		view := checkView{Argv: commandText(check.Argv), State: "not run"}
		for position := len(gate.Checks) - 1; position >= 0; position-- {
			if slices.Equal(gate.Checks[position].Argv, check.Argv) {
				view.Log = gate.Checks[position].Log
				break
			}
		}
		if inPlace {
			if receipt := gate.currentReceipt(check); receipt != nil {
				switch {
				case receipt.ReturnCode == 0:
					view.State = "passed"
				case receipt.Preexisting:
					view.State = "failed before the task too"
				default:
					view.State = fmt.Sprintf("failed (%d)", receipt.ReturnCode)
				}
			} else if view.Log != "" {
				view.State = "stale"
			}
			t.Next = gate.next(cfg, t.Blocked > 0)
		}
		t.Checks = append(t.Checks, view)
	}
	if inPlace {
		t.Next = gate.next(cfg, t.Blocked > 0)
		_ = os.Chdir(here)
	}
}

// tidy shortens the long state paths that fill a worker's commands: the task's own directory, the state of
// its checkout, and the home directory.
func (t *taskView) tidy() *strings.Replacer {
	pairs := []string{}
	if t.Gate != nil {
		pairs = append(pairs, t.Gate.dir()+"/", "task:/", t.Gate.dir(), "task:")
		if t.Gate.Worktree != "" {
			pairs = append(pairs, t.Gate.Worktree+"/", "worktree:/", t.Gate.Worktree, "worktree:")
		}
	}
	pairs = append(pairs, filepath.Dir(t.Dir)+"/", "state:/")
	if home, err := os.UserHomeDir(); err == nil && home != "/" {
		pairs = append(pairs, home+"/", "~/")
	}
	return strings.NewReplacer(pairs...)
}

// runsLine lists how long each run of a lane took, when it ran more than once.
func runsLine(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "runs.jsonl"))
	if err != nil {
		return ""
	}
	var parts []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var run struct {
			Started int64 `json:"started_at"`
			Ended   int64 `json:"observed_at"`
			Resumed bool  `json:"resumed"`
		}
		if json.Unmarshal([]byte(line), &run) == nil && run.Started > 0 {
			text := clock(float64(run.Ended - run.Started))
			if run.Resumed {
				text += " (continued)"
			}
			parts = append(parts, text)
		}
	}
	if len(parts) < 2 {
		return ""
	}
	return "   runs: " + strings.Join(parts, ", ")
}

// item is one selectable row of the selected task.
type item struct {
	Kind  string // lane, check, findings, plan
	Index int
}

type panel struct {
	cfg    *config.Config
	global bool
	tasks  []*taskView
	chosen string // session directory of the selected task: the selection survives reordering
	focus  int    // 0: the task list, 1: the rows of the selected task
	row    int
	view   *item // an opened detail view, or nil
	scroll int
	follow bool
	width  int
	height int
	paint  paint
	limits string
}

func (m *panel) selected() *taskView {
	for _, task := range m.tasks {
		if task.Dir == m.chosen {
			return task
		}
	}
	if len(m.tasks) > 0 {
		m.chosen = m.tasks[0].Dir
		return m.tasks[0]
	}
	return nil
}

func (m *panel) items() []item {
	task := m.selected()
	if task == nil {
		return nil
	}
	var out []item
	for index := range task.Lanes {
		out = append(out, item{"lane", index})
	}
	for index := range task.Checks {
		out = append(out, item{"check", index})
	}
	if len(task.Open) > 0 || task.Notes > 0 {
		out = append(out, item{"findings", 0})
	}
	if task.Gate != nil {
		out = append(out, item{"plan", 0})
	}
	return out
}

// refresh reads the state again. It is cheap enough to run every second.
func (m *panel) refresh() {
	m.tasks = loadTasks(m.global)
	if task := m.selected(); task != nil {
		task.describe(m.cfg)
	}
	if count := len(m.items()); m.row >= count {
		m.row = max(0, count-1)
	}
	m.limits = limitsLine(filepath.Join(Root(), "accounts.json"))
}

// limitsLine is the last known use of each subscription, from the readings `claude-dev accounts` and the
// workers leave behind. The panel itself never asks a provider.
func limitsLine(path string) string {
	var saved struct {
		Accounts map[string]accounts.Status `json:"accounts"`
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &saved) != nil {
		return ""
	}
	names := make([]string, 0, len(saved.Accounts))
	for name := range saved.Accounts {
		names = append(names, name)
	}
	sort.Strings(names)
	var parts []string
	for _, name := range names {
		status := saved.Accounts[name]
		if !status.Configured || len(status.Windows) == 0 {
			continue
		}
		worst := 0.0
		for _, window := range status.Windows {
			worst = max(worst, window.Used)
		}
		parts = append(parts, fmt.Sprintf("%s %.0f%%", name, worst))
	}
	return strings.Join(parts, " · ")
}

// key applies one keypress and reports whether the panel should close.
func (m *panel) key(name string) bool {
	if m.view != nil {
		page := max(1, m.height-6)
		switch name {
		case "q", "ctrl-c":
			return true
		case "esc", "left", "back", "h":
			m.view = nil
		case "up", "k":
			m.scroll, m.follow = m.scroll-1, false
		case "down", "j":
			m.scroll++
		case "pgup":
			m.scroll, m.follow = m.scroll-page, false
		case "pgdn", " ":
			m.scroll += page
		case "home", "g":
			m.scroll, m.follow = 0, false
		case "end", "G", "f":
			m.follow = true
		}
		return false
	}
	move := func(delta int) {
		if m.focus == 0 {
			for index, task := range m.tasks {
				if task.Dir == m.chosen {
					next := min(max(index+delta, 0), len(m.tasks)-1)
					m.chosen, m.row = m.tasks[next].Dir, 0
					return
				}
			}
			return
		}
		m.row = min(max(m.row+delta, 0), max(0, len(m.items())-1))
	}
	switch name {
	case "q", "ctrl-c":
		return true
	case "up", "k":
		move(-1)
	case "down", "j":
		move(1)
	case "tab", "backtab":
		m.focus = 1 - m.focus
	case "right", "l":
		m.focus = 1
	case "left", "h", "esc":
		m.focus = 0
	case "a":
		m.global, m.chosen, m.row = !m.global, "", 0
	case "enter":
		if m.focus == 0 {
			m.focus = 1
		} else if items := m.items(); m.row < len(items) {
			chosen := items[m.row]
			m.view, m.scroll, m.follow = &chosen, 0, chosen.Kind == "lane"
		}
	}
	m.refresh()
	return false
}

// Text helpers. Width is counted in characters; the panel avoids characters that take two cells.

func fit(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) > width {
		return string(runes[:width-1]) + "…"
	}
	return text + strings.Repeat(" ", width-len(runes))
}

func wrap(text string, width int) []string {
	var out []string
	for _, paragraph := range strings.Split(text, "\n") {
		line := ""
		for _, word := range strings.Fields(paragraph) {
			for len([]rune(word)) > width {
				if line != "" {
					out, line = append(out, line), ""
				}
				out, word = append(out, string([]rune(word)[:width])), string([]rune(word)[width:])
			}
			if line != "" && len([]rune(line))+1+len([]rune(word)) > width {
				out, line = append(out, line), ""
			}
			if line != "" {
				line += " "
			}
			line += word
		}
		out = append(out, line)
	}
	return out
}

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
	}
	return p.dim("•")
}

// frame draws the whole screen as lines.
func (m *panel) frame() []string {
	p, width := m.paint, max(m.width, 40)
	now := time.Now()
	rule := p.dim(strings.Repeat("─", width))
	scope := "this checkout"
	if m.global {
		scope = "every checkout"
	}
	left := " claude-dev · " + scope
	right := now.Format("15:04:05") + " "
	if m.limits != "" {
		right = m.limits + "  " + right
	}
	head := p.bold(left) + strings.Repeat(" ", max(1, width-len([]rune(left))-len([]rune(right)))) + p.dim(right)
	if m.view != nil {
		return m.finish(append([]string{head, rule}, m.detail(width, m.height-3)...), "↑↓ PgUp PgDn scroll · f follow · esc back · q quit")
	}
	lines := []string{head, rule}
	if len(m.tasks) == 0 {
		lines = append(lines, "", p.dim("  No /dev task here yet. Start one in Claude Code with /dev <task>; press a for every checkout."))
		return m.finish(lines, "a every checkout · q quit")
	}
	// Task list: a window around the selection.
	room := min(len(m.tasks), max(3, (m.height-6)/3))
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
	if gate != nil && gate.Phase == "held" {
		for _, line := range wrap("on hold: "+gate.HoldReason, width-2) {
			lines = append(lines, " "+p.yellow(line))
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
				label = " stages   "
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
			text := fit(lane.Kind, 8) + fit(lane.Name, 20) + p.dim(fit(lane.Route, 24)) + fit(span(lane.Started, lane.Ended, now.Unix())+runs, 11)
			lines = append(lines, cursor("lane", index)+m.mark(lane.Status, lane.Live)+" "+text+fit(note, max(8, width-4-63)))
		}
	}
	if len(task.Checks) > 0 {
		lines = append(lines, "", p.bold(" CHECKS"))
		for index, check := range task.Checks {
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
	return m.finish(lines, "↑↓ move · tab switch list · enter open · a every checkout · q quit")
}

// finish cuts the frame to the screen and puts the key hints on the last line.
func (m *panel) finish(lines []string, hints string) []string {
	room := max(3, m.height-1)
	if len(lines) > room {
		lines = lines[:room]
	}
	for len(lines) < room {
		lines = append(lines, "")
	}
	return append(lines, m.paint.dim(" "+fit(hints, max(10, m.width-2))))
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
				body = append(body, p.dim("          ")+file)
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
	m := &panel{cfg: cfg, global: global, paint: paint{color}, width: width, height: height, focus: -1}
	if height == 0 {
		m.height = 400
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

// runPanel is the interactive loop: redraw every second and on every key.
func runPanel(cfg *config.Config, global bool) {
	restore, err := rawMode(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Print(PanelText(cfg, global, false, 110, 0))
		return
	}
	fmt.Print("\x1b[?1049h\x1b[?25l")
	defer func() {
		fmt.Print("\x1b[?25h\x1b[?1049l")
		restore()
	}()
	m := &panel{cfg: cfg, global: global, paint: paint{true}}
	if !global && len(loadTasks(false)) == 0 && len(loadTasks(true)) > 0 {
		m.global = true // outside a repository with tasks, show the machine
	}
	keys := make(chan []byte, 8)
	go func() {
		for {
			buffer := make([]byte, 64)
			count, err := os.Stdin.Read(buffer)
			if err != nil {
				close(keys)
				return
			}
			keys <- buffer[:count]
		}
	}()
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	m.refresh()
	for {
		m.width, m.height = termSize(int(os.Stdout.Fd()))
		var out strings.Builder
		out.WriteString("\x1b[H")
		for index, line := range m.frame() {
			if index > 0 {
				out.WriteString("\r\n")
			}
			out.WriteString(line + "\x1b[K")
		}
		out.WriteString("\x1b[J")
		fmt.Print(out.String())
		select {
		case raw, open := <-keys:
			if !open {
				return
			}
			for _, name := range parseKeys(raw) {
				if m.key(name) {
					return
				}
			}
		case received := <-signals:
			if received != syscall.SIGWINCH {
				return
			}
		case <-ticker.C:
			m.refresh()
		}
	}
}
