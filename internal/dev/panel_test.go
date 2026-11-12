package dev

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const codexStream = `{"type":"thread.started","thread_id":"t"}
{"type":"item.completed","item":{"id":"i0","type":"agent_message","text":"I will read the change first."}}
{"type":"item.started","item":{"id":"i1","type":"command_execution","command":"/bin/bash -lc 'rg -n bankCarry libs'"}}
{"type":"item.completed","item":{"id":"i1","type":"command_execution","command":"/bin/bash -lc 'rg -n bankCarry libs'","aggregated_output":"libs/a.ts:1:bankCarry\n","exit_code":0}}
{"type":"item.started","item":{"id":"i2","type":"command_execution","command":"/bin/bash -lc 'yarn test'"}}
{"type":"item.completed","item":{"id":"i2","type":"command_execution","command":"/bin/bash -lc 'yarn test'","aggregated_output":"1 failed\n","exit_code":1}}
{"type":"item.completed","item":{"id":"i3","type":"file_change","changes":[{"path":"libs/a.ts","kind":"update"}]}}
not json at all
{"type":"item.started","item":{"id":"i4","type":"command_execution","command":"/bin/bash -lc 'sleep 5'"}}
{"type":"item.completed","item":{"id":"i5","type":"agent_message","text":"{\"reports\":[{\"axis\":\"correctness\",\"status\":\"PASS\",\"findings\":[]}]}"}}
`

const claudeStream = `{"type":"system","subtype":"init"}
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"The brief asks for one line."},{"type":"tool_use","id":"a","name":"Read","input":{"file_path":"calc.py"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"1\tdef add"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"b","name":"Bash","input":{"command":"cat > calc.py <<'EOF'\ndef add(a, b):\n    return a + b\nEOF\npython3 -m pytest -q"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b","content":[{"type":"text","text":"2 passed"}]}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"c","name":"Edit","input":{"file_path":"calc.py"}},{"type":"text","text":"Fixed the sign."}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"d","name":"StructuredOutput","input":{"status":"COMPLETED","summary":"Fixed  the sign"}}]}}
{"type":"result","subtype":"success","is_error":false,"session_id":"s","result":"{}"}
`

func kinds(events []Event) string {
	var out []string
	for _, event := range events {
		out = append(out, event.Kind)
	}
	return strings.Join(out, " ")
}

func TestWorkerActivityIsReadFromBothCLIs(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(codexStream), 0o600))
	events := laneEvents(dir)
	if kinds(events) != "say run run edit run done" || events[5].Text != "reported: correctness PASS, 0 findings" {
		t.Fatalf("codex events: %s", kinds(events))
	}
	if events[1].Text != "rg -n bankCarry libs" || events[1].Detail != "libs/a.ts:1:bankCarry" || events[1].Failed {
		t.Fatalf("a command loses its shell wrapper and gains its output: %+v", events[1])
	}
	if !events[2].Failed || events[2].Detail != "1 failed" || events[3].Text != "update libs/a.ts" || events[4].Detail != "" {
		t.Fatalf("a failed command, a file change and a command still running: %+v", events[2:])
	}
	must(t, os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(claudeStream), 0o600))
	events = laneEvents(dir)
	if kinds(events) != "think read run edit say done" {
		t.Fatalf("claude events: %s", kinds(events))
	}
	if events[1].Text != "calc.py" || events[1].Detail != "" || !strings.HasPrefix(events[2].Text, "cat > calc.py <<'EOF'\ndef add") ||
		events[2].Detail != "2 passed" || events[5].Text != "reported COMPLETED: Fixed the sign" {
		t.Fatalf("a read shows its file, a command its output: %+v", events[1:3])
	}
	if laneEvents(t.TempDir()) != nil {
		t.Fatal("a lane without a stream has no activity")
	}
	long := strings.Repeat("line\n", 40)
	if got := lastLines(long, 8); strings.Count(got, "\n") != 8 || !strings.HasPrefix(got, "(32 lines above)") {
		t.Fatalf("long output is cut to its end: %q", got)
	}
}

func TestKeysAreParsed(t *testing.T) {
	got := strings.Join(parseKeys([]byte("j\x1b[A\x1b[6~\r\t\x1bq\x03\x1b[")), " ")
	if got != "j up pgdn enter tab esc q ctrl-c" {
		t.Fatalf("keys: %q", got)
	}
}

func TestPanelShowsTasksStagesWorkersAndOpensDetails(t *testing.T) {
	f := newFixture(t)
	empty := &panel{cfg: f.cfg, width: 100, height: 30}
	empty.refresh()
	if text := strings.Join(empty.frame(), "\n"); !strings.Contains(text, "No /dev task here yet") {
		t.Fatalf("an empty checkout says so:\n%s", text)
	}
	plan := f.plan("-m change the line")
	plan.Checks = []Check{{Argv: []string{"env", "NX_DAEMON=false", "sh", "-c", "echo checked the line"}}}
	f.launch(plan)
	gate := Current()

	// A reviewer is running right now: a receipt with a live process and a stream of events.
	lane := filepath.Join(gate.dir(), "review-0-abcd", "lane-1", "run")
	must(t, os.MkdirAll(lane, 0o700))
	must(t, os.WriteFile(filepath.Join(filepath.Dir(lane), "packet.json"), []byte(`{"axes":["correctness"]}`), 0o600))
	must(t, os.WriteFile(filepath.Join(lane, "process.json"), []byte(fmt.Sprintf(
		`{"state":"starting","pid":%d,"route":{"alias":"astra","effort":"medium"},"account":"codex","started_at":%d}`, os.Getpid(), time.Now().Unix()-75)), 0o600))
	must(t, os.WriteFile(filepath.Join(lane, "events.jsonl"), []byte(codexStream), 0o600))
	must(t, os.WriteFile(filepath.Join(lane, "runs.jsonl"), []byte("{}\n{}\n"), 0o600))

	m := &panel{cfg: f.cfg, width: 120, height: 40, paint: paint{}}
	m.refresh()
	text := strings.Join(m.frame(), "\n")
	for _, want := range []string{"TASKS 1", "awaiting-review", "steps    launch", "WORKERS", "writer  core", "review  correctness", "astra:medium @codex",
		"1m15s ×2", "✔ reported: correctness PASS, 0 findings", "CHECKS", "sh -c echo checked the line", "PLAN  1 streams", "enter open"} {
		if !strings.Contains(text, want) {
			t.Errorf("the panel lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "NX_DAEMON") {
		t.Fatal("a check is named without its environment assignments")
	}
	for _, line := range m.frame() {
		if len([]rune(line)) > 121 {
			t.Fatalf("a line is wider than the screen: %q", line)
		}
	}

	// enter moves into the task; the reviewer is the second row; enter opens its activity.
	for _, key := range []string{"enter", "down", "enter"} {
		if m.key(key) {
			t.Fatal("navigation must not close the panel")
		}
	}
	if m.view == nil || m.view.Kind != "lane" || !m.follow {
		t.Fatalf("a worker opens as a followed activity view: %+v", m.view)
	}
	detail := strings.Join(m.frame(), "\n")
	for _, want := range []string{"review correctness", "RUNNING", "▸ I will read the change first.", "$ rg -n bankCarry libs", "│ libs/a.ts:1:bankCarry",
		"$ yarn test", "│ 1 failed", "✎ update libs/a.ts", "task:/review-0-abcd/lane-1/run", "esc back"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the activity view lacks %q:\n%s", want, detail)
		}
	}
	m.key("up")
	if m.follow {
		t.Fatal("scrolling up stops following")
	}
	m.key("esc")
	if m.view != nil {
		t.Fatal("esc returns to the task")
	}

	// The check's row opens its output; the plan row opens the brief.
	m.key("down")
	m.key("enter")
	if m.view == nil || m.view.Kind != "check" || !strings.Contains(strings.Join(m.frame(), "\n"), "checked the line") {
		t.Fatalf("a check opens its log:\n%s", strings.Join(m.frame(), "\n"))
	}
	m.key("esc")
	m.key("down")
	m.key("enter")
	if m.view == nil || m.view.Kind != "plan" || !strings.Contains(strings.Join(m.frame(), "\n"), "Change the specified line") {
		t.Fatalf("the plan row opens the brief:\n%s", strings.Join(m.frame(), "\n"))
	}
	if m.key("esc") || !m.key("q") {
		t.Fatal("q closes the panel")
	}

	// From another directory the same task is found in the view of every checkout.
	t.Chdir(t.TempDir())
	if local := PanelText(f.cfg, false, false, 120, 0); !strings.Contains(local, "every checkout") || !strings.Contains(local, "repo") {
		t.Fatalf("outside a repository with tasks the panel shows the machine:\n%s", local)
	}
}
