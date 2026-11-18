package dev

import (
	"encoding/json"
	"fmt"
	"github.com/knaus94/agentgate/internal/accounts"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// The panel is the interactive `agentgate watch`: every task of the checkout (or of every checkout), the
// stages each went through with their timings, its workers, checks and findings, and — one keypress deeper —
// what a worker is doing right now. It only reads state; the owning lead session drives the task.

// The panel redraws every second, from files that rarely change. Each is read again only when its identity,
// size or time changed: between changes a redraw reads no content.
type keptFile struct {
	info  os.FileInfo
	value any
}

var keptFiles = struct {
	sync.Mutex
	files map[string]keptFile
}{files: map[string]keptFile{}}

// readKept is what parse made of a file, read again only when the file changed; ok is false when it cannot be
// read.
func readKept[T any](path string, parse func([]byte) T) (value T, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return value, false
	}
	key := path + "\x00" + fmt.Sprintf("%T", value) // one file may be read into different shapes
	keptFiles.Lock()
	entry, known := keptFiles.files[key]
	keptFiles.Unlock()
	if known && os.SameFile(entry.info, info) && entry.info.Size() == info.Size() && entry.info.ModTime().Equal(info.ModTime()) {
		return entry.value.(T), true
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return value, false
	}
	value = parse(raw)
	keptFiles.Lock()
	if len(keptFiles.files) >= 4096 {
		clear(keptFiles.files)
	}
	keptFiles.files[key] = keptFile{info, value}
	keptFiles.Unlock()
	return value, true
}

// operationOf is the last operation of a session directory and whether its process still runs.
func operationOf(dir string) *ops.Record {
	parsed, ok := readKept(filepath.Join(dir, "operation.json"), parseOperation)
	if !ok || parsed == nil {
		return nil
	}
	record := *parsed
	record.Live = ops.Alive(&record)
	return &record
}

func parseOperation(raw []byte) *ops.Record {
	record := &ops.Record{}
	if json.Unmarshal(raw, record) != nil {
		return nil
	}
	return record
}

// keptGate is a task as its state file was last read.
type keptGate struct {
	gate *Gate
	err  error
}

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
	case t.Gate.Worktree != "" && t.Gate.Landed == "" && t.Gate.Step == StepDone:
		return "ready to land"
	case t.Gate.Landed != "":
		return "landed: " + t.Gate.Landed
	case t.Gate.Pause != nil && t.Gate.active():
		return "paused"
	}
	return t.Gate.Step
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
		task := &taskView{Dir: dir, Op: operationOf(dir)}
		state, _ := readKept(filepath.Join(dir, "state.json"), func(raw []byte) keptGate {
			gate, err := parseGate(raw)
			return keptGate{gate, err}
		})
		switch gate, err := state.gate, state.err; {
		case err != nil:
			task.Warning = "Task state cannot be read. Preserve state.json and restore its last good copy: " + err.Error()
		case gate != nil && sessionDir(gate.Cwd, gate.Session) != dir:
			task.Warning = "Task state belongs to another checkout. Preserve state.json and restore its last good copy."
		case gate != nil:
			task.Gate, task.Title, task.Repo = gate, gate.Request.Task, filepath.Base(gate.Cwd)
		}
		if task.Gate == nil {
			if task.Warning != "" {
				task.Title = "Unreadable task state · " + filepath.Base(dir)
			} else if !task.live() {
				continue
			} else {
				task.Title = "(" + task.Op.Action + " before a task starts)"
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

// processReceipt is what a worker's process.json says of its present run.
type processReceipt struct {
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

// readLane reads the receipt of a worker's present run.
func readLane(dir string) (laneView, bool) {
	parsed, ok := readKept(filepath.Join(dir, "process.json"), func(raw []byte) *processReceipt {
		receipt := &processReceipt{}
		if json.Unmarshal(raw, receipt) != nil {
			return nil
		}
		return receipt
	})
	if !ok || parsed == nil {
		return laneView{}, false
	}
	receipt := *parsed
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
	if runs, ok := readKept(filepath.Join(dir, "runs.jsonl"), func(raw []byte) int { return strings.Count(string(raw), "\n") }); ok {
		lane.Runs = runs
	}
	return lane, true
}

// findLanes reads the worker receipts at the places the tool puts workers, given as patterns below a root.
func findLanes(root string, places ...string) []laneView {
	var out []laneView
	for _, place := range places {
		dirs, _ := filepath.Glob(filepath.Join(root, place))
		for _, dir := range dirs {
			if lane, ok := readLane(dir); ok {
				out = append(out, lane)
			}
		}
	}
	return out
}

// The places a task's workers run: writers, review lanes and their alternates, browser reviewers.
var taskLanes = []string{"writers/*", "review-*/lane-*/run*", "visual-*/reviewer*"}

func routeText(route config.Route) string {
	if route.Alias == "session" {
		return routeName(route)
	}
	if route.Account != "" {
		return route.String() + " @" + route.Account
	}
	return route.String()
}

// commandText is a check as a person names it: without the environment assignments in front.
func commandText(argv []string) string { return strings.Join(commandArgv(argv), " ") }

func axesOf(laneDir string) string {
	axes, _ := readKept(filepath.Join(filepath.Dir(laneDir), "packet.json"), func(raw []byte) string {
		var packet struct {
			Axes []string `json:"axes"`
		}
		_ = json.Unmarshal(raw, &packet)
		return strings.Join(packet.Axes, "+")
	})
	return axes
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
		if parsed, ok := readKept(path, parseOperation); ok && parsed != nil && parsed.GateID == gateID {
			record := *parsed
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
		lanes = findLanes(t.Gate.dir(), taskLanes...)
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
		shared := append(findLanes(checkout, "scout/*", "scout/*/*", "designs/*/*"), findLanes(t.Dir, "consults/*/*/run*")...)
		for _, lane := range shared {
			if lane.Live {
				lanes = append(lanes, lane)
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
		if t.Gate != nil && len(t.Gate.Plan.Stages) > 1 && lane.Kind == "writer" && !lane.Live &&
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
		if gate.Dispositions[finding.ID].Decision == "rejected" {
			continue
		}
		if gate.isNote(cfg, finding) {
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
	gate.reading(func() { t.verifyChecks(cfg) })
}

// verifyChecks judges the declared checks of the task and what it needs, against one record of its files.
func (t *taskView) verifyChecks(cfg *config.Config) {
	gate := t.Gate
	// Check receipts are judged against the files of the task, so the files must be where the process is.
	here, _ := os.Getwd()
	where := gate.Cwd
	if gate.Worktree != "" && gate.Landed == "" {
		where = gate.Worktree
	}
	inPlace := os.Chdir(where) == nil
	receipts := map[string]*CheckReceipt{}
	if inPlace && gate.legacy == nil {
		receipts = gate.currentReceipts()
	}
	checks := gate.checks()
	for index := range checks {
		check := &checks[index]
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
	switch {
	case inPlace && gate.legacy != nil:
		t.Next = "Saved by an earlier version of the tool: its next command in the owning lead session brings it to the present format."
		_ = os.Chdir(here)
	case inPlace:
		needs := gate.needs(cfg)
		t.Next = gate.next(needs)
		// A person watching wants to know what the task waits for, not only that it waits.
		for index, need := range needs {
			if index == 2 {
				t.Next += fmt.Sprintf(" (+%d more)", len(needs)-index)
				break
			}
			t.Next += " · " + need.Kind + ": " + oneLine(need.What)
		}
		_ = os.Chdir(here)
	default:
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
	raw, ok := readKept(filepath.Join(dir, "runs.jsonl"), func(raw []byte) []byte { return raw })
	if !ok {
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

// savedAccounts are the configured accounts as the last readings left them, in the order of the configuration.
// The configuration, not the saved reading, says what an account may do. The panel itself asks no provider
// here: readings come from `agentgate accounts`, worker calls and the panel's own background refresh.
func savedAccounts(cfg *config.Config) []accounts.Status {
	type file struct {
		Accounts map[string]accounts.Status `json:"accounts"`
	}
	saved, _ := readKept(filepath.Join(Root(), "accounts.json"), func(raw []byte) file {
		var saved file
		_ = json.Unmarshal(raw, &saved)
		return saved
	})
	out := []accounts.Status{}
	for _, account := range cfg.Accounts {
		status := saved.Accounts[account.Name]
		if status.Home != account.Home || status.Provider != account.Provider {
			status = accounts.Status{} // a reading of another directory under this name
		}
		status.Account, status.Configured = account, true
		out = append(out, status)
	}
	return out
}

// limitsLine is the header's figure: per account its tightest limit, by name.
func limitsLine(statuses []accounts.Status) string {
	var parts []string
	for _, status := range statuses {
		if window, ok := status.Tightest(); ok {
			parts = append(parts, fmt.Sprintf("%s %s %.0f%%", status.Name, status.Label(window, true), window.Used))
		}
	}
	return strings.Join(parts, " · ")
}
