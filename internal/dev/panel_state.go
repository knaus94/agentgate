package dev

import (
	"encoding/json"
	"fmt"
	"github.com/knaus94/agentgate/internal/accounts"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// The panel is the interactive `agentgate watch`: every task of the checkout (or of every checkout), the
// stages each went through with their timings, its workers, checks and findings, and — one keypress deeper —
// what a worker is doing right now. It only reads state; the owning lead session drives the task.

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
	Warning string
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
	case t.Warning != "":
		return "state error"
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
	case t.Warning != "":
		return 1
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
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		task := &taskView{Dir: dir, Op: ops.Status(dir)}
		if raw, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
			gate := &Gate{}
			if err := json.Unmarshal(raw, gate); err != nil {
				task.Warning = "Task state cannot be decoded. Preserve state.json and restore its last good copy."
			} else if validateGateState(gate) != nil || sessionDir(gate.Cwd, gate.Session) != dir {
				task.Warning = "Task state is incomplete or belongs to another checkout. Preserve state.json and restore its last good copy."
			} else {
				task.Gate, task.Title, task.Repo = gate, gate.Request.Task, filepath.Base(gate.Cwd)
			}
		} else if !os.IsNotExist(err) {
			task.Warning = "Task state cannot be read: " + err.Error()
		}
		if task.Gate == nil {
			if task.Warning != "" {
				task.Title = "Unreadable task state · " + filepath.Base(dir)
			} else if !task.live() {
				continue
			} else {
				task.Title = "(" + task.Op.Action + " before launch)"
			}
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
	if t.Op != nil {
		return int64(t.Op.StartedAt)
	}
	return 0
}

// readLane reads the receipt of a worker's present run.
func readLane(dir string) (laneView, bool) {
	var receipt struct {
		State    string `json:"state"`
		PID      int    `json:"pid"`
		Identity string `json:"process_identity"`
		Route    struct {
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
		identity := ops.ProcessIdentity(receipt.PID)
		if identity != "" && (receipt.Identity == "" || receipt.Identity == identity) {
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
	t.describeWithChecks(cfg, true)
}

func (t *taskView) describeWithChecks(cfg *config.Config, verify bool) {
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
	var newestTime int64
	latestWriters := map[string]*Writer{}
	if t.Gate != nil {
		for _, writer := range t.Gate.latest() {
			latestWriters[writer.Dir] = writer
		}
		if count := len(t.Gate.Reviews); count > 0 {
			newestReview = filepath.Base(t.Gate.Reviews[count-1].Dir)
		}
	}
	for _, lane := range lanes {
		if rel, err := filepath.Rel(t.Dir, lane.Dir); err == nil {
			if part := strings.Split(rel, string(filepath.Separator)); len(part) > 1 && strings.HasPrefix(part[1], "review-") &&
				(t.Gate == nil || len(t.Gate.Reviews) == 0) && (newestReview == "" || max(lane.Started, lane.Ended) > newestTime) {
				newestReview = part[1]
				newestTime = max(lane.Started, lane.Ended)
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
		if writer := latestWriters[lane.Dir]; writer != nil {
			lane.Kind, lane.Name = "writer", writer.Stream
		} else if lane.Kind == "writer" && !lane.Live {
			continue // a superseded stream or failed fallback attempt has an archived receipt
		}
		// A staged task shows the workers of its present stage; those of accepted stages are in the archive.
		if t.Gate != nil && t.Gate.Epic != nil && lane.Kind == "writer" && !lane.Live &&
			!slices.ContainsFunc(t.Gate.Writers, func(writer *Writer) bool { return writer.Dir == lane.Dir }) {
			continue
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
	if !verify {
		return
	}
	// Check receipts are judged against the files of the task, so the files must be where the process is.
	here, _ := os.Getwd()
	where := gate.Cwd
	if gate.Worktree != "" && gate.Landed == "" {
		where = gate.Worktree
	}
	inPlace := os.Chdir(where) == nil
	receipts := map[string]*CheckReceipt{}
	if inPlace {
		receipts = gate.currentReceipts()
	}
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
			if receipt := receipts[checkID(check.Argv)]; receipt != nil {
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
		}
		t.Checks = append(t.Checks, view)
	}
	if inPlace {
		t.Next = gate.nextWithReceipts(cfg, t.Blocked > 0, receipts)
		_ = os.Chdir(here)
	} else {
		t.Next = "The task checkout is unavailable; restore it before continuing."
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

// limitsLine is the last known use of each subscription, from the readings `agentgate accounts` and the
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
