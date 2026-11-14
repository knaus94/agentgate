package dev

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/knaus94/agentgate/internal/provider"
)

func identity(t *testing.T) {
	for _, name := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(name, "Fixture")
	}
	for _, name := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(name, "fixture@example.invalid")
	}
}

func fileText(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	must(t, err)
	return string(raw)
}

func (f *fixture) land(how string) map[string]any {
	f.t.Helper()
	return f.do("land", func() (any, error) { return Land(f.cfg, Current(), LandInput{How: how}) })
}

func (f *fixture) amend(plan *Plan) map[string]any {
	f.t.Helper()
	return f.do("amend", func() (any, error) { return Amend(f.cfg, Current(), plan) })
}

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
			stream["name"].(string) + `","deviations":[],"disputes":[]}`, nil
	}
}

func TestALargeSpecificationIsDoneStageByStage(t *testing.T) {
	f := newFixture(t)
	var briefs []string
	stageWriter(f, &briefs)
	plan := stagedPlan("-m --step build the feature")

	route, err := Route(f.cfg, plan)
	must(t, err)
	if raw, _ := json.Marshal(route); !strings.Contains(string(raw), `"integration_review":true`) || strings.Count(string(raw), `"stage":`) != 3 {
		t.Fatalf("the route shows every stage and the pass over the whole: %s", raw)
	}
	first := f.start(plan)
	gate := Current()
	if first["step"] != StepWrite || gate.Pause == nil || gate.Pause.Kind != PauseStep || f.count("writer") != 1 || fileText(t, "b.txt") != "before\n" {
		t.Fatalf("with --step only the first stage is written, then the task waits for the user: %v", first)
	}
	if position := first["stage"].(map[string]any); position["name"] != "feature" || position["accepted"] != float64(1) {
		t.Fatalf("accepting a stage opens the next one, not the end of the task: %v", position)
	}
	record := gate.Accepted[0]
	archive := map[string]any{}
	raw, err := os.ReadFile(record.Archive)
	must(t, err)
	must(t, json.Unmarshal(raw, &archive))
	judged := archive["reviews"].([]any)[0].(map[string]any)["lanes"].([]any)[0].(map[string]any)["quality"].(map[string]any)["requirements"].([]any)
	if len(judged) != 1 || judged[0].(map[string]any)["id"] != "R1" {
		t.Fatalf("a stage judges only the requirements it works on: %v", judged)
	}
	if record.Summary != "did base-core" || record.Handoffs["base-core"] == "" || record.Writers["base-core"].Session == "" {
		t.Fatalf("what the stage produced is kept in its writers' own words: %+v", record)
	}
	if len(gate.Reviews) != 0 || len(gate.Writers) != 0 || gate.Round != nil {
		t.Fatal("the next stage starts with a clean slate")
	}
	if hook := StopHook(gate.Cwd, "fixture-session"); len(hook) != 0 {
		t.Fatal("a task that waits for the user's word between stages may end the turn")
	}
	changed := *plan
	changed.Requirements = slices.Clone(plan.Requirements)
	changed.Requirements[0].Text = "a different accepted requirement"
	changed.Update, changed.Authorized = "revise the remaining work", "the user said so"
	f.fails("amend", "belongs to an accepted stage", func() (any, error) { return Amend(f.cfg, Current(), &changed) })

	// The second stage builds on the first: its brief says so, and its review sees only its own change.
	f.proceed()
	second := briefs[len(briefs)-1]
	for _, want := range []string{"## This stage: feature (2 of 3)", "R2: the feature works (due at the end of this stage)", "Earlier stages, accepted",
		"base: did base-core", "contract of base-core", "Later stages — not part of this one", "polish: Tidy up"} {
		if !strings.Contains(second, want) {
			t.Errorf("the brief of a stage lacks %q:\n%s", want, second)
		}
	}
	gate = Current()
	if before, _, _ := treeFile(gate.objects(), gate.Accepted[1].Tree, "a.txt"); string(before) != "before\n+base-core\n+feature-core\n" {
		t.Fatalf("a stage is accepted at the tree it left: %q", before)
	}
	if before, _, _ := treeFile(gate.objects(), gate.StageTree, "a.txt"); string(before) != "before\n+base-core\n+feature-core\n" || gate.Stage != 2 {
		t.Fatalf("a stage is measured from where the stage before it ended: %q", before)
	}

	// The pass over the whole change finds a seam; the fix goes to the writer of the stage that owned the file last.
	stageModel := f.model
	var raised atomic.Bool
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" && strings.Contains(packet["brief"].(string), "## Integration pass") && raised.CompareAndSwap(false, true) {
			if changes := packet["changes"].([]any); len(changes) != 2 {
				t.Errorf("the last pass reads the whole change from where the task began: %v", changes)
			}
			return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + finding("P1") + `],"checked":["read"],"resolutions":[]}]}`, nil
		}
		return stageModel(kind, packet, call)
	}
	featureSession := gate.Accepted[1].Writers["feature-core"].Session
	reviewsBefore := f.count("review")
	done := f.proceed()
	gate = Current()
	if done["step"] != StepDone || !gate.Whole || f.count("review") != reviewsBefore+3 {
		t.Fatalf("three stages end with a real review of the whole change: %v (%d reviews)", done, f.count("review")-reviewsBefore)
	}
	if gate.origin() != gate.BaseTree || !slices.Equal(gate.scope(), []string{"a.txt", "b.txt"}) {
		t.Fatal("the last pass is measured from where the task began")
	}
	if owner := gate.owner("a.txt"); owner == nil || owner.Name != "feature.feature-core" {
		t.Fatalf("in the last pass a file belongs to the stage that owned it last: %+v", owner)
	}
	if writer := gate.latest()["feature.feature-core"]; writer == nil || writer.Status != "COMPLETED" || writer.Session != featureSession {
		t.Fatalf("the fix of the last pass continues the session of the writer that wrote that stage: %+v", gate.latest())
	}
	if fileText(t, "a.txt") != "before\n+base-core\n+feature-core\n+feature.feature-core\n" || fileText(t, "b.txt") != "before\n+feature-core\n+polish-core\n" {
		t.Fatalf("the task ends after the last pass: %q %q", fileText(t, "a.txt"), fileText(t, "b.txt"))
	}
	if text := PanelText(f.cfg, false, false, 120, 0); !strings.Contains(text, "STAGES  3 of 3 accepted") || !strings.Contains(text, "✔ 2. feature") ||
		strings.Contains(text, "writer  base-core") {
		t.Fatalf("the panel shows the stages:\n%s", text)
	}
	saved, err := PlanSave(f.cfg, plan, "the-feature")
	must(t, err)
	brief, _ := PlanShow("the-feature")
	for _, want := range []string{"## Requirements", "**R2** (due at feature): the feature works", "### 2. feature — medium", "#### feature-core — backend",
		"one more review, for the seams", "evidence method: code"} {
		if !strings.Contains(brief.(string), want) {
			t.Errorf("the saved brief lacks %q (%v):\n%s", want, saved, brief)
		}
	}
}

func TestARequirementIsDueAtTheLastStageThatCoversIt(t *testing.T) {
	f := newFixture(t)
	plan := stagedPlan("-m build the feature")
	plan.Stages = plan.Stages[:2]
	plan.Requirements = plan.Requirements[:2]
	plan.Stages[0].Covers = []string{"R1", "R2"} // the base contributes to the feature, and the feature stage completes it
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "review" || packet["quality"] == nil {
			return "", nil
		}
		report := fixtureQuality(packet)
		for index := range report.Requirements {
			// The reviewer is right: after the base stage the feature is only partly there.
			if report.Requirements[index].ID == "R2" && strings.Contains(packet["brief"].(string), "## This stage: base") {
				report.Requirements[index].Status, report.Requirements[index].Reason = "partial", "the base is there, the feature is not"
			}
		}
		raw, _ := json.Marshal(map[string]any{"reports": []map[string]any{{"axis": "correctness", "status": "PASS", "findings": []any{},
			"checked": []string{"read"}, "resolutions": []any{}}, {"axis": "architecture", "status": "PASS", "findings": []any{}, "checked": []string{"read"}, "resolutions": []any{}},
			{"axis": "simplicity", "status": "PASS", "findings": []any{}, "checked": []string{"read"}, "resolutions": []any{}}}, "quality": report})
		return string(raw), nil
	}
	done := f.start(plan)
	if done["step"] != StepDone || len(Current().Accepted) != 2 {
		t.Fatalf("a requirement that is only started at a stage does not hold that stage back: %v", done)
	}

	// The same verdict on a requirement that is due blocks, and says what can be done about it.
	f2 := newFixture(t)
	f2.model = f.model
	due := stagedPlan("-m build the feature")
	due.Stages = due.Stages[:2]
	due.Requirements = due.Requirements[:2]
	due.Stages[0].Covers, due.Stages[1].Covers = []string{"R1", "R2"}, nil
	stopped := f2.start(due)
	if stopped["step"] != StepDecide || !slices.Equal(needs(stopped), []string{"requirement"}) || len(Current().Accepted) != 0 {
		t.Fatalf("a due requirement that is partial keeps the stage open: %v", stopped)
	}
	if got := offered(stopped); !slices.Contains(got, "amend") || !slices.Contains(got, "decide") || !slices.Contains(got, "run") {
		t.Fatalf("a correct verdict always leaves a legal answer: %v", got)
	}
	// The plan was wrong, not the code: the feature stage completes R2. Moving the coverage touches no code.
	fixed := stagedPlan("-m build the feature")
	fixed.Stages = fixed.Stages[:2]
	fixed.Requirements = fixed.Requirements[:2]
	fixed.Stages[0].Covers = []string{"R1", "R2"}
	fixed.Update = "R2 is completed by the feature stage; the base only starts it"
	amended := f2.amend(fixed)
	calls := len(f2.calls)
	if amended["step"] != StepVerify || !strings.Contains(strings.Join(asStrings(amended["did"]), "\n"), "only the criteria changed") {
		t.Fatalf("moving a requirement makes only the criteria stale: %v", amended["did"])
	}
	done = f2.proceed()
	gate := Current()
	if done["step"] != StepDone || len(gate.Revisions) != 2 || !strings.Contains(strings.Join(gate.Revisions[1].Changes, "\n"), "R2 is now due at feature") {
		t.Fatalf("the amended task runs to the end, and the amendment is on record: %v %+v", done, gate.Revisions)
	}
	base := f2.calls[calls]
	if kindOf(base) != "review" || base.Resume == "" || !strings.Contains(base.Prompt, "do not review it again") {
		t.Fatalf("the reviewer reassesses the criteria in its own session without reading the code again: %s", base.Prompt)
	}
}

func asStrings(value any) []string {
	var out []string
	list, _ := value.([]any)
	for _, item := range list {
		out = append(out, fmt.Sprint(item))
	}
	return out
}

func TestAPlanIsValidatedWholeBeforeAnythingStarts(t *testing.T) {
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
	refuse("lists requirements", func(p *Plan) { p.Requirements = nil })
	refuse("inside the stages", func(p *Plan) { p.Streams = []Stream{{Name: "x", Domain: "backend", Files: []string{"a.txt"}}} })
	refuse("needs a goal", func(p *Plan) { p.Stages[1].Goal = " " })
	refuse("stage names are unique", func(p *Plan) { p.Stages[1].Name = "base" })
	refuse(`stage "feature"`, func(p *Plan) { p.Stages[1].Streams[0].Domain = "database" })
	refuse("--single asks for one pass", func(p *Plan) { p.Request = "-m --single build" })
	refuse("needs at least one declared check", func(p *Plan) { p.Checks = nil })
	if _, err := ParseRequest("--staged --single task"); err == nil {
		t.Error("--staged and --single contradict each other")
	}
	if _, err := ParseRequest("--step review the branch"); err == nil {
		t.Error("stages belong to work that writes")
	}

	// Every problem is reported at once, so one correction can fix them all.
	broken := stagedPlan("-m build the feature")
	broken.Stages[0].Covers = []string{"R9"}
	broken.Stages[1].Streams[0].Domain = "database"
	broken.Checks = []Check{{Argv: []string{"true"}, Kind: "check"}}
	_, err := Resolve(f.cfg, broken)
	var problems PlanProblems
	if !asProblems(err, &problems) || len(problems) < 3 {
		t.Fatalf("validation reports every problem of a plan: %v", err)
	}
	for _, want := range []string{"unknown requirement", "domain must be backend or frontend", `unknown check kind "check"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the report lacks %q: %v", want, err)
		}
	}

	// A plan that could not finish is refused before it starts, not after its last stage.
	waived := stagedPlan("-m write the documents")
	waived.Checks = nil
	waived.ChecksWaiver = "prose only: no command verifies it"
	if _, err := Resolve(f.cfg, waived); err != nil {
		t.Fatalf("a stated reason replaces a check that cannot exist: %v", err)
	}

	// Every stage has its own mode; the task as a whole needs what its most thorough stage needs.
	risky := stagedPlan("build the feature")
	risky.Stages[1].Assessment = &Assessment{Size: "ordinary", Risks: []string{"money"}, Evidence: "the feature moves balances"}
	resolved, err := Resolve(f.cfg, risky)
	must(t, err)
	if resolved.Modes[0] != "medium" || resolved.Modes[1] != "heavy" || resolved.Mode != "heavy" {
		t.Fatalf("modes per stage: %v, whole %s", resolved.Modes, resolved.Mode)
	}
	f.fails("start", "needs an architecture pass", func() (any, error) { return Start(f.cfg, risky, "", false, RunInput{}) })

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

func asProblems(err error, into *PlanProblems) bool {
	problems, ok := err.(PlanProblems)
	*into = problems
	return ok
}

func TestStagesStepByStepInAWorktreeWithAnAmendedPlan(t *testing.T) {
	f := newFixture(t)
	identity(t)
	var briefs []string
	stageWriter(f, &briefs)
	plan := stagedPlan("-m --step -w build the feature")
	plan.Stages = plan.Stages[:2]
	plan.Requirements = plan.Requirements[:2]

	paused := f.start(plan)
	gate := Current()
	if gate.Pause == nil || gate.Pause.Kind != PauseStep || !strings.Contains(gate.Pause.Reason, "confirm each stage") || gate.Stage != 1 || paused["step"] != StepWrite {
		t.Fatalf("--step waits for the user after an accepted stage: %v", paused)
	}
	log, err := exec.Command("git", "-C", gate.Worktree, "log", "--format=%s").Output()
	must(t, err)
	if !strings.Contains(string(log), "dev: base: did base-core") || gate.Accepted[0].Commit == "" || fileText(t, "a.txt") != "before\n" {
		t.Fatalf("an accepted stage is a commit on the task's branch, and the checkout is untouched: %s", log)
	}

	// Between stages the plan may change for what is still ahead, with a reason; what is accepted may not.
	rewritten := *plan
	rewritten.Stages = []Stage{plan.Stages[0], plan.Stages[1]}
	rewritten.Stages[0].Goal = "A different base"
	rewritten.Update = "rewrite history"
	f.fails("amend", "is accepted and cannot change", func() (any, error) { return Amend(f.cfg, Current(), &rewritten) })
	revised := *plan
	revised.Stages = []Stage{plan.Stages[0], plan.Stages[1]}
	revised.Stages[1].Goal = "Add the feature, without the extra flag"
	f.fails("amend", "needs update", func() (any, error) { return Amend(f.cfg, Current(), &revised) })
	revised.Update = "the flag turned out to be unnecessary"
	amended := f.amend(&revised)
	if Current().Pause != nil || amended["step"] != StepWrite || f.count("writer") != 1 {
		t.Fatalf("an amendment is the decision the pause waited for, and it starts no worker: %v", amended)
	}
	done := f.proceed()
	if !strings.Contains(briefs[len(briefs)-1], "without the extra flag") {
		t.Fatal("the amended stage is what runs")
	}
	if done["step"] != StepDone || Current().Whole {
		t.Fatalf("two ordinary stages need no pass over the whole: %v", done)
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
