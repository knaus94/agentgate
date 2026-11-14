package dev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/provider"
)

func TestPreparationDraftsWithoutWritingAndReusesDiscovery(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-l the worker must not change the requested mode")
	policy := ".agents/skills/client-ui/SKILL.md"
	must(t, os.MkdirAll(filepath.Dir(policy), 0o755))
	write(t, policy, "Use the project's field components.")
	f.cfg.ReviewRules = map[string]config.ReviewRule{
		"ui":        {Instructions: []string{policy}, Files: []string{"a.*"}},
		"unrelated": {Instructions: []string{"missing-backend.md"}, Files: []string{"server/**"}},
	}
	f.cfg.ReviewRuleSources = map[string][]string{"ui": {".agentgate.json"}}
	f.cfg.Design.Stand = json.RawMessage(`{"command":["yarn","dev"],"ready":"http://127.0.0.1:4200","cookies":[{"value":"private-cookie"}],"env":{"TOKEN":"private-value"}}`)
	var attempts int
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "prepare" {
			return "", nil
		}
		attempts++
		if call.Writer || (attempts == 2 && call.Resume == "") {
			t.Fatal("preparation must read only and repair its draft in the same session")
		}
		raw, _ := json.Marshal(packet)
		if strings.Contains(string(raw), "private-cookie") || strings.Contains(string(raw), "private-value") {
			t.Fatal("stand credentials reached the preparation worker")
		}
		catalogue, _ := json.Marshal(packet["review_rule_catalogue"])
		if !strings.Contains(string(catalogue), policy) || !strings.Contains(string(catalogue), "a.*") ||
			strings.Contains(string(catalogue), "field components") {
			t.Fatal("discovery needs scoped instruction paths, not the bodies of every skill")
		}
		proposal, _ := json.Marshal(plan)
		if attempts == 1 {
			proposal = []byte(`{"brief":"missing streams"}`)
		}
		answer := map[string]any{"status": "READY", "summary": "one line change", "questions": []string{}, "setup": []string{}, "plan_json": string(proposal),
			"context_pack": map[string]any{"summary": "fixture map", "files": []packFile{{Path: "a.txt", Role: "owned"}}}}
		out, _ := json.Marshal(answer)
		return string(out), nil
	}
	result := f.do("prepare", func() (any, error) {
		return Prepare(f.cfg, PrepareInput{Request: "-m change the line", Name: "prepared"})
	})
	if result["status"] != "READY" || attempts != 2 || fileText(t, "a.txt") != "before\n" || Current() != nil {
		t.Fatalf("preparation executed work: %v", result)
	}
	rules := result["review_rules"].([]any)
	if len(rules) != 1 || rules[0].(map[string]any)["id"] != "ui" ||
		rules[0].(map[string]any)["sources"].([]any)[0] != ".agentgate.json" {
		t.Fatalf("preparation must report the applied rules and their sources: %v", rules)
	}
	path, err := PlanFile("prepared")
	must(t, err)
	var saved Plan
	must(t, json.Unmarshal([]byte(fileText(t, path)), &saved))
	if saved.Request != "-m change the line" {
		t.Fatal("the original request was weakened")
	}
	f.model = nil
	f.launch(&saved)
	if f.count("scout") != 0 || Current().ContextPack == "" {
		t.Fatal("preparation's discovery must be handed to the writer without another scout")
	}
	f.review()
	f.finish()
}

func TestPreparationReportsMissingInstructionsForTheDiscoveredScope(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m implement the UI")
	f.cfg.ReviewRules = map[string]config.ReviewRule{"ui": {Instructions: []string{"missing-skill.md"}, Files: []string{"a.*"}}}
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind != "prepare" {
			return "", nil
		}
		proposal, _ := json.Marshal(plan)
		answer, _ := json.Marshal(map[string]any{"status": "READY", "summary": "scoped the UI", "questions": []string{}, "setup": []string{}, "plan_json": string(proposal), "context_pack": map[string]any{}})
		return string(answer), nil
	}
	result := f.do("prepare", func() (any, error) { return Prepare(f.cfg, PrepareInput{Request: plan.Request, Name: "missing-rules"}) })
	if result["status"] != "BLOCKED" || !strings.Contains(result["summary"].(string), "missing-skill.md") || len(result["review_rules"].([]any)) != 1 {
		t.Fatalf("missing applicable instructions need a visible setup blocker: %v", result)
	}
	if _, err := os.Stat(filepath.Join(PlansDir, "missing-rules", "plan.json")); !os.IsNotExist(err) {
		t.Fatal("an executable plan was saved without its required project rules")
	}
}

func TestPreparationKeepsMissingDecisionsAndToolFailuresOutOfTheLead(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind == "prepare" {
			return `{"status":"BLOCKED","summary":"no business contract","questions":["Which API supplies the balance?"],"setup":[],"plan_json":"","context_pack":{}}`, nil
		}
		return "", nil
	}
	result := f.do("prepare", func() (any, error) { return Prepare(f.cfg, PrepareInput{Request: "-m add a page", Name: "blocked"}) })
	if result["status"] != "BLOCKED" || Current() != nil {
		t.Fatal(result)
	}
	if _, err := os.Stat(filepath.Join(PlansDir, "blocked", "plan.json")); !os.IsNotExist(err) {
		t.Fatal("a blocked draft was saved as executable")
	}
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind == "prepare" {
			return `{"status":"BLOCKED","summary":"measurement requires a supported mobile context","questions":[],"setup":["Enable mobile emulation"],"plan_json":"","context_pack":{}}`, nil
		}
		return "", nil
	}
	result = f.do("prepare", func() (any, error) {
		return Prepare(f.cfg, PrepareInput{Request: "-m repair measurement", Name: "technical-blocker"})
	})
	if result["status"] != "BLOCKED" || f.count("prepare") != 2 {
		t.Fatal("a technical blocker without product questions must be returned, not retried or hidden in a private result file")
	}
	command := filepath.Join(t.TempDir(), "catalogue")
	write(t, command, "#!/bin/sh\nprintf '%s\\n' 'profile unavailable' >&2\nexit 2\n")
	must(t, os.Chmod(command, 0o755))
	old := layoutCommand
	layoutCommand = func() string { return command }
	t.Cleanup(func() { layoutCommand = old })
	f.fails("prepare", "profile unavailable", func() (any, error) {
		return Prepare(f.cfg, PrepareInput{Request: "-m build https://www.figma.com/design/abc/example?node-id=1-2"})
	})
	if f.count("prepare") != 2 {
		t.Fatal("do not spend a worker when the design catalogue is unavailable")
	}
}

func TestReplanKeepsTheTaskAndRequiresFreshReview(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m change the line")
	f.launch(plan)
	f.review()
	before := Current()
	plan.Update = "Add a precise check without changing the implementation"
	plan.Checks = append(plan.Checks, Check{Argv: []string{"test", "-s", "a.txt"}})
	f.do("replan", func() (any, error) { return Replan(f.cfg, Current(), plan) })
	after := Current()
	if after.ID != before.ID || digest(after.Baseline) != digest(before.Baseline) || len(after.Writers) != len(before.Writers) || after.Phase != "awaiting-review" {
		t.Fatal("replan reset the task or its original snapshot")
	}
	f.fails("finish", "no current independent review", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "not yet"}) })
	f.do("hold", func() (any, error) { return Hold(f.cfg, Current(), HoldInput{Reason: "user pause"}) })
	f.fails("replan", "explicitly resume", func() (any, error) { return Replan(f.cfg, Current(), plan) })
	f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) })
	f.review()
	f.finish()
}

func TestReplanCanRepairABlockedCorrectionAfterExplicitResume(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m correct the page")
	f.launch(plan)
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind == "review" {
			return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + finding("P1") + `],"checked":["read"],"resolutions":[]}]}`, nil
		}
		if kind == "writer" {
			return `{"status":"BLOCKED","summary":"measurement contract needs correction","blockers":["Which content boundary should be measured?"],"handoff":"","deviations":[]}`, nil
		}
		return "", nil
	}
	result := f.review()
	id := result["untriaged"].([]any)[0].(string)
	f.do("triage", func() (any, error) {
		return Triage(f.cfg, Current(), TriageInput{Dispositions: []Disposition{{ID: id, Decision: "accepted", Evidence: "confirmed"}}})
	})
	f.do("fix", func() (any, error) { return Fix(f.cfg, Current(), FixInput{}) })
	before := Current()
	if before.Phase != "held" || before.ResumePhase != "correcting" {
		t.Fatal("fixture did not reproduce the blocked correction")
	}
	plan.Update = "Correct the verification contract, preserving unfinished findings"
	plan.Checks = append(plan.Checks, Check{Argv: []string{"test", "-s", "a.txt"}})
	f.fails("replan", "explicitly resume", func() (any, error) { return Replan(f.cfg, Current(), plan) })
	f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) })
	f.do("replan", func() (any, error) { return Replan(f.cfg, Current(), plan) })
	after := Current()
	if after.ID != before.ID || digest(after.Baseline) != digest(before.Baseline) || len(after.accepted()) != 1 || after.Phase != "awaiting-review" {
		t.Fatal("measurement repair reset the task or waived an unfinished finding")
	}
}
