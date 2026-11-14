package dev

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/provider"
)

func TestProjectRulesReachArchitectureAndInvalidateItsAcceptance(t *testing.T) {
	f := newFixture(t)
	path := "ui-policy.md"
	write(t, path, "Use the existing field component.")
	f.cfg.ReviewRules = map[string]config.ReviewRule{"ui": {Instructions: []string{path}, Files: []string{"a.*"}}}
	plan := f.plan("-h change the UI contract")
	f.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		if kind == "architecture" || kind == "writer" {
			raw, _ := json.Marshal(packet["review_rules"])
			if !strings.Contains(string(raw), fileText(t, path)) {
				t.Errorf("%s did not receive the current project policy", kind)
			}
		}
		return "", nil
	}
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	plan.Architecture = &Accepted{ID: receipt["id"].(string), Acceptance: "Use the project components"}
	write(t, path, "Use the existing field component and its read-only variant.")
	f.fails("start", "project rules changed", func() (any, error) { return Start(f.cfg, plan, "", false, RunInput{}) })
	if f.count("writer") != 0 {
		t.Fatal("a writer started under stale architectural policy")
	}
	receipt = f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	if f.count("architecture") != 4 {
		t.Fatal("the changed policy reused old architecture work")
	}
	plan.Architecture.ID = receipt["id"].(string)
	if done := f.start(plan); done["step"] != StepDone {
		t.Fatalf("start: %v", done)
	}
}

func TestAnUnprovenRequirementKeepsTheTaskOpenAndIsReassessedCheaply(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m implement the criterion")
	plan.Requirements = []Requirement{{ID: "R1", Text: "the changed line is verified", Method: "check"}}
	plan.Checks[0].Kind = "test"
	changeQuality(f, func(report *QualityReport) {
		report.Requirements[0].Status = "partial"
		report.Requirements[0].Reason = "the command does not exercise the required behavior"
	})
	// As a live reviewer did: it finds no defect in the code and still marks its axis as owing something.
	verdict := f.model
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		answer, err := verdict(kind, packet, call)
		var parsed map[string]any
		if json.Unmarshal([]byte(answer), &parsed) == nil {
			if reports, _ := parsed["reports"].([]any); len(reports) > 0 {
				reports[0].(map[string]any)["status"] = "NEEDS_FIX"
				raw, _ := json.Marshal(parsed)
				answer = string(raw)
			}
		}
		return answer, err
	}
	result := f.start(plan)
	need := result["needs"].([]any)[0].(map[string]any)
	if result["step"] != StepDecide || need["kind"] != "requirement" || need["id"] != "R1" || !strings.Contains(need["what"].(string), "does not exercise") {
		t.Fatalf("a clean code review must still expose an unproven criterion: %v", result)
	}
	calls := f.count("review")
	if calls != 1 || Current().Reviews[0].Lanes[0].Reports[0].Status != "NEEDS_FIX" {
		t.Fatalf("an axis that owes something because a criterion is unmet is a usable report, not a reason to pay for another review: %d calls", calls)
	}
	f.proceed()
	if f.count("review") != calls {
		t.Fatal("unchanged evidence keeps its incomplete verdict: nobody is paid to repeat it")
	}
	// A new receipt on identical code is new evidence: the reviewer judges it, without reading the code again.
	f.do("check", func() (any, error) { return RunChecks(Current(), CheckInput{All: true}) })
	f.proceed()
	last := f.calls[len(f.calls)-1]
	if f.count("review") != calls+1 || last.Resume == "" || !strings.Contains(last.Prompt, "do not review it again") {
		t.Fatalf("fresh evidence is reassessed in the reviewer's own session: %s", last.Prompt)
	}
	if lane := Current().Reviews[len(Current().Reviews)-1].Lanes[0]; lane.Quality.Requirements[0].Status != "partial" || len(lane.Reports) != 3 {
		t.Fatalf("a reassessment replaces the verdict on the criteria and keeps the code review: %+v", lane)
	}
	// The lead may ask for a fresh judgement of the same evidence; that is a real review.
	f.raw, f.model = false, nil
	done := f.do("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{Recheck: true}) })
	if done["step"] != StepDone || f.count("review") != calls+2 {
		t.Fatalf("an explicit reconsideration asks the reviewer again: %v", done)
	}
	gate := Current()
	proof := gate.Reviews[len(gate.Reviews)-1].Lanes[0].Quality.Requirements[0].Evidence[0]
	if proof.Kind != "check" || proof.Ref != checkID(plan.Checks[0].Argv) {
		t.Fatalf("the requirement is verified by the declared kind of proof: %+v", proof)
	}
}

func TestAnUnmetRequirementCanBeSentToTheWritersAsWork(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m implement the criterion")
	plan.Requirements = []Requirement{{ID: "R1", Text: "the line is corrected"}}
	changeQuality(f, func(report *QualityReport) {
		if fileText(t, "a.txt") != "corrected\n" {
			report.Requirements[0].Status, report.Requirements[0].Reason = "missing", "the line still holds the old value"
		}
	})
	reviewer := f.model
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && packet["correction"] != nil {
			findings, _ := json.Marshal(packet["correction"].(map[string]any)["findings"])
			if !strings.Contains(string(findings), "the line still holds the old value") || !strings.Contains(string(findings), "Use the corrected value") {
				t.Errorf("the writer is told which criterion is unmet and what the lead asks: %s", findings)
			}
			write(t, "a.txt", "corrected\n")
			return `{"status":"COMPLETED","summary":"corrected the line","blockers":[],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return reviewer(kind, packet, call)
	}
	f.start(plan)
	f.fails("decide", "is not an unmet requirement", func() (any, error) {
		return Decide(f.cfg, Current(), DecideInput{Requirements: []CriterionDecision{{ID: "R9", Action: "fix"}}})
	})
	done := f.decide(DecideInput{Requirements: []CriterionDecision{{ID: "R1", Action: "fix", Note: "Use the corrected value"}}})
	if done["step"] != StepDone || f.count("writer") != 2 {
		t.Fatalf("missing code goes to the writer and the criterion is judged again: %v", done)
	}
}

func TestQualityRejectsMissingDuplicateAndInventedProof(t *testing.T) {
	cases := []struct {
		name, want string
		change     func(*QualityReport)
	}{
		{"omitted", "exactly once", func(r *QualityReport) { r.Requirements = nil }},
		{"duplicate", "unique id", func(r *QualityReport) { r.Requirements[1] = r.Requirements[0] }},
		{"invented receipt", "current successful receipt", func(r *QualityReport) { r.Requirements[0].Evidence[0].Ref = "not-a-check" }},
		{"wrong method", "declared method", func(r *QualityReport) {
			r.Requirements[0].Evidence = []Proof{{Kind: "code", Ref: "a.txt:1", Detail: "this is not an executed check"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			plan := f.plan("-m implement criteria")
			plan.Requirements = []Requirement{{ID: "R1", Text: "first behavior", Method: "check"}, {ID: "R2", Text: "second behavior"}}
			changeQuality(f, tc.change)
			f.fails("start", tc.want, func() (any, error) { return Start(f.cfg, plan, "", false, RunInput{}) })
			if gate := Current(); gate.Step != StepVerify || len(gate.Reviews) != 0 {
				t.Fatal("invalid evidence must not leave an accepted review")
			}
		})
	}
	// A code reference names one line or a range of lines in a file of the scope, and every problem of a report
	// is named at once, so the lead or the reviewer corrects them in one round.
	t.Run("code references and every problem at once", func(t *testing.T) {
		f := newFixture(t)
		f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
			if kind != "writer" {
				return "", nil
			}
			write(t, "a.txt", "one\ntwo\nthree\n")
			return `{"status":"COMPLETED","summary":"wrote three lines","blockers":[],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		plan := f.plan("-l implement criteria")
		plan.Checks = nil
		plan.Requirements = []Requirement{{ID: "R1", Text: "first behavior", Method: "code"}, {ID: "R2", Text: "second behavior", Method: "code"}}
		f.start(plan)
		gate := Current()
		input, err := gate.qualityInputs(f.cfg)
		must(t, err)
		proof := func(ref string) []Proof { return []Proof{{Kind: "code", Ref: ref, Detail: "read it"}} }
		report := func(first, second string) *QualityReport {
			return &QualityReport{Rules: []RuleResult{}, Requirements: []RequirementResult{
				{ID: "R1", Status: "verified", Reason: "read", Evidence: proof(first)},
				{ID: "R2", Status: "verified", Reason: "read", Evidence: proof(second)}}}
		}
		must(t, gate.validateQuality(report("a.txt:2", "a.txt:1-3"), input))
		err = gate.validateQuality(report("a.txt:3-1", "a.txt:2-9"), input)
		if err == nil || !strings.Contains(err.Error(), `"a.txt:3-1" must be path:line or path:first-last`) || !strings.Contains(err.Error(), `"a.txt:2-9" cannot be read`) {
			t.Fatalf("a reversed range and a range past the end are both named in one answer: %v", err)
		}
		bad := Finding{Severity: "P9", Confidence: "high", Path: "b.txt", Line: 0, Evidence: "e", Remedy: "r"}
		err = gate.read(f.cfg, LeadReview{Summary: "read", Findings: []Finding{bad}, Quality: report("a.txt:1", "elsewhere.txt:1")}, map[string]Finding{})
		for _, want := range []string{"severity is one of", "path must be a file of the change's scope", "line is one line number", "impact is empty", `"elsewhere.txt:1"`} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("the lead's review is refused with every problem of findings and criteria at once, missing %q: %v", want, err)
			}
		}
	})
	// A failure that existed before the task still blocks, and a failing command proves nothing.
	t.Run("preexisting failure", func(t *testing.T) {
		f := newFixture(t)
		plan := f.plan("-m preserve existing behavior")
		plan.Checks = []Check{{Argv: []string{"false"}, Baseline: true}}
		plan.Requirements = []Requirement{{ID: "R1", Text: "behavior works", Method: "check"}}
		f.start(plan)
		gate := Current()
		if f.count("review") != 0 || len(gate.failing()) != 1 {
			t.Fatal("a failure from before the task was treated as successful verification")
		}
		input, err := gate.qualityInputs(f.cfg)
		must(t, err)
		claimed := &QualityReport{Rules: []RuleResult{}, Requirements: []RequirementResult{{ID: "R1", Status: "verified", Reason: "claimed success",
			Evidence: []Proof{{Kind: "check", Ref: checkID([]string{"false"}), Detail: "claimed success"}}}}}
		if err := gate.validateQuality(claimed, input); err == nil || !strings.Contains(err.Error(), "current successful receipt") {
			t.Fatalf("a failing check cannot verify a requirement: %v", err)
		}
	})
}

func TestQualityRulesAreScopedRequiredAndBoundToTheirContents(t *testing.T) {
	f := newFixture(t)
	path := ".agentgate/rules/contracts.md"
	f.cfg.ReviewRules = map[string]config.ReviewRule{
		"contracts": {Instructions: []string{path}, Files: []string{"a.*"}, Mode: "blocking"},
		"optional":  {Instructions: []string{"missing-advice.md"}, Mode: "advisory"},
		"elsewhere": {Instructions: []string{"missing-required.md"}, Files: []string{"src/**"}, Mode: "blocking"},
	}
	plan := f.plan("-m preserve the contracts")
	f.fails("start", "contracts", func() (any, error) { return Start(f.cfg, plan, "", false, RunInput{}) })
	if f.count("writer") != 0 || Current() != nil {
		t.Fatal("unavailable required rules must be caught before starting work")
	}
	write(t, path, "Preserve the line contract.\n")
	changeQuality(f, func(report *QualityReport) {
		report.Rules[0].Status, report.Rules[0].Evidence = "block", "the delimiter was dropped"
	})
	reviewer := f.model
	var writerSawRules bool
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" {
			raw, _ := json.Marshal(packet["review_rules"])
			writerSawRules = strings.Contains(string(raw), "Preserve the line contract.") && !strings.Contains(string(raw), "elsewhere")
			return "", nil
		}
		return reviewer(kind, packet, call)
	}
	stopped := f.start(plan)
	rules := Current().Reviews[0].Lanes[0].Quality.Rules
	if !writerSawRules || len(rules) != 2 {
		t.Fatal("writers and the reviewer must see the applicable instruction snapshots")
	}
	if !slices.Equal(needs(stopped), []string{"rule"}) || stopped["needs"].([]any)[0].(map[string]any)["id"] != "contracts" {
		t.Fatalf("a blocking rule that is not satisfied keeps the task open; an unavailable advisory one does not: %v", stopped)
	}
	// The rule's own text is part of what was judged.
	reviews := f.count("review")
	write(t, path, "Preserve the line contract and its delimiter.\n")
	f.proceed()
	if f.count("review") != reviews+1 || !strings.Contains(f.calls[len(f.calls)-1].Prompt, "do not review it again") {
		t.Fatal("a changed instruction file is judged again, by the reviewer that read the code")
	}
	must(t, exec.Command("rm", path).Run())
	f.fails("run", "contracts", func() (any, error) { return Run(f.cfg, Current(), RunInput{}) })
	write(t, path, "Preserve the line contract and its delimiter.\n")
	// Removing a rule leaves nothing to judge; the code review stands.
	f.cfg.ReviewRules = nil
	f.raw, f.model = false, nil
	if done := f.proceed(); done["step"] != StepDone || f.count("review") != reviews+1 {
		t.Fatalf("a task without criteria needs no further judgement: %v", done)
	}
}

func TestQualityRejectsRulesChangedWhileReviewing(t *testing.T) {
	f := newFixture(t)
	path := ".agentgate/rules/contracts.md"
	write(t, path, "Keep the line.\n")
	f.cfg.ReviewRules = map[string]config.ReviewRule{"contracts": {Instructions: []string{path}}}
	f.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		if kind == "review" && packet["quality"] != nil {
			write(t, path, "Keep both lines.\n")
		}
		return "", nil
	}
	f.fails("start", "changed during the review", func() (any, error) { return Start(f.cfg, f.plan("-m change the line"), "", false, RunInput{}) })
	f.model = nil
	if done := f.proceed(); done["step"] != StepDone {
		t.Fatalf("the next run judges the rule as it is now: %v", done)
	}
}

func TestBrowserEvidenceIsObservedBeforeItIsJudgedAndCannotBeAssumed(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	f := newFixture(t)
	tool := filepath.Join(t.TempDir(), "cli.js")
	write(t, tool, "")
	f.cfg.PlaywrightMCP = tool
	plan := f.plan("-m inspect behavior")
	plan.Requirements = []Requirement{{ID: "R1", Text: "the result is observed", Method: "browser"}}
	plan.UI = &UI{Summary: "result", Criteria: []string{"visible"}, Preserve: []string{"other states"}}
	plan.Visual = &Visual{Enabled: true, Scenarios: []Scenario{
		{ID: "result", Screen: "List", State: "loaded", Viewport: "1440x900", Role: "user", Expected: "result visible"}}}
	waiting := f.start(plan)
	if waiting["step"] != StepDecide || !slices.Equal(needs(waiting), []string{"scenarios"}) {
		t.Fatalf("opted-in scenarios that were not observed keep the task open: %v", waiting)
	}
	// Recording that the stand is unavailable does not prove what only the browser can show.
	f.do("visual", func() (any, error) {
		return VisualPass(f.cfg, Current(), VisualInput{SkipReason: "stand not available"})
	})
	if skipped := f.proceed(); !slices.Equal(needs(skipped), []string{"requirement"}) {
		t.Fatalf("a skipped browser pass leaves the browser requirement unproven: %v", skipped)
	}
	f.do("visual", func() (any, error) { return VisualPass(f.cfg, Current(), VisualInput{Choice: plan.Visual}) })
	stand := VisualInput{URL: "http://127.0.0.1:4200", Origins: []string{"http://127.0.0.1:4200"}}
	done := f.do("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{Visual: &stand}) })
	gate := Current()
	last := gate.Reviews[len(gate.Reviews)-1]
	if done["step"] != StepDone || last.Lanes[0].Quality.Requirements[0].Evidence[0].Kind != "browser" {
		t.Fatalf("the reviewer judges the observation made in the same run: %v", done)
	}
}

func TestQualityLightReviewRequiresTheSameEvidence(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-l change the line")
	plan.Requirements = []Requirement{{ID: "R1", Text: "the line changed", Method: "code"}}
	result := f.start(plan)
	need := result["needs"].([]any)[0].(map[string]any)
	packet := fileText(t, need["detail"].(map[string]any)["packet"].(string))
	if !strings.Contains(packet, `"quality"`) || !strings.Contains(packet, `"R1"`) {
		t.Fatal("the lead needs the same criteria as a worker reviewer")
	}
	f.fails("decide", "exactly once", func() (any, error) {
		return Decide(f.cfg, Current(), DecideInput{Review: &LeadReview{Summary: "read"}})
	})
	report := &QualityReport{Requirements: []RequirementResult{{ID: "R1", Status: "verified", Reason: "inspected the line",
		Evidence: []Proof{{Kind: "code", Ref: "a.txt:1", Detail: "line is after"}}}}}
	if done := f.decide(DecideInput{Review: &LeadReview{Summary: "read", Quality: report}}); done["step"] != StepDone {
		t.Fatalf("the lead's review with its evidence completes the task: %v", done)
	}
}
