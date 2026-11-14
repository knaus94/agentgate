package dev

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/knaus94/agentgate/internal/provider"
)

// scanFiles scans what a change added to each file, as verification does for the files a reviewer reads.
func scanFiles(files map[string][2]string) []Signal {
	var out []Signal
	for path, versions := range files {
		if scannable(path) {
			out = append(out, scanText(path, versions[0], versions[1])...)
		}
	}
	return out
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
	signals := scanFiles(map[string][2]string{"src/total.ts": {before, after}, "docs/notes.md": {"", "TODO: write this\nconsole.log(1)\n"},
		"generated/client.ts": {"", "console.log(1)\n"},
		"src/new.py":          {"", "try:\n    load()\nexcept Exception:\n    pass\n# in a real implementation this would retry\n"}})
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
	if len(scanText("a.go", "x := 1\n", "// the limit comes from the provider's quota\nx := 1\n")) != 0 {
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
			return `{"status":"COMPLETED","summary":"implemented","blockers":[],"handoff":"","deviations":[],"disputes":[]}`, nil
		case "review":
			mu.Lock()
			packetSignals = packet["slop_signals"]
			mu.Unlock()
		}
		return "", nil
	}
	summary := f.start(f.plan("-m add the limit", stream))
	raw, _ := json.Marshal(packetSignals)
	if !strings.Contains(string(raw), `"kind":"debug-output"`) || !strings.Contains(string(raw), `"line":2`) {
		t.Fatalf("reviewers receive the signals in their packet: %s", raw)
	}
	if summary["step"] != StepDone || len(Current().Signals) != 1 {
		t.Fatalf("a signal is a lead for the reviewer, not a verdict: %v", summary)
	}
	lanes := Current().Reviews[0].Lanes
	if strings.Join(lanes[0].Axes, "+") != "correctness+architecture+simplicity" || len(lanes[0].Reports) != 3 {
		t.Fatalf("simplicity is judged by the same reviewer, with its own report: %+v", lanes[0])
	}
	if text := PanelText(f.cfg, false, false, 120, 0); !strings.Contains(text, "SIGNALS  1 leftovers") {
		t.Fatalf("the panel shows the signals:\n%s", text)
	}

	// Light mode has no reviewer process: the signals come with the patch for the lead to judge.
	g := newFixture(t)
	g.model = f.model
	handed := g.start(g.plan("-l add the limit", stream))
	need := handed["needs"].([]any)[0].(map[string]any)
	packet := fileText(t, need["detail"].(map[string]any)["packet"].(string))
	if !strings.Contains(packet, `"kind": "debug-output"`) || !strings.Contains(need["resolve"].(string), "slop_signal") {
		t.Fatalf("in light mode the lead gets the signals with the patch: %v", need)
	}
}
