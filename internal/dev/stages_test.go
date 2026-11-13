package dev

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/knaus94/agentgate/internal/provider"
)

// stagedPlan is a specification of three requirements split into three stages; two of them share a file.
func stagedPlan(request string) *Plan {
	stream := func(name string, files ...string) []Stream {
		return []Stream{{Name: name, Domain: "backend", Files: files}}
	}
	return &Plan{Request: request, Brief: "Build the feature in three parts",
		Assessment:   &Assessment{Size: "ordinary", Evidence: "three isolated fixture files"},
		Requirements: []Requirement{{ID: "R1", Text: "the base exists", Method: "code"}, {ID: "R2", Text: "the feature works"}, {ID: "R3", Text: "the result is tidy"}},
		Stages: []Stage{
			{Name: "base", Goal: "Lay the base", Acceptance: []string{"a.txt has the base"}, Covers: []string{"R1"}, Streams: stream("base-core", "a.txt")},
			{Name: "feature", Goal: "Add the feature on the base", Covers: []string{"R2"}, Streams: stream("feature-core", "a.txt", "b.txt")},
			{Name: "polish", Goal: "Tidy up", Covers: []string{"R3"}, Streams: stream("polish-core", "b.txt")},
		},
		Checks: []Check{{Argv: []string{"true"}}}}
}

// stageWriter appends the name of its stream to every file it owns, so each stage leaves its own trace.
func stageWriter(f *fixture, briefs *[]string) {
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "writer" {
			return "", nil
		}
		stream := packet["stream"].(map[string]any)
		*briefs = append(*briefs, packet["brief"].(string))
		for _, file := range stream["files"].([]any) {
			raw, _ := os.ReadFile(file.(string))
			_ = os.WriteFile(file.(string), append(raw, []byte("+"+stream["name"].(string)+"\n")...), 0o644)
		}
		return `{"status":"COMPLETED","summary":"did ` + stream["name"].(string) + `","blockers":[],"handoff":"contract of ` +
			stream["name"].(string) + `","deviations":[]}`, nil
	}
}

func TestALargeSpecificationIsDoneStageByStage(t *testing.T) {
	f := newFixture(t)
	var briefs []string
	stageWriter(f, &briefs)
	plan := stagedPlan("-m build the feature")

	route, err := Route(f.cfg, plan)
	must(t, err)
	if raw, _ := json.Marshal(route); !strings.Contains(string(raw), `"integration_review":true`) || strings.Count(string(raw), `"stage":`) != 3 {
		t.Fatalf("the route shows every stage and the pass over the whole: %s", raw)
	}
	first := f.launch(plan)
	if first["stage"] != "base (1 of 3)" || f.count("writer") != 1 || fileText(t, "b.txt") != "before\n" {
		t.Fatalf("only the first stage is written: %v", first["stage"])
	}
	f.review()
	if results := Current().Reviews[0].Lanes[0].Quality.Requirements; len(results) != 1 || results[0].ID != "R1" {
		t.Fatalf("a stage verifies only the requirements it covers: %+v", results)
	}
	accepted := f.finish()
	gate := Current()
	if accepted["phase"] != "staged" || len(gate.StageRecords) != 1 || gate.StageIndex != 1 || !strings.Contains(accepted["next"].(string), "Stage 2 of 3, feature") {
		t.Fatalf("accepting a stage opens the next one, not the end of the task: %v", accepted["next"])
	}
	if _, err := os.Stat(gate.StageRecords[0].Archive); err != nil || gate.StageRecords[0].Handoffs["base-core"] == "" {
		t.Fatalf("what the stage produced is kept: %+v", gate.StageRecords[0])
	}
	if len(gate.Reviews) != 0 || len(gate.Writers) != 0 {
		t.Fatal("the next stage starts with a clean slate")
	}
	changed := *plan
	changed.Requirements = slices.Clone(plan.Requirements)
	changed.Requirements[0].Text = "a different accepted requirement"
	changed.Update = "revise the remaining work"
	if err := gate.replan(&changed); err == nil || !strings.Contains(err.Error(), "accepted stage") {
		t.Fatalf("accepted requirement evidence cannot be silently repurposed: %v", err)
	}
	if hook := StopHook(gate.Cwd, "fixture-session"); hook["decision"] != "block" {
		t.Fatal("a task between stages is not finished")
	}
	f.fails("finish", "no current independent review", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "x"}) })

	// The second stage builds on the first: its brief says so, and its review sees only its own change.
	f.launch(plan)
	second := briefs[len(briefs)-1]
	for _, want := range []string{"## This stage: feature (2 of 3)", "R2: the feature works", "Earlier stages, accepted", "base: done", "contract of base-core",
		"Later stages — not part of this one", "polish: Tidy up"} {
		if !strings.Contains(second, want) {
			t.Errorf("the brief of a stage lacks %q:\n%s", want, second)
		}
	}
	if got := Current().Baseline["a.txt"].text(); got != "before\n+base-core\n" {
		t.Fatalf("a stage is measured from where the stage before it ended: %q", got)
	}
	f.review()
	f.finish()
	f.launch(plan)
	f.review()
	whole := f.finish()
	gate = Current()
	if whole["phase"] != "awaiting-review" || !gate.Integration || !strings.Contains(whole["next"].(string), "whole change") {
		t.Fatalf("three stages end with a pass over the whole change: %v", whole["next"])
	}
	if gate.Baseline["a.txt"].text() != "before\n" || gate.Baseline["b.txt"].text() != "before\n" || len(gate.Files) != 2 {
		t.Fatalf("the last pass is measured from where the task began: %q", gate.Baseline["a.txt"].text())
	}
	if !strings.Contains(gate.Plan.Brief, "## Integration pass") || len(gate.Plan.Streams) != 2 ||
		gate.Plan.Streams[0].Name != "feature.feature-core" || gate.Plan.Streams[1].Name != "polish.polish-core" {
		t.Fatalf("the last pass reads the whole change, each file under the stage that owned it last: %+v", gate.Plan.Streams)
	}
	// The last pass finds a seam; the fix goes to a writer for the stage that owned the file last.
	stageModel := f.model
	var raised atomic.Bool
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" && packet["axes"].([]any)[0] == "correctness" && raised.CompareAndSwap(false, true) {
			return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + finding("P1") + `],"checked":["read"],"resolutions":[]}]}`, nil
		}
		return stageModel(kind, packet, call)
	}
	reviewsBefore := f.count("review")
	found := f.review()
	if f.count("review") != reviewsBefore+2 || len(found["untriaged"].([]any)) != 1 {
		t.Fatalf("the pass over the whole change is a real review: %v", found["untriaged"])
	}
	f.do("triage", func() (any, error) {
		return Triage(f.cfg, Current(), TriageInput{[]Disposition{{ID: found["untriaged"].([]any)[0].(string), Decision: "accepted", Evidence: "verified in a.txt"}}})
	})
	f.do("fix", func() (any, error) { return Fix(f.cfg, Current(), FixInput{Brief: "close the seam"}) })
	if writer := Current().latest()["feature.feature-core"]; writer == nil || writer.Status != "COMPLETED" || strings.Contains(filepath.Base(writer.Dir), "/") {
		t.Fatalf("the fix of the last pass is written by the owning stage's stream: %+v", Current().latest())
	}
	f.review()
	done := f.finish()
	if done["phase"] != "complete" || fileText(t, "a.txt") != "before\n+base-core\n+feature-core\n+feature.feature-core\n" || fileText(t, "b.txt") != "before\n+feature-core\n+polish-core\n" {
		t.Fatalf("the task ends after the last pass: %v", done["phase"])
	}
	if _, shown := done["stage"]; shown {
		t.Fatal("a finished task has no present stage")
	}
	if text := PanelText(f.cfg, false, false, 120, 0); !strings.Contains(text, "STAGES  3 of 3 accepted") || !strings.Contains(text, "✔ 2. feature") ||
		strings.Contains(text, "writer  base-core") {
		t.Fatalf("the panel shows the stages:\n%s", text)
	}
	saved, err := PlanSave(f.cfg, plan, "the-feature")
	must(t, err)
	brief, _ := PlanShow("the-feature")
	for _, want := range []string{"## Requirements", "**R2** (feature): the feature works", "### 2. feature — medium", "#### feature-core — backend",
		"one more review, for the seams", "evidence method: code"} {
		if !strings.Contains(brief.(string), want) {
			t.Errorf("the saved brief lacks %q (%v):\n%s", want, saved, brief)
		}
	}
}

func TestAStagedPlanMustBeWholeAndALargePlanMustBeStaged(t *testing.T) {
	f := newFixture(t)
	refuse := func(why string, change func(*Plan)) {
		t.Helper()
		plan := stagedPlan("-m build the feature")
		change(plan)
		if _, err := Resolve(f.cfg, plan); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("want an error containing %q, got %v", why, err)
		}
	}
	refuse("no stage covers these requirements: R3", func(p *Plan) { p.Stages[2].Covers = nil })
	refuse("unknown requirement", func(p *Plan) { p.Stages[0].Covers = []string{"R9"} })
	refuse("at least two", func(p *Plan) { p.Stages = p.Stages[:1] })
	refuse("lists requirements", func(p *Plan) { p.Requirements = nil })
	refuse("inside the stages", func(p *Plan) { p.Streams = []Stream{{Name: "x", Domain: "backend", Files: []string{"a.txt"}}} })
	refuse("needs a goal", func(p *Plan) { p.Stages[1].Goal = " " })
	refuse("stage names are unique", func(p *Plan) { p.Stages[1].Name = "base" })
	refuse(`stage "feature"`, func(p *Plan) { p.Stages[1].Streams[0].Domain = "database" })
	refuse("--single asks for one pass", func(p *Plan) { p.Request = "-m --single build" })
	if _, err := ParseRequest("--staged --single task"); err == nil {
		t.Error("--staged and --single contradict each other")
	}
	if _, err := ParseRequest("--step review the branch"); err == nil {
		t.Error("stages belong to work that writes")
	}

	// Every stage has its own mode; the task as a whole needs what its most thorough stage needs.
	risky := stagedPlan("build the feature")
	risky.Stages[1].Assessment = &Assessment{Size: "ordinary", Risks: []string{"money"}, Evidence: "the feature moves balances"}
	resolved, err := Resolve(f.cfg, risky)
	must(t, err)
	if resolved.Stages[0].Mode != "medium" || resolved.Stages[1].Mode != "heavy" || resolved.Mode != "heavy" {
		t.Fatalf("modes per stage: %s %s, whole %s", resolved.Stages[0].Mode, resolved.Stages[1].Mode, resolved.Mode)
	}
	f.fails("launch", "needs an architecture pass", func() (any, error) { return Launch(f.cfg, risky) })

	// A plan too large for one pass has to be staged, unless the user says it is one mechanical change.
	var files []string
	for index := range 26 {
		files = append(files, fmt.Sprintf("big/file%02d.txt", index))
	}
	large := f.plan("-m rename everything", Stream{Name: "core", Domain: "backend", Files: files})
	if _, err := Resolve(f.cfg, large); err == nil || !strings.Contains(err.Error(), "Split it into stages") {
		t.Fatalf("a plan past the limits is refused with the way out: %v", err)
	}
	large.Request = "-m --single rename everything"
	if _, err := Resolve(f.cfg, large); err != nil {
		t.Fatalf("--single allows one large mechanical pass: %v", err)
	}
	large.Request = "-m review the branch"
	if _, err := Resolve(f.cfg, large); err != nil {
		t.Fatalf("reviewing an existing change has no size limit: %v", err)
	}
	if _, err := Resolve(f.cfg, f.plan("-m --staged small change")); err == nil || !strings.Contains(err.Error(), "--staged asks") {
		t.Fatalf("--staged without stages is refused: %v", err)
	}
}

func TestStagesStepByStepInAWorktreeWithARevisedPlan(t *testing.T) {
	f := newFixture(t)
	identity(t)
	var briefs []string
	stageWriter(f, &briefs)
	plan := stagedPlan("-m --step -w build the feature")
	plan.Stages = plan.Stages[:2]
	plan.Requirements = plan.Requirements[:2]

	f.launch(plan)
	f.review()
	paused := f.finish()
	gate := Current()
	if paused["phase"] != "held" || !strings.Contains(paused["hold_reason"].(string), "confirm each stage") || gate.ResumePhase != "staged" {
		t.Fatalf("--step waits for the user after an accepted stage: %v", paused["hold_reason"])
	}
	log, err := exec.Command("git", "-C", gate.Worktree, "log", "--format=%s").Output()
	must(t, err)
	if !strings.Contains(string(log), "dev: base: done") || gate.StageRecords[0].Commit == "" || fileText(t, "a.txt") != "before\n" {
		t.Fatalf("an accepted stage is a commit on the task's branch, and the checkout is untouched: %s", log)
	}
	f.fails("launch", "on hold", func() (any, error) { return Launch(f.cfg, plan) })
	f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) })

	// Between stages the plan may change for what is still ahead, with a reason; what is accepted may not.
	rewritten := *plan
	rewritten.Stages = []Stage{plan.Stages[0], plan.Stages[1]}
	rewritten.Stages[0].Goal = "A different base"
	f.fails("launch", "is accepted and cannot change", func() (any, error) { return Launch(f.cfg, &rewritten) })
	revised := *plan
	revised.Stages = []Stage{plan.Stages[0], plan.Stages[1]}
	revised.Stages[1].Goal = "Add the feature, without the extra flag"
	f.fails("launch", "say what and why in update", func() (any, error) { return Launch(f.cfg, &revised) })
	revised.Update = "the flag turned out to be unnecessary"
	single := f.plan("-m -w build the feature")
	f.fails("launch", "keeps its shape", func() (any, error) { return Launch(f.cfg, single) })
	f.launch(&revised)
	if !strings.Contains(briefs[len(briefs)-1], "without the extra flag") {
		t.Fatal("the revised stage is what runs")
	}
	f.review()
	done := f.finish()
	if done["phase"] != "complete" || Current().Integration {
		t.Fatalf("two ordinary stages need no pass over the whole: %v", done["phase"])
	}
	log, _ = exec.Command("git", "-C", Current().Worktree, "log", "--format=%s").Output()
	if strings.Count(string(log), "dev: ") != 2 {
		t.Fatalf("each stage is its own commit: %s", log)
	}
	landed := f.land("apply")
	if landed["landed"] != "apply" || fileText(t, "a.txt") != "before\n+base-core\n+feature-core\n" || fileText(t, "b.txt") != "before\n+feature-core\n" {
		t.Fatalf("landing brings every stage in at once: %v", landed)
	}
}
