package dev

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/knaus94/agentgate/internal/provider"
)

// What a real session showed: an interface nobody designed for its jobs, and an environment nobody prepared.
// Each test is one promise of the 0.21 brief, end to end through the task.

// interfacePlan adds a page for a streamer: a backend stream and an interface stream, with the jobs it is for.
func interfacePlan(f *fixture, request string) *Plan {
	plan := f.plan(request, Stream{Name: "core", Domain: "backend", Files: []string{"a.txt"}},
		Stream{Name: "web", Domain: "frontend", Files: []string{"page.tsx"}})
	plan.UI = &UI{Summary: "A page to give a streamer access", Criteria: []string{"access is given without SQL"}, Preserve: []string{"the promo list"},
		Usage: &Usage{Who: "an operator", Jobs: []Job{{ID: "find", Job: "find a streamer", StartsFrom: "the streamer's Steam link"}}}}
	return plan
}

// designer answers as an interface designer that walked the given jobs.
func designer(jobs ...string) string {
	var walks []map[string]any
	for _, job := range jobs {
		walks = append(walks, map[string]any{"id": job, "steps": []string{"open Streamers", "paste the link"}, "must_know": []string{}})
	}
	var answer map[string]any
	_ = json.Unmarshal([]byte((&fixture{}).defaultAnswer("design", nil)), &answer)
	answer["jobs"] = walks
	raw, _ := json.Marshal(answer)
	return string(raw)
}

func TestInterfaceIsDesignedForItsJobsBeforeAnyoneWritesIt(t *testing.T) {
	f := newFixture(t)
	plan := interfacePlan(f, "-h give streamers access from the admin")
	f.fails("start", "designed for its jobs", func() (any, error) { return Start(f.cfg, plan, "", false, RunInput{}) })
	// A designer that did not walk a job did not design for it.
	f.fails("architecture", "without walking jobs find", func() (any, error) { return Architecture(f.cfg, plan) })
	var architects []map[string]any
	f.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		switch kind {
		case "design":
			if packet["usage"] == nil {
				t.Error("the designer is given the jobs")
			}
			return designer("find"), nil
		case "architecture":
			architects = append(architects, packet)
		}
		return "", nil
	}
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	ui, _ := receipt["ui_design"].(map[string]any)
	input, _ := receipt["input"].(map[string]any)["ui"].(map[string]any)
	if f.count("design") != 2 || ui == nil || input == nil || input["design"] != ui["id"] || len(architects) != 3 {
		t.Fatalf("one call designs the interface first and the architecture on it: %v", receipt)
	}
	for _, packet := range architects {
		if contract, _ := packet["ui"].(map[string]any); contract == nil || contract["usage"] == nil || contract["spec"] == nil {
			t.Fatalf("the architects design on the interface and its jobs: %v", packet["ui"])
		}
	}
	plan.Architecture = &Accepted{ID: receipt["id"].(string)}
	plan.UI = &UI{Design: input["design"].(string), Acceptance: "The user: «yes, by the Steam link»", Usage: plan.UI.Usage}
	if done := f.start(plan); done["step"] != StepDone {
		t.Fatalf("accepting the interface keeps the architecture it was designed with: %v", done)
	}
}

func TestInterfaceJobsAreReviewedWhereItsCodeIsWritten(t *testing.T) {
	f := newFixture(t)
	plan := interfacePlan(f, "-m give streamers access from the admin")
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind == "design" {
			return designer("find"), nil
		}
		return "", nil
	}
	designed := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	input, _ := designed["input"].(map[string]any)["ui"].(map[string]any)
	if input == nil || f.count("architecture") != 0 {
		t.Fatalf("a mode without architects still gets the interface designed: %v", designed)
	}
	plan.UI = &UI{Design: input["design"].(string), Acceptance: "The user approved it", Usage: plan.UI.Usage}
	f.start(plan)
	if axes := Current().Reviews[0].Lanes[0].Axes; !slices.Contains(axes, "usability") {
		t.Fatalf("the first lane judges the jobs of an interface the change adds: %v", axes)
	}
	plain := newFixture(t)
	local := interfacePlan(plain, "-m restyle the page")
	local.UI.Usage = nil
	plain.start(local)
	if axes := Current().Reviews[0].Lanes[0].Axes; slices.Contains(axes, "usability") {
		t.Fatalf("a change that adds no interface has no jobs to judge: %v", axes)
	}
}

func TestPrerequisiteHoldsWhatNeedsItUntilTheLeadMadeIt(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m use the disposable database")
	plan.Prerequisites = []Prerequisite{{ID: "db", What: "the disposable database", Argv: []string{"tools/db", "create"}}}
	waiting := f.start(plan)
	if !slices.Equal(needs(waiting), []string{"setup"}) || f.count("writer") != 0 {
		t.Fatalf("no writer starts before the database exists: %v", waiting)
	}
	f.fails("run", "its prerequisites: db", func() (any, error) { return Run(f.cfg, Current(), RunInput{Prepared: []string{"database"}}) })
	if done := f.do("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{Prepared: []string{"db"}}) }); done["step"] != StepDone {
		t.Fatalf("the lead's word that it exists lets the work go on: %v", done)
	}
	// One made from what a stream writes waits for that stream, and holds the checks after it.
	after := newFixture(t)
	staged := after.plan("-m migrate and use the database")
	staged.Prerequisites = []Prerequisite{{ID: "migrated", What: "the migration applied", After: "core"}}
	var told any
	after.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		if kind == "writer" {
			told = packet["prepared_after_you"]
		}
		return "", nil
	}
	held := after.start(staged)
	if !slices.Equal(needs(held), []string{"setup"}) || after.count("writer") != 1 || len(Current().Checks) != 0 || told == nil {
		t.Fatalf("the writer runs, is told not to wait, and the checks wait for the migration: %v", held)
	}
	if done := after.do("run", func() (any, error) { return Run(after.cfg, Current(), RunInput{Prepared: []string{"migrated"}}) }); done["step"] != StepDone {
		t.Fatalf("the checks run once it exists: %v", done)
	}
}

func TestEnvFileGivesTheChecksTheirVariables(t *testing.T) {
	f := newFixture(t)
	env := filepath.Join(t.TempDir(), "checks.env")
	must(t, os.WriteFile(env, []byte("# the disposable database\nexport AGENTGATE_FIXTURE_URL=\"postgres://fixture-secret\"\n"), 0o600))
	t.Setenv("AGENTGATE_FIXTURE_URL", "")
	plan := f.plan("-m query the database")
	plan.EnvFile = env
	plan.Checks = []Check{{Argv: []string{"sh", "-c", `test "${AGENTGATE_FIXTURE_URL%%://*}" = postgres`}}}
	if done := f.start(plan); done["step"] != StepDone {
		t.Fatalf("a check run without the variable in the lead's shell still gets it: %v", done)
	}
	if strings.Contains(fileText(t, filepath.Join(sessionDir(Current().Cwd, Current().Session), "state.json")), "fixture-secret") {
		t.Fatal("the task keeps the path of the file, never its values")
	}
	must(t, os.Chmod(env, 0o644))
	if _, err := Resolve(f.cfg, plan); err == nil || !strings.Contains(err.Error(), "can be read by others") {
		t.Fatalf("a file others can read is refused: %v", err)
	}
}

func TestPendingAdditionReachesTheReview(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, _ map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && call.Resume == "" {
			return `{"status":"BLOCKED","summary":"which limit?","blockers":["the limit is not given"],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return "", nil
	}
	f.start(f.plan("-m enforce the limit"))
	unlock, err := lock()
	must(t, err)
	if _, err := Execute("amend", func() (any, error) { return nil, nil }); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second command of the session is refused as busy: %v", err)
	}
	if _, err := Pend(f.cfg, Operations{Update: "a mode", Mode: "heavy"}); err == nil || !strings.Contains(err.Error(), "can only add") {
		t.Fatalf("what changes the plan waits for nobody: %v", err)
	}
	added := Operations{Update: "The user: «and the refusal names the limit»", Requirements: []RequirementChange{{ID: "R9", Text: "the refusal names the limit"}}}
	if _, err := Pend(f.cfg, added); err != nil {
		t.Fatal(err)
	}
	unlock()
	if waiting := asMap(Current().report(f.cfg, nil)); len(asStrings(waiting["pending_amendments"])) != 1 {
		t.Fatalf("status lists what waits: %v", waiting)
	}
	var judged []string
	f.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		if kind == "review" && packet["quality"] != nil {
			for _, requirement := range packet["quality"].(map[string]any)["requirements"].([]any) {
				judged = append(judged, requirement.(map[string]any)["id"].(string))
			}
		}
		return "", nil
	}
	done := f.do("answer", func() (any, error) {
		return Answer(f.cfg, Current(), AnswerInput{Stream: "core", Answer: "100 per minute"})
	})
	if done["step"] != StepDone || !slices.Contains(judged, "R9") || len(Current().pendingUpdates()) != 0 {
		t.Fatalf("the running command applies the addition before its review: %v %v", judged, done)
	}
}

func TestFurtherStageTakesNewWorkOnAFinishedTask(t *testing.T) {
	f := newFixture(t)
	if done := f.start(f.plan("-m add the owner guard")); done["step"] != StepDone {
		t.Fatalf("start: %v", done)
	}
	first := Current().Writers[0].Session
	opened := f.do("amend", func() (any, error) {
		return AmendOperations(f.cfg, Current(), Operations{Update: "The user: «a foreign owner gets a reason»",
			Requirements: []RequirementChange{{ID: "R2", Text: "a foreign owner is told why"}}})
	})
	gate := Current()
	if opened["step"] != StepWrite || len(gate.Plan.Stages) != 2 || gate.Plan.Stages[1].Name != "further" ||
		!slices.Contains(gate.Plan.Stages[1].Covers, "R2") {
		t.Fatalf("the new work is a stage after the accepted one: %v", opened)
	}
	var resumed string
	f.model = func(kind string, _ map[string]any, call provider.Call) (string, error) {
		if kind == "writer" {
			resumed = call.Resume
		}
		return "", nil
	}
	if done := f.proceed(); done["step"] != StepDone || resumed != first {
		t.Fatalf("the writer of the last stage continues its own session, and the task finishes again: %q %v", resumed, done)
	}
}
