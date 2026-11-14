package dev

import (
	"github.com/knaus94/agentgate/internal/config"
	"path/filepath"
	"time"
)

// item is one selectable row of the selected task.
type item struct {
	Kind  string // lane, check, findings, plan
	Index int
	Key   string // stable identity across refreshes and reviewer replacement
}

type panel struct {
	cfg       *config.Config
	global    bool
	tasks     []*taskView
	chosen    string // session directory of the selected task: the selection survives reordering
	focus     int    // 0: the task list, 1: the rows of the selected task
	row       int
	view      *item // an opened detail view, or nil
	scroll    int
	follow    bool
	width     int
	height    int
	paint     paint
	limits    string
	offset    int // overview viewport; keep the selected row visible on small terminals
	unlimited bool
	help      bool
	proofAt   time.Time
	proofKey  string
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
		out = append(out, item{Kind: "lane", Index: index, Key: task.Lanes[index].Dir})
	}
	for index := range task.Checks {
		out = append(out, item{Kind: "check", Index: index, Key: task.Checks[index].Argv})
	}
	if len(task.Open) > 0 || task.Notes > 0 {
		out = append(out, item{Kind: "findings"})
	}
	if task.Gate != nil && len(task.Gate.Signals) > 0 {
		out = append(out, item{Kind: "signals"})
	}
	if task.Gate != nil {
		out = append(out, item{Kind: "plan"})
	}
	return out
}

// refresh reads the state again. It is cheap enough to run every second.
func (m *panel) refresh() {
	previous := m.chosen
	oldTask := m.selected()
	m.tasks = loadTasks(m.global)
	if task := m.selected(); task != nil {
		proofKey := ""
		if task.Gate != nil {
			gate := task.Gate
			proofKey = digest([]any{task.Dir, gate.ID, gate.Phase, gate.PlanVersion, gate.Checks, gate.Plan.Checks, gate.ReviewDigest, gate.Resolutions, gate.Dispositions})
		}
		verify := oldTask == nil || proofKey != m.proofKey || time.Since(m.proofAt) >= 5*time.Second
		task.describeWithChecks(m.cfg, verify)
		if verify {
			m.proofAt, m.proofKey = time.Now(), proofKey
		} else if oldTask != nil {
			task.Checks, task.Next = oldTask.Checks, oldTask.Next
		}
	}
	if count := len(m.items()); m.row >= count {
		m.row = max(0, count-1)
	}
	if previous != "" && previous != m.chosen {
		m.view, m.offset = nil, 0
	}
	if m.view != nil {
		found := false
		for _, candidate := range m.items() {
			if candidate.Kind == m.view.Kind && candidate.Key == m.view.Key {
				*m.view, found = candidate, true
				break
			}
		}
		if !found {
			m.view = nil // do not silently show a different worker in the same row
		}
	}
	m.limits = limitsLine(filepath.Join(Root(), "accounts.json"))
}

// key applies one keypress and reports whether the panel should close.
func (m *panel) key(name string) bool {
	previous, global := m.chosen, m.global
	if name == "ctrl-c" || name == "q" {
		return true
	}
	if name == "?" {
		m.help = !m.help
		return false
	}
	if m.help {
		m.help = false
		return false
	}
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
			m.scroll, m.follow = m.scroll+1, false
		case "pgup":
			m.scroll, m.follow = m.scroll-page, false
		case "pgdn", " ":
			m.scroll, m.follow = m.scroll+page, false
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
					m.chosen, m.row, m.offset = m.tasks[next].Dir, 0, 0
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
	case "pgup":
		move(-max(1, m.height/3))
	case "pgdn", " ":
		move(max(1, m.height/3))
	case "home", "g":
		move(-max(len(m.tasks), len(m.items())))
	case "end", "G":
		move(max(len(m.tasks), len(m.items())))
	case "tab", "backtab":
		m.focus = 1 - m.focus
	case "right", "l":
		m.focus = 1
	case "left", "h", "esc":
		m.focus = 0
	case "a":
		m.global, m.chosen, m.row, m.offset = !m.global, "", 0, 0
	case "enter":
		if m.focus == 0 {
			m.focus = 1
		} else if items := m.items(); m.row < len(items) {
			chosen := items[m.row]
			m.view, m.scroll, m.follow = &chosen, 0, chosen.Kind == "lane"
		}
	}
	if name == "r" || previous != m.chosen || global != m.global {
		m.proofAt = time.Time{}
		m.refresh()
	}
	return false
}
