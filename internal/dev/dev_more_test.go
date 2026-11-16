package dev

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/knaus94/agentgate/internal/accounts"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

// waiting opens a light task: it is written and then waits for the lead's own review, so it stays active.
func (f *fixture) waiting(request string) map[string]any {
	f.t.Helper()
	plan := f.plan("-l " + request)
	plan.Checks = nil
	return f.start(plan)
}

func TestPlanValidationRejectsUnsafeOrAmbiguousScope(t *testing.T) {
	f := newFixture(t)
	must(t, os.MkdirAll("dir", 0o755))
	must(t, os.Symlink("a.txt", "link.txt"))
	must(t, os.Symlink("dir", "linked-dir"))
	for name, files := range map[string][]string{"absolute": {"/etc/passwd"}, "parent": {"../x"}, "git": {".git/config"}, "symlink": {"link.txt"},
		"through a symlink": {"linked-dir/new.txt"}, "a pattern through a symlink": {"linked-dir/**"}, "empty": {}, "a citation": {"a.txt:10-20 — the line"}} {
		if _, err := Resolve(f.cfg, f.plan("-m task", Stream{Name: "core", Domain: "backend", Files: files})); err == nil {
			t.Errorf("%s scope must be rejected", name)
		}
	}
	owned, err := Resolve(f.cfg, f.plan("-m task", Stream{Name: "core", Domain: "backend", Files: []string{"dir", "new/", "src/**/*.go", "new/file.txt"}}))
	must(t, err)
	if got := owned.Plan.Stages[0].Streams[0].Files; !slices.Equal(got, []string{"dir/**", "new/**", "new/file.txt", "src/**/*.go"}) {
		t.Fatalf("a stream owns files, directories and patterns; a file that does not exist yet is a valid scope: %v", got)
	}
	for name, pair := range map[string][2][]string{
		"the same file":             {{"a.txt"}, {"a.txt"}},
		"a file under a directory":  {{"src/"}, {"src/a.go"}},
		"nested patterns":           {{"src/**"}, {"src/api/**"}},
		"a pattern and its file":    {{"*.txt"}, {"a.txt"}},
		"patterns of the same kind": {{"src/**/*.go"}, {"src/api/*.go"}},
	} {
		plan := f.plan("-m task", Stream{Name: "a", Domain: "backend", Files: pair[0]}, Stream{Name: "b", Domain: "backend", Files: pair[1]})
		if _, err := Resolve(f.cfg, plan); err == nil || !strings.Contains(err.Error(), "disjoint") {
			t.Errorf("streams that could write %s must be refused, got %v", name, err)
		}
	}
	apart := f.plan("-m task", Stream{Name: "a", Domain: "backend", Files: []string{"src/api/**", "web/**/*.ts"}},
		Stream{Name: "b", Domain: "frontend", Files: []string{"src/ui/**", "web/**/*.css"}})
	apart.UI = &UI{Skip: "fixture"}
	if _, err := Resolve(f.cfg, apart); err != nil {
		t.Fatalf("separate directories and separate kinds of file do not collide: %v", err)
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
	err = json.Unmarshal([]byte(`{"request":"-m x","brief":"b","streams":[{"name":"c","domain":"backend","files":["a.txt"]}],"checks":[["true"],{"argv":["false"],"baseline":true}]}`), plan)
	if err != nil || len(plan.Checks) != 2 || plan.Checks[0].Argv[0] != "true" || !plan.Checks[1].Baseline {
		t.Fatalf("checks accept both forms: %+v %v", plan, err)
	}
	if before := digest(plan); true {
		if _, err := Resolve(f.cfg, plan); err != nil || digest(plan) != before {
			t.Fatalf("validating a plan does not change it: %v", err)
		}
	}
}

func TestUIFilesNeedAContract(t *testing.T) {
	f := newFixture(t)
	write(t, "view.tsx", "before\n")
	ui := Stream{Name: "web", Domain: "frontend", Files: []string{"view.tsx"}}
	if _, err := Resolve(f.cfg, f.plan("-m change the label", ui)); err == nil || !strings.Contains(err.Error(), "UI files") {
		t.Fatalf("UI files without a contract must be rejected, got %v", err)
	}
	if _, err := Resolve(f.cfg, f.plan("-m change the labels", Stream{Name: "web", Domain: "frontend", Files: []string{"app/**/*.tsx"}})); err == nil || !strings.Contains(err.Error(), "UI files") {
		t.Fatalf("a pattern of UI files needs a contract too, got %v", err)
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

func TestReviewOfExistingWorkIsReadOnlyAndEndsWithItsFindings(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap is not installed")
	}
	f := newFixture(t)
	write(t, "a.txt", "changed\n")
	write(t, "b.txt", "changed\n")
	f.reviewOnce(finding("P1"))
	// The scope of a review needs no hand-written list: it is whatever differs from the base.
	plan := &Plan{Request: "-m review the branch", Brief: "Review what is in the working tree",
		Checks: []Check{{Argv: []string{"sh", "-c", "echo leak > leaked.txt; exit 1"}}}}
	done := f.start(plan)
	gate := Current()
	if done["step"] != StepDone || f.count("writer") != 0 || f.count("review") != 1 {
		t.Fatalf("a review writes nothing, and ends with its report even when a check fails and a finding blocks: %v", done)
	}
	if !slices.Equal(gate.scope(), []string{"a.txt", "b.txt"}) {
		t.Fatalf("the reviewed scope is what differs from the base: %v", gate.scope())
	}
	if before, _, _ := treeFile(gate.objects(), gate.BaseTree, "a.txt"); string(before) != "before\n" {
		t.Fatal("the reviewed change starts at the base commit, not at the working tree")
	}
	if result := done["outcome"].(map[string]any); len(result["findings"].([]any)) != 1 {
		t.Fatalf("the findings are the result of a review: %v", result)
	}
	if _, err := os.Stat("leaked.txt"); err == nil {
		t.Fatal("a check of a read-only review must not be able to write the checkout")
	}
	if _, err := gate.openCorrection(f.cfg, false, nil); err == nil || !strings.Contains(err.Error(), "no authority") {
		t.Fatalf("a review without --fix never changes code: %v", err)
	}
	clean := newFixture(t)
	clean.fails("start", "there is no change to review", func() (any, error) {
		return Start(clean.cfg, &Plan{Request: "-m review the branch", Brief: "Review"}, "", false, RunInput{})
	})
}

func TestAStreamMarkedDoneKeepsItsFilesAndStartsNoWriter(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m build it", Stream{Name: "core", Domain: "backend", Files: []string{"a.txt"}, Done: true}, Stream{Name: "extra", Domain: "backend", Files: []string{"b.txt"}})
	write(t, "a.txt", "written before the task\n")
	if done := f.start(plan); done["step"] != StepDone || f.count("writer") != 1 || fileText(t, "a.txt") != "written before the task\n" {
		t.Fatalf("only the stream that has work gets a writer: %v", done)
	}
	if !slices.Equal(Current().scope(), []string{"a.txt", "b.txt"}) {
		t.Fatal("a done stream still owns its files")
	}
}

func TestTheLeadRulesOnlyOnWhatTheAuthorAndTheReviewerDisagreeAbout(t *testing.T) {
	f := newFixture(t)
	variant := func(severity, evidence string) string {
		return strings.Replace(finding(severity), "wrong value", evidence, 1)
	}
	reviewed := false
	f.raw = true
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		switch {
		case kind == "writer" && packet["correction"] != nil:
			// The author knows the brief: it declines both findings, with evidence, and changes nothing.
			var disputes []Dispute
			for _, raw := range packet["correction"].(map[string]any)["findings"].([]any) {
				disputes = append(disputes, Dispute{ID: raw.(map[string]any)["id"].(string), Evidence: "the brief asks for exactly this value (a.txt:1)"})
			}
			out, _ := json.Marshal(map[string]any{"status": "COMPLETED", "summary": "disputed both", "blockers": []string{}, "handoff": "", "deviations": []string{}, "disputes": disputes})
			return string(out), nil
		case kind != "review":
			return "", nil
		case !reviewed:
			reviewed = true
			return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + variant("P1", "first") + `,` + variant("P1", "second") + `,` +
				variant("P2", "third") + `,` + variant("P3", "polish") + `],"checked":["read"],"resolutions":[]},` +
				`{"axis":"architecture","status":"PASS","findings":[],"checked":["read"],"resolutions":[]},` +
				`{"axis":"simplicity","status":"PASS","findings":[],"checked":["read"],"resolutions":[]}]}`, nil
		}
		// The reviewer reads the disputes: it withdraws the second finding and keeps the first.
		disputes, _ := json.Marshal(packet["disputes"])
		if !strings.Contains(string(disputes), "the brief asks for exactly this value") {
			t.Errorf("the reviewer is shown the author's evidence: %s", disputes)
		}
		ids := map[string]string{}
		for _, raw := range packet["accepted_findings"].([]any) {
			item := raw.(map[string]any)
			ids[strings.Fields(item["evidence"].(string))[0]] = item["id"].(string)
		}
		kept := strings.Replace(variant("P1", "first"), `"id":""`, `"id":"`+ids["first"]+`"`, 1)
		return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + kept + `],"checked":["read the dispute"],"resolutions":[{"id":"` + ids["second"] +
			`","resolved":true,"evidence":"the author is right: the brief asks for it"}]},` +
			`{"axis":"architecture","status":"PASS","findings":[],"checked":["read"],"resolutions":[]},` +
			`{"axis":"simplicity","status":"PASS","findings":[],"checked":["read"],"resolutions":[]}]}`, nil
	}
	stopped := f.start(f.plan("-m change the line"))
	by := map[string]string{}
	for _, item := range Current().Findings() {
		by[strings.Fields(item.Evidence)[0]] = item.ID
	}
	gate := Current()
	if stopped["step"] != StepDecide || !slices.Equal(needs(stopped), []string{"finding"}) || f.count("writer") != 2 {
		t.Fatalf("only the dispute the reviewer upheld reaches the lead: %v", stopped)
	}
	need := stopped["needs"].([]any)[0].(map[string]any)
	if need["id"] != by["first"] || !strings.Contains(fmt.Sprint(need["detail"]), "the brief asks for exactly this value") || !strings.Contains(need["what"].(string), "upheld") {
		t.Fatalf("the lead gets both arguments: %v", need)
	}
	if _, withdrawn := gate.Resolutions[by["second"]]; !withdrawn {
		t.Fatal("a dispute the reviewer accepts closes the finding without the lead")
	}
	if !gate.isNote(f.cfg, gate.Findings()[2]) || !gate.isNote(f.cfg, gate.Findings()[3]) {
		t.Fatal("a P2 in medium and a P3 are notes: nobody is asked to judge them")
	}
	rule := func(items ...Disposition) error {
		_, err := Execute("decide", func() (any, error) { return Decide(f.cfg, Current(), DecideInput{Findings: items}) })
		return err
	}
	for name, item := range map[string]Disposition{
		"unknown id":           {ID: "nope", Decision: "accepted", Evidence: "e"},
		"no evidence":          {ID: by["first"], Decision: "accepted"},
		"bad decision":         {ID: by["first"], Decision: "maybe", Evidence: "e"},
		"polish without basis": {ID: by["polish"], Decision: "accepted", Evidence: "e"},
		"duplicate of itself":  {ID: by["third"], Decision: "rejected", Evidence: "e", DuplicateOf: by["third"]},
		"duplicate of nothing": {ID: by["third"], Decision: "rejected", Evidence: "e", DuplicateOf: "nope"},
		"a file outside scope": {ID: by["first"], Decision: "accepted", Evidence: "e", Files: []string{"b.txt"}},
	} {
		if err := rule(item); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	// Deferring a finding that blocks does not make it stop blocking. A duplicate is closed as a duplicate,
	// whatever decision the lead wrote for it.
	must(t, rule(Disposition{ID: by["first"], Decision: "deferred", Evidence: "later"},
		Disposition{ID: by["third"], Decision: "accepted", DuplicateOf: by["first"]}))
	if duplicate := Current().Dispositions[by["third"]]; duplicate.Decision != "rejected" || duplicate.DuplicateOf != by["first"] || duplicate.Evidence == "" {
		t.Fatalf("a duplicate is recorded as a rejected duplicate of its canonical finding: %+v", duplicate)
	}
	if still := asMap(Current().report(f.cfg, nil)); !slices.Equal(needs(still), []string{"finding"}) || Current().Step != StepDecide {
		t.Fatalf("a deferred P1 still blocks in medium: %v", still)
	}
	must(t, rule(Disposition{ID: by["first"], Decision: "needs-approval", Evidence: "the user must decide"}))
	if Current().Step != StepDecide {
		t.Fatal("a finding that waits for an approval keeps the task open")
	}
	must(t, rule(Disposition{ID: by["first"], Decision: "rejected", Evidence: "contrary evidence in a.txt:1"}))
	gate = Current()
	if gate.Step != StepDone {
		t.Fatalf("rejecting the last blocking finding with evidence lets the task finish: %s", gate.Step)
	}
	write(t, "a.txt", "edited after the task was done\n")
	if hook := StopHook(gate.Cwd, "fixture-session"); hook["decision"] != "block" || Current().Step != StepVerify || Current().Final != nil {
		t.Fatal("editing finished work reopens the task")
	}
}

func TestAnUpheldFindingIsFixedOnceTheLeadSaysSo(t *testing.T) {
	f := newFixture(t)
	var id string
	disputed := false
	f.raw = true
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		pass := func(findings string, status string) string {
			return `{"reports":[{"axis":"correctness","status":"` + status + `","findings":[` + findings + `],"checked":["read"],"resolutions":[]},` +
				`{"axis":"architecture","status":"PASS","findings":[],"checked":["read"],"resolutions":[]},{"axis":"simplicity","status":"PASS","findings":[],"checked":["read"],"resolutions":[]}]}`
		}
		switch {
		case kind == "writer" && packet["correction"] != nil:
			correction := packet["correction"].(map[string]any)
			id = correction["findings"].([]any)[0].(map[string]any)["id"].(string)
			if upheld := asStrings(correction["upheld"]); len(upheld) == 1 {
				if work := asStrings(correction["work"]); len(work) != 1 || !strings.Contains(work[0], "The reviewer is right") {
					t.Errorf("the lead's ruling reaches the writer: %v", correction["work"])
				}
				write(t, "a.txt", "corrected\n")
				return `{"status":"COMPLETED","summary":"fixed it","blockers":[],"handoff":"","deviations":[],"disputes":[{"id":"` + id + `","evidence":"still disagree"}]}`, nil
			}
			disputed = true
			return `{"status":"COMPLETED","summary":"disputed","blockers":[],"handoff":"","deviations":[],"disputes":[{"id":"` + id + `","evidence":"it is intended"}]}`, nil
		case kind != "review":
			return "", nil
		case fileText(t, "a.txt") == "corrected\n":
			return f.defaultAnswer(kind, packet), nil
		case !disputed:
			return pass(finding("P1"), "NEEDS_FIX"), nil
		}
		return pass(strings.Replace(finding("P1"), `"id":""`, `"id":"`+id+`"`, 1), "NEEDS_FIX"), nil
	}
	stopped := f.start(f.plan("-m change the line"))
	if !slices.Equal(needs(stopped), []string{"finding"}) {
		t.Fatalf("an upheld dispute stops for the lead: %v", stopped)
	}
	done := f.decide(DecideInput{Findings: []Disposition{{ID: id, Decision: "accepted", Evidence: "The reviewer is right: a.txt:1 holds the wrong value"}},
		Work: []WorkOrder{{Stream: "core", Note: "The reviewer is right; use the agreed value"}}})
	gate := Current()
	if done["step"] != StepDone || len(gate.Disputes) != 0 {
		t.Fatalf("a finding the lead upheld is fixed, and cannot be disputed a second time: %v %+v", done, gate.Disputes)
	}
}

func TestAStalledCorrectionNeedsANewDiagnosis(t *testing.T) {
	f := newFixture(t)
	var id string
	f.raw = true
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" {
			report := strings.Replace(finding("P1"), `"id":""`, `"id":"`+id+`"`, 1)
			return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + report + `],"checked":["read"],"resolutions":[]},` +
				`{"axis":"architecture","status":"PASS","findings":[],"checked":["read"],"resolutions":[]},{"axis":"simplicity","status":"PASS","findings":[],"checked":["read"],"resolutions":[]}]}`, nil
		}
		if kind == "writer" && packet["correction"] != nil {
			correction := packet["correction"].(map[string]any)
			id = correction["findings"].([]any)[0].(map[string]any)["id"].(string)
			raw, _ := os.ReadFile("a.txt")
			_ = os.WriteFile("a.txt", append(raw, []byte("attempt\n")...), 0o644)
			if len(correction["findings"].([]any)) != 1 || packet["your_previous_report"] == nil {
				t.Errorf("the correction names the findings and the writer's history: %v", correction)
			}
			return `{"status":"COMPLETED","summary":"tried","blockers":[],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return "", nil
	}
	stopped := f.start(f.plan("-m change the line"))
	gate := Current()
	if !slices.Equal(needs(stopped), []string{"stall"}) || len(gate.Findings()) != 1 || gate.Iteration != 1 {
		t.Fatalf("a finding reported again keeps its id, and a correction that fixed nothing is not repeated: %v", stopped)
	}
	if again := f.proceed(); !slices.Equal(needs(again), []string{"stall"}) || f.count("writer") != 2 {
		t.Fatalf("running again does not buy another identical attempt: %v", again)
	}
	f.fails("decide", "needs note", func() (any, error) {
		return Decide(f.cfg, Current(), DecideInput{Work: []WorkOrder{{Stream: "core"}}})
	})
	f.decide(DecideInput{Work: []WorkOrder{{Stream: "core", Note: "the cause is elsewhere: fix the caller"}}})
	gate = Current()
	if gate.Iteration != 2 || len(gate.Corrections) != 2 || !strings.Contains(gate.Round.Work[0].Note, "fix the caller") {
		t.Fatalf("a reassessed correction is a new round that carries the new diagnosis: %d", gate.Iteration)
	}
	efforts := []string{}
	for _, writer := range gate.Writers {
		efforts = append(efforts, writer.Route.Effort)
	}
	if !slices.Equal(efforts, []string{"medium", "medium", "high"}) {
		t.Fatalf("effort rises only after a correction stalled, and one step: %v", efforts)
	}
}

func TestStaleOrUnusableReviewsAreDiscarded(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" {
			_ = os.WriteFile("a.txt", []byte("edited while the reviewer was reading\n"), 0o644)
		}
		return "", nil
	}
	f.fails("start", "changed during the review", func() (any, error) { return Start(f.cfg, f.plan("-m change the line"), "", false, RunInput{}) })
	if gate := Current(); gate.Step != StepVerify || len(gate.Reviews) != 0 || gate.ReviewError == "" {
		t.Fatal("a stale review is discarded and the reason is kept")
	}
	run := func() error {
		_, err := Execute("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{}) })
		return err
	}
	blocked := `{"reports":[{"axis":"correctness","status":"BLOCKED","findings":[],"checked":["the packet could not be read"],"resolutions":[]},` +
		`{"axis":"architecture","status":"PASS","findings":[],"checked":["read"],"resolutions":[]},{"axis":"simplicity","status":"PASS","findings":[],"checked":["read"],"resolutions":[]}]}`
	f.raw = true
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" {
			return blocked, nil
		}
		return "", nil
	}
	if err := run(); err == nil || !strings.Contains(err.Error(), "could not review") {
		t.Fatalf("a reviewer that could not read its evidence fails its lane: %v", err)
	}
	others := `,{"axis":"architecture","status":"PASS","findings":[],"checked":["x"],"resolutions":[]},{"axis":"simplicity","status":"PASS","findings":[],"checked":["x"],"resolutions":[]}]}`
	for name, answer := range map[string]string{
		"missing axis":     `{"reports":[]}`,
		"an extra axis":    `{"reports":[{"axis":"correctness","status":"PASS","findings":[],"checked":["x"],"resolutions":[]},{"axis":"security","status":"PASS","findings":[],"checked":["x"],"resolutions":[]}` + others,
		"nothing checked":  `{"reports":[{"axis":"correctness","status":"PASS","findings":[],"checked":[],"resolutions":[]}` + others,
		"unscoped finding": `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + strings.Replace(finding("P1"), "a.txt", "b.txt", 1) + `],"checked":["x"],"resolutions":[]}` + others,
		"empty needs-fix":  `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[],"checked":["x"],"resolutions":[]}` + others,
		"not json":         `the code looks fine`,
	} {
		f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
			if kind == "review" {
				return answer, nil
			}
			return "", nil
		}
		before := f.count("review")
		if err := run(); err == nil {
			t.Errorf("a review with %s must be rejected", name)
		}
		last := f.calls[len(f.calls)-1]
		if f.count("review") != before+1+reportCorrections || last.Resume == "" || !strings.Contains(last.Prompt, "cannot be used") {
			t.Errorf("%s: the reviewer is asked to correct its report in its own session before its lane fails: %d calls", name, f.count("review")-before)
		}
	}
	if gate := Current(); gate.Step != StepVerify || len(gate.Reviews) != 0 {
		t.Fatal("no unusable review was recorded")
	}
	// A reviewer that corrects its report is not paid for a second review, and the first is not thrown away.
	asked := 0
	f.raw = false
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "review" {
			return "", nil
		}
		if asked++; asked == 1 {
			f.raw = true
			return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[],"checked":["x"],"resolutions":[]}` + others, nil
		}
		f.raw = false
		if call.Resume == "" || !strings.Contains(call.Prompt, "says NEEDS_FIX and lists no finding") {
			t.Errorf("the correction names the problem and continues the session: %q", call.Prompt)
		}
		return "", nil
	}
	if err := run(); err != nil || asked != 2 || Current().Step != StepDone {
		t.Fatalf("a corrected report completes the review: %v after %d calls", err, asked)
	}
}

func TestPauseResumeStopAndStatusAreHonest(t *testing.T) {
	f := newFixture(t)
	f.waiting("change the line")
	f.fails("pause", "reason", func() (any, error) { return Pause(f.cfg, Current(), PauseInput{}) })
	paused := f.do("pause", func() (any, error) { return Pause(f.cfg, Current(), PauseInput{Reason: "waiting for the user"}) })
	if paused["step"] != StepDecide || paused["paused"].(map[string]any)["reason"] != "waiting for the user" || offered(paused)[0] != "resume" {
		t.Fatalf("a pause lies on top of the position and does not change it: %v", paused)
	}
	before, _ := os.ReadFile(filepath.Join(home(), "state.json"))
	_, _ = Status(f.cfg, Current(), "")
	_, _ = Status(f.cfg, Current(), "all")
	_ = PanelText(f.cfg, false, false, 120, 0)
	if after, _ := os.ReadFile(filepath.Join(home(), "state.json")); string(after) != string(before) {
		t.Fatal("status and watch must not change the task")
	}
	if resumed := f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) }); resumed["paused"] != nil || resumed["step"] != StepDecide {
		t.Fatalf("resume lifts the pause and leaves the position: %v", resumed)
	}
	f.fails("resume", "only a paused task", func() (any, error) { return Resume(f.cfg, Current()) })
	// A pause that waits for a decision is lifted by taking the decision.
	f.do("pause", func() (any, error) { return Pause(f.cfg, Current(), PauseInput{Reason: "thinking"}) })
	if f.proceed(); Current().Pause != nil {
		t.Fatal("running the task is the decision a pause waited for")
	}
	stopped, err := Stop(f.cfg)
	must(t, err)
	gate := Current()
	if gate.Pause == nil || gate.Pause.Kind != PauseUser || !slices.Equal(offered(asMap(stopped)), []string{"resume", "stop"}) {
		t.Fatalf("stop without a running operation parks the task as stopped by the user: %v", stopped)
	}
	for name, action := range map[string]func() (any, error){
		"run":    func() (any, error) { return Run(f.cfg, Current(), RunInput{}) },
		"decide": func() (any, error) { return Decide(f.cfg, Current(), DecideInput{Review: &LeadReview{Summary: "x"}}) },
		"amend":  func() (any, error) { return Amend(f.cfg, Current(), &Plan{Update: "x"}) },
	} {
		f.fails(name, "the user stopped this task", action)
	}
	f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) })
	cancelled := f.do("pause", func() (any, error) { return Pause(f.cfg, Current(), PauseInput{Reason: "dropped", Cancelled: true}) })
	if cancelled["step"] != StepCancelled || len(StopHook(Current().Cwd, "fixture-session")) != 0 || len(offered(cancelled)) != 0 {
		t.Fatalf("a cancelled task is over and does not block the stop: %v", cancelled)
	}
	if summary := f.start(f.plan("-m a new task")); summary["step"] != StepDone {
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
	f.waiting("change the line")
	posted, err := Tell(Current(), TellInput{Message: "use the v2 contract"})
	must(t, err)
	_, err = Tell(Current(), TellInput{Message: "and keep the old field"})
	must(t, err)
	raw, _ := os.ReadFile(posted["posted"].(string))
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
		t.Fatal("a consultation opens no task")
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
	if done := f.start(plan); done["step"] != StepDone {
		t.Fatalf("a task with an accepted design runs: %v", done)
	}
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
	if waiting := f.start(plan); !slices.Equal(needs(waiting), []string{"scenarios"}) {
		t.Fatalf("opted-in scenarios keep the task open until they are observed: %v", waiting)
	}
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
	wide := stand
	wide.Exclusions = []Excluded{{ID: "narrow", Reason: "no mobile fixture"}}
	done := f.do("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{Visual: &wide}) })
	run := Current().VisualRuns[0]
	if done["step"] != StepDone || len(run.Coverage) != 1 || run.Coverage[0].ScenarioID != "wide" || !strings.HasSuffix(run.Coverage[0].Screenshot, "shot.png") {
		t.Fatalf("only the active scenario is observed, with its screenshot; the excluded one is on record with its reason: %+v", run.Coverage)
	}
	var visualCall provider.Call
	for _, call := range f.calls {
		if call.MCP != nil {
			visualCall = call
		}
	}
	args := strings.Join(visualCall.MCP["playwright"].Args, " ")
	if !strings.Contains(args, "--allowed-origins http://localhost:4200") || !strings.Contains(args, "--viewport-size 1440x900") || visualCall.Route.Alias != "astra" {
		t.Fatalf("the browser is confined to the stand: %s", args)
	}
}

// browserPlan is a page change with browser verification opted in, run through a stand-in Playwright server.
func browserPlan(t *testing.T, f *fixture, scenarios ...Scenario) *Plan {
	t.Helper()
	tool := filepath.Join(t.TempDir(), "cli.js")
	write(t, tool, "")
	f.cfg.PlaywrightMCP = tool
	write(t, "view.tsx", "before\n")
	plan := f.plan("-m rename the label", Stream{Name: "web", Domain: "frontend", Files: []string{"view.tsx"}})
	plan.UI = &UI{Summary: "rename", Criteria: []string{"new copy"}, Preserve: []string{"the rest"}}
	plan.Visual = &Visual{Enabled: true, Reason: "the user asked", Scenarios: scenarios}
	return plan
}

func TestABrowserPassThatObservedIsRecordedWithWhatItCouldNotEstablish(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	f := newFixture(t)
	observed := false
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "visual" {
			return "", nil
		}
		shots := packet["screenshots_dir"].(string)
		_ = os.WriteFile(filepath.Join(shots, "shot.png"), []byte("png"), 0o600)
		var coverage []map[string]any
		for _, raw := range packet["scenarios"].([]any) {
			row := raw.(map[string]any)
			coverage = append(coverage, map[string]any{"scenario_id": row["id"], "screen": row["screen"], "state": row["state"],
				"viewport": row["viewport"], "role": row["role"], "observed": observed, "screenshot": "shot.png", "note": "seen"})
		}
		raw, _ := json.Marshal(map[string]any{"status": "UNVERIFIED", "summary": "no defects in what was seen", "coverage": coverage, "findings": []any{},
			"resolutions": []any{}, "console_errors": []any{}, "unverified": []string{"slow responses cannot be driven by the browser tools"}})
		return string(raw), nil
	}
	f.start(browserPlan(t, f, Scenario{ID: "list", Screen: "List", State: "filled", Viewport: "1440x900", Role: "operator", Expected: "label"}))
	stand := VisualInput{URL: "http://localhost:4200/list", Origins: []string{"http://localhost:4200"}}
	_, err := Execute("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{Visual: &stand}) })
	if len(Current().VisualRuns) != 0 || !strings.Contains(fmt.Sprint(err)+Current().ReviewError, "could not verify the stand") {
		t.Fatalf("a pass that observed nothing failed: %v / %q", err, Current().ReviewError)
	}
	observed = true
	done := f.do("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{Visual: &stand}) })
	run := Current().VisualRuns[0]
	receipt, _ := done["receipt"].(map[string]any)
	if done["step"] != StepDone || run.Status != "PASS" || len(run.Limits) != 1 ||
		!strings.Contains(fmt.Sprint(done["did"]), "not established: slow responses") || !strings.Contains(fmt.Sprint(receipt["browser"]), "not established") {
		t.Fatalf("a pass that observed its scenarios is recorded, with what it could not establish beside it: %+v %v", run, done)
	}
}

func TestBrowserScenariosAreDueAtTheirStage(t *testing.T) {
	f := newFixture(t)
	write(t, "view.tsx", "before\n")
	plan := &Plan{Request: "-m add the endpoint, then the page", Brief: "An endpoint, then the page that shows it",
		Assessment:   &Assessment{Size: "ordinary", Evidence: "two fixture files"},
		Requirements: []Requirement{{ID: "R1", Text: "the endpoint serves the data"}, {ID: "R2", Text: "the page shows it"}},
		Stages: []Stage{
			{Name: "api", Goal: "Serve the data", Covers: []string{"R1"}, Streams: []Stream{{Name: "api", Domain: "backend", Files: []string{"a.txt"}}}},
			{Name: "page", Goal: "Show the data", Covers: []string{"R2"}, Streams: []Stream{{Name: "web", Domain: "frontend", Files: []string{"view.tsx"}}}},
		},
		Checks: []Check{{Argv: []string{"true"}}},
		UI:     &UI{Summary: "show the data", Criteria: []string{"a row per item"}, Preserve: []string{"the rest"}},
		Visual: &Visual{Enabled: true, Reason: "the user asked", Scenarios: []Scenario{
			{ID: "error", Screen: "List", State: "the endpoint fails", Viewport: "1440x900", Role: "operator", Expected: "a message", Stage: "api"},
			{ID: "list", Screen: "List", State: "filled", Viewport: "1440x900", Role: "operator", Expected: "rows"}}}}
	due := func() (ids []string) {
		for _, row := range Current().dueScenarios() {
			ids = append(ids, row.ID)
		}
		return ids
	}
	first := f.start(plan)
	if !slices.Equal(needs(first), []string{"scenarios"}) || !slices.Equal(due(), []string{"error"}) {
		t.Fatalf("the first stage owes only the scenario that names it: %v %v", needs(first), due())
	}
	f.do("visual", func() (any, error) {
		return VisualPass(f.cfg, Current(), VisualInput{SkipReason: "no stand for the endpoint yet"})
	})
	second := f.proceed()
	if Current().Stage != 1 || !slices.Equal(needs(second), []string{"scenarios"}) || !slices.Equal(due(), []string{"list"}) {
		t.Fatalf("a skip of one stage does not cover the next, and a scenario without a stage is due at the last: %v %v", needs(second), due())
	}
}

func TestAReviewerOnTheWritersModelRunsAsConfigured(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	f := newFixture(t)
	f.cfg.VisualReviewer = "opus:medium" // the model that writes medium
	f.cfg.Fallbacks["opus"] = nil
	plan := browserPlan(t, f, Scenario{ID: "list", Screen: "List", State: "filled", Viewport: "1440x900", Role: "operator", Expected: "label"})
	f.start(plan)
	stand := VisualInput{URL: "http://localhost:4200/list", Origins: []string{"http://localhost:4200"}}
	f.do("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{Visual: &stand}) })
	var browser provider.Call
	for _, call := range f.calls {
		if call.MCP != nil {
			browser = call
		}
	}
	if browser.Route.Alias != "opus" || len(Current().Substitutions) != 0 || len(Current().VisualRuns) != 1 {
		t.Fatalf("which model checks the browser is the user's choice, also the model that writes: %+v %+v", browser.Route, Current().Substitutions)
	}
}

func TestVisualChoiceAndSkipAreRecorded(t *testing.T) {
	f := newFixture(t)
	write(t, "view.tsx", "before\n")
	plan := f.plan("-l rename the label", Stream{Name: "web", Domain: "frontend", Files: []string{"view.tsx"}})
	plan.UI = &UI{Summary: "rename", Criteria: []string{"new copy"}, Preserve: []string{"the rest"}}
	plan.Checks = nil
	f.start(plan)
	f.fails("visual", "not opted in", func() (any, error) {
		return VisualPass(f.cfg, Current(), VisualInput{URL: "http://localhost:1/", Origins: []string{"http://localhost:1"}})
	})
	choice := &Visual{Enabled: true, Reason: "the user asked", Scenarios: []Scenario{{ID: "s", Screen: "S", State: "x", Viewport: "1440x900", Role: "r", Expected: "e"}}}
	f.do("visual", func() (any, error) { return VisualPass(f.cfg, Current(), VisualInput{Choice: choice}) })
	if !Current().visualRequired() {
		t.Fatal("the recorded choice makes the scenarios required")
	}
	if held := f.decide(DecideInput{Review: &LeadReview{Summary: "read the patch"}}); !slices.Equal(needs(held), []string{"scenarios"}) {
		t.Fatalf("a clean review does not finish a task whose scenarios were not observed: %v", held)
	}
	skipped := f.do("visual", func() (any, error) {
		return VisualPass(f.cfg, Current(), VisualInput{SkipReason: "the stand cannot start"})
	})
	if Current().visualRequired() || len(needs(skipped)) != 0 {
		t.Fatalf("a recorded skip lifts the requirement: %v", skipped)
	}
	if done := f.proceed(); done["step"] != StepDone {
		t.Fatalf("the task finishes, with the skip on record: %v", done)
	}
}

// inputsIn lists what input patterns name in the working tree as a task records it.
func inputsIn(t *testing.T, patterns ...string) ([]string, error) {
	t.Helper()
	store := filepath.Join(t.TempDir(), "objects")
	tree, err := snapshotTree(store, nil)
	must(t, err)
	entries, err := treeListing(store, tree)
	must(t, err)
	files, err := inputFiles(entries, patterns)
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(files)), nil
}

func TestInputGlobs(t *testing.T) {
	newFixture(t)
	for _, path := range []string{"src/a.go", "src/deep/b.go", "src/deep/c.txt", "lib/d.go", "top.go"} {
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
		got, err := inputsIn(t, pattern)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: want %v, got %v (%v)", pattern, want, got, err)
		}
	}
	// What git ignores is not an input of a pattern that only passes over it; a pattern pointing into it reads it.
	write(t, ".gitignore", "src/cache/\nbuild/\n")
	write(t, "src/cache/results.json", "{}")
	write(t, "build/out.js", "out")
	if got, _ := inputsIn(t, "src/**"); slices.Contains(got, "src/cache/results.json") {
		t.Fatalf("a tool's cache that git ignores is an input of src/**: %v", got)
	}
	if got, _ := inputsIn(t, "build/**"); !slices.Equal(got, []string{"build/out.js"}) {
		t.Fatalf("a pattern into an ignored directory counts its files: %v", got)
	}
	// A link counts with what it leads to; one leading outside the repository is refused.
	must(t, os.Symlink("../top.go", "src/link.go"))
	if got, err := inputsIn(t, "src/link.go"); err != nil || !slices.Equal(got, []string{"src/link.go", "top.go"}) {
		t.Fatalf("a link counts with its target: %v (%v)", got, err)
	}
	must(t, os.Symlink("../../elsewhere", "src/out.go"))
	if _, err := inputsIn(t, "src/*.go"); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("a link leading outside the repository must be refused: %v", err)
	}
}

func TestPruneRemovesOnlyOldFinishedTasks(t *testing.T) {
	f := newFixture(t)
	f.waiting("change the line")
	old := time.Now().Add(-30 * 24 * time.Hour)
	state := filepath.Join(home(), "state.json")
	must(t, os.Chtimes(state, old, old))
	if Prune(14) != 0 {
		t.Fatal("an unfinished task is never pruned, however old")
	}
	f.do("pause", func() (any, error) { return Pause(f.cfg, Current(), PauseInput{Reason: "dropped", Cancelled: true}) })
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
	plan := f.plan("-l change the line")
	f.start(plan)
	t.Setenv("AGENTGATE_SESSION", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	if HasSession() {
		t.Fatal("a viewer has no session")
	}
	panel := PanelText(f.cfg, false, false, 120, 0)
	for _, want := range []string{"TASKS 1", "light", "decide", "change the line", "writer  core", "sol:medium", "implemented", "CHECKS", "true", "next ", "read the patch"} {
		if !strings.Contains(panel, want) {
			t.Errorf("the panel lacks %q:\n%s", want, panel)
		}
	}
	if strings.Contains(panel, "\x1b[") || !strings.Contains(PanelText(f.cfg, false, true, 120, 0), "\x1b[") {
		t.Fatal("colour is used only when asked for")
	}
	overview := Overview()["gates"].([]map[string]any)
	if len(overview) != 1 || overview[0]["step"] != StepDecide || overview[0]["operation_live"] != false {
		t.Fatalf("overview: %v", overview)
	}
	idle, err := StopFromOutside()
	must(t, err)
	if idle.(map[string]any)["stopped"] != false {
		t.Fatalf("nothing is running: %v", idle)
	}
	if _, err := Execute("run", func() (any, error) { return nil, nil }); err == nil {
		t.Fatal("without a session nobody can drive the task")
	}
	// A live operation of the checkout can be stopped from any terminal.
	t.Setenv("AGENTGATE_SESSION", "fixture-session")
	must(t, ops.Begin(home(), "run", ""))
	t.Setenv("AGENTGATE_SESSION", "")
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
	for _, want := range []string{`"mode":"heavy"`, `"api":"opus:high"`, `"web":"opus:high"`, `[["api"],["web"]]`, `"read_only":true`, "opus:high", `"planning":"sol:medium (as in medium)"`} {
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
		t.Fatal("route and preview start no model and create no task")
	}
	if _, err := os.Stat(Root()); err == nil {
		t.Fatal("route and preview write nothing")
	}
	if table := Modes(f.cfg); !strings.Contains(table, "ultra") || !strings.Contains(table, "fallback astra, opus") {
		t.Fatalf("modes table: %s", table)
	}
	// A reviewer on the model that writes runs as configured: which model reviews is the user's choice.
	medium := f.cfg.Modes["medium"]
	medium.Review = []config.Lane{{Axes: []string{"correctness"}, Route: "opus:medium"}}
	f.cfg.Modes["medium"] = medium
	f.cfg.Fallbacks["opus"] = nil
	if done := f.start(f.plan("-m task")); done["step"] != StepDone || Current().Reviews[0].Lanes[0].Route.Alias != "opus" {
		t.Fatalf("a plan reviewed by its writer's model starts and is reviewed by it: %v", done)
	}
}

func TestZedTasksAreAddedWithoutDamagingTheFile(t *testing.T) {
	isolateHomes(t)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	first, err := Install(InstallOptions{Zed: true})
	must(t, err)
	tasks := filepath.Join(dir, "zed", "tasks.json")
	raw, _ := os.ReadFile(tasks)
	if first.(map[string]any)["zed"].(map[string]any)["changed"] != true || strings.Count(string(raw), "agentgate: ") != 3 {
		t.Fatalf("three tasks are created: %s", raw)
	}
	write(t, tasks, `[{"label":"mine","command":"make"},{"label":"agentgate: watch","command":"agentgate watch"}]`)
	_, err = Install(InstallOptions{Zed: true})
	must(t, err)
	raw, _ = os.ReadFile(tasks)
	if !strings.Contains(string(raw), `"mine"`) || strings.Count(string(raw), "agentgate: watch") != 1 || strings.Count(string(raw), "agentgate: ") != 3 {
		t.Fatalf("existing tasks are kept and nothing is duplicated: %s", raw)
	}
	commented := "// my tasks\n[{\"label\":\"mine\",\"command\":\"make\"}]\n"
	write(t, tasks, commented)
	third, err := Install(InstallOptions{Zed: true})
	must(t, err)
	zed := third.(map[string]any)["zed"].(map[string]any)
	if after, _ := os.ReadFile(tasks); string(after) != commented || zed["changed"] != false {
		t.Fatal("a file with comments is left exactly as it was")
	}
	if side, _ := os.ReadFile(zed["paste_from"].(string)); !strings.Contains(string(side), "agentgate: watch") {
		t.Fatal("the tasks are offered in a side file instead")
	}
}

func TestDoctorReportsWhatIsMissing(t *testing.T) {
	isolateHomes(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Chdir(dir)
	report := Doctor()
	checks := map[string]probe{}
	for _, item := range report["checks"].([]probe) {
		checks[item.Name] = item
	}
	if report["healthy"] != false || checks["/dev command"].OK || checks["lead hooks"].OK || !checks["config"].OK {
		t.Fatalf("a machine without the command and hook is not healthy: %+v", checks)
	}
	if checks["browser tooling"].Required {
		t.Fatal("browser tooling is optional")
	}
	_, err := Install(InstallOptions{Lead: "claude"})
	must(t, err)
	checks = map[string]probe{}
	for _, item := range Doctor()["checks"].([]probe) {
		checks[item.Name] = item
	}
	if !checks["/dev command"].OK || !checks["lead hooks"].OK {
		t.Fatalf("install satisfies the doctor: %+v", checks)
	}
}

func TestRecheckRunsOneEffortStepLower(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && packet["correction"] != nil {
			write(t, "a.txt", "corrected\n")
		}
		return "", nil
	}
	f.reviewOnce(finding("P1"))
	plan := f.plan("-h change the contract")
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	plan.Architecture = &Accepted{ID: receipt["id"].(string), Acceptance: "accepted"}
	if done := f.start(plan); done["step"] != StepDone {
		t.Fatalf("start: %v", done)
	}
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
	if _, err := Resolve(f.cfg, hard); err != nil {
		t.Fatal(err)
	}
	if route := writerRoute(f.cfg, "medium", hard.Streams[0], 0); route.String() != "opus:high" {
		t.Fatalf("a hard stream may ask for more effort: %s", route)
	}
	if route := writerRoute(f.cfg, "medium", hard.Streams[0], 5); route.Effort != "high" {
		t.Fatalf("escalation stops at the mode's ceiling: %s", route)
	}
	hard.Streams[0].Effort = "max"
	if _, err := Resolve(f.cfg, hard); err == nil || !strings.Contains(err.Error(), "above what medium allows") {
		t.Fatalf("medium cannot be pushed to max: %v", err)
	}
	ultra := f.plan("-x move the money")
	if base, top := writerRoute(f.cfg, "ultra", ultra.Streams[0], 0), writerRoute(f.cfg, "ultra", ultra.Streams[0], 1); base.Effort != "xhigh" || top.Effort != "max" {
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

func TestAFailingCheckGoesBackToTheWriterBeforeAnyReviewer(t *testing.T) {
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
		return `{"status":"COMPLETED","summary":"tried","blockers":[],"handoff":"","deviations":[],"disputes":[]}`, nil
	}
	plan := f.plan("-m make it say fixed")
	plan.Checks = []Check{{Argv: []string{"sh", "-c", "grep -q fixed a.txt || { echo expected fixed; exit 1; }"}}}
	stalled := f.start(plan)
	if !slices.Equal(needs(stalled), []string{"stall"}) || f.count("review") != 0 || f.count("writer") != 2 {
		t.Fatalf("a failing check goes back to the writer once without the lead; nobody reviews code that fails its own check: %v", stalled)
	}
	if failing := stalled["checks"].(map[string]any)["failing"].([]any); len(failing) != 1 {
		t.Fatalf("the answer names the failing check: %v", stalled["checks"])
	}
	f.fails("decide", "not a stream of the present position", func() (any, error) {
		return Decide(f.cfg, Current(), DecideInput{Work: []WorkOrder{{Stream: "ghost", Note: "edit a.txt"}}})
	})
	repaired = true
	done := f.decide(DecideInput{Work: []WorkOrder{{Stream: "core", Note: "the file was untouched: write the word into a.txt"}}})
	if writers := Current().Writers; writers[len(writers)-1].Route.Effort != "high" {
		t.Fatalf("a check that keeps failing earns one more effort step: %s", writers[len(writers)-1].Route)
	}
	if done["step"] != StepDone || f.count("review") != 1 {
		t.Fatalf("once the check passes the reviewer runs, and a clean review finishes the task in the same call: %v", done)
	}
}

func TestTheArchitectAsksTheUserAndWritersFollowItsOneDesign(t *testing.T) {
	f := newFixture(t)
	retention := `{"id":"retention","question":"How long are exports kept?","options":["30 days","forever"],"recommended":"30 days"}`
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		switch {
		case kind == "architecture" && packet["round"] == "final":
			if other, _ := json.Marshal(packet["other_design"]); !strings.Contains(string(other), "design by astra") {
				t.Errorf("the closing round reads the other design: %s", other)
			}
			return design("the closing design", retention), nil
		case kind == "architecture" && packet["round"] == "revision":
			if answers, _ := json.Marshal(packet["answers"]); call.Resume == "" || !strings.Contains(string(answers), "How long are exports kept?") ||
				!strings.Contains(string(answers), "forever") || packet["notes"] != "The user: «and no archive table»" {
				t.Errorf("a revision continues the architect's session with the user's answers and words: %s", answers)
			}
			return design("the design that keeps exports", retention), nil // a repeated question is not asked again
		case kind == "architecture":
			return design("design by "+call.Route.Alias, ""), nil
		case kind == "writer":
			accepted := packet["architecture"].(map[string]any)
			if accepted["design"] != "the design that keeps exports" || accepted["acceptance"] != "The user: ship it behind the flag" || accepted["lanes"] != nil {
				t.Errorf("the writer follows the one design and the user's decisions on it: %v", accepted)
			}
		}
		return "", nil
	}
	plan := f.plan("-h change the contract")
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	if asked := receipt["questions"].([]any); len(asked) != 1 || asked[0].(map[string]any)["recommended"] != "30 days" {
		t.Fatalf("the architect's question reaches the lead with its options and recommendation: %v", receipt["questions"])
	}
	plan.Architecture = &Accepted{ID: receipt["id"].(string)}
	f.fails("start", "wait for the user: retention", func() (any, error) { return Start(f.cfg, plan, "", false, RunInput{}) })
	plan.Architecture.Answers = map[string]string{"retention": "forever"}
	f.fails("start", "chose otherwise on retention", func() (any, error) { return Start(f.cfg, plan, "", false, RunInput{}) })
	_, err := PlanSave(f.cfg, plan, "exports")
	must(t, err)
	f.fails("architecture", "does not ask", func() (any, error) {
		return ArchitectureRevise(f.cfg, ArchitectureInput{Plan: "exports", Revise: plan.Architecture.ID, Answers: map[string]string{"ghost": "x"}})
	})
	revised := f.do("architecture", func() (any, error) {
		return ArchitectureRevise(f.cfg, ArchitectureInput{Plan: "exports", Revise: plan.Architecture.ID,
			Answers: map[string]string{"retention": "forever"}, Notes: "The user: «and no archive table»"})
	})
	if revised["questions"] != nil || revised["follows"] != "revision by fable:high" {
		t.Fatalf("the revision took the answer; nothing waits for the user: %v", revised)
	}
	plan.Architecture = &Accepted{ID: revised["id"].(string), Acceptance: "The user: ship it behind the flag"}
	f.start(plan)
	if f.count("writer") != 1 {
		t.Fatal("the writer must have run")
	}
}

func TestADesignIsRevisedInTheMiddleOfATask(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		switch {
		case kind == "architecture" && packet["round"] == "revision":
			if task, _ := packet["task"].(map[string]any); task == nil || task["step"] == nil || packet["notes"] != "The user: «will it hold for a large account?»" {
				t.Errorf("a revision during a task knows where the task stands and what the user said: %v", packet["task"])
			}
			return `{"summary":"revised","design":"read only the window","alternatives":["a materialized view: refreshes every account at once"],` +
				`"risks":[],"questions":[],"answered":[],"plan_changes":["Add a requirement: a refresh reads only the rows of its window"]}`, nil
		case kind == "writer" && packet["update"] == nil:
			return `{"status":"BLOCKED","summary":"need a decision","blockers":["Which wording?"],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return "", nil
	}
	plan := f.plan("-h change the contract")
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	plan.Architecture = &Accepted{ID: receipt["id"].(string)}
	f.start(plan)
	revised := f.do("architecture", func() (any, error) {
		return ArchitectureRevise(f.cfg, ArchitectureInput{Revise: plan.Architecture.ID, Notes: "The user: «will it hold for a large account?»"})
	})
	if changes := asStrings(revised["plan_changes"]); len(changes) != 1 || !strings.Contains(revised["next"].(string), "plan_changes") {
		t.Fatalf("the architect says what the plan needs: %v", revised)
	}
	f.do("amend", func() (any, error) {
		return AmendOperations(f.cfg, Current(), Operations{Update: "The architect revised the design for large accounts", Rework: []string{"core"},
			Architecture: &Accepted{ID: revised["id"].(string)},
			Requirements: []RequirementChange{{ID: "R1", Text: "A refresh reads only the rows of its window"}}})
	})
	if gate := Current(); gate.Plan.Architecture.ID != revised["id"] || len(gate.Plan.Requirements) != 1 {
		t.Fatalf("the task follows the revised design: %+v", gate.Plan.Architecture)
	}
}

func TestLoadFactsReachTheWorkersAndAddThePerformanceAxis(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		if kind == "writer" && !strings.Contains(packet["brief"].(string), "## Load\n\n50k participants, refreshed every 15 s, under 2 s") {
			t.Errorf("the writer reads the load facts in its brief: %v", packet["brief"])
		}
		return "", nil
	}
	plan := f.plan("-m recount the participants")
	plan.Assessment.Load = "50k participants, refreshed every 15 s, under 2 s"
	if done := f.start(plan); done["step"] != StepDone {
		t.Fatalf("start: %v", done)
	}
	if lane := Current().Reviews[0].Lanes[0]; !slices.Equal(lane.Axes, []string{"correctness", "architecture", "simplicity", "performance"}) || len(lane.Reports) != 4 {
		t.Fatalf("the lane that judges correctness also judges the cost of the path: %v", lane.Axes)
	}
}

func TestUltraRechecksOnlyWhatTheCorrectionTouched(t *testing.T) {
	f := newFixture(t)
	first := true
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && packet["correction"] != nil {
			write(t, "a.txt", "corrected\n")
		}
		if kind == "review" && packet["axes"].([]any)[0] == "security" && first {
			first = false
			return `{"reports":[{"axis":"security","status":"NEEDS_FIX","findings":[` + finding("P1") + `],"checked":["read"],"resolutions":[]}]}`, nil
		}
		return "", nil
	}
	plan := f.plan("-x move the money")
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	plan.Architecture = &Accepted{ID: receipt["id"].(string), Acceptance: "accepted"}
	done := f.start(plan)
	reviews := Current().Reviews
	var axes []string
	for _, lane := range reviews[1].Lanes {
		axes = append(axes, lane.Axes[0])
	}
	if len(reviews[0].Lanes) != 4 || !slices.Equal(axes, []string{"correctness", "security"}) || done["step"] != StepDone {
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
	if saved.Plan != ".agentgate/plans/contract-change/plan.json" || !strings.Contains(saved.Next, "start --plan contract-change") {
		t.Fatalf("a plan is kept inside the repository: %+v", saved)
	}
	brief, err := PlanShow("contract-change")
	must(t, err)
	for _, want := range []string{"# contract-change", "**Mode:** heavy", "### core — backend, opus:high", "- `a.txt`", "the first design as written", "- `true`"} {
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
	t.Setenv("AGENTGATE_SESSION", "another-session")
	path, err := PlanFile("contract-change")
	must(t, err)
	raw, err := os.ReadFile(path)
	must(t, err)
	loaded := &Plan{}
	must(t, json.Unmarshal(raw, loaded))
	summary := f.do("start", func() (any, error) { return Start(f.cfg, loaded, "contract-change", false, RunInput{}) })
	if summary["mode"] != "heavy" || summary["step"] != StepDone || f.count("architecture") != 3 {
		t.Fatalf("the plan starts without a second design pass: %v", summary)
	}
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
	both := []config.Account{{Name: "main", Provider: "claude", Home: "~/main"}, {Name: "second", Provider: "claude", Home: "~/second"}}
	provider.Accounts = accounts.New(both, filepath.Join(Root(), "accounts.json"))
	t.Cleanup(func() { accounts.ProbeClaude, provider.Accounts = previous, nil })
	f.reviewOnce(finding("P1"))
	review := f.model
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		answer, err := review(kind, packet, call)
		if kind == "review" && answer != "" {
			// From here on the other account has far more room, but the writer's session lives on "second".
			accounts.ProbeClaude = func(account config.Account) accounts.Status {
				status := probe(account)
				status.Windows[0].Used = map[string]float64{"main": 1, "second": 90}[account.Name]
				return status
			}
			provider.Accounts = accounts.New(both, filepath.Join(Root(), "accounts-2.json"))
		}
		if kind == "writer" && packet["correction"] != nil {
			write(t, "a.txt", "corrected\n")
		}
		return answer, err
	}
	f.start(f.plan("change the line"))
	writers := Current().Writers
	if len(writers) != 2 || writers[0].Route.Account != "second" {
		t.Fatalf("the writer runs on the account with the most room: %+v", writers[0].Route)
	}
	var correction provider.Call
	for _, call := range f.calls {
		if call.Writer && call.Resume != "" {
			correction = call
		}
	}
	if correction.Resume != writers[0].Session || correction.Account.Name != "second" {
		t.Fatalf("a correction resumes the writer's session under the same account: %+v", correction.Account)
	}
	if text := PanelText(f.cfg, false, false, 120, 0); !strings.Contains(text, "@second") {
		t.Fatalf("the panel names the account a lane ran on:\n%s", text)
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
	summary := f.start(f.plan("-w change the line"))
	gate := Current()
	if gate.Worktree == "" || !strings.HasPrefix(gate.Branch, "dev/change-the-line-") {
		t.Fatalf("an isolated task has a worktree and a branch: %q %q", gate.Worktree, gate.Branch)
	}
	if fileText(t, "a.txt") != "before\n" || strings.TrimSpace(gitOut(t, "status", "--porcelain")) != "" {
		t.Fatal("neither working on an isolated task nor finishing it touches the checkout")
	}
	if fileText(t, filepath.Join(gate.Worktree, "a.txt")) != "after\n" || fileText(t, filepath.Join(gate.Worktree, ".ready")) != "SECRET=1\n" {
		t.Fatal("the writer works in the worktree, prepared by copy and setup")
	}
	if summary["step"] != StepDone || !strings.Contains(summary["next"].(string), "land") || summary["isolated"].(map[string]any)["branch"] != gate.Branch ||
		!slices.Equal(offered(summary), []string{"land"}) {
		t.Fatalf("a finished isolated task waits for the user's word: %v", summary)
	}
	if hook := StopHook(Current().Cwd, "fixture-session"); len(hook) != 0 {
		t.Fatalf("a finished task waiting to land does not hold the turn: %v", hook)
	}
	f.fails("start", "not landed yet", func() (any, error) { return Start(f.cfg, f.plan("-w another change"), "", false, RunInput{}) })

	landed := resultOf(f.land(""))
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

	// An unfinished isolated task cannot land, but it can be dropped and leaves no trace.
	gitOut(t, "checkout", "-q", "--", "a.txt")
	plan := f.plan("-l -w change it again")
	plan.Checks = nil
	f.start(plan)
	second := Current()
	f.fails("land", "only a finished task lands", func() (any, error) { return Land(f.cfg, Current(), LandInput{}) })
	if dropped := f.land("drop"); dropped["step"] != StepCancelled || fileText(t, "a.txt") != "before\n" {
		t.Fatalf("drop discards the work: %v", dropped)
	}
	if _, err := os.Stat(second.Worktree); err == nil {
		t.Fatal("a dropped task's worktree is removed")
	}
	f.start(f.plan("change the line"))
	f.fails("land", "not isolated", func() (any, error) { return Land(f.cfg, Current(), LandInput{}) })
}

func TestIsolatedSessionsCatchUpAndNeverConflictInTheCheckout(t *testing.T) {
	f := newFixture(t)
	identity(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "writer" {
			return "", nil
		}
		text := os.Getenv("AGENTGATE_SESSION") + "\n"
		if _, repairing := packet["merge_conflicts"]; repairing {
			text = "both changes\n"
		}
		for _, file := range packet["stream"].(map[string]any)["files"].([]any) {
			_ = os.WriteFile(file.(string), []byte(text), 0o644)
		}
		return `{"status":"COMPLETED","summary":"implemented","blockers":[],"handoff":"","deviations":[],"disputes":[]}`, nil
	}
	as := func(session string) { t.Setenv("AGENTGATE_SESSION", session) }
	other := Stream{Name: "core", Domain: "backend", Files: []string{"b.txt"}}

	// Three sessions start from the same commit; two of them change the same file.
	as("first")
	f.start(f.plan("-w first change"))
	as("second")
	f.start(f.plan("-w second change"))
	as("third")
	f.start(f.plan("-w third change", other))
	if strings.TrimSpace(gitOut(t, "status", "--porcelain")) != "" {
		t.Fatal("three finished tasks, and the checkout is still clean")
	}
	if all := PanelText(f.cfg, false, false, 120, 0); strings.Count(all, "ready to land") != 3 || !strings.Contains(all, "TASKS 3") {
		t.Fatalf("the panel lists every session's task waiting to land:\n%s", all)
	}

	as("first")
	if landed := resultOf(f.land("commit")); landed["commit"] == nil || fileText(t, "a.txt") != "first\n" || strings.TrimSpace(gitOut(t, "status", "--porcelain")) != "" {
		t.Fatalf("commit lands the task as a commit on the current branch: %v", landed)
	}

	// The third task touches other files: the checkout's new commit merges in cleanly, its checks run again, it lands.
	as("third")
	checks := len(Current().Checks)
	if landed := resultOf(f.land("apply")); landed["landed"] != "apply" || fileText(t, "b.txt") != "third\n" || fileText(t, "a.txt") != "first\n" {
		t.Fatalf("a task on other files lands on top of the new commit: %v", landed)
	}
	if len(Current().Checks) != checks+1 {
		t.Fatal("after catching up every declared check runs again")
	}

	// The second task changed the same line: the conflict stays in its worktree and goes back to its writer.
	as("second")
	reopened := f.land("apply")
	gate := Current()
	if reopened["landed"] != false || gate.Step != StepVerify || len(gate.Conflicts) != 1 || gate.Conflicts[0] != "a.txt" {
		t.Fatalf("a conflict reopens the task instead of landing: %v", reopened)
	}
	if fileText(t, "a.txt") != "first\n" || !strings.Contains(fileText(t, filepath.Join(gate.Worktree, "a.txt")), "<<<<<<<") {
		t.Fatal("conflict markers live in the worktree, never in the checkout")
	}
	if closed := f.proceed(); closed["step"] != StepDone || f.count("writer") != 4 {
		t.Fatalf("the conflict goes to the writer that owns the file, and the merged result is reviewed again: %v", closed)
	}
	gitOut(t, "stash", "-q", "--include-untracked") // the third task's uncommitted change is the user's to keep or drop
	if landed := resultOf(f.land("branch")); landed["branch"] != gate.Branch || fileText(t, "a.txt") != "first\n" {
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
	f.start(f.plan("-w change the line"))
	must(t, os.WriteFile("a.txt", []byte("the user's own edit\n"), 0o644))
	f.fails("land", "own uncommitted changes", func() (any, error) { return Land(f.cfg, Current(), LandInput{}) })
	if fileText(t, "a.txt") != "the user's own edit\n" || Current().Landed != "" {
		t.Fatal("a refused landing changes nothing and can be repeated")
	}
	gitOut(t, "checkout", "-q", "--", "a.txt")
	if landed := resultOf(f.land("apply")); landed["landed"] != "apply" || fileText(t, "a.txt") != "after\n" {
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
	must(t, os.MkdirAll(".agentgate/tools", 0o755))
	must(t, os.WriteFile(".agentgate/tools/check.sh", []byte("#!/bin/sh\ntest -n \"$1\"\n"), 0o755))
	must(t, os.WriteFile(".agentgate/config.json", []byte(`{"max_parallel": 2, "accounts": [{"name": "x", "provider": "claude", "home": "/x"}],
		"worktree": {"setup": [["sh", "-c", "echo \"$AGENTGATE_CHECKOUT\" > .origin"]]}}`), 0o644))

	linked, err := PlanLink(f.cfg, synced)
	must(t, err)
	target := filepath.Join(synced, "repo")
	if linked.(map[string]any)["target"] != target || fileText(t, filepath.Join(target, "plans", "the-change", "plan.json")) == "" {
		t.Fatalf("the directory moves into the synced folder under the repository's name: %v", linked)
	}
	if where, _ := os.Readlink(".agentgate"); where != target {
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
	must(t, json.Unmarshal([]byte(scenarioTable), cfg))
	f.cfg = cfg
	plan.Checks = []Check{{Argv: []string{".agentgate/tools/check.sh", "x"}}}
	if done := f.start(plan); done["step"] != StepDone {
		t.Fatalf("start: %v", done)
	}
	gate := Current()
	if len(gate.Foreign) != 0 || fileText(t, filepath.Join(gate.Worktree, ".origin")) != repo+"\n" {
		t.Fatalf("setup knows the checkout it was started from; the link is not a foreign change: %v", gate.Foreign)
	}
	if landed := resultOf(f.land("apply")); landed["landed"] != "apply" || len(landed["files"].([]any)) != 1 {
		t.Fatalf("only the task's file lands, not the link: %v", landed)
	}

	// A second computer: the synced folder already has the data, the checkout has none.
	must(t, os.Remove(".agentgate"))
	if _, err := PlanLink(cfg, ""); err != nil || fileText(t, ".agentgate/plans/the-change/brief.md") == "" {
		t.Fatalf("linking picks up what the other computer saved: %v", err)
	}
	must(t, os.Remove(".agentgate"))
	must(t, os.MkdirAll(".agentgate/plans", 0o755))
	if _, err := PlanLink(cfg, ""); err == nil || !strings.Contains(err.Error(), "merge them by hand") {
		t.Fatalf("two copies are never merged silently: %v", err)
	}
}

func TestWorkerToolsGoToTheirRolesAndIndexesAreRefreshedOnce(t *testing.T) {
	f := newFixture(t)
	refreshed = sync.Map{}
	marker := filepath.Join(t.TempDir(), "refreshed")
	f.cfg.WorkerMCP = map[string]config.WorkerMCP{
		"graph": {Command: "graph-mcp", Tools: []string{"query"}, Roles: []string{"scout", "reviewer"}, Hint: "who calls what",
			Refresh: []string{"sh", "-c", "echo x >> " + marker}, RefreshTimeoutS: 30},
		"docs": {Command: "docs-mcp", Writable: []string{"cache-dir", "missing-dir"}},
	}
	must(t, os.MkdirAll("cache-dir", 0o755))
	plan := f.plan("-m change the line")
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind == "prepare" {
			return prepared("READY", "one line", plan, nil, nil), nil
		}
		return "", nil
	}
	f.prepare(PrepareInput{Request: plan.Request, Name: "tools"})
	f.start(plan)
	seen := map[string]string{}
	for _, call := range f.calls {
		var names []string
		for name := range call.MCP {
			names = append(names, name)
		}
		sort.Strings(names)
		kind := kindOf(call)
		seen[kind] = strings.Join(names, "+")
		told := strings.Contains(call.Prompt, "MCP server `graph` (tools: query): who calls what")
		if told != (call.MCP["graph"].Command != "") {
			t.Errorf("%s: a worker is told about exactly the tools it has", kind)
		}
	}
	for _, call := range f.calls {
		if kindOf(call) == "prepare" && (len(call.Writable) != 1 || filepath.Base(call.Writable[0]) != "cache-dir") {
			t.Fatalf("a server's cache directory is opened for writing when it exists: %v", call.Writable)
		}
	}
	if seen["prepare"] != "docs+graph" || seen["writer"] != "" || seen["review"] != "graph" {
		t.Fatalf("each role gets its servers; one without roles belongs to discovery: %v", seen)
	}
	if raw, _ := os.ReadFile(marker); strings.Count(string(raw), "x") != 2 {
		t.Fatalf("an index is refreshed once per operation, before its first reader: %q", raw)
	}
}

func TestCodexLeadIsInstalledChosenAndRemoved(t *testing.T) {
	newFixture(t)
	home, _ := os.UserHomeDir()
	claude := filepath.Join(home, ".claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	must(t, os.MkdirAll(codexDir(), 0o700))
	must(t, os.WriteFile(filepath.Join(codexDir(), "hooks.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"other-tool stop"}]}]}}`), 0o600))

	asked := 0
	first, err := Install(InstallOptions{Ask: func() string { asked++; return "codex" }})
	must(t, err)
	cfg, err := config.Load()
	must(t, err)
	if asked != 1 || strings.Join(cfg.Leads, ",") != "codex" || cfg.Profile != "codex" {
		t.Fatalf("the first install asks, remembers the answer and selects the codex profile: %+v %v", cfg.Leads, first)
	}
	skill, _ := os.ReadFile(filepath.Join(home, ".agents", "skills", "dev", "SKILL.md"))
	policy, _ := os.ReadFile(filepath.Join(home, ".agents", "skills", "dev", "agents", "openai.yaml"))
	rule, _ := os.ReadFile(filepath.Join(codexDir(), "rules", "agentgate.rules"))
	hooks, _ := os.ReadFile(filepath.Join(codexDir(), "hooks.json"))
	if !strings.Contains(string(skill), "name: dev") || !strings.Contains(string(policy), "allow_implicit_invocation: false") ||
		!strings.Contains(string(rule), `prefix_rule(pattern=["agentgate"], decision="allow")`) ||
		strings.Count(string(hooks), " hook\"") != 2 || !strings.Contains(string(hooks), "UserPromptSubmit") || !strings.Contains(string(hooks), "other-tool stop") {
		t.Fatalf("codex gets the skill, the rule and both hooks next to its own hooks:\n%s\n%s\n%s", skill, rule, hooks)
	}
	if _, err := os.Stat(filepath.Join(claude, "commands", "dev.md")); err == nil {
		t.Fatal("a codex-only install puts nothing into Claude Code")
	}
	if !strings.Contains(first.(map[string]any)["next"].(string), "/hooks") {
		t.Fatalf("the user is told to trust the hook: %v", first)
	}
	for _, mode := range cfg.Modes {
		for _, spec := range append([]string{mode.Backend, mode.Frontend, mode.Scout}, mode.Architecture...) {
			if spec != "" && cfg.MustRoute(spec).Provider != "codex" {
				t.Fatalf("in the codex profile every worker is a Codex model, got %s", spec)
			}
		}
		for _, lane := range mode.Review {
			if route := cfg.MustRoute(lane.Route); route.Provider != "codex" || route.Model == cfg.MustRoute(mode.Backend).Model {
				t.Fatalf("a reviewer is a Codex model other than the writer's: %s", lane.Route)
			}
		}
	}
	if _, known := cfg.Models["opus"]; known || len(cfg.Fallbacks["sol"]) == 0 || cfg.Fallbacks["sol"][0] != "astra" {
		t.Fatalf("the profile removes the Claude models and their fallbacks: %v", cfg.Fallbacks)
	}

	// Later installs (an update runs one) ask nothing and change nothing.
	again, err := Install(InstallOptions{Ask: func() string { asked++; return "claude" }})
	must(t, err)
	if asked != 1 || again.(map[string]any)["note"] != "already up to date" {
		t.Fatalf("an install repeats the remembered choice: %v", again)
	}
	doctor := Doctor()
	names := map[string]bool{}
	for _, item := range doctor["checks"].([]probe) {
		names[item.Name] = item.OK
		if item.Name == "claude" && item.Required {
			t.Fatal("a codex-only setup does not need the Claude CLI")
		}
	}
	if !names["$dev skill"] || !names["Codex rule"] || names["/dev command"] {
		t.Fatalf("doctor checks what this lead needs: %v", names)
	}

	// Both leads: Claude Code gets its command too, and the workers go back to the default table.
	_, err = Install(InstallOptions{Lead: "both"})
	must(t, err)
	cfg, _ = config.Load()
	if _, err := os.Stat(filepath.Join(claude, "commands", "dev.md")); err != nil || cfg.Profile != "" || len(cfg.Leads) != 2 {
		t.Fatalf("both leads are installed with the default workers: %+v %q", cfg.Leads, cfg.Profile)
	}
	if _, err := Install(InstallOptions{Lead: "gemini"}); err == nil {
		t.Fatal("an unknown lead is refused")
	}
	_, err = Install(InstallOptions{Uninstall: true})
	must(t, err)
	hooks, _ = os.ReadFile(filepath.Join(codexDir(), "hooks.json"))
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "dev")); err == nil || strings.Contains(string(hooks), selfPath()+" hook") ||
		!strings.Contains(string(hooks), "other-tool stop") {
		t.Fatalf("uninstall removes the skill, the rule and our hook, and nothing else: %s", hooks)
	}
	if _, err := os.Stat(codexRulePath()); err == nil {
		t.Fatal("uninstall removes the rule")
	}
}

func TestTheLeadIsTheSessionThatCalls(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AGENTGATE_SESSION", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-1")
	if config.Lead() != "claude" || session() != "claude-1" {
		t.Fatalf("a Claude Code session leads: %s %s", config.Lead(), session())
	}
	// A Codex tool call wins over whatever the environment inherited from a terminal.
	t.Setenv("CODEX_THREAD_ID", "codex-1")
	t.Setenv("CODEX_TOOL_CALL_ID", "call-1")
	if config.Lead() != "codex" || session() != "codex-1" {
		t.Fatalf("inside a Codex tool call the Codex thread leads: %s %s", config.Lead(), session())
	}
	f.waiting("change the line")
	if gate := Current(); gate == nil || gate.Session != "codex-1" {
		t.Fatal("the task belongs to the Codex thread")
	}
	if hook := StopHook(Current().Cwd, "codex-1"); hook["decision"] != "block" {
		t.Fatalf("the Stop hook holds the Codex thread to its unfinished task: %v", hook)
	}
	t.Setenv("CODEX_TOOL_CALL_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	if config.Lead() != "codex" {
		t.Fatal("a Codex thread without a Claude session is a Codex lead")
	}
	if Confined() {
		t.Fatal("a process that can write its state is not confined, whatever variables Codex set")
	}
	locked := filepath.Join(t.TempDir(), "locked")
	must(t, os.MkdirAll(locked, 0o500))
	previous := Root
	Root = func() string { return filepath.Join(locked, "state") }
	defer func() { Root = previous }()
	if !Confined() {
		t.Fatal("a process that cannot write its state is inside someone's sandbox")
	}
}

func TestWhatTheOldNameLeftIsAdopted(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	must(t, os.MkdirAll(filepath.Join(root, "state", "claude-dev", "abc"), 0o700))
	must(t, os.MkdirAll(filepath.Join(root, "config", "claude-dev"), 0o700))
	must(t, os.WriteFile(filepath.Join(root, "config", "claude-dev", "config.json"), []byte(`{"max_parallel": 3}`), 0o600))
	AdoptLegacy()
	AdoptLegacy()
	if target, _ := os.Readlink(filepath.Join(root, "state", "claude-dev")); target != filepath.Join(root, "state", "agentgate") {
		t.Fatalf("the state moves and a link stays at the old place: %q", target)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "claude-dev", "abc")); err != nil {
		t.Fatal("paths under the old name still resolve")
	}
	t.Chdir(root)
	cfg, err := config.Load()
	if err != nil || cfg.MaxParallel != 3 {
		t.Fatalf("the settings are read from their new place: %v %v", cfg, err)
	}
	// A repository set up under the old name keeps its plans and its settings file.
	must(t, os.MkdirAll(".claude-dev/plans/old-plan", 0o755))
	must(t, os.WriteFile(".claude-dev/plans/old-plan/plan.json", []byte(`{"request":"x","brief":"b","streams":[]}`), 0o644))
	must(t, os.WriteFile(".claude-dev.json", []byte(`{"max_parallel": 5}`), 0o644))
	cfg, err = config.Load()
	listed, _ := PlanList()
	if err != nil || cfg.MaxParallel != 5 || config.RepoDir() != ".claude-dev" || !strings.Contains(fmt.Sprint(listed), "old-plan") {
		t.Fatalf("old repository files are read: %v %v", cfg, listed)
	}
	if !ownNotes(".claude-dev/plans/x") || !ownNotes(".agentgate") || ownNotes("src/.agentgate.go") {
		t.Fatal("the tool's own directory is never part of a change, under either name")
	}
	must(t, os.MkdirAll(".agentgate", 0o755))
	if config.RepoDir() != ".agentgate" {
		t.Fatal("the new name wins once it exists")
	}
}
