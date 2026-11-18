package dev

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/knaus94/agentgate/internal/provider"
)

// The scenarios below drive a task the lead's own session writes: no writer process exists, and the test
// plays the session — it edits the files and reports with `run {written}`.

func sessionFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.cfg.Executor = "session"
	return f
}

// written is the session saying it wrote the open stage, or made the fixes it was asked for.
func (f *fixture) written(summary string) map[string]any {
	f.t.Helper()
	return f.do("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{Written: &Written{Summary: summary}}) })
}

// grounded gives a plan what every mode above light asks of it.
func grounded(plan *Plan) *Plan {
	plan.Grounding = &contextPack{Summary: "one text file per value", Files: []packFile{{Path: "context.txt", Role: "the neighbour"}}}
	if len(plan.Stages) == 0 {
		plan.Like = []string{"context.txt"}
	}
	return plan
}

// read has the plan read and records the user's approval, as a session does before it starts.
func (f *fixture) read(plan *Plan, design string) *Plan {
	f.t.Helper()
	receipt := f.do("architecture", func() (any, error) { return architecture(f.cfg, plan, ArchitectureInput{Design: design}) })
	plan.Architecture, plan.Approved = &Accepted{ID: receipt["id"].(string)}, "go ahead"
	return plan
}

// threeStages is a plan whose first stage is a checkpoint and whose second names a risk of its own.
func threeStages(request string) *Plan {
	stream := func(file string) []Stream { return []Stream{{Name: "core", Domain: "backend", Files: []string{file}}} }
	return grounded(&Plan{Request: request, Brief: "Change three lines", Checks: []Check{{Argv: []string{"true"}}},
		Requirements: []Requirement{{ID: "R1", Text: "a.txt says after"}, {ID: "R2", Text: "b.txt says after", Method: "check"}, {ID: "R3", Text: "context.txt says after"}},
		Stages: []Stage{
			{Name: "first", Goal: "the first line", Covers: []string{"R1"}, Like: []string{"context.txt"}, Checkpoint: true, Streams: stream("a.txt")},
			{Name: "second", Goal: "the amount", Covers: []string{"R2"}, Like: []string{"a.txt"}, Streams: stream("b.txt"),
				Assessment: &Assessment{Size: "ordinary", Risks: []string{"money"}, Evidence: "b.txt holds the amount"}},
			{Name: "third", Goal: "the last line", Covers: []string{"R3"}, Like: []string{"a.txt"}, Streams: stream("context.txt")}}})
}

func edit(t *testing.T, files ...string) {
	t.Helper()
	for _, file := range files {
		must(t, os.WriteFile(file, []byte("after\n"), 0o644))
	}
}

func TestALightTaskTheSessionWritesStartsNoModel(t *testing.T) {
	f := sessionFixture(t)
	request := "-l change the line"
	opened := f.do("prepare", func() (any, error) { return Prepare(f.cfg, PrepareInput{Request: request}) })
	if opened["status"] != "PLAN" || opened["mode"] != "light" || !strings.Contains(opened["instructions"].(string), "## In light") ||
		strings.Contains(opened["instructions"].(string), "## In heavy") {
		t.Fatalf("prepare starts no worker and says what the plan needs in this mode: %v", opened)
	}
	plan := f.plan(request)
	plan.Requirements = []Requirement{{ID: "R1", Text: "a.txt says after", Method: "code"}}
	saved, err := PlanSave(f.cfg, plan, "light-change")
	must(t, err)
	if saved.Executor != "session" || saved.Runs != 0 || !strings.Contains(fileText(t, saved.Brief), "written by the session") {
		t.Fatalf("a light plan is priced at no model run: %+v", saved)
	}
	started := f.start(plan)
	if started["step"] != StepWrite || !slices.Equal(needs(started), []string{"write"}) || fileText(t, "a.txt") != "before\n" {
		t.Fatalf("the stage is the session's to write: %v", started)
	}
	edit(t, "a.txt")
	handed := f.written("a.txt says after")
	if handed["step"] != StepDecide || !slices.Equal(needs(handed), []string{"review"}) {
		t.Fatalf("with its checks green, the session reads its own patch: %v", handed)
	}
	done := f.decide(DecideInput{Review: &LeadReview{Summary: "read the one-line patch", Quality: &QualityReport{Rules: []RuleResult{},
		Requirements: []RequirementResult{{ID: "R1", Status: "verified", Reason: "the line is there", Evidence: []Proof{{Kind: "code", Ref: "a.txt:1", Detail: "after"}}}}}}})
	gate := Current()
	if done["step"] != StepDone || len(f.calls) != 0 || gate.Executor != "session" || gate.Worktree != "" {
		t.Fatalf("a light task ends without a model process, in the checkout: %v (%d calls)", done, len(f.calls))
	}
	if writers := fmt.Sprint(done["receipt"].(map[string]any)["writers"]); !strings.Contains(writers, "the session") {
		t.Fatalf("the receipt names the session as the writer: %v", writers)
	}
}

func TestMediumStartsOnlyOnAReadAndApprovedPlan(t *testing.T) {
	f := sessionFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "architecture" && packet["round"] == "advise" {
			return `{"summary":"read the plan","agreements":"the scope is right","blockers":[{"id":"order","evidence":"a.txt:1","remedy":"name the neighbour"}],"questions":[]}`, nil
		}
		return "", nil
	}
	plan := grounded(f.plan("change the line"))
	resolved, err := Resolve(f.cfg, plan)
	must(t, err)
	if resolved.Mode != "medium" || resolved.Decision.Source != "default" {
		t.Fatalf("without a flag the mode is medium, whatever the assessment: %+v", resolved.Decision)
	}
	f.fails("start", "has the plan read", func() (any, error) { return Start(f.cfg, plan, "", false, RunInput{}) })
	reading := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	blockers := reading["blockers"].([]any)
	if f.count("architecture") != 1 || len(blockers) != 1 || blockers[0].(map[string]any)["id"] != "fable/order" {
		t.Fatalf("the advisor reads the plan once and its blocker comes back: %v", reading)
	}
	plan.Architecture, plan.Approved = &Accepted{ID: reading["id"].(string)}, "go ahead"
	f.fails("start", "blockers of the reading are open", func() (any, error) { return Start(f.cfg, plan, "", false, RunInput{}) })
	closed := f.do("architecture", func() (any, error) {
		return architecture(f.cfg, plan, ArchitectureInput{Revise: reading["id"].(string),
			Answered: []BlockerAnswer{{ID: "order", Resolved: true, How: "the stage now follows context.txt"}}})
	})
	if f.count("architecture") != 1 || closed["id"] == reading["id"] {
		t.Fatalf("answering the blockers runs no model and gives a new record: %v", closed)
	}
	plan.Architecture, plan.Approved = &Accepted{ID: closed["id"].(string)}, ""
	f.fails("start", "approval", func() (any, error) { return Start(f.cfg, plan, "", false, RunInput{}) })
	plan.Approved = "да, начинай"
	if started := f.start(plan); !slices.Equal(needs(started), []string{"write"}) {
		t.Fatalf("a read and approved plan starts: %v", started)
	}
}

func TestAFailingCheckComesBackToTheSessionBeforeAnyoneReads(t *testing.T) {
	f := sessionFixture(t)
	plan := grounded(f.plan("change the line"))
	plan.Checks = []Check{{Argv: []string{"grep", "-q", "after", "a.txt"}}}
	f.start(f.read(plan, ""))
	must(t, os.WriteFile("a.txt", []byte("wrong\n"), 0o644))
	failed := f.written("changed the line")
	if failed["step"] != StepWrite || !slices.Equal(needs(failed), []string{"fix"}) || f.count("review") != 0 {
		t.Fatalf("a failing check goes back to the session and no reader is started: %v", failed)
	}
	detail := failed["needs"].([]any)[0].(map[string]any)["detail"].(map[string]any)
	if checks := detail["failing_checks"].([]any); len(checks) != 1 || !strings.Contains(fmt.Sprint(checks[0]), "grep") {
		t.Fatalf("the need names the check that failed: %v", detail)
	}
	edit(t, "a.txt")
	if done := f.written("the line says after now"); done["step"] != StepDone || f.count("review") != 1 {
		t.Fatalf("with the check green the advisor reads the change once: %v (%d reviews)", done, f.count("review"))
	}
}

func TestAChangeOutsideThePlanIsSettledBeforeItIsRead(t *testing.T) {
	f := sessionFixture(t)
	request := "-l change the line"
	f.do("prepare", func() (any, error) { return Prepare(f.cfg, PrepareInput{Request: request}) })
	edit(t, "context.txt") // written before the task started: still the task's change, not what it found
	f.start(f.plan(request))
	edit(t, "a.txt", "b.txt")
	outside := f.written("changed the line")
	if outside["step"] != StepDecide || !slices.Equal(needs(outside), []string{"scope"}) {
		t.Fatalf("files no stage owns are settled first: %v", outside)
	}
	if what := outside["needs"].([]any)[0].(map[string]any)["what"].(string); !strings.Contains(what, "b.txt") || !strings.Contains(what, "context.txt") {
		t.Fatalf("both the file written beside the stage and the one written before the start are named: %s", what)
	}
	settled := f.do("run", func() (any, error) {
		return Run(f.cfg, Current(), RunInput{Adopt: []string{"b.txt"}, Disown: []string{"context.txt"}})
	})
	gate := Current()
	if !slices.Equal(needs(settled), []string{"review"}) || !slices.Contains(gate.Plan.Stages[0].Streams[0].Files, "b.txt") ||
		!slices.Equal(gate.Disowned, []string{"context.txt"}) {
		t.Fatalf("an adopted file joins the stage and a disowned one leaves the task: %v", settled)
	}
	patch := fileText(t, gate.Reading.Patch)
	if !strings.Contains(patch, "b.txt") || strings.Contains(patch, "context.txt") {
		t.Fatalf("what is read is the stage with the adopted file, without the disowned one:\n%s", patch)
	}
}

func TestReadingsArePlacedAtCheckpointsAndAtTheEnd(t *testing.T) {
	for mode, want := range map[string][]int{"-m": {1, 1, 2}, "-h": {2, 4, 6}} {
		t.Run(mode, func(t *testing.T) {
			f := sessionFixture(t)
			design := ""
			if mode == "-h" {
				design = "one writer per file; the amount is changed last"
			}
			started := f.start(f.read(threeStages(mode+" change three lines"), design))
			if !slices.Equal(needs(started), []string{"write"}) {
				t.Fatalf("start: %v", started)
			}
			for index, file := range []string{"a.txt", "b.txt", "context.txt"} {
				edit(t, file)
				if index == 2 {
					// The last stage removes the file the first stage followed: a running task is not held to it.
					must(t, os.Remove(file))
				}
				answer := f.written("changed " + file)
				if f.count("review") != want[index] {
					t.Fatalf("%s after stage %d: want %d reader processes, got %d: %v", mode, index+1, want[index], f.count("review"), answer)
				}
				if index < 2 && (!slices.Equal(needs(answer), []string{"write"}) || answer["stage"].(map[string]any)["index"] != float64(index+2)) {
					t.Fatalf("an accepted stage hands over the next one in the same answer: %v", answer)
				}
				if index == 2 && answer["step"] != StepDone {
					t.Fatalf("the whole change is read after the last stage and the task ends: %v", answer)
				}
			}
			records := Current().Accepted
			read := []bool{len(records[0].Reviewers) > 0, len(records[1].Reviewers) > 0, len(records[2].Reviewers) > 0}
			if !slices.Equal(read, []bool{true, mode == "-h", false}) {
				t.Fatalf("%s: the marked stage is read, the risky one in heavy only, the last one with the whole: %v", mode, read)
			}
		})
	}
}

func TestTheDigestCarriesTheUsersWordsToTheLeadsOwnProviderOnly(t *testing.T) {
	f := sessionFixture(t)
	at := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	line := func(kind, message string) string {
		return `{"type":"` + kind + `","timestamp":"` + at + `","message":` + message + "}\n"
	}
	transcript := line("user", `{"content":"сделай строку after, но b.txt не трогай"}`) +
		line("assistant", `{"id":"m1","model":"claude-opus-5-5","content":[{"type":"thinking","thinking":"private reasoning"},{"type":"text","text":"Сначала прочитаю файл."},`+
			`{"type":"tool_use","name":"Read","input":{"file_path":"a.txt"}}],"usage":{"input_tokens":10,"cache_read_input_tokens":90,"output_tokens":5}}`) +
		line("user", `{"content":[{"type":"tool_result","content":"TOP-SECRET file contents"}]}`)
	dir := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "repo")
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "fixture-session.jsonl"), []byte(transcript), 0o644))

	digest := fileText(t, conversation(t.TempDir(), time.Now().Unix()))
	for _, want := range []string{"[user] сделай строку after, но b.txt не трогай", "[session] Сначала прочитаю файл.", "[tool] Read: a.txt"} {
		if !strings.Contains(digest, want) {
			t.Fatalf("the digest lacks %q:\n%s", want, digest)
		}
	}
	if strings.Contains(digest, "TOP-SECRET") || strings.Contains(digest, "private reasoning") {
		t.Fatalf("tool results and reasoning stay out of the digest:\n%s", digest)
	}
	if model := leadModel(); model != "claude-opus-5-5" {
		t.Fatalf("the session's model is read from its own record: %q", model)
	}

	shown := map[string]bool{}
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		_, given := packet["conversation"]
		shown[kind+" "+call.Route.Alias] = given
		return "", nil
	}
	plan := f.read(grounded(f.plan("change the line")), "")
	f.start(plan)
	edit(t, "a.txt")
	if done := f.written("changed the line"); done["step"] != StepDone || !shown["architecture fable"] || !shown["review fable"] {
		t.Fatalf("the advisor and the reader of the lead's own provider are shown the conversation: %v %v", shown, done)
	}

	other := sessionFixture(t)
	must(t, os.MkdirAll(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "repo"), 0o755))
	must(t, os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "repo", "fixture-session.jsonl"), []byte(transcript), 0o644))
	process := other.cfg.Session["medium"]
	process.Review[0].Route = "astra:high"
	other.cfg.Session["medium"], other.cfg.Advisor = process, "astra:high"
	shown = map[string]bool{}
	other.model = f.model
	other.start(other.read(grounded(other.plan("change the line")), ""))
	edit(t, "a.txt")
	if done := other.written("changed the line"); done["step"] != StepDone || shown["architecture astra"] || shown["review astra"] || len(shown) != 2 {
		t.Fatalf("a model of another provider reads the plan and the patch without the conversation: %v %v", shown, done)
	}
}

func TestUltraTakesAnExecutedProofForWhatARiskyStageCompletes(t *testing.T) {
	f := sessionFixture(t)
	plan := threeStages("-x change three lines")
	plan.Requirements[1].Method = "code"
	if _, err := PlanSave(f.cfg, plan, "risky"); err == nil || !strings.Contains(err.Error(), "ultra takes a proof that is executed") ||
		!strings.Contains(err.Error(), `"R2"`) || strings.Contains(err.Error(), `"R1"`) {
		t.Fatalf("a reading of the code does not prove what a risky stage completes; other stages are not held to it: %v", err)
	}
	plan.Requirements[1].Method = "check"
	saved, err := PlanSave(f.cfg, plan, "risky")
	must(t, err)
	// The advisor, a critic that designs blind and then reads, and four lanes at each of three readings.
	if saved.Runs != 1+2+4*3 {
		t.Fatalf("the price counts the readers of the plan and every reading of the code: %d %v", saved.Runs, saved.Price)
	}
}
