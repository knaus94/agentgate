package dev

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/knaus94/claude-dev/internal/accounts"
	"github.com/knaus94/claude-dev/internal/config"
	"github.com/knaus94/claude-dev/internal/ops"
	"github.com/knaus94/claude-dev/internal/provider"
)

func write(t *testing.T, name, content string) {
	t.Helper()
	must(t, os.WriteFile(name, []byte(content), 0o644))
}

func TestPlanValidationRejectsUnsafeOrAmbiguousScope(t *testing.T) {
	f := newFixture(t)
	must(t, os.MkdirAll("dir", 0o755))
	must(t, os.Symlink("a.txt", "link.txt"))
	for name, files := range map[string][]string{"absolute": {"/etc/passwd"}, "parent": {"../x"}, "git": {".git/config"},
		"directory": {"dir"}, "symlink": {"link.txt"}, "empty": {}} {
		if _, err := Resolve(f.cfg, f.plan("-m task", Stream{Name: "core", Domain: "backend", Files: files})); err == nil {
			t.Errorf("%s scope must be rejected", name)
		}
	}
	overlap := f.plan("-m task", Stream{Name: "a", Domain: "backend", Files: []string{"a.txt"}}, Stream{Name: "b", Domain: "backend", Files: []string{"a.txt"}})
	if _, err := Resolve(f.cfg, overlap); err == nil || !strings.Contains(err.Error(), "disjoint") {
		t.Fatalf("streams must be disjoint, got %v", err)
	}
	created := f.plan("-m task", Stream{Name: "core", Domain: "backend", Files: []string{"new/file.txt"}})
	if _, err := Resolve(f.cfg, created); err != nil {
		t.Fatalf("a file that does not exist yet is a valid scope: %v", err)
	}
	bad := f.plan("-m task")
	bad.Streams[0].Domain = "database"
	if _, err := Resolve(f.cfg, bad); err == nil {
		t.Fatal("an unknown domain must be rejected")
	}
	risky := f.plan("task")
	risky.Assessment.Risks = []string{"vibes"}
	if _, err := Resolve(f.cfg, risky); err == nil {
		t.Fatal("an unknown risk must be rejected")
	}
	plan := &Plan{}
	err := json.Unmarshal([]byte(`{"request":"-m x","brief":"b","streams":[{"name":"c","domain":"backend","files":["a.txt"]}],"checks":[["true"],{"argv":["false"],"baseline":true}]}`), plan)
	if err != nil || len(plan.Checks) != 2 || plan.Checks[0].Argv[0] != "true" || !plan.Checks[1].Baseline {
		t.Fatalf("checks accept both forms: %+v %v", plan, err)
	}
}

func TestUIFilesNeedAContract(t *testing.T) {
	f := newFixture(t)
	write(t, "view.tsx", "before\n")
	ui := Stream{Name: "web", Domain: "frontend", Files: []string{"view.tsx"}}
	if _, err := Resolve(f.cfg, f.plan("-m change the label", ui)); err == nil || !strings.Contains(err.Error(), "UI files") {
		t.Fatalf("UI files without a contract must be rejected, got %v", err)
	}
	for _, contract := range []*UI{{Summary: "rename"}, {Summary: "rename", Criteria: []string{"c"}, Preserve: []string{"p"}, Skip: "x"}, {Design: "abc"}} {
		plan := f.plan("-m change the label", ui)
		plan.UI = contract
		if _, err := Resolve(f.cfg, plan); err == nil {
			t.Errorf("incomplete or mixed ui contract must be rejected: %+v", contract)
		}
	}
	plan := f.plan("-m change the label", ui)
	plan.UI = &UI{Skip: "no visible effect"}
	plan.Visual = &Visual{Enabled: true, Reason: "asked", Scenarios: []Scenario{{ID: "s", Screen: "S", State: "x", Viewport: "1440x900", Role: "r", Expected: "e"}}}
	if _, err := Resolve(f.cfg, plan); err == nil {
		t.Fatal("browser verification needs a real ui contract")
	}
	plan.UI = &UI{Summary: "rename", Criteria: []string{"new copy"}, Preserve: []string{"the rest"}}
	plan.Visual.Scenarios[0].Viewport = "big"
	if _, err := Resolve(f.cfg, plan); err == nil {
		t.Fatal("a viewport must be WIDTHxHEIGHT")
	}
}

func TestReviewOfAnExistingDiffIsReadOnly(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap is not installed")
	}
	f := newFixture(t)
	write(t, "a.txt", "changed\n")
	write(t, "b.txt", "changed\n")
	plan := f.plan("-2 -m review the branch", Stream{Name: "one", Domain: "backend", Files: []string{"a.txt"}},
		Stream{Name: "two", Domain: "backend", Files: []string{"b.txt"}})
	plan.Checks = []Check{{Argv: []string{"sh", "-c", "echo leak > leaked.txt"}}}
	started := f.launch(plan)
	if started["phase"] != "awaiting-review" || f.count("writer") != 0 {
		t.Fatalf("a review starts no writers: %v", started)
	}
	if Current().Baseline["a.txt"].text() != "before\n" {
		t.Fatal("the reviewed diff starts at the base commit, not at the working tree")
	}
	f.review()
	lanes := Current().Reviews[0].Lanes
	if len(lanes) != 4 || lanes[0].Scope != "one" || lanes[1].Scope != "two" || !slices.Equal(lanes[1].Files, []string{"b.txt"}) {
		t.Fatalf("a multi-stream review gives each scope its own lane: %+v", lanes)
	}
	if _, err := os.Stat("leaked.txt"); err == nil {
		t.Fatal("a check of a read-only review must not be able to write the checkout")
	}
	f.fails("fix", "no authority", func() (any, error) { return Fix(f.cfg, Current(), FixInput{Brief: "x"}) })
}

func TestContinuationKeepsTheGateAndItsBaseline(t *testing.T) {
	f := newFixture(t)
	first := f.plan("-m build it")
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && packet["stream"].(map[string]any)["name"] == "core" && f.count("writer") == 1 {
			_ = os.WriteFile("a.txt", []byte("partial\n"), 0o644)
			return "", os.ErrDeadlineExceeded
		}
		return "", nil
	}
	if _, err := Execute("launch", func() (any, error) { return Launch(f.cfg, first) }); err == nil {
		t.Fatal("a failed writer fails the launch")
	}
	gate := Current()
	if gate.Phase != "held" || gate.latest()["core"].Status != "FAILED" {
		t.Fatalf("a failed writer holds the task with partial work kept: %s", gate.Phase)
	}
	f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) })
	second := f.plan("-m build it", Stream{Name: "core", Domain: "backend", Files: []string{"a.txt"}}, Stream{Name: "extra", Domain: "backend", Files: []string{"b.txt"}})
	f.fails("launch", "update", func() (any, error) { return Launch(f.cfg, second) })
	second.Update = "the user added b.txt"
	dropped := f.plan("-m build it", Stream{Name: "extra", Domain: "backend", Files: []string{"b.txt"}})
	dropped.Update = "drop a.txt"
	f.fails("launch", "keeps every owned file", func() (any, error) { return Launch(f.cfg, dropped) })
	done := f.launch(second)
	after := Current()
	if done["phase"] != "awaiting-review" || after.ID != gate.ID || after.PlanVersion != 2 || after.Baseline["a.txt"].text() != "before\n" ||
		!slices.Equal(after.Files, []string{"a.txt", "b.txt"}) {
		t.Fatalf("the same gate continues with its original baseline: %v %+v", done, after.Files)
	}
	skip := f.plan("-m build it", Stream{Name: "core", Domain: "backend", Files: []string{"a.txt"}, Done: true}, Stream{Name: "extra", Domain: "backend", Files: []string{"b.txt"}})
	skip.Update = "only extra remains"
	before := f.count("writer")
	Current().Phase = "implementing"
	gate = Current()
	gate.Phase = "implementing"
	must(t, gate.save())
	f.launch(skip)
	if f.count("writer") != before+1 {
		t.Fatal("a stream marked done keeps its files and starts no writer")
	}
}

func TestTriageRules(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" && len(packet["accepted_findings"].([]any)) == 0 {
			variant := func(severity, evidence string) string {
				return strings.Replace(finding(severity), "wrong value", evidence, 1)
			}
			return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + variant("P1", "first") + `,` + variant("P1", "second") + `,` +
				variant("P2", "third") + `,` + variant("P3", "polish") + `],"checked":["read"],"resolutions":[]},` +
				`{"axis":"architecture","status":"PASS","findings":[],"checked":["read"],"resolutions":[]}]}`, nil
		}
		return "", nil
	}
	f.launch(f.plan("-m change the line"))
	f.review()
	by := map[string]string{}
	for _, item := range Current().Findings() {
		by[strings.Fields(item.Evidence)[0]] = item.ID
	}
	triage := func(items ...Disposition) error {
		_, err := Execute("triage", func() (any, error) { return Triage(f.cfg, Current(), TriageInput{items}) })
		return err
	}
	for name, item := range map[string]Disposition{
		"unknown id":             {ID: "nope", Decision: "accepted", Evidence: "e"},
		"no evidence":            {ID: by["first"], Decision: "accepted"},
		"bad decision":           {ID: by["first"], Decision: "maybe", Evidence: "e"},
		"polish without basis":   {ID: by["polish"], Decision: "accepted", Evidence: "e"},
		"duplicate not rejected": {ID: by["second"], Decision: "accepted", Evidence: "e", DuplicateOf: by["first"]},
		"duplicate of itself":    {ID: by["second"], Decision: "rejected", Evidence: "e", DuplicateOf: by["second"]},
	} {
		if err := triage(item); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	must(t, triage(Disposition{ID: by["first"], Decision: "deferred", Evidence: "later"},
		Disposition{ID: by["second"], Decision: "rejected", Evidence: "same root cause", DuplicateOf: by["first"]},
		Disposition{ID: by["third"], Decision: "deferred", Evidence: "not now"}))
	blocking := f.do("status", func() (any, error) { return Current().Summary(f.cfg), nil })["blocking"].([]any)
	if len(blocking) != 1 || blocking[0] != by["first"] {
		t.Fatalf("a deferred P1 still blocks in medium; a deferred P2, a rejected duplicate and a note do not: %v", blocking)
	}
	must(t, triage(Disposition{ID: by["first"], Decision: "needs-approval", Evidence: "user must decide"}))
	f.fails("finish", "block", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "s"}) })
	must(t, triage(Disposition{ID: by["first"], Decision: "rejected", Evidence: "contrary evidence in a.txt:1"}))
	f.finish()
	write(t, "a.txt", "edited after the review\n")
	if hook := StopHook(Current().Cwd, "fixture-session"); hook["decision"] != "block" || Current().Phase != "awaiting-review" {
		t.Fatal("editing finished work reopens the gate")
	}
}

func TestStalledCorrectionNeedsANewDiagnosis(t *testing.T) {
	f := newFixture(t)
	var id string
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" {
			report := strings.Replace(finding("P1"), `"id":""`, `"id":"`+id+`"`, 1)
			return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + report + `],"checked":["read"],"resolutions":[]},` +
				`{"axis":"architecture","status":"PASS","findings":[],"checked":["read"],"resolutions":[]}]}`, nil
		}
		if kind == "writer" && packet["correction"] != nil {
			raw, _ := os.ReadFile("a.txt")
			_ = os.WriteFile("a.txt", append(raw, []byte("attempt\n")...), 0o644)
			if len(packet["correction"].(map[string]any)["findings"].([]any)) != 1 || packet["your_previous_report"] == nil {
				t.Errorf("the correction packet names the findings and the writer's history: %v", packet["correction"])
			}
			return `{"status":"COMPLETED","summary":"tried","blockers":[],"handoff":"","deviations":[]}`, nil
		}
		return "", nil
	}
	f.launch(f.plan("-m change the line"))
	f.review()
	id = Current().Findings()[0].ID
	accept := func() {
		f.do("triage", func() (any, error) {
			return Triage(f.cfg, Current(), TriageInput{[]Disposition{{ID: id, Decision: "accepted", Evidence: "verified"}}})
		})
	}
	accept()
	f.do("fix", func() (any, error) { return Fix(f.cfg, Current(), FixInput{Brief: "first remedy"}) })
	f.review()
	if len(Current().Findings()) != 1 || Current().progress().Status != "no-confirmed-fix" {
		t.Fatalf("a finding reported again keeps its id and counts as no progress: %+v", Current().progress())
	}
	f.fails("fix", "reassessment", func() (any, error) { return Fix(f.cfg, Current(), FixInput{Brief: "same again"}) })
	f.do("fix", func() (any, error) {
		return Fix(f.cfg, Current(), FixInput{Brief: "new remedy", Reassessment: &Reassessment{Evidence: "the cause is elsewhere", ChangedApproach: "fix the caller"}})
	})
	if Current().Iteration != 2 || len(Current().Corrections) != 2 {
		t.Fatalf("a reassessed correction is a new round: %d", Current().Iteration)
	}
	efforts := []string{}
	for _, writer := range Current().Writers {
		efforts = append(efforts, writer.Route.Effort)
	}
	if !slices.Equal(efforts, []string{"medium", "medium", "high"}) {
		t.Fatalf("effort rises only after a correction stalled, and one step: %v", efforts)
	}
}

func TestReviewRefusesStaleOrBlockedEvidence(t *testing.T) {
	f := newFixture(t)
	f.launch(f.plan("-m change the line"))
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" {
			_ = os.WriteFile("a.txt", []byte("edited while the reviewer was reading\n"), 0o644)
		}
		return "", nil
	}
	f.fails("review", "changed during the review", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{}) })
	if Current().Phase != "awaiting-review" || len(Current().Reviews) != 0 {
		t.Fatal("a stale review is discarded")
	}
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" {
			return `{"reports":[{"axis":"correctness","status":"BLOCKED","findings":[],"checked":["the packet could not be read"],"resolutions":[]},` +
				`{"axis":"architecture","status":"PASS","findings":[],"checked":["read"],"resolutions":[]}]}`, nil
		}
		return "", nil
	}
	f.fails("review", "could not review", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{}) })
	for name, answer := range map[string]string{
		"missing axis":     `{"reports":[]}`,
		"an extra axis":    `{"reports":[{"axis":"correctness","status":"PASS","findings":[],"checked":["x"],"resolutions":[]},{"axis":"security","status":"PASS","findings":[],"checked":["x"],"resolutions":[]}]}`,
		"nothing checked":  `{"reports":[{"axis":"correctness","status":"PASS","findings":[],"checked":[],"resolutions":[]}]}`,
		"unscoped finding": `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + strings.Replace(finding("P1"), "a.txt", "b.txt", 1) + `],"checked":["x"],"resolutions":[]}]}`,
		"empty needs-fix":  `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[],"checked":["x"],"resolutions":[]}]}`,
		"not json":         `the code looks fine`,
	} {
		f.raw = true
		f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
			if kind == "review" && packet["axes"].([]any)[0] == "correctness" {
				return answer, nil
			}
			return "", nil
		}
		if _, err := Execute("review", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{}) }); err == nil {
			t.Errorf("a review with %s must be rejected", name)
		}
	}
	f.fails("finish", "no current independent review", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "s"}) })
}

func TestHoldResumeStopAndStatusAreHonest(t *testing.T) {
	f := newFixture(t)
	f.launch(f.plan("-m change the line"))
	f.fails("hold", "reason", func() (any, error) { return Hold(f.cfg, Current(), HoldInput{}) })
	held := f.do("hold", func() (any, error) { return Hold(f.cfg, Current(), HoldInput{Reason: "waiting for the user"}) })
	if held["phase"] != "held" || held["resume_phase"] != "awaiting-review" || held["hold_reason"] != "waiting for the user" {
		t.Fatalf("hold: %v", held)
	}
	before, _ := os.ReadFile(filepath.Join(home(), "state.json"))
	_ = Current().Summary(f.cfg)
	_ = PanelText(f.cfg, false, false, 120, 0)
	if after, _ := os.ReadFile(filepath.Join(home(), "state.json")); string(after) != string(before) {
		t.Fatal("status and watch must not change the gate")
	}
	f.fails("review", "held", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{}) })
	if resumed := f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) }); resumed["phase"] != "awaiting-review" {
		t.Fatalf("resume returns to the previous phase: %v", resumed)
	}
	f.fails("resume", "only a held task", func() (any, error) { return Resume(f.cfg, Current()) })
	stopped, err := Stop(f.cfg)
	must(t, err)
	if stopped.(map[string]any)["phase"] != "held" || Current().HoldReason != "the user stopped /dev" {
		t.Fatalf("stop without a running operation holds the task: %v", stopped)
	}
	f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) })
	cancelled := f.do("hold", func() (any, error) { return Hold(f.cfg, Current(), HoldInput{Reason: "dropped", Cancelled: true}) })
	if cancelled["phase"] != "cancelled" || len(StopHook(Current().Cwd, "fixture-session")) != 0 {
		t.Fatal("a cancelled task is over and does not block the stop")
	}
	if summary := f.launch(f.plan("-m a new task")); summary["phase"] != "awaiting-review" || summary["iteration"] != float64(0) {
		t.Fatalf("a new task may start after a cancelled one: %v", summary)
	}
}

func TestTellPostsToTheBoardWritersShare(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" {
			board := packet["board"].(string)
			if !slices.Contains(call.Writable, board) || !strings.HasPrefix(packet["your_board_note"].(string), board) {
				t.Errorf("a writer can write the shared board: %v %v", call.Writable, packet["board"])
			}
		}
		return "", nil
	}
	f.launch(f.plan("-m change the line"))
	posted, err := Tell(Current(), TellInput{Message: "use the v2 contract"})
	must(t, err)
	_, err = Tell(Current(), TellInput{Message: "and keep the old field"})
	must(t, err)
	raw, _ := os.ReadFile(posted.(map[string]any)["posted"].(string))
	if !strings.Contains(string(raw), "use the v2 contract") || !strings.Contains(string(raw), "and keep the old field") {
		t.Fatalf("notes accumulate on the board: %s", raw)
	}
	if _, err := Tell(Current(), TellInput{}); err == nil {
		t.Fatal("an empty note is rejected")
	}
}

func TestConsultAndDesign(t *testing.T) {
	f := newFixture(t)
	answers := f.do("consult", func() (any, error) {
		return Consult(f.cfg, ConsultInput{Lanes: []ConsultLane{{Name: "callers", Question: "who calls it?", Files: []string{"a.txt"}},
			{Name: "risk", Question: "what can break?", Route: "fable:high"}}})
	})
	rows := answers["answers"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["route"].(map[string]any)["alias"] != "sonnet" || rows[1].(map[string]any)["route"].(map[string]any)["alias"] != "fable" {
		t.Fatalf("each lane answers on its route: %v", rows)
	}
	if Current() != nil {
		t.Fatal("a consultation opens no gate")
	}
	f.fails("consult", "lanes", func() (any, error) { return Consult(f.cfg, ConsultInput{}) })
	f.fails("design", "usage_context", func() (any, error) { return Design(f.cfg, DesignInput{Task: "t", Brief: "b"}) })
	input := DesignInput{Task: "new list", Brief: "show orders", UsageContext: "operators, daily"}
	ready := f.do("design", func() (any, error) { return Design(f.cfg, input) })
	if ready["status"] != "READY" || ready["route"].(map[string]any)["alias"] != "opus" {
		t.Fatalf("design: %v", ready)
	}
	write(t, "view.tsx", "before\n")
	plan := f.plan("-m build the list", Stream{Name: "web", Domain: "frontend", Files: []string{"view.tsx"}})
	plan.UI = &UI{Design: ready["design"].(string), Acceptance: "matches the brief"}
	f.launch(plan)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "design" {
			return strings.Replace(f.defaultAnswer("design", nil), `"READY"`, `"BLOCKED"`, 1), nil
		}
		return "", nil
	}
	input.Revise, input.Notes = ready["design"].(string), "narrow the scope"
	blocked := f.do("design", func() (any, error) { return Design(f.cfg, input) })
	if blocked["status"] != "BLOCKED" || blocked["revision"] != float64(1) {
		t.Fatalf("revision: %v", blocked)
	}
	other := f.plan("-m x", Stream{Name: "web", Domain: "frontend", Files: []string{"view.tsx"}})
	other.UI = &UI{Design: blocked["design"].(string), Acceptance: "ok"}
	if _, err := loadUI(other); err == nil {
		t.Fatal("a blocked design cannot be accepted")
	}
	input.Revise = blocked["design"].(string)
	second := f.do("design", func() (any, error) { return Design(f.cfg, input) })
	input.Revise = second["design"].(string)
	f.fails("design", "revised twice", func() (any, error) { return Design(f.cfg, input) })
}

func TestBrowserVerificationIsBoundToScenariosAndLoopback(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	f := newFixture(t)
	tool := filepath.Join(t.TempDir(), "cli.js")
	write(t, tool, "")
	f.cfg.PlaywrightMCP = tool
	write(t, "view.tsx", "before\n")
	plan := f.plan("-m rename the label", Stream{Name: "web", Domain: "frontend", Files: []string{"view.tsx"}})
	plan.UI = &UI{Summary: "rename", Criteria: []string{"new copy"}, Preserve: []string{"the rest"}}
	plan.Visual = &Visual{Enabled: true, Reason: "the user asked", Scenarios: []Scenario{
		{ID: "wide", Screen: "List", State: "filled", Viewport: "1440x900", Role: "operator", Expected: "label"},
		{ID: "narrow", Screen: "List", State: "filled", Viewport: "390x844", Role: "operator", Expected: "label"}}}
	f.launch(plan)
	stand := VisualInput{URL: "http://localhost:4200/list", Origins: []string{"http://localhost:4200"}}
	for name, bad := range map[string]VisualInput{
		"a public origin":       {URL: "http://93.184.216.34/", Origins: []string{"http://93.184.216.34"}},
		"a url outside origins": {URL: "http://localhost:9/", Origins: []string{"http://localhost:4200"}},
		"an origin with a path": {URL: "http://localhost:4200/", Origins: []string{"http://localhost:4200/app"}},
		"an unknown exclusion":  {URL: stand.URL, Origins: stand.Origins, Exclusions: []Excluded{{ID: "ghost", Reason: "r"}}},
	} {
		if _, err := Execute("visual", func() (any, error) { return VisualPass(f.cfg, Current(), bad) }); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	partial := stand
	partial.Exclusions = []Excluded{{ID: "narrow", Reason: "no mobile fixture"}}
	f.do("review", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{Visual: &partial}) })
	run := Current().VisualRuns[0]
	if len(run.Coverage) != 1 || run.Coverage[0].ScenarioID != "wide" || !strings.HasSuffix(run.Coverage[0].Screenshot, "shot.png") {
		t.Fatalf("only the active scenario is observed, with its screenshot: %+v", run.Coverage)
	}
	visualCall := f.calls[len(f.calls)-1]
	for _, call := range f.calls {
		if call.MCP != nil {
			visualCall = call
		}
	}
	args := strings.Join(visualCall.MCP["playwright"].Args, " ")
	if !strings.Contains(args, "--allowed-origins http://localhost:4200") || !strings.Contains(args, "--viewport-size 1440x900") || visualCall.Route.Alias != "astra" {
		t.Fatalf("the browser is confined to the stand: %s", args)
	}
	f.finish()
	gate := Current()
	gate.Phase, gate.Final, gate.VisualRuns[0].Excluded = "reviewed", nil, nil
	must(t, gate.save())
	f.fails("finish", "not observed", func() (any, error) {
		return Finish(f.cfg, Current(), FinishInput{Summary: "s"})
	})
	f.do("finish", func() (any, error) {
		return Finish(f.cfg, Current(), FinishInput{Summary: "s", VisualExclusions: []Excluded{{ID: "narrow", Reason: "no mobile fixture"}}})
	})
}

func TestVisualChoiceAndSkipAreRecorded(t *testing.T) {
	f := newFixture(t)
	write(t, "view.tsx", "before\n")
	plan := f.plan("-m rename the label", Stream{Name: "web", Domain: "frontend", Files: []string{"view.tsx"}})
	plan.UI = &UI{Summary: "rename", Criteria: []string{"new copy"}, Preserve: []string{"the rest"}}
	f.launch(plan)
	f.fails("visual", "not opted in", func() (any, error) {
		return VisualPass(f.cfg, Current(), VisualInput{URL: "http://localhost:1/", Origins: []string{"http://localhost:1"}})
	})
	choice := &Visual{Enabled: true, Reason: "the user asked", Scenarios: []Scenario{{ID: "s", Screen: "S", State: "x", Viewport: "1440x900", Role: "r", Expected: "e"}}}
	f.do("visual", func() (any, error) { return VisualPass(f.cfg, Current(), VisualInput{Choice: choice}) })
	f.review()
	f.fails("finish", "browser verification is unfinished", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "s"}) })
	f.do("visual", func() (any, error) {
		return VisualPass(f.cfg, Current(), VisualInput{SkipReason: "the stand cannot start"})
	})
	if Current().visualRequired() {
		t.Fatal("a recorded skip lifts the requirement")
	}
	f.finish()
}

func TestInputGlobs(t *testing.T) {
	newFixture(t)
	for _, path := range []string{"src/a.go", "src/deep/b.go", "src/deep/c.txt", "lib/d.go", "top.go"} {
		must(t, os.MkdirAll(filepath.Dir(path), 0o755))
		write(t, path, path)
	}
	cases := map[string][]string{
		"src/**":      {"src/a.go", "src/deep/b.go", "src/deep/c.txt"},
		"src/**/*.go": {"src/a.go", "src/deep/b.go"},
		"src/*.go":    {"src/a.go"},
		"src/deep":    {"src/deep/b.go", "src/deep/c.txt"},
		"*.go":        {"top.go"},
		"top.go":      {"top.go"},
		"missing/**":  nil,
	}
	for pattern, want := range cases {
		got, err := inputFiles([]string{pattern})
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: want %v, got %v (%v)", pattern, want, got, err)
		}
	}
	must(t, os.Symlink("top.go", "src/link.go"))
	if _, err := inputFiles([]string{"src/*.go"}); err == nil {
		t.Fatal("a symlinked input must be reported")
	}
}

func TestPruneRemovesOnlyOldFinishedTasks(t *testing.T) {
	f := newFixture(t)
	f.launch(f.plan("-m change the line"))
	old := time.Now().Add(-30 * 24 * time.Hour)
	state := filepath.Join(home(), "state.json")
	must(t, os.Chtimes(state, old, old))
	if Prune(14) != 0 {
		t.Fatal("an unfinished task is never pruned, however old")
	}
	f.do("hold", func() (any, error) { return Hold(f.cfg, Current(), HoldInput{Reason: "dropped", Cancelled: true}) })
	if Prune(14) != 0 {
		t.Fatal("a recently finished task is kept")
	}
	must(t, os.Chtimes(state, old, old))
	if Prune(0) != 0 || Prune(14) != 1 || Current() != nil {
		t.Fatal("an old finished task is removed; retention 0 disables pruning")
	}
}

func TestViewersWorkOutsideTheLeadSession(t *testing.T) {
	f := newFixture(t)
	f.launch(f.plan("-m change the line"))
	t.Setenv("CLAUDE_DEV_SESSION", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	if HasSession() {
		t.Fatal("a viewer has no session")
	}
	panel := PanelText(f.cfg, false, false, 120, 0)
	for _, want := range []string{"TASKS 1", "medium", "awaiting-review", "change the line", "writer  core", "opus:medium", "implemented", "CHECKS", "true", "next "} {
		if !strings.Contains(panel, want) {
			t.Errorf("the panel lacks %q:\n%s", want, panel)
		}
	}
	if strings.Contains(panel, "\x1b[") || !strings.Contains(PanelText(f.cfg, false, true, 120, 0), "\x1b[") {
		t.Fatal("colour is used only when asked for")
	}
	overview := Overview()["gates"].([]map[string]any)
	if len(overview) != 1 || overview[0]["phase"] != "awaiting-review" || overview[0]["operation_live"] != false {
		t.Fatalf("overview: %v", overview)
	}
	idle, err := StopFromOutside()
	must(t, err)
	if idle.(map[string]any)["stopped"] != false {
		t.Fatalf("nothing is running: %v", idle)
	}
	if _, err := Execute("review", func() (any, error) { return nil, nil }); err == nil {
		t.Fatal("without a session nobody can drive the gate")
	}
	// A live operation of the checkout can be stopped from any terminal.
	t.Setenv("CLAUDE_DEV_SESSION", "fixture-session")
	must(t, ops.Begin(home(), "launch", ""))
	t.Setenv("CLAUDE_DEV_SESSION", "")
	requested, err := StopFromOutside()
	must(t, err)
	if requested.(map[string]any)["cancellation_requested"] != true || !ops.IsCancelled(ops.Checkpoint()) {
		t.Fatalf("the running operation must receive the stop: %v", requested)
	}
	ops.End(nil)
}

func TestParallelismHonoursTheFlagAndTheMachine(t *testing.T) {
	f := newFixture(t)
	resolved, err := Resolve(f.cfg, f.plan("-2 -m task"))
	must(t, err)
	if limit, err := resolved.Parallelism(); err != nil || limit != 2 {
		t.Fatalf("-2 caps parallelism at 2: %d %v", limit, err)
	}
	automatic, _ := Resolve(f.cfg, f.plan("-m task"))
	if limit, _ := automatic.Parallelism(); limit < 1 || limit > f.cfg.MaxParallel {
		t.Fatalf("automatic parallelism stays within max_parallel: %d", limit)
	}
	f.cfg.WorkerMemoryMiB = 1 << 30
	if _, err := resolved.Parallelism(); err == nil {
		t.Fatal("too little free memory for one worker must stop the launch")
	}
}

func TestRouteAndPreviewStartNothing(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-h build both", Stream{Name: "web", Domain: "frontend", Files: []string{"b.txt"}, After: []string{"api"}},
		Stream{Name: "api", Domain: "backend", Files: []string{"a.txt"}})
	routed, err := Route(f.cfg, plan)
	must(t, err)
	raw, _ := json.Marshal(routed)
	for _, want := range []string{`"mode":"heavy"`, `"api":"fable:high"`, `"web":"fable:high"`, `[["api"],["web"]]`, `"read_only":true`, "opus:high", `"scout":"sol:medium"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("route lacks %s: %s", want, raw)
		}
	}
	preview, err := Preview(f.cfg, "-x --preview move the money")
	must(t, err)
	raw, _ = json.Marshal(preview)
	if !strings.Contains(string(raw), `"mode":"ultra"`) || !strings.Contains(string(raw), "cross-critique") {
		t.Fatalf("preview: %s", raw)
	}
	automatic, _ := Preview(f.cfg, "do something")
	raw, _ = json.Marshal(automatic)
	if !strings.Contains(string(raw), "assessment") || !strings.Contains(string(raw), `"light"`) {
		t.Fatalf("without a flag the preview shows every mode: %s", raw)
	}
	if len(f.calls) != 0 || Current() != nil {
		t.Fatal("route and preview start no model and create no gate")
	}
	if _, err := os.Stat(Root()); err == nil {
		t.Fatal("route and preview write nothing")
	}
	if table := Modes(f.cfg); !strings.Contains(table, "ultra") || !strings.Contains(table, "fallback opus, astra") {
		t.Fatalf("modes table: %s", table)
	}
}

func TestZedTasksAreAddedWithoutDamagingTheFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	first, err := Install(false, true)
	must(t, err)
	tasks := filepath.Join(dir, "zed", "tasks.json")
	raw, _ := os.ReadFile(tasks)
	if first.(map[string]any)["zed"].(map[string]any)["changed"] != true || strings.Count(string(raw), "claude-dev: ") != 3 {
		t.Fatalf("three tasks are created: %s", raw)
	}
	write(t, tasks, `[{"label":"mine","command":"make"},{"label":"claude-dev: watch","command":"claude-dev watch"}]`)
	_, err = Install(false, true)
	must(t, err)
	raw, _ = os.ReadFile(tasks)
	if !strings.Contains(string(raw), `"mine"`) || strings.Count(string(raw), "claude-dev: watch") != 1 || strings.Count(string(raw), "claude-dev: ") != 3 {
		t.Fatalf("existing tasks are kept and nothing is duplicated: %s", raw)
	}
	commented := "// my tasks\n[{\"label\":\"mine\",\"command\":\"make\"}]\n"
	write(t, tasks, commented)
	third, err := Install(false, true)
	must(t, err)
	zed := third.(map[string]any)["zed"].(map[string]any)
	if after, _ := os.ReadFile(tasks); string(after) != commented || zed["changed"] != false {
		t.Fatal("a file with comments is left exactly as it was")
	}
	if side, _ := os.ReadFile(zed["paste_from"].(string)); !strings.Contains(string(side), "claude-dev: watch") {
		t.Fatal("the tasks are offered in a side file instead")
	}
}

func TestDoctorReportsWhatIsMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Chdir(dir)
	report := Doctor()
	checks := map[string]probe{}
	for _, item := range report["checks"].([]probe) {
		checks[item.Name] = item
	}
	if report["healthy"] != false || checks["/dev command"].OK || checks["Stop hook"].OK || !checks["config"].OK {
		t.Fatalf("a machine without the command and hook is not healthy: %+v", checks)
	}
	if checks["browser tooling"].Required {
		t.Fatal("browser tooling is optional")
	}
	_, err := Install(false, false)
	must(t, err)
	checks = map[string]probe{}
	for _, item := range Doctor()["checks"].([]probe) {
		checks[item.Name] = item
	}
	if !checks["/dev command"].OK || !checks["Stop hook"].OK {
		t.Fatalf("install satisfies the doctor: %+v", checks)
	}
}

func TestScoutGathersContextOnceForEveryWorker(t *testing.T) {
	f := newFixture(t)
	seen := map[string]bool{}
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if pack, _ := packet["context_pack"].(string); pack != "" {
			raw, err := os.ReadFile(pack)
			if err != nil || !strings.Contains(string(raw), "keep the line format") {
				t.Errorf("%s got an unreadable context pack: %v", kind, err)
			}
			if !slices.Contains(call.ReadDirs, filepath.Dir(pack)) {
				t.Errorf("%s cannot read the pack from its sandbox: %v", kind, call.ReadDirs)
			}
			f.mu.Lock()
			seen[kind] = true
			f.mu.Unlock()
		}
		return "", nil
	}
	plan := f.plan("-h change the contract")
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	plan.Architecture = &Accepted{ID: receipt["id"].(string), Acceptance: "accepted"}
	f.launch(plan)
	f.review()
	if f.count("scout") != 1 {
		t.Fatalf("the design pass and the writers share one scouting run, ran %d", f.count("scout"))
	}
	for _, kind := range []string{"architecture", "writer", "review"} {
		if !seen[kind] {
			t.Errorf("the %s did not receive the context pack", kind)
		}
	}
	var scoutCall provider.Call
	for _, call := range f.calls {
		if kindOf(call) == "scout" {
			scoutCall = call
		}
	}
	if scoutCall.Route.Alias != "sol" || scoutCall.Writer {
		t.Fatalf("scouting is read-only work for the cheap model: %+v", scoutCall.Route)
	}
}

func TestLightModeHasNoScoutAndAFailedScoutNeverBlocks(t *testing.T) {
	f := newFixture(t)
	light := f.plan("-l fix the typo")
	light.Checks = nil
	f.launch(light)
	if f.count("scout") != 0 || Current().ContextPack != "" {
		t.Fatal("light relies on the lead's own context")
	}
	f.do("hold", func() (any, error) { return Hold(f.cfg, Current(), HoldInput{Reason: "done", Cancelled: true}) })
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "scout" {
			return `{"summary":"","files":[],"callers":[],"invariants":[],"conventions":[],"unknowns":[]}`, nil
		}
		return "", nil
	}
	summary := f.launch(f.plan("-m change the line"))
	if summary["phase"] != "awaiting-review" || summary["scout_note"] == nil || Current().ContextPack != "" {
		t.Fatalf("an empty pack is reported and the task goes on without it: %v", summary)
	}
}

func TestRecheckRunsOneEffortStepLower(t *testing.T) {
	f := newFixture(t)
	first := true
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" && packet["axes"].([]any)[0] == "correctness" && first { // only the one correctness lane touches the flag
			first = false
			return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + finding("P1") + `],"checked":["read"],"resolutions":[]}]}`, nil
		}
		return "", nil
	}
	plan := f.plan("-h change the contract")
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	plan.Architecture = &Accepted{ID: receipt["id"].(string), Acceptance: "accepted"}
	f.launch(plan)
	summary := f.review()
	id := summary["untriaged"].([]any)[0].(string)
	f.do("triage", func() (any, error) {
		return Triage(f.cfg, Current(), TriageInput{[]Disposition{{ID: id, Decision: "accepted", Evidence: "verified"}}})
	})
	f.do("fix", func() (any, error) { return Fix(f.cfg, Current(), FixInput{Brief: "use the agreed value"}) })
	write(t, "a.txt", "corrected\n")
	f.review()
	reviews := Current().Reviews
	if len(reviews[0].Lanes) != 2 || len(reviews[1].Lanes) != 1 {
		t.Fatalf("the lane without a finding is not paid to recheck: %d then %d lanes", len(reviews[0].Lanes), len(reviews[1].Lanes))
	}
	lane, recheck := reviews[0].Lanes[1], reviews[1].Lanes[0]
	if lane.Route.Effort != "high" || recheck.Route.Effort != "medium" || recheck.Session != lane.Session {
		t.Fatalf("the first review runs at the mode's effort and the recheck one step lower, in the same session: %s -> %s", lane.Route, recheck.Route)
	}
	writers := Current().Writers
	if writers[0].Route.Effort != "high" || writers[1].Route.Effort != "high" {
		t.Fatalf("a first correction is not a stall and earns no extra effort: %s %s", writers[0].Route, writers[1].Route)
	}
}

func TestEffortCeilings(t *testing.T) {
	f := newFixture(t)
	hard := f.plan("-m change the line")
	hard.Streams[0].Effort = "high"
	resolved, err := Resolve(f.cfg, hard)
	must(t, err)
	if route := resolved.WriterRoute(hard.Streams[0], 0); route.String() != "opus:high" {
		t.Fatalf("a hard stream may ask for more effort: %s", route)
	}
	if route := resolved.WriterRoute(hard.Streams[0], 5); route.Effort != "high" {
		t.Fatalf("escalation stops at the mode's ceiling: %s", route)
	}
	hard.Streams[0].Effort = "max"
	if _, err := Resolve(f.cfg, hard); err == nil || !strings.Contains(err.Error(), "above what medium allows") {
		t.Fatalf("medium cannot be pushed to max: %v", err)
	}
	ultra := f.plan("-x move the money")
	resolved, err = Resolve(f.cfg, ultra)
	must(t, err)
	if base, top := resolved.WriterRoute(ultra.Streams[0], 0), resolved.WriterRoute(ultra.Streams[0], 1); base.Effort != "xhigh" || top.Effort != "max" {
		t.Fatalf("ultra reaches max only by escalation: %s then %s", base, top)
	}
	for _, name := range []string{"light", "medium", "heavy", "ultra"} {
		mode := f.cfg.Modes[name]
		specs := append(append([]string{mode.Backend, mode.Frontend, mode.Scout}, mode.Architecture...), f.cfg.Consultant, f.cfg.UIDesigner)
		for _, lane := range mode.Review {
			specs = append(specs, lane.Route)
		}
		for _, spec := range specs {
			if strings.HasSuffix(spec, ":max") {
				t.Errorf("%s uses max by default (%s); max is reached only by escalation", name, spec)
			}
		}
	}
}

func TestFailingCheckGoesBackToTheWriterBeforeAnyReviewer(t *testing.T) {
	f := newFixture(t)
	repaired := false
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "writer" {
			return "", nil
		}
		correction, _ := packet["correction"].(map[string]any)
		if correction == nil {
			return "", nil
		}
		failures := correction["failing_checks"].([]any)
		if len(failures) != 1 || !strings.Contains(failures[0].(map[string]any)["output_tail"].(string), "expected fixed") || call.Resume == "" {
			t.Errorf("the writer gets the failing command and its output in its own session: %v", failures)
		}
		if repaired {
			_ = os.WriteFile("a.txt", []byte("fixed\n"), 0o644)
		}
		return `{"status":"COMPLETED","summary":"tried","blockers":[],"handoff":"","deviations":[]}`, nil
	}
	plan := f.plan("-m make it say fixed")
	plan.Checks = []Check{{Argv: []string{"sh", "-c", "grep -q fixed a.txt || { echo expected fixed; exit 1; }"}}}
	f.launch(plan)
	blocked := f.review()
	if blocked["phase"] != "awaiting-review" || len(blocked["checks_failed"].([]any)) != 1 || f.count("review") != 0 ||
		!strings.Contains(blocked["next"].(string), "no reviewer was started") {
		t.Fatalf("a failing check stops the review before any reviewer is paid: %v", blocked["next"])
	}
	f.fails("finish", "no current independent review", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "s"}) })
	f.do("fix", func() (any, error) { return Fix(f.cfg, Current(), FixInput{Brief: "write the agreed word"}) })
	if still := f.review(); len(still["checks_failed"].([]any)) != 1 || f.count("review") != 0 {
		t.Fatal("the check still fails, so reviewers still wait")
	}
	f.fails("fix", "check-still-failing", func() (any, error) { return Fix(f.cfg, Current(), FixInput{Brief: "same again"}) })
	f.fails("fix", "names no stream", func() (any, error) {
		return Fix(f.cfg, Current(), FixInput{Brief: "b", Streams: []string{"ghost"}, Reassessment: &Reassessment{Evidence: "e", ChangedApproach: "c"}})
	})
	repaired = true
	f.do("fix", func() (any, error) {
		return Fix(f.cfg, Current(), FixInput{Brief: "write the word", Reassessment: &Reassessment{Evidence: "the file was untouched", ChangedApproach: "edit a.txt"}})
	})
	if writers := Current().Writers; writers[len(writers)-1].Route.Effort != "high" {
		t.Fatalf("a check that keeps failing earns one more effort step: %s", writers[len(writers)-1].Route)
	}
	done := f.do("review", func() (any, error) {
		return ReviewTask(f.cfg, Current(), ReviewInput{Finish: &FinishInput{Summary: "a.txt says fixed"}})
	})
	if done["phase"] != "complete" || f.count("review") != 2 {
		t.Fatalf("once the check passes the reviewers run, and a clean review closes the task in the same call: %v", done["phase"])
	}
}

func TestCleanReviewDoesNotCloseABlockedTask(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" && packet["axes"].([]any)[0] == "correctness" {
			return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + finding("P1") + `],"checked":["read"],"resolutions":[]}]}`, nil
		}
		return "", nil
	}
	f.launch(f.plan("-m change the line"))
	summary := f.do("review", func() (any, error) {
		return ReviewTask(f.cfg, Current(), ReviewInput{Finish: &FinishInput{Summary: "done"}})
	})
	if summary["phase"] != "reviewed" || !strings.Contains(summary["finish_refused"].(string), "triage") {
		t.Fatalf("finish-in-review never closes a task with findings: %v", summary["finish_refused"])
	}
}

func TestWritersReceiveOneAcceptedDesign(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "architecture" {
			return `{"summary":"design","design":"design by ` + call.Route.Alias + `","risks":[],"open_questions":[]}`, nil
		}
		if kind == "writer" {
			accepted := packet["architecture"].(map[string]any)
			if accepted["design"] != "design by astra" || accepted["lead_acceptance"] != "astra's boundary, with one amendment" || accepted["lanes"] != nil {
				t.Errorf("the writer follows one design as amended by the lead, not both: %v", accepted)
			}
		}
		return "", nil
	}
	plan := f.plan("-h change the contract")
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	plan.Architecture = &Accepted{ID: receipt["id"].(string), Acceptance: "astra's boundary, with one amendment", Base: "ghost"}
	f.fails("launch", "names no design", func() (any, error) { return Launch(f.cfg, plan) })
	plan.Architecture.Base = "astra"
	f.launch(plan)
	if f.count("writer") != 1 {
		t.Fatal("the writer must have run")
	}
}

func TestUltraRechecksOnlyWhatTheCorrectionTouched(t *testing.T) {
	f := newFixture(t)
	first := true
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" && packet["axes"].([]any)[0] == "security" && first {
			first = false
			return `{"reports":[{"axis":"security","status":"NEEDS_FIX","findings":[` + finding("P1") + `],"checked":["read"],"resolutions":[]}]}`, nil
		}
		return "", nil
	}
	plan := f.plan("-x move the money")
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	plan.Architecture = &Accepted{ID: receipt["id"].(string), Acceptance: "accepted"}
	f.launch(plan)
	summary := f.review()
	id := summary["untriaged"].([]any)[0].(string)
	f.do("triage", func() (any, error) {
		return Triage(f.cfg, Current(), TriageInput{[]Disposition{{ID: id, Decision: "accepted", Evidence: "verified"}}})
	})
	f.do("fix", func() (any, error) { return Fix(f.cfg, Current(), FixInput{Brief: "validate the input"}) })
	write(t, "a.txt", "corrected\n")
	done := f.review()
	reviews := Current().Reviews
	var axes []string
	for _, lane := range reviews[1].Lanes {
		axes = append(axes, lane.Axes[0])
	}
	if len(reviews[0].Lanes) != 4 || !slices.Equal(axes, []string{"correctness", "security"}) || len(done["blocking"].([]any)) != 0 {
		t.Fatalf("four lanes review; the recheck is the lane with the finding plus one correctness lane: %v", axes)
	}
}

func TestSavedPlanIsReadableAndRunsFromAnotherSession(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-h change the contract")
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	plan.Architecture = &Accepted{ID: receipt["id"].(string), Acceptance: "the first design as written"}
	saved, err := PlanSave(f.cfg, plan, "contract-change")
	must(t, err)
	if saved.(map[string]any)["plan"] != ".claude-dev/plans/contract-change/plan.json" {
		t.Fatalf("a plan is kept inside the repository: %v", saved)
	}
	brief, err := PlanShow("contract-change")
	must(t, err)
	for _, want := range []string{"# contract-change", "**Mode:** heavy", "### core — backend, fable:high", "- `a.txt`", "the first design as written", "- `true`"} {
		if !strings.Contains(brief.(string), want) {
			t.Errorf("the brief lacks %q:\n%s", want, brief)
		}
	}
	if _, err := PlanSave(f.cfg, plan, "Not A Name"); err == nil {
		t.Fatal("a plan name is a safe directory name")
	}
	listed, _ := PlanList()
	if raw, _ := json.Marshal(listed); !strings.Contains(string(raw), `"name":"contract-change"`) {
		t.Fatalf("the saved plan is listed: %s", raw)
	}

	// Another conversation in the same checkout picks the plan up: the design receipt belongs to the checkout.
	t.Setenv("CLAUDE_DEV_SESSION", "another-session")
	path, err := PlanFile("contract-change")
	must(t, err)
	raw, err := os.ReadFile(path)
	must(t, err)
	loaded := &Plan{}
	must(t, json.Unmarshal(raw, loaded))
	if summary := f.launch(loaded); summary["mode"] != "heavy" || f.count("architecture") != 2 {
		t.Fatalf("the plan launches without a second design pass: %v", summary)
	}
	if foreign := Current().Foreign; len(foreign) != 0 {
		t.Fatalf("saved plans are not part of the change: %v", foreign)
	}
	f.review()
	if foreign := Current().Foreign; len(foreign) != 0 {
		t.Fatalf("saved plans are not part of the change: %v", foreign)
	}
}

func TestCorrectionContinuesOnTheAccountThatHoldsTheSession(t *testing.T) {
	f := newFixture(t)
	probe := func(account config.Account) accounts.Status {
		return accounts.Status{Account: account, LoggedIn: true, Identity: account.Name, ProbedAt: time.Now().Unix(),
			Windows: []accounts.Window{{Label: "week", Used: map[string]float64{"main": 70, "second": 20}[account.Name]}}}
	}
	previous := accounts.ProbeClaude
	accounts.ProbeClaude = probe
	provider.Accounts = accounts.New([]config.Account{{Name: "main", Provider: "claude", Home: "~/main"}, {Name: "second", Provider: "claude", Home: "~/second"}},
		filepath.Join(Root(), "accounts.json"))
	t.Cleanup(func() { accounts.ProbeClaude, provider.Accounts = previous, nil })
	reviews := 0
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" && packet["axes"].([]any)[0] == "correctness" {
			if reviews++; reviews == 1 {
				return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + finding("P1") + `],"checked":["read"],"resolutions":[]}]}`, nil
			}
		}
		return "", nil
	}
	f.launch(f.plan("change the line"))
	writer := Current().latest()["core"]
	if writer.Route.Account != "second" {
		t.Fatalf("the writer runs on the account with the most room: %+v", writer.Route)
	}
	summary := f.review()
	id := summary["untriaged"].([]any)[0].(string)
	f.do("triage", func() (any, error) {
		return Triage(f.cfg, Current(), TriageInput{[]Disposition{{ID: id, Decision: "accepted", Evidence: "verified in a.txt"}}})
	})
	// The other account now has far more room, but the session lives on "second".
	accounts.ProbeClaude = func(account config.Account) accounts.Status {
		status := probe(account)
		status.Windows[0].Used = map[string]float64{"main": 1, "second": 90}[account.Name]
		return status
	}
	provider.Accounts = accounts.New([]config.Account{{Name: "main", Provider: "claude", Home: "~/main"}, {Name: "second", Provider: "claude", Home: "~/second"}},
		filepath.Join(Root(), "accounts-2.json"))
	f.do("fix", func() (any, error) { return Fix(f.cfg, Current(), FixInput{Brief: "use the agreed value"}) })
	last := f.calls[len(f.calls)-1]
	if !last.Writer || last.Resume != writer.Session || last.Account.Name != "second" {
		t.Fatalf("a correction resumes the writer's session under the same account: %+v", last.Account)
	}
	if text := PanelText(f.cfg, false, false, 120, 0); !strings.Contains(text, "@second") {
		t.Fatalf("the panel names the account a lane ran on:\n%s", text)
	}
}

// identity lets git commit inside the fixture without touching any configuration.
func identity(t *testing.T) {
	for _, name := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(name, "Fixture")
	}
	for _, name := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(name, "fixture@example.invalid")
	}
}

func gitOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

func fileText(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	must(t, err)
	return string(raw)
}

func (f *fixture) land(how string) map[string]any {
	return f.do("land", func() (any, error) { return Land(f.cfg, Current(), LandInput{How: how}) })
}

func TestIsolatedTaskLeavesTheCheckoutUntouchedUntilLanded(t *testing.T) {
	f := newFixture(t)
	identity(t)
	must(t, os.WriteFile(".gitignore", []byte("local.env\n.ready\n"), 0o644))
	gitOut(t, "add", ".gitignore")
	gitOut(t, "commit", "-qm", "ignore local files")
	must(t, os.WriteFile("local.env", []byte("SECRET=1\n"), 0o600))
	f.cfg.Worktree.Copy = []string{"local.env", "missing.env"}
	f.cfg.Worktree.Setup = [][]string{{"sh", "-c", "cat local.env > .ready"}}

	for _, bad := range []string{"-w review the branch", "-w status", "-w -w task"} {
		if _, err := ParseRequest(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	f.launch(f.plan("-w change the line"))
	gate := Current()
	if gate.Worktree == "" || !strings.HasPrefix(gate.Branch, "dev/change-the-line-") {
		t.Fatalf("an isolated task has a worktree and a branch: %q %q", gate.Worktree, gate.Branch)
	}
	if fileText(t, "a.txt") != "before\n" || strings.TrimSpace(gitOut(t, "status", "--porcelain")) != "" {
		t.Fatal("the checkout must not change while an isolated task works")
	}
	if fileText(t, filepath.Join(gate.Worktree, "a.txt")) != "after\n" || fileText(t, filepath.Join(gate.Worktree, ".ready")) != "SECRET=1\n" {
		t.Fatal("the writer works in the worktree, prepared by copy and setup")
	}
	f.fails("land", "only a finished task lands", func() (any, error) { return Land(f.cfg, Current(), LandInput{}) })
	f.review()
	summary := f.finish()
	if !strings.Contains(summary["next"].(string), "land") || summary["isolated"].(map[string]any)["branch"] != gate.Branch {
		t.Fatalf("a finished isolated task waits for the user's word: %v", summary["next"])
	}
	if fileText(t, "a.txt") != "before\n" {
		t.Fatal("finishing does not touch the checkout either")
	}
	if hook := StopHook(Current().Cwd, "fixture-session"); len(hook) != 0 {
		t.Fatalf("a finished task waiting to land does not hold the turn: %v", hook)
	}
	f.fails("launch", "not landed yet", func() (any, error) { return Launch(f.cfg, f.plan("-w another change")) })

	landed := f.land("")
	if landed["landed"] != "apply" || fileText(t, "a.txt") != "after\n" || !strings.Contains(gitOut(t, "status", "--porcelain"), " M a.txt") {
		t.Fatalf("apply leaves the change in the checkout, uncommitted: %v", landed)
	}
	if _, err := os.Stat(gate.Worktree); err == nil || strings.Contains(gitOut(t, "branch", "--list", gate.Branch), gate.Branch) {
		t.Fatal("the worktree and its branch are removed after landing")
	}
	if _, err := os.Stat(".ready"); err == nil {
		t.Fatal("what setup produced stays out of the checkout")
	}
	f.fails("land", "already closed", func() (any, error) { return Land(f.cfg, Current(), LandInput{}) })

	// An ordinary task has nothing to land; a dropped one leaves no trace.
	gitOut(t, "checkout", "-q", "--", "a.txt")
	f.launch(f.plan("-w change it again"))
	second := Current()
	if dropped := f.land("drop"); dropped["phase"] != "cancelled" || fileText(t, "a.txt") != "before\n" {
		t.Fatalf("drop discards the work: %v", dropped)
	}
	if _, err := os.Stat(second.Worktree); err == nil {
		t.Fatal("a dropped task's worktree is removed")
	}
	f.launch(f.plan("change the line"))
	f.fails("land", "not isolated", func() (any, error) { return Land(f.cfg, Current(), LandInput{}) })
}

func TestIsolatedSessionsCatchUpAndNeverConflictInTheCheckout(t *testing.T) {
	f := newFixture(t)
	identity(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "writer" {
			return "", nil
		}
		text := os.Getenv("CLAUDE_DEV_SESSION") + "\n"
		if _, repairing := packet["merge_conflicts"]; repairing {
			text = "both changes\n"
		}
		for _, file := range packet["stream"].(map[string]any)["files"].([]any) {
			_ = os.WriteFile(file.(string), []byte(text), 0o644)
		}
		return `{"status":"COMPLETED","summary":"implemented","blockers":[],"handoff":"","deviations":[]}`, nil
	}
	as := func(session string) { t.Setenv("CLAUDE_DEV_SESSION", session) }
	other := Stream{Name: "core", Domain: "backend", Files: []string{"b.txt"}}

	// Three sessions start from the same commit; two of them change the same file.
	as("first")
	f.launch(f.plan("-w first change"))
	as("second")
	f.launch(f.plan("-w second change"))
	as("third")
	f.launch(f.plan("-w third change", other))
	for _, session := range []string{"first", "second", "third"} {
		as(session)
		f.review()
		f.finish()
	}
	if strings.TrimSpace(gitOut(t, "status", "--porcelain")) != "" {
		t.Fatal("three finished tasks, and the checkout is still clean")
	}
	if all := PanelText(f.cfg, false, false, 120, 0); strings.Count(all, "ready to land") != 3 || !strings.Contains(all, "TASKS 3") {
		t.Fatalf("the panel lists every session's task waiting to land:\n%s", all)
	}

	as("first")
	if landed := f.land("commit"); landed["commit"] == nil || fileText(t, "a.txt") != "first\n" || strings.TrimSpace(gitOut(t, "status", "--porcelain")) != "" {
		t.Fatalf("commit lands the task as a commit on the current branch: %v", landed)
	}

	// The third task touches other files: the checkout's new commit merges in cleanly, its checks run again, it lands.
	as("third")
	checks := len(Current().Checks)
	if landed := f.land("apply"); landed["landed"] != "apply" || fileText(t, "b.txt") != "third\n" || fileText(t, "a.txt") != "first\n" {
		t.Fatalf("a task on other files lands on top of the new commit: %v", landed)
	}
	if len(Current().Checks) != checks+1 {
		t.Fatal("after catching up every declared check runs again")
	}

	// The second task changed the same line: the conflict stays in its worktree and goes back to its writer.
	as("second")
	reopened := f.land("apply")
	gate := Current()
	if reopened["landed"] != false || gate.Phase != "awaiting-review" || len(gate.Conflicts) != 1 || gate.Conflicts[0] != "a.txt" {
		t.Fatalf("a conflict reopens the task instead of landing: %v", reopened)
	}
	if fileText(t, "a.txt") != "first\n" || !strings.Contains(fileText(t, filepath.Join(gate.Worktree, "a.txt")), "<<<<<<<") {
		t.Fatal("conflict markers live in the worktree, never in the checkout")
	}
	f.fails("review", "conflict markers remain", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{}) })
	f.do("fix", func() (any, error) { return Fix(f.cfg, Current(), FixInput{Brief: "keep both changes"}) })
	f.review()
	// A second review call of unchanged code still honours `finish`.
	if closed := f.do("review", func() (any, error) {
		return ReviewTask(f.cfg, Current(), ReviewInput{Finish: &FinishInput{Summary: "done"}})
	}); closed["phase"] != "complete" {
		t.Fatalf("review closes the task when asked, also when the review itself is already current: %v", closed)
	}
	gitOut(t, "stash", "-q", "--include-untracked") // the third task's uncommitted change is the user's to keep or drop
	if landed := f.land("branch"); landed["branch"] != gate.Branch || fileText(t, "a.txt") != "first\n" {
		t.Fatalf("branch keeps the work for a pull request and leaves the checkout alone: %v", landed)
	}
	if got := gitOut(t, "show", gate.Branch+":a.txt"); got != "both changes\n" {
		t.Fatalf("the kept branch holds the resolved work: %q", got)
	}
	if _, err := os.Stat(gate.Worktree); err == nil {
		t.Fatal("the worktree is removed even when the branch is kept")
	}
}

func TestLandingRefusesToOverwriteTheUsersOwnEdits(t *testing.T) {
	f := newFixture(t)
	identity(t)
	f.launch(f.plan("-w change the line"))
	f.review()
	f.finish()
	must(t, os.WriteFile("a.txt", []byte("the user's own edit\n"), 0o644))
	f.fails("land", "own uncommitted changes", func() (any, error) { return Land(f.cfg, Current(), LandInput{}) })
	if fileText(t, "a.txt") != "the user's own edit\n" || Current().Landed != "" {
		t.Fatal("a refused landing changes nothing and can be repeated")
	}
	gitOut(t, "checkout", "-q", "--", "a.txt")
	if landed := f.land("apply"); landed["landed"] != "apply" || fileText(t, "a.txt") != "after\n" {
		t.Fatalf("once the edit is out of the way the task lands: %v", landed)
	}
}

func TestPlansMoveToASyncedFolderAndStayUsable(t *testing.T) {
	f := newFixture(t)
	identity(t)
	repo, _ := os.Getwd()
	synced := filepath.Join(filepath.Dir(repo), "cloud")
	plan := f.plan("-w change the line")
	_, err := PlanSave(f.cfg, plan, "the-change")
	must(t, err)
	must(t, os.MkdirAll(".claude-dev/tools", 0o755))
	must(t, os.WriteFile(".claude-dev/tools/check.sh", []byte("#!/bin/sh\ntest -n \"$1\"\n"), 0o755))
	must(t, os.WriteFile(".claude-dev/config.json", []byte(`{"max_parallel": 2, "accounts": [{"name": "x", "provider": "claude", "home": "/x"}],
		"worktree": {"setup": [["sh", "-c", "echo \"$CLAUDE_DEV_CHECKOUT\" > .origin"]]}}`), 0o644))

	linked, err := PlanLink(f.cfg, synced)
	must(t, err)
	target := filepath.Join(synced, "repo")
	if linked.(map[string]any)["target"] != target || fileText(t, filepath.Join(target, "plans", "the-change", "plan.json")) == "" {
		t.Fatalf("the directory moves into the synced folder under the repository's name: %v", linked)
	}
	if where, _ := os.Readlink(".claude-dev"); where != target {
		t.Fatalf("a link is left in its place: %q", where)
	}
	cfg, err := config.Load()
	must(t, err)
	if cfg.PlansHome != synced || cfg.MaxParallel != 2 || len(cfg.Accounts) != 0 || len(cfg.Worktree.Setup) != 1 {
		t.Fatalf("the folder is remembered and the personal file next to the plans is read, without accounts: %+v", cfg)
	}
	if again, err := PlanLink(cfg, ""); err != nil || again.(map[string]any)["target"] != target {
		t.Fatalf("linking again is harmless: %v %v", again, err)
	}
	if listed, _ := PlanList(); !strings.Contains(fmt.Sprint(listed), "the-change") {
		t.Fatalf("plans are found through the link: %v", listed)
	}

	// The link itself is not part of any change, and an isolated task still reaches the helper scripts.
	f.cfg = cfg
	plan.Checks = []Check{{Argv: []string{".claude-dev/tools/check.sh", "x"}}}
	f.launch(plan)
	gate := Current()
	if len(gate.Foreign) != 0 || fileText(t, filepath.Join(gate.Worktree, ".origin")) != repo+"\n" {
		t.Fatalf("setup knows the checkout it was started from; the link is not a foreign change: %v", gate.Foreign)
	}
	f.review()
	f.finish()
	if landed := f.land("apply"); landed["landed"] != "apply" || len(landed["files"].([]any)) != 1 {
		t.Fatalf("only the task's file lands, not the link: %v", landed)
	}

	// A second computer: the synced folder already has the data, the checkout has none.
	must(t, os.Remove(".claude-dev"))
	if _, err := PlanLink(cfg, ""); err != nil || fileText(t, ".claude-dev/plans/the-change/brief.md") == "" {
		t.Fatalf("linking picks up what the other computer saved: %v", err)
	}
	must(t, os.Remove(".claude-dev"))
	must(t, os.MkdirAll(".claude-dev/plans", 0o755))
	if _, err := PlanLink(cfg, ""); err == nil || !strings.Contains(err.Error(), "merge them by hand") {
		t.Fatalf("two copies are never merged silently: %v", err)
	}
}
