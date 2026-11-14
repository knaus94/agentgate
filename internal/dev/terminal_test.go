package dev

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
)

func TestTerminalInputSurvivesSplitSequencesAndPaste(t *testing.T) {
	decoder := keyDecoder{}
	for _, part := range []string{"\x1b", "["} {
		if got := decoder.feed([]byte(part)); len(got) != 0 {
			t.Fatalf("partial escape became input: %q", got)
		}
	}
	if got := strings.Join(decoder.feed([]byte("A\x1bO")), " "); got != "up" {
		t.Fatal(got)
	}
	if got := strings.Join(decoder.feed([]byte("H\x1b[1;5B")), " "); got != "home down" {
		t.Fatal(got)
	}
	if got := decoder.feed([]byte("\x1b[200~quit this pasted text\n")); len(got) != 0 {
		t.Fatalf("pasted command escaped: %q", got)
	}
	if got := decoder.feed([]byte("\x1b[20")); len(got) != 0 {
		t.Fatal(got)
	}
	if got := strings.Join(decoder.feed([]byte("1~j")), " "); got != "j" {
		t.Fatal(got)
	}
	if got := decoder.feed([]byte("\x1b")); len(got) != 0 {
		t.Fatal(got)
	}
	if got := strings.Join(decoder.flush(), " "); got != "esc" {
		t.Fatal(got)
	}
}

func TestCharacterDevicesAreNotAutomaticallyTerminals(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if isTerminal(file) {
		t.Fatal("/dev/null is not a terminal")
	}
	if isTerminal(nil) {
		t.Fatal("nil is not a terminal")
	}
}

func TestTerminalTextCannotSetClipboardOrMoveCursor(t *testing.T) {
	unsafe := "before\x1b]52;c;YXR0YWNr\x07middle\x1b[2Jafter\x1bPpayload\x1b\\!\x1b[31mred\x1b[0m\x00\u202esecret"
	got := terminalText(unsafe)
	if got != "beforemiddleafter!redsecret" {
		t.Fatalf("unsafe controls survived: %q", got)
	}
	for _, value := range []string{fit(unsafe, 100), strings.Join(wrap(unsafe, 8), ""), paint{}.bold(unsafe)} {
		if strings.ContainsAny(value, "\x1b\x00\x07") || strings.Contains(value, "payload") {
			t.Fatalf("unsafe terminal text: %q", value)
		}
	}
	styled := clipStyled("\x1b[31m你好\x1b[0m second", 5)
	if cells(styled) > 5 || !strings.Contains(styled, "你好") {
		t.Fatalf("wide chars or styles broke clipping: %q", styled)
	}
	if cells(fit("你好吗", 5)) != 5 || cells(fit("e\u0301", 3)) != 3 {
		t.Fatal("fit must use display cells")
	}
	if wrap("anything", 0) != nil {
		t.Fatal("zero width must terminate")
	}
	if got := wrap("你", 1); len(got) == 0 || cells(got[0]) != 1 {
		t.Fatal(got)
	}
}

func renderFixture(width, height int) *panel {
	gate := &Gate{ID: strings.Repeat("a", 32), Session: "session", Cwd: "/tmp/repo", Phase: "awaiting-review", Mode: "medium", CreatedAt: time.Now().Unix() - 10,
		Plan: &Plan{Brief: "A readable plan", Streams: []Stream{{Name: "core", Files: []string{"main.go"}}}}}
	task := &taskView{Dir: "task", Gate: gate, Title: "Проверить 你好 task \x1b]52;c;clipboard\x07", Repo: "repo"}
	for i := 0; i < 30; i++ {
		task.Lanes = append(task.Lanes, laneView{Kind: "writer", Name: fmt.Sprintf("stream-%02d", i), Route: "sol:high", Status: "COMPLETED", Note: "done"})
	}
	return &panel{cfg: &config.Config{}, tasks: []*taskView{task}, chosen: "task", width: width, height: height, focus: 1, row: 29}
}

func TestDashboardFitsNarrowTerminalsAndKeepsSelectionVisible(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {20, 5}, {39, 12}, {60, 20}, {80, 24}, {120, 40}} {
		m := renderFixture(size[0], size[1])
		lines := m.frame()
		if len(lines) != size[1] {
			t.Fatalf("%v: %d rows", size, len(lines))
		}
		for _, line := range lines {
			if cells(line) > size[0] {
				t.Fatalf("%v: overflowing line %q", size, line)
			}
			if strings.Contains(line, "clipboard") || strings.Contains(line, "\x1b]52") {
				t.Fatalf("untrusted terminal content: %q", line)
			}
		}
		if size[0] >= 39 && !strings.Contains(strings.Join(lines, "\n"), "stream-29") {
			t.Fatalf("%v: selected worker is outside viewport:\n%s", size, strings.Join(lines, "\n"))
		}
	}
}

func TestDashboardReportsCorruptStateInsteadOfPanicking(t *testing.T) {
	f := newFixture(t)
	dir := home()
	must(t, os.MkdirAll(dir, 0o700))
	for _, raw := range []string{`{broken`, `{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","phase":"reviewed"}`} {
		must(t, os.WriteFile(filepath.Join(dir, "state.json"), []byte(raw), 0o600))
		text := PanelText(f.cfg, false, false, 80, 0)
		if !strings.Contains(text, "Unreadable task state") || !strings.Contains(text, "state.json") {
			t.Fatal(text)
		}
		snapshot := WatchSnapshot(f.cfg, false)
		tasks := snapshot["tasks"].([]map[string]any)
		if len(tasks) != 1 || tasks[0]["error"] == nil {
			t.Fatal(snapshot)
		}
	}
}

func TestDashboardDoesNotReportAReusedPIDAsRunning(t *testing.T) {
	dir := t.TempDir()
	receipt := map[string]any{"state": "starting", "pid": os.Getpid(), "process_identity": "not-this-process"}
	raw, err := json.Marshal(receipt)
	must(t, err)
	must(t, os.WriteFile(filepath.Join(dir, "process.json"), raw, 0o600))
	if lane, ok := readLane(dir); !ok || lane.Live || lane.Status != "stopped" {
		t.Fatalf("%+v", lane)
	}
	receipt["process_identity"] = ops.ProcessIdentity(os.Getpid())
	raw, err = json.Marshal(receipt)
	must(t, err)
	must(t, os.WriteFile(filepath.Join(dir, "process.json"), raw, 0o600))
	if lane, ok := readLane(dir); !ok || !lane.Live {
		t.Fatalf("%+v", lane)
	}
}

func TestWatchSnapshotIsReadOnlyAndDoesNotNeedATerminal(t *testing.T) {
	f := newFixture(t)
	out := WatchSnapshot(f.cfg, false)
	if out["schema_version"] != 1 || out["read_only"] != true || len(out["tasks"].([]map[string]any)) != 0 {
		t.Fatal(out)
	}
	text := PanelText(f.cfg, false, false, 60, 0)
	if strings.Contains(text, "\x1b") || !strings.Contains(text, "No /dev task") {
		t.Fatal(text)
	}
}

func TestDashboardUsesRecordedReviewOrderAndLatestWriterRoute(t *testing.T) {
	f := newFixture(t)
	f.launch(f.plan("-m change the line"))
	gate := Current()
	for index, name := range []string{"review-9-ffff", "review-10-aaaa"} {
		dir := filepath.Join(gate.dir(), name)
		lane := filepath.Join(dir, "lane-0", "run")
		must(t, os.MkdirAll(lane, 0o700))
		write(t, filepath.Join(lane, "process.json"), fmt.Sprintf(`{"state":"finished","observed_at":%d,"route":{"alias":"astra","effort":"high"}}`, index+1))
		gate.Reviews = append(gate.Reviews, Review{Dir: dir, Lanes: []LaneRun{{Dir: lane}}})
	}
	writer := gate.Writers[len(gate.Writers)-1]
	old := writer.Dir
	writer.Dir += "-alt1"
	for _, dir := range []string{old, writer.Dir} {
		must(t, os.MkdirAll(dir, 0o700))
		write(t, filepath.Join(dir, "process.json"), `{"state":"finished","route":{"alias":"sol","effort":"high"}}`)
	}
	task := &taskView{Gate: gate, Dir: home()}
	task.describe(f.cfg)
	var reviewers, writers int
	for _, lane := range task.Lanes {
		if lane.Kind == "review" {
			reviewers++
			if !strings.Contains(lane.Dir, "review-10-aaaa") {
				t.Fatalf("lexical order selected stale review: %+v", lane)
			}
		}
		if lane.Kind == "writer" {
			writers++
			if lane.Dir != writer.Dir || lane.Name != writer.Stream {
				t.Fatalf("fallback lane lost stream identity: %+v", lane)
			}
		}
	}
	if reviewers != 1 || writers != 1 {
		t.Fatalf("workers=%d reviewers=%d", writers, reviewers)
	}
}

func TestTerminalReaderStopsWithoutInput(t *testing.T) {
	reader, writer, err := os.Pipe()
	must(t, err)
	defer reader.Close()
	defer writer.Close()
	done := make(chan struct{})
	keys := terminalInput(reader, done)
	close(done)
	select {
	case _, open := <-keys:
		if open {
			t.Fatal("unexpected input")
		}
	case <-time.After(time.Second):
		t.Fatal("terminal reader leaked after close")
	}
}
