package dev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/provider"
)

// prepared is the answer of a scripted preparation worker.
func prepared(status, summary string, plan any, questions []Question, pack any) string {
	if questions == nil {
		questions = []Question{}
	}
	raw, _ := json.Marshal(map[string]any{"status": status, "summary": summary, "questions": questions, "setup": []string{}, "plan": plan, "context_pack": pack})
	return string(raw)
}

var fixturePack = map[string]any{"summary": "fixture map", "files": []packFile{{Path: "a.txt", Role: "owned"}}, "callers": []any{},
	"invariants": []string{}, "conventions": []string{}, "unknowns": []string{}}

func (f *fixture) prepare(input PrepareInput) map[string]any {
	f.t.Helper()
	return f.do("prepare", func() (any, error) { return Prepare(f.cfg, input) })
}

func TestPreparationDraftsWithoutWritingAndCorrectsItsPlanInTheSameSession(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-l the worker must not change the requested mode")
	policy := ".agents/skills/client-ui/SKILL.md"
	write(t, policy, "Use the project's field components.")
	f.cfg.ReviewRules = map[string]config.ReviewRule{
		"ui":        {Instructions: []string{policy}, Files: []string{"a.*"}},
		"unrelated": {Instructions: []string{"missing-backend.md"}, Files: []string{"server/**"}},
	}
	f.cfg.ReviewRuleSources = map[string][]string{"ui": {".agentgate.json"}}
	var attempts int
	var session string
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "prepare" {
			return "", nil
		}
		attempts++
		if call.Writer || (attempts > 1 && call.Resume == "") {
			t.Fatal("preparation must read only and correct its draft in the same session")
		}
		if attempts == 1 {
			catalogue, _ := json.Marshal(packet["review_rule_catalogue"])
			if !strings.Contains(string(catalogue), policy) || !strings.Contains(string(catalogue), "a.*") || strings.Contains(string(catalogue), "field components") {
				t.Fatal("discovery needs scoped instruction paths, not the bodies of every skill")
			}
			// Three mistakes of form at once: a citation in the scope, an unknown kind of check, no domain.
			return prepared("READY", "one line change", map[string]any{"brief": "Change the line",
				"assessment": map[string]any{"size": "ordinary", "evidence": "one fixture"},
				"streams":    []map[string]any{{"name": "core", "files": []string{"/abs/a.txt:10-20 — the line"}}},
				"checks":     []map[string]any{{"argv": []string{"true"}, "kind": "check"}}}, nil, fixturePack), nil
		}
		if attempts == 2 {
			problems := asStrings(packet["validation_problems"])
			if len(problems) < 3 || !strings.Contains(strings.Join(problems, "\n"), "repository-relative") || !strings.Contains(strings.Join(problems, "\n"), `unknown check kind "check"`) {
				t.Fatalf("the worker is told every problem of its plan at once: %v", problems)
			}
		}
		session = call.Resume
		return prepared("READY", "one line change", plan, nil, fixturePack), nil
	}
	result := f.prepare(PrepareInput{Request: "-m change the line", Name: "prepared"})
	first := session
	if result["status"] != "READY" || attempts != 2 || fileText(t, "a.txt") != "before\n" || Current() != nil {
		t.Fatalf("preparation executed work or was not corrected in place: %v", result)
	}
	rules := result["review_rules"].([]any)
	if len(rules) != 1 || rules[0].(map[string]any)["id"] != "ui" || rules[0].(map[string]any)["sources"].([]any)[0] != ".agentgate.json" {
		t.Fatalf("preparation must report the applied rules and their sources: %v", rules)
	}
	if stages := result["stages"].([]any); len(stages) != 1 || result["mode"] != "medium" || !strings.Contains(result["next"].(string), "start --plan prepared") {
		t.Fatalf("the lead gets the facts it decides on, not the whole plan: %v", result)
	}
	path, err := PlanFile("prepared")
	must(t, err)
	saved := &Plan{}
	must(t, json.Unmarshal([]byte(fileText(t, path)), saved))
	if saved.Request != "-m change the line" {
		t.Fatal("the original request was weakened")
	}

	// A note of the lead continues the same worker; it does not explore again.
	f.prepare(PrepareInput{Notes: "Keep the trailing newline"})
	if last := f.calls[len(f.calls)-1]; last.Resume != first || !strings.Contains(last.Prompt, "You already read the repository") {
		t.Fatalf("a later call continues the open preparation: %+v", last.Resume)
	}

	f.model = nil
	saved = &Plan{}
	must(t, json.Unmarshal([]byte(fileText(t, path)), saved))
	done := f.do("start", func() (any, error) { return Start(f.cfg, saved, "prepared", false, RunInput{}) })
	gate := Current()
	if done["step"] != StepDone || gate.ContextPack == "" || gate.Preparation == "" || gate.PlanName != "prepared" {
		t.Fatalf("the map drawn by preparation goes to the task's workers, and its worker can revise the plan later: %+v", gate.ContextPack)
	}
	for _, call := range f.calls {
		if kindOf(call) == "writer" && !slices.Contains(call.ReadDirs, filepath.Dir(gate.ContextPack)) {
			t.Fatal("the writer must be able to read the context pack")
		}
	}
}

func TestAnswersGoToTheSameWorker(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m add a page")
	var sessions []string
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "prepare" {
			return "", nil
		}
		sessions = append(sessions, call.Resume)
		if len(sessions) == 1 {
			return prepared("BLOCKED", "the balance has two possible sources", nil, []Question{{ID: "source", Question: "Which API supplies the balance?",
				Options: []string{"wallet service", "ledger export"}, Recommended: "wallet service"}}, nil), nil
		}
		answers, _ := json.Marshal(packet["answers"])
		if !strings.Contains(string(answers), "Which API supplies the balance?") || !strings.Contains(string(answers), "the wallet service") {
			t.Fatalf("the answer travels with its question: %s", answers)
		}
		return prepared("READY", "one page", plan, nil, fixturePack), nil
	}
	blocked := f.prepare(PrepareInput{Request: plan.Request, Name: "page"})
	questions := blocked["questions"].([]any)
	if blocked["status"] != "BLOCKED" || len(questions) != 1 || questions[0].(map[string]any)["id"] != "source" || Current() != nil {
		t.Fatalf("a missing decision comes back as a question the lead can pass on: %v", blocked)
	}
	if _, err := os.Stat(filepath.Join(PlansDir, "page", "plan.json")); !os.IsNotExist(err) {
		t.Fatal("a blocked draft was saved as a plan")
	}
	// A lead that lost the answer reads it from status instead of running the worker again.
	status := map[string]any{}
	if AddPreparation(nil, status); status["preparation"].(map[string]any)["questions"] == nil || status["next"] != blocked["next"] {
		t.Fatalf("status must show the questions that wait on the lead: %v", status)
	}
	ready := f.prepare(PrepareInput{Answers: map[string]string{"source": "the wallet service"}})
	if ready["status"] != "READY" || len(sessions) != 2 || sessions[0] != "" || sessions[1] == "" || ready["plan"] != "page" {
		t.Fatalf("the user's answer continues the worker that asked: %v %v", ready, sessions)
	}
	if blocked["preparation"] != ready["preparation"] {
		t.Fatal("an answer must not start another preparation")
	}

	// A different request is a different preparation.
	f.model = func(kind string, _ map[string]any, call provider.Call) (string, error) {
		if kind == "prepare" && call.Resume != "" {
			t.Error("another request must not inherit a worker's conversation")
		}
		return prepared("READY", "another task", f.plan("-m rename the label"), nil, nil), nil
	}
	if other := f.prepare(PrepareInput{Request: "-m rename the label", Name: "label"}); other["preparation"] == ready["preparation"] {
		t.Fatal("a new request needs its own preparation")
	}
}

func TestAProposalIsNotAnAuthorization(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m --staged plan integrate the backend")
	plan.Streams = nil
	plan.Requirements = []Requirement{{ID: "R1", Text: "preserve the contract"}, {ID: "R2", Text: "implement the adapter"}}
	plan.Stages = []Stage{
		{Name: "contract", Goal: "Define the contract", Covers: []string{"R1"}, Streams: []Stream{{Name: "contract", Domain: "backend", Files: []string{"a.txt"}}}},
		{Name: "adapter", Goal: "Build the adapter", Covers: []string{"R2"}, Streams: []Stream{{Name: "adapter", Domain: "backend", Files: []string{"b.txt"}}}},
	}
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind != "prepare" {
			return "", nil
		}
		return prepared("READY", "Two backend stages", plan, nil, nil), nil
	}
	result := f.prepare(PrepareInput{Request: plan.Request, Name: "backend-proposal"})
	if result["status"] != "READY" || f.count("prepare") != 1 || Current() != nil || f.count("writer") != 0 || !strings.Contains(result["next"].(string), "--implement") {
		t.Fatalf("a planning request keeps the stages it proposes and starts nothing: %v", result)
	}
	path, err := PlanFile("backend-proposal")
	must(t, err)
	saved := &Plan{}
	must(t, json.Unmarshal([]byte(fileText(t, path)), saved))
	if saved.Request != plan.Request || len(saved.Stages) != 2 {
		t.Fatal("planning lost its scope or changed what the user asked for")
	}
	f.fails("start", "--implement", func() (any, error) { return Start(f.cfg, saved, "backend-proposal", false, RunInput{}) })
	if fileText(t, "a.txt") != "before\n" || fileText(t, "b.txt") != "before\n" || Current() != nil {
		t.Fatal("planning changed application files or opened a task")
	}
	f.model = nil
	done := f.do("start", func() (any, error) { return Start(f.cfg, saved, "backend-proposal", true, RunInput{}) })
	if done["step"] != StepDone || Current().Request.Action != "implement" || fileText(t, "b.txt") != "after\n" {
		t.Fatalf("the same plan runs once the user authorized it, without being edited: %v", done)
	}
}

func TestAPlanThatStaysInvalidStopsWithEveryProblemNamed(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, _ map[string]any, call provider.Call) (string, error) {
		if kind == "prepare" {
			return prepared("READY", "invalid proposal", map[string]any{"brief": "no streams"}, nil, nil), nil
		}
		return "", nil
	}
	result := f.prepare(PrepareInput{Request: "-m plan the backend", Name: "invalid-proposal"})
	if result["status"] != "BLOCKED" || result["reason"] != "invalid_plan" || !strings.Contains(strings.Join(asStrings(result["problems"]), "\n"), "streams are required") ||
		f.count("prepare") != corrections+1 || Current() != nil {
		t.Fatalf("an invalid preparation ends with a bounded, public diagnostic: %v", result)
	}
	for index, call := range f.calls {
		if (index > 0) != (call.Resume != "") {
			t.Fatal("every correction goes into the session of the worker that made the mistake")
		}
	}
	if _, err := os.Stat(filepath.Join(PlansDir, "invalid-proposal", "plan.json")); !os.IsNotExist(err) {
		t.Fatal("an invalid proposal was saved")
	}
}

func TestPreparationReportsMissingInstructionsForTheDiscoveredScope(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m implement the UI")
	f.cfg.ReviewRules = map[string]config.ReviewRule{"ui": {Instructions: []string{"missing-skill.md"}, Files: []string{"a.*"}}}
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind != "prepare" {
			return "", nil
		}
		return prepared("READY", "scoped the UI", plan, nil, nil), nil
	}
	result := f.prepare(PrepareInput{Request: plan.Request, Name: "missing-rules"})
	if result["status"] != "BLOCKED" || !strings.Contains(result["summary"].(string), "missing-skill.md") || len(result["review_rules"].([]any)) != 1 {
		t.Fatalf("missing applicable instructions need a visible setup blocker: %v", result)
	}
	if _, err := os.Stat(filepath.Join(PlansDir, "missing-rules", "plan.json")); !os.IsNotExist(err) {
		t.Fatal("a plan was saved without its required project rules")
	}
}

func TestATechnicalBlockerAndAFailedCatalogueReachTheLeadAsTheyAre(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind == "prepare" {
			raw, _ := json.Marshal(map[string]any{"status": "BLOCKED", "summary": "measurement requires a supported mobile context", "questions": []Question{},
				"setup": []string{"Enable mobile emulation"}, "plan": nil, "context_pack": nil})
			return string(raw), nil
		}
		return "", nil
	}
	result := f.prepare(PrepareInput{Request: "-m repair measurement", Name: "technical-blocker"})
	if result["status"] != "BLOCKED" || f.count("prepare") != 1 || len(asStrings(result["setup"])) != 1 || !strings.Contains(result["next"].(string), "prerequisite") {
		t.Fatalf("a technical blocker without product questions is returned, not retried: %v", result)
	}
	command := filepath.Join(t.TempDir(), "catalogue")
	write(t, command, "#!/bin/sh\nprintf '%s\\n' 'profile unavailable' >&2\nexit 2\n")
	must(t, os.Chmod(command, 0o755))
	old := layoutCommand
	layoutCommand = func() string { return command }
	t.Cleanup(func() { layoutCommand = old })
	f.fails("prepare", "profile unavailable", func() (any, error) {
		return Prepare(f.cfg, PrepareInput{Request: "-m build https://www.figma.com/design/abc/example?node-id=1-2", Fresh: true})
	})
	if f.count("prepare") != 1 {
		t.Fatal("do not spend a worker when the design catalogue is unavailable")
	}
}

func TestPreparationRevisesThePlanOfARunningTask(t *testing.T) {
	f := newFixture(t)
	plan := stagedPlan("-m build the feature")
	changeQuality(f, func(q *QualityReport) { q.Requirements[0].Status, q.Requirements[0].Cause = "partial", "plan" })
	reviewer := f.model
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "prepare" {
			return prepared("READY", "three stages", plan, nil, fixturePack), nil
		}
		return reviewer(kind, packet, call)
	}
	f.prepare(PrepareInput{Request: plan.Request, Name: "feature"})
	saved := &Plan{}
	path, _ := PlanFile("feature")
	must(t, json.Unmarshal([]byte(fileText(t, path)), saved))
	stopped := f.do("start", func() (any, error) { return Start(f.cfg, saved, "feature", false, RunInput{}) })
	if !slices.Equal(needs(stopped), []string{"requirement"}) {
		t.Fatalf("the fixture must stop on the partial requirement: %v", stopped)
	}
	session := loadPreparation(Current().Preparation).Session
	// Words that continue the task are not a new request: preparing them would rewrite the plan around them.
	calls := f.count("prepare")
	f.fails("prepare", "already has an unfinished task", func() (any, error) {
		return Prepare(f.cfg, PrepareInput{Request: "-m continue the work", Name: "again"})
	})
	if f.count("prepare") != calls {
		t.Fatal("a refused preparation must not start a worker")
	}
	revised := revisedFoundation(t, plan)
	var seen map[string]any
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "prepare" {
			return "", nil
		}
		if seen == nil {
			seen = packet
			if call.Resume != session {
				t.Error("the worker that drafted the plan revises it")
			}
			forgetful := clonePlan(t, revised)
			forgetful.Update = ""
			return prepared("READY", "moved R1 to the stage that completes it", forgetful, nil, nil), nil
		}
		if !strings.Contains(strings.Join(asStrings(packet["validation_problems"]), "\n"), "set update") {
			t.Errorf("a revision without its reason is sent back: %v", packet["validation_problems"])
		}
		return prepared("READY", "moved R1 to the stage that completes it", revised, nil, nil), nil
	}
	result := f.prepare(PrepareInput{Notes: "R1 is completed by the last stage"})
	needed, _ := json.Marshal(seen["needs"])
	if seen["current_plan"] == nil || seen["position"] == nil || !strings.Contains(string(needed), `"kind":"requirement"`) {
		t.Fatalf("the worker sees the task it revises and what the task needs: %s", needed)
	}
	gate := Current()
	if result["status"] != "READY" || !strings.Contains(result["next"].(string), "amend --plan feature") || len(gate.Revisions) != 1 || gate.ContextPack == "" {
		t.Fatalf("a revision is a proposal until it is amended, and it keeps the map it had: %v", result)
	}
	must(t, json.Unmarshal([]byte(fileText(t, path)), saved))
	f.raw, f.model = false, nil
	f.amend(saved)
	if done := f.proceed(); done["step"] != StepDone || len(Current().Revisions) != 2 {
		t.Fatalf("the amended task runs to its end: %v", done)
	}
	brief, _ := PlanShow("feature")
	if !strings.Contains(brief.(string), "## Requirements") {
		t.Fatalf("the brief of the plan stays readable: %v", brief)
	}
}
