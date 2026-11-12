package dev

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/knaus94/agentgate/internal/provider"
)

func state(text string) *FileState {
	return &FileState{Bytes: base64.StdEncoding.EncodeToString([]byte(text)), Mode: 0o644}
}

func TestOnlyAddedLinesRaiseSignals(t *testing.T) {
	before := "export function total(items) {\n  // TODO: handle discounts\n  console.log(items)\n  return sum(items)\n}\n"
	after := `import { sum } from './sum'
// Added: totals are now rounded
export function total(items: Item[]) {
  // TODO: handle discounts
  console.log(items)
  console.log('total', items)
  const safe = items as any
  try {
    return round(sum(safe))
  } catch (error) {
  }
  // rounds half away from zero because invoices are printed that way
  return 0
} // end total
it.only('totals', () => {})
// eslint-disable-next-line no-console
`
	signals := scanSignals(Snapshot{"src/total.ts": state(before), "docs/notes.md": state("")},
		Snapshot{"src/total.ts": state(after), "docs/notes.md": state("TODO: write this\nconsole.log(1)\n"),
			"generated/client.ts": state("console.log(1)\n"), "src/new.py": state("try:\n    load()\nexcept Exception:\n    pass\n# in a real implementation this would retry\n")},
		[]string{"src/total.ts", "docs/notes.md", "generated/client.ts", "src/new.py"})
	got := map[string]int{}
	for _, signal := range signals {
		got[signal.Kind]++
		if signal.Path != "src/total.ts" && signal.Path != "src/new.py" {
			t.Errorf("prose and generated files are not scanned: %+v", signal)
		}
	}
	want := map[string]int{"change-narration": 1, "debug-output": 1, "suppression": 2, "swallowed-error": 2, "brace-label": 1, "disabled-test": 1, "placeholder": 1}
	for kind, count := range want {
		if got[kind] != count {
			t.Errorf("%s: want %d, got %d in %+v", kind, count, got[kind], signals)
		}
	}
	if got["todo"] != 0 {
		t.Fatalf("a line that was there before the task is not the task's: %+v", signals)
	}
	for _, signal := range signals {
		if signal.Kind == "debug-output" && (signal.Line != 6 || !strings.Contains(signal.Text, "'total'")) {
			t.Fatalf("the signal names the added line, not the old one: %+v", signal)
		}
	}
	if len(scanSignals(Snapshot{"a.go": state("x := 1\n")}, Snapshot{"a.go": state("// the limit comes from the provider's quota\nx := 1\n")}, []string{"a.go"})) != 0 {
		t.Fatal("an ordinary comment raises nothing")
	}
}

func TestSignalsReachTheReviewersAndTheLead(t *testing.T) {
	f := newFixture(t)
	stream := Stream{Name: "core", Domain: "backend", Files: []string{"app.ts"}}
	var mu sync.Mutex
	var packetSignals any
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		switch kind {
		case "writer":
			_ = os.WriteFile("app.ts", []byte("export const limit = 5\nconsole.log('limit', limit)\n"), 0o644)
			return `{"status":"COMPLETED","summary":"implemented","blockers":[],"handoff":"","deviations":[]}`, nil
		case "review":
			mu.Lock()
			packetSignals = packet["slop_signals"]
			mu.Unlock()
		}
		return "", nil
	}
	f.launch(f.plan("-m add the limit", stream))
	summary := f.review()
	raw, _ := json.Marshal(packetSignals)
	if !strings.Contains(string(raw), `"kind":"debug-output"`) || !strings.Contains(string(raw), `"line":2`) {
		t.Fatalf("reviewers receive the signals in their packet: %s", raw)
	}
	if listed := summary["slop_signals"].([]any); len(listed) != 1 {
		t.Fatalf("the lead sees them in the summary: %v", summary["slop_signals"])
	}
	lanes := Current().Reviews[0].Lanes
	if strings.Join(lanes[0].Axes, "+") != "architecture+simplicity" || len(lanes[0].Reports) != 2 {
		t.Fatalf("simplicity is judged in the design lane, with its own report: %+v", lanes[0])
	}
	if text := PanelText(f.cfg, false, false, 120, 0); !strings.Contains(text, "SIGNALS  1 leftovers") {
		t.Fatalf("the panel shows the signals:\n%s", text)
	}
	f.finish()

	// Light mode has no reviewer process: the signals come back with the diffs for the lead to judge.
	g := newFixture(t)
	g.model = f.model
	g.launch(g.plan("-l add the limit", stream))
	handed := g.do("review", func() (any, error) { return ReviewTask(g.cfg, Current(), ReviewInput{}) })
	if listed, _ := handed["slop_signals"].([]any); len(listed) != 1 || !strings.Contains(handed["next"].(string), "slop_signal") {
		t.Fatalf("in light mode the lead gets the signals with the diffs: %v", handed)
	}
}
