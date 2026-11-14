package dev

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/provider"
)

// changeQuality keeps the ordinary review intact while scripting its acceptance judgement.
func changeQuality(f *fixture, change func(*QualityReport)) {
	f.raw = true
	f.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		if kind != "review" || packet["quality"] == nil {
			return "", nil
		}
		var answer map[string]any
		_ = json.Unmarshal([]byte(f.defaultAnswer(kind, packet)), &answer)
		report := fixtureQuality(packet)
		change(report)
		answer["quality"] = report
		raw, _ := json.Marshal(answer)
		return string(raw), nil
	}
}

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
				t.Fatalf("%s did not receive the current project policy", kind)
			}
		}
		return "", nil
	}
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	plan.Architecture = &Accepted{ID: receipt["id"].(string), Acceptance: "Use the project components"}
	write(t, path, "Use the existing field component and its read-only variant.")
	f.fails("launch", "project rules changed", func() (any, error) { return Launch(f.cfg, plan) })
	if f.count("writer") != 0 {
		t.Fatal("a writer started under stale architectural policy")
	}
	receipt = f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	if f.count("architecture") != 4 {
		t.Fatal("the changed policy reused old architecture work")
	}
	plan.Architecture.ID = receipt["id"].(string)
	f.launch(plan)
	f.review()
	f.finish()
}

func TestQualityRequirementsBlockCompletionAndCanBeReconsidered(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m implement the criterion")
	plan.Requirements = []Requirement{{ID: "R1", Text: "the changed line is verified", Method: "check"}}
	plan.Checks[0].Kind = "test"
	f.launch(plan)
	changeQuality(f, func(report *QualityReport) {
		report.Requirements[0].Status = "partial"
		report.Requirements[0].Reason = "the command does not exercise the required behavior"
	})
	result := f.review()
	if !strings.Contains(result["next"].(string), "Quality criteria") {
		t.Fatal("a clean code review must still expose an unproven criterion")
	}
	f.fails("finish", "R1: partial", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "done"}) })
	calls := f.count("review")
	f.review()
	if f.count("review") != calls {
		t.Fatal("unchanged evidence should reuse its incomplete verdict")
	}
	f.model = nil
	f.do("review", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{Recheck: true}) })
	if f.count("review") <= calls {
		t.Fatal("an explicit reconsideration must ask the reviewer again")
	}
	quality := Current().qualityStatus(f.cfg)
	if quality["current"] != true || len(quality["problems"].([]string)) != 0 ||
		quality["checks"].(map[string]qualityCheck)[checkID(plan.Checks[0].Argv)].Kind != "test" {
		t.Fatalf("typed evidence missing: %+v", quality)
	}
	// A new receipt on identical code still requires assessment of that new evidence.
	f.do("check", func() (any, error) { return RunChecks(Current(), CheckInput{All: true}) })
	f.fails("finish", "current review", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "done"}) })
	f.review()
	f.finish()
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
			f.launch(plan)
			changeQuality(f, tc.change)
			f.fails("review", tc.want, func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{}) })
			if Current().Phase != "awaiting-review" || len(Current().Reviews) != 0 {
				t.Fatal("invalid evidence must not leave an accepted review")
			}
		})
	}
	// A grandfathered failure can unblock ordinary checks, but cannot prove a criterion.
	t.Run("preexisting failure", func(t *testing.T) {
		f := newFixture(t)
		plan := f.plan("-m preserve existing behavior")
		plan.Checks = []Check{{Argv: []string{"false"}, Baseline: true}}
		plan.Requirements = []Requirement{{ID: "R1", Text: "behavior works", Method: "check"}}
		f.launch(plan)
		changeQuality(f, func(r *QualityReport) {
			r.Requirements[0].Status = "verified"
			r.Requirements[0].Evidence = []Proof{{Kind: "check", Ref: checkID([]string{"false"}), Detail: "claimed success"}}
		})
		f.fails("review", "current successful receipt", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{}) })
	})
}

func TestQualityRulesAreScopedRequiredAndBoundToTheirContents(t *testing.T) {
	f := newFixture(t)
	path := ".agentgate/rules/contracts.md"
	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	f.cfg.ReviewRules = map[string]config.ReviewRule{
		"contracts": {Instructions: []string{path}, Files: []string{"a.*"}, Mode: "blocking"},
		"optional":  {Instructions: []string{"missing-advice.md"}, Mode: "advisory"},
		"elsewhere": {Instructions: []string{"missing-required.md"}, Files: []string{"src/**"}, Mode: "blocking"},
	}
	plan := f.plan("-m preserve the contracts")
	f.fails("launch", "contracts", func() (any, error) { return Launch(f.cfg, plan) })
	if f.count("writer") != 0 || Current() != nil {
		t.Fatal("unavailable required rules must be caught before starting work")
	}
	write(t, path, "Preserve the line contract.\n")
	var writerSawRules bool
	f.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		if kind == "writer" {
			raw, _ := json.Marshal(packet["review_rules"])
			writerSawRules = strings.Contains(string(raw), "Preserve the line contract.") && !strings.Contains(string(raw), "elsewhere")
		}
		return "", nil
	}
	f.launch(plan)
	f.review()
	if !writerSawRules || len(Current().Reviews[0].Lanes[0].Quality.Rules) != 2 {
		t.Fatal("writers and the quality reviewer must see applicable instruction snapshots")
	}
	if problems := Current().qualityStatus(f.cfg)["problems"].([]string); len(problems) != 0 {
		t.Fatalf("unavailable advisory rules must be visible without blocking: %v", problems)
	}
	write(t, path, "Preserve the line contract and its delimiter.\n")
	f.fails("finish", "current review", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "done"}) })
	f.review()
	must(t, os.Remove(path))
	f.fails("finish", "contracts", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "done"}) })
	f.fails("review", "contracts", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{}) })
	write(t, path, "Preserve the line contract and its delimiter.\n")
	changeQuality(f, func(report *QualityReport) { report.Rules[0].Status = "block" })
	f.do("review", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{Recheck: true}) })
	f.fails("finish", "contracts: block", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "done"}) })
	// Removing a policy changes the contract too; its old review cannot be reused.
	f.cfg.ReviewRules = nil
	f.fails("finish", "current review", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "done"}) })
	f.model = nil
	f.review()
	f.finish()
}

func TestQualityRejectsRulesChangedWhileReviewing(t *testing.T) {
	f := newFixture(t)
	path := ".agentgate/rules/contracts.md"
	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	write(t, path, "Keep the line.\n")
	f.cfg.ReviewRules = map[string]config.ReviewRule{"contracts": {Instructions: []string{path}}}
	f.launch(f.plan("-m change the line"))
	f.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		if kind == "review" && packet["quality"] != nil {
			write(t, path, "Keep both lines.\n")
		}
		return "", nil
	}
	f.fails("review", "changed during the review", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{}) })
	f.model = nil
	f.review()
	f.finish()
}

func TestQualityBrowserEvidencePrecedesItsReviewAndCannotBeSkipped(t *testing.T) {
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
	f.launch(plan)
	f.review()
	f.fails("finish", "R1: missing", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "done"}) })
	stand := VisualInput{URL: "http://127.0.0.1:4200", Origins: []string{"http://127.0.0.1:4200"}}
	f.do("review", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{Visual: &stand}) })
	last := Current().Reviews[len(Current().Reviews)-1]
	if last.Lanes[0].Quality.Requirements[0].Evidence[0].Kind != "browser" {
		t.Fatal("the reviewer must receive the observation from the same review call")
	}
	f.do("visual", func() (any, error) {
		return VisualPass(f.cfg, Current(), VisualInput{SkipReason: "stand no longer available"})
	})
	f.fails("finish", "current review", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "done"}) })
	f.review()
	f.fails("finish", "R1: missing", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "done"}) })
}

func TestQualityLightReviewRequiresTheSameEvidence(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-l change the line")
	plan.Requirements = []Requirement{{ID: "R1", Text: "the line changed", Method: "code"}}
	f.launch(plan)
	result := f.review()
	if result["quality"] == nil {
		t.Fatal("the lead needs the same quality packet as a worker reviewer")
	}
	f.fails("review", "exactly once", func() (any, error) {
		return ReviewTask(f.cfg, Current(), ReviewInput{LeadReview: &LeadReview{Summary: "read"}})
	})
	report := &QualityReport{Requirements: []RequirementResult{{ID: "R1", Status: "verified", Reason: "inspected the line",
		Evidence: []Proof{{Kind: "code", Ref: "a.txt:1", Detail: "line is after"}}}}}
	f.do("review", func() (any, error) {
		return ReviewTask(f.cfg, Current(), ReviewInput{LeadReview: &LeadReview{Summary: "read", Quality: report}})
	})
	f.finish()
}
