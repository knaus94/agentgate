package dev

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

// fixture is a throwaway repository with an isolated state root and a scripted model.
type fixture struct {
	t     *testing.T
	cfg   *config.Config
	real  func(provider.Call) (provider.Result, error)
	raw   bool // hand scripted reviews to the lane unchanged, to test how malformed reports are rejected
	mu    sync.Mutex
	calls []provider.Call
	// model answers one call; the default writes "after" into the stream's files and passes every review.
	model func(kind string, packet map[string]any, call provider.Call) (string, error)
}

// scenarioTable is the routing the scenarios exercise: the built-in table of 0.19.0.
const scenarioTable = `{"fallbacks":{"fable":["astra","opus"],"opus":["fable","astra","sonnet"],"sonnet":["opus","sol"],"sol":["sonnet"],"astra":["fable","opus"]},"ui_designer":"opus:medium","visual_reviewer":"astra:medium","modes":{"light":{"scout":"","architecture":[],"critique":false,"backend":"sol:medium","frontend":"sonnet:medium","review":[{"axes":["correctness","architecture","simplicity"],"route":"astra:medium"}],"blocking":["P0","P1"],"escalate_to":"high"},"medium":{"scout":"sol:medium","architecture":[],"critique":false,"backend":"opus:medium","frontend":"opus:medium","review":[{"axes":["correctness","architecture","simplicity"],"route":"astra:medium"}],"blocking":["P0","P1"],"escalate_to":"high"},"heavy":{"scout":"","architecture":["fable:high","astra:high"],"critique":false,"architecture_timeout_s":900,"reviewer_timeout_s":1200,"backend":"opus:high","frontend":"opus:high","review":[{"axes":["architecture","simplicity"],"route":"fable:high"},{"axes":["correctness"],"route":"astra:high"}],"blocking":["P0","P1","P2"],"escalate_to":"xhigh"},"ultra":{"scout":"","architecture":["fable:xhigh","astra:xhigh"],"critique":true,"architecture_timeout_s":1800,"writer_timeout_s":3600,"reviewer_timeout_s":1800,"backend":"opus:xhigh","frontend":"opus:xhigh","review":[{"axes":["architecture","simplicity"],"route":"fable:high"},{"axes":["correctness"],"route":"fable:xhigh"},{"axes":["correctness"],"route":"astra:xhigh"},{"axes":["security"],"route":"astra:high"}],"blocking":["P0","P1","P2"],"escalate_to":"max"}}}`

var packetPath = regexp.MustCompile(`(/\S+\.json)`)

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	must(t, os.MkdirAll(repo, 0o755))
	t.Chdir(repo)
	t.Setenv("AGENTGATE_SESSION", "fixture-session")
	isolateHomes(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv(provider.WorkerEnv, "")
	previousRoot, previousRun := Root, provider.Run
	Root = func() string { return filepath.Join(root, "state") }
	f := &fixture{t: t, real: previousRun}
	provider.Run = f.run
	t.Cleanup(func() { Root, provider.Run = previousRoot, previousRun })
	for _, name := range []string{"a.txt", "b.txt", "context.txt"} {
		must(t, os.WriteFile(name, []byte("before\n"), 0o644))
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."},
		{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	cfg, err := config.Load()
	must(t, err)
	// The fixtures exercise the lead's own review in light mode: the configuration that has it is review: [].
	// The scenarios were written for this table, not for whatever the built-in one is at the time: escalation from
	// medium effort, substitutes between providers, opus writing medium.
	must(t, json.Unmarshal([]byte(scenarioTable), cfg))
	light := cfg.Modes["light"]
	light.Review = nil
	cfg.Modes["light"] = light
	f.cfg = cfg
	return f
}

// isolateHomes gives a test its own home directories. Installing writes into the homes of both CLIs, and a
// test must never reach the real ones; it also must not inherit the session of whoever runs the tests.
func isolateHomes(t *testing.T) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, name := range []string{"CODEX_THREAD_ID", "CODEX_TOOL_CALL_ID", "CODEX_SANDBOX", "CODEX_SANDBOX_NETWORK_DISABLED", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(name, "")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) run(call provider.Call) (provider.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	kind := kindOf(call)
	packet := map[string]any{}
	if match := packetPath.FindString(call.Prompt); match != "" {
		raw, _ := os.ReadFile(match)
		_ = json.Unmarshal(raw, &packet)
	}
	session := call.Resume
	if session == "" {
		session = "session-" + newID()[:8]
	}
	if f.model != nil {
		if answer, err := f.model(kind, packet, call); answer != "" || err != nil {
			if kind == "review" && !f.raw && err == nil {
				answer = f.fitAxes(answer, packet)
			}
			return provider.Result{Answer: answer, Session: session}, err
		}
	}
	return provider.Result{Answer: f.defaultAnswer(kind, packet), Session: session}, nil
}

// fitAxes keeps the reports of a scripted review that belong to the asking lane, so one script
// serves every lane layout; a lane the script says nothing about passes.
func (f *fixture) fitAxes(answer string, packet map[string]any) string {
	var scripted struct {
		Reports []map[string]any `json:"reports"`
		Quality *QualityReport   `json:"quality"`
	}
	if json.Unmarshal([]byte(answer), &scripted) != nil {
		return answer
	}
	if packet["axes"] == nil { // the reviewer is asked to reassess the criteria only
		if scripted.Quality == nil {
			scripted.Quality = fixtureQuality(packet)
		}
		raw, _ := json.Marshal(map[string]any{"quality": scripted.Quality})
		return string(raw)
	}
	var mine []map[string]any
	for _, report := range scripted.Reports {
		if slices.Contains(packet["axes"].([]any), report["axis"]) {
			mine = append(mine, report)
		}
	}
	if len(mine) == 0 {
		return f.defaultAnswer("review", packet)
	}
	// An axis of the lane the script says nothing about passes, like a lane it says nothing about.
	for _, axis := range packet["axes"].([]any) {
		if !slices.ContainsFunc(mine, func(report map[string]any) bool { return report["axis"] == axis }) {
			mine = append(mine, map[string]any{"axis": axis, "status": "PASS", "findings": []any{}, "checked": []string{"read the diff"}, "resolutions": []any{}})
		}
	}
	if scripted.Quality == nil {
		scripted.Quality = fixtureQuality(packet)
	}
	raw, _ := json.Marshal(map[string]any{"reports": mine, "quality": scripted.Quality})
	return string(raw)
}

func kindOf(call provider.Call) string {
	switch {
	case call.Writer:
		return "writer"
	case call.MCP["playwright"].Command != "":
		return "visual"
	case strings.Contains(call.Dir, "/preparations/"):
		return "prepare"
	case strings.Contains(call.Dir, "/consults/"):
		return "consult"
	case strings.Contains(call.Dir, "/designs/") && filepath.Base(call.Dir) == "designer":
		return "design"
	case strings.Contains(call.Dir, "/designs/"):
		return "architecture"
	}
	return "review"
}

func (f *fixture) defaultAnswer(kind string, packet map[string]any) string {
	switch kind {
	case "consult":
		return "Verified in a.txt:1. Unknown: nothing."
	case "design":
		return `{"status":"READY","summary":"one screen","users_and_goals":[],"existing_patterns":[],"flows":[],"screens":[{"name":"List",` +
			`"location":"a.txt","purpose":"p","layout":"l","wireframe":"","components":[],"states":[],"interactions":[],"copy":[],` +
			`"validation":[],"responsive":"","accessibility":""}],"data_requirements":[],"rejected_alternatives":[],"open_questions":[],` +
			`"assumptions":[],"revision_notes":[]}`
	case "visual":
		shots := packet["screenshots_dir"].(string)
		_ = os.WriteFile(filepath.Join(shots, "shot.png"), []byte("png"), 0o600)
		var coverage []map[string]any
		for _, raw := range packet["scenarios"].([]any) {
			row := raw.(map[string]any)
			coverage = append(coverage, map[string]any{"scenario_id": row["id"], "screen": row["screen"], "state": row["state"],
				"viewport": row["viewport"], "role": row["role"], "observed": true, "screenshot": "shot.png", "note": "seen"})
		}
		raw, _ := json.Marshal(map[string]any{"status": "PASS", "summary": "observed", "coverage": coverage, "findings": []any{},
			"resolutions": []any{}, "console_errors": []any{}, "unverified": []any{}})
		return string(raw)
	case "writer":
		stream := packet["stream"].(map[string]any)
		for _, file := range stream["files"].([]any) {
			if exact(file.(string)) {
				_ = os.WriteFile(file.(string), []byte("after\n"), 0o644)
			}
		}
		return `{"status":"COMPLETED","summary":"implemented","blockers":[],"handoff":"contract of ` + stream["name"].(string) + `","deviations":[],"disputes":[]}`
	case "architecture":
		if packet["round"] == "critique" {
			return `{"summary":"critique","agreements":"sound","blockers":[]}`
		}
		return `{"summary":"design","design":"keep the contract","risks":[],"open_questions":[]}`
	}
	if packet["axes"] == nil { // the reviewer is asked to reassess the criteria only
		raw, _ := json.Marshal(map[string]any{"quality": fixtureQuality(packet)})
		return string(raw)
	}
	var reports []map[string]any
	for _, axis := range packet["axes"].([]any) {
		var resolutions []map[string]any
		for _, finding := range packet["accepted_findings"].([]any) {
			resolutions = append(resolutions, map[string]any{"id": finding.(map[string]any)["id"], "resolved": true, "evidence": "verified the correction"})
		}
		reports = append(reports, map[string]any{"axis": axis, "status": "PASS", "findings": []any{}, "checked": []string{"read the diff"},
			"resolutions": append([]map[string]any{}, resolutions...)})
	}
	raw, _ := json.Marshal(map[string]any{"reports": reports, "quality": fixtureQuality(packet)})
	return string(raw)
}

// fixtureQuality verifies every criterion a packet asks about, with the proof its method requires.
func fixtureQuality(packet map[string]any) *QualityReport {
	if packet["quality"] == nil {
		return nil
	}
	raw, _ := json.Marshal(packet["quality"])
	var input qualityInputs
	_ = json.Unmarshal(raw, &input)
	cited := "a.txt"
	if changes, _ := packet["changes"].([]any); len(changes) > 0 {
		cited = changes[0].(map[string]any)["path"].(string)
	} else if scope, _ := packet["scope_files"].([]any); len(scope) > 0 && exact(scope[0].(string)) {
		cited = scope[0].(string)
	}
	report := &QualityReport{Requirements: []RequirementResult{}, Rules: []RuleResult{}}
	for _, requirement := range input.Requirements {
		proof := Proof{Kind: "code", Ref: cited + ":1", Detail: "inspected the fixture change"}
		switch requirement.Method {
		case "check":
			proof.Kind, proof.Ref = "check", ""
			for id, check := range input.Checks {
				if check.Receipt.ReturnCode == 0 {
					proof.Ref = id
					break
				}
			}
		case "browser":
			proof.Kind, proof.Ref = "browser", ""
			if input.Browser != nil {
				for _, row := range input.Browser.Coverage {
					if row.Observed {
						proof.Ref = row.ScenarioID
						break
					}
				}
			}
		}
		result := RequirementResult{ID: requirement.ID, Status: "verified", Reason: "fixture evidence", Evidence: []Proof{proof}}
		if proof.Ref == "" {
			result.Status, result.Cause, result.Reason, result.Evidence = "missing", "proof", "no proof source", nil
		}
		report.Requirements = append(report.Requirements, result)
	}
	for _, rule := range input.Rules {
		result := RuleResult{ID: rule.ID, Status: "pass", Evidence: "read the rule and the fixture change"}
		if rule.Unavailable != "" {
			result.Status, result.Evidence = "unavailable", rule.Unavailable
		}
		report.Rules = append(report.Rules, result)
	}
	return report
}

func (f *fixture) count(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, call := range f.calls {
		if kindOf(call) == kind {
			total++
		}
	}
	return total
}

func (f *fixture) plan(request string, streams ...Stream) *Plan {
	if len(streams) == 0 {
		streams = []Stream{{Name: "core", Domain: "backend", Files: []string{"a.txt"}}}
	}
	return &Plan{Request: request, Brief: "Change the specified line", Streams: streams,
		Assessment: &Assessment{Size: "ordinary", Evidence: "one isolated fixture"},
		Checks:     []Check{{Argv: []string{"true"}}}}
}

func asMap(value any) map[string]any {
	raw, _ := json.Marshal(value)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func (f *fixture) do(action string, run func() (any, error)) map[string]any {
	f.t.Helper()
	value, err := Execute(action, run)
	must(f.t, err)
	out := asMap(value)
	f.noDeadEnd(action, out)
	return out
}

// noDeadEnd holds every answer about a task to the promise of the protocol: each action it offers is legal
// now, an unfinished task that is not parked offers one that moves it, and every need says how it is settled.
func (f *fixture) noDeadEnd(action string, answer map[string]any) {
	f.t.Helper()
	list, described := answer["actions"].([]any)
	gate := Current()
	if !described || gate == nil || answer["task"] != gate.ID[:8] {
		return
	}
	for _, name := range list {
		if err := gate.can(Action(name.(string))); err != nil {
			f.t.Fatalf("%s offers %q, which is refused: %v", action, name, err)
		}
	}
	if gate.active() && (len(list) == 0 || gate.Pause == nil && !slices.Contains([]any{"run", "answer", "decide"}, list[0])) {
		f.t.Fatalf("%s left an unfinished task without an action that moves it: %v", action, answer)
	}
	needs, _ := answer["needs"].([]any)
	for _, need := range needs {
		if resolve, _ := need.(map[string]any)["resolve"].(string); strings.TrimSpace(resolve) == "" {
			f.t.Fatalf("%s reports a need without a way to settle it: %v", action, need)
		}
	}
}

func (f *fixture) fails(action, want string, run func() (any, error)) {
	f.t.Helper()
	if _, err := Execute(action, run); err == nil || !strings.Contains(err.Error(), want) {
		f.t.Fatalf("%s: want an error containing %q, got %v", action, want, err)
	}
}

// start opens a task for the plan and runs it until a person is needed or it is done.
func (f *fixture) start(plan *Plan) map[string]any {
	f.t.Helper()
	return f.do("start", func() (any, error) { return Start(f.cfg, plan, "", false, RunInput{}) })
}

func (f *fixture) proceed() map[string]any {
	f.t.Helper()
	return f.do("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{}) })
}

func (f *fixture) decide(input DecideInput) map[string]any {
	f.t.Helper()
	return f.do("decide", func() (any, error) { return Decide(f.cfg, Current(), input) })
}

// resultOf is what a command answered of its own, under result.
func resultOf(answer map[string]any) map[string]any {
	result, _ := answer["result"].(map[string]any)
	return result
}

// needs lists the kinds of what an answer says only a person can settle.
func needs(summary map[string]any) []string {
	var kinds []string
	list, _ := summary["needs"].([]any)
	for _, item := range list {
		kinds = append(kinds, item.(map[string]any)["kind"].(string))
	}
	return kinds
}

// offered lists what an answer says is legal now.
func offered(summary map[string]any) []string {
	var out []string
	list, _ := summary["actions"].([]any)
	for _, item := range list {
		out = append(out, item.(string))
	}
	return out
}

// reviewOnce scripts the first review of the correctness axis and lets every later review pass.
func (f *fixture) reviewOnce(findings ...string) {
	calls := 0
	previous := f.model
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if previous != nil {
			if answer, err := previous(kind, packet, call); answer != "" || err != nil {
				return answer, err
			}
		}
		if kind != "review" || packet["axes"] == nil || !slices.Contains(packet["axes"].([]any), any("correctness")) {
			return "", nil
		}
		if calls++; calls > 1 {
			return "", nil
		}
		return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + strings.Join(findings, ",") +
			`],"checked":["read"],"resolutions":[]}]}`, nil
	}
}

func finding(severity string) string {
	return `{"id":"","severity":"` + severity + `","confidence":"high","category":"logic","path":"a.txt","line":1,"evidence":"wrong value",` +
		`"impact":"wrong result","remedy":"use the agreed value","preserve":"other behaviour","boundary_break":false}`
}

func TestParseRequest(t *testing.T) {
	request, err := ParseRequest("-3 -h fix --no-grill repair the parser -x stays text")
	must(t, err)
	if request.Parallel != 3 || request.Mode != "heavy" || request.Action != "fix" || request.Grill != "skip" ||
		request.Task != "repair the parser -x stays text" {
		t.Fatalf("unexpected request: %+v", request)
	}
	if legacy, _ := ParseRequest("--critical task"); legacy.Mode != "ultra" {
		t.Fatalf("--critical must select ultra, got %q", legacy.Mode)
	}
	for _, bad := range []string{"-l -m task", "--fix task", "-h research topic", "status now", "--unknown task", "", "-21 task"} {
		if _, err := ParseRequest(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestModeIsDecidedFromFacts(t *testing.T) {
	cases := []struct {
		assessment Assessment
		files      int
		want       string
	}{
		{Assessment{Size: "trivial"}, 2, "light"},
		{Assessment{Size: "trivial"}, 5, "medium"},
		{Assessment{Size: "trivial", Uncertainty: "material"}, 1, "medium"},
		{Assessment{Size: "complex"}, 9, "medium"},
		{Assessment{Size: "complex", Uncertainty: "material"}, 9, "heavy"},
		{Assessment{Size: "ordinary", Risks: []string{"concurrency"}}, 4, "heavy"},
		{Assessment{Size: "trivial", Risks: []string{"money"}}, 1, "heavy"},
		{Assessment{Size: "complex", Risks: []string{"money"}}, 9, "ultra"},
		{Assessment{Size: "ordinary", Risks: []string{"migration"}, Irreversible: true}, 3, "ultra"},
	}
	for _, c := range cases {
		if got := decide(&c.assessment, c.files, 1, true); got.Mode != c.want || len(got.Because) == 0 {
			t.Errorf("%+v with %d files: want %s, got %s", c.assessment, c.files, c.want, got.Mode)
		}
	}
	if got := decide(&Assessment{Size: "trivial"}, 0, 1, false); got.Mode != "medium" {
		t.Errorf("a scope of unknown size is never light, got %s", got.Mode)
	}
	f := newFixture(t)
	flagged, err := Resolve(f.cfg, f.plan("-x task"))
	must(t, err)
	if flagged.Mode != "ultra" || flagged.Decision.Source != "flag" {
		t.Fatalf("an explicit flag wins over the assessment: %+v", flagged.Decision)
	}
	missing := f.plan("task")
	missing.Assessment = nil
	if _, err := Resolve(f.cfg, missing); err == nil {
		t.Fatal("without a flag the plan needs an assessment")
	}
}

func TestOneCommandTakesACleanTaskToTheEnd(t *testing.T) {
	f := newFixture(t)
	summary := f.start(f.plan("change the line"))
	if summary["step"] != StepDone || summary["mode"] != "medium" || len(needs(summary)) != 0 {
		t.Fatalf("a clean task is written, checked, reviewed and accepted by one command: %v", summary)
	}
	gate := Current()
	if f.count("writer") != 1 || f.count("review") != 1 || len(gate.Reviews) != 1 {
		t.Fatalf("one writer and one reviewer were paid for: %d writers, %d reviews", f.count("writer"), f.count("review"))
	}
	lane := gate.Reviews[0].Lanes[0]
	if lane.Route.Alias != "astra" || !slices.Equal(lane.Axes, []string{"correctness", "architecture", "simplicity"}) || len(gate.Reviews[0].Lanes) != 1 {
		t.Fatalf("medium has one reviewer for every axis, on a model that did not write the code: %+v", lane)
	}
	if gate.Final == nil || gate.Final.Summary != "implemented" {
		t.Fatalf("the completion record is assembled from the writers' reports: %+v", gate.Final)
	}
	if hook := StopHook(gate.Cwd, "fixture-session"); len(hook) != 0 {
		t.Fatalf("a finished, unchanged task must not block the stop: %v", hook)
	}
	must(t, os.WriteFile("b.txt", []byte("somebody else's edit\n"), 0o644))
	if hook := StopHook(gate.Cwd, "fixture-session"); len(hook) != 0 {
		t.Fatalf("an edit outside the task's files must not reopen a finished task: %v", hook)
	}
	f.fails("run", "the task is done", func() (any, error) { return Run(f.cfg, Current(), RunInput{}) })
}

func TestEvidenceIsReusedWhileItIsCurrent(t *testing.T) {
	f := newFixture(t)
	counter := filepath.Join(t.TempDir(), "runs")
	plan := f.plan("-l change the line")
	plan.Checks = []Check{{Argv: []string{"sh", "-c", "echo x >> " + counter}, Inputs: []string{"a.txt", "deps/**"}}}
	summary := f.start(plan)
	if summary["step"] != StepDecide || !slices.Equal(needs(summary), []string{"review"}) {
		t.Fatalf("light mode stops for the lead's own review: %v", summary)
	}
	runs := func() int { raw, _ := os.ReadFile(counter); return strings.Count(string(raw), "x") }
	if runs() != 1 {
		t.Fatalf("the declared check runs before any review, ran %d times", runs())
	}
	again := f.proceed()
	if runs() != 1 || again["step"] != StepDecide || f.count("review") != 0 {
		t.Fatalf("a run with nothing settled changes nothing and starts no model: %v", again)
	}
	must(t, os.WriteFile("b.txt", []byte("unrelated\n"), 0o644))
	f.do("check", func() (any, error) { return RunChecks(Current(), CheckInput{All: true}) })
	if runs() != 1 {
		t.Fatal("an unrelated edit must not invalidate a check with declared inputs")
	}
	must(t, os.MkdirAll("deps", 0o755))
	must(t, os.WriteFile("deps/new.txt", []byte("dependency\n"), 0o644))
	f.do("check", func() (any, error) { return RunChecks(Current(), CheckInput{All: true}) })
	if runs() != 2 {
		t.Fatal("a changed declared input must rerun the check")
	}
	// An edit that keeps a file's size, made in the second the index was written in, is invisible to git's
	// stat cache unless the snapshot keeps the index's own time. Evidence must go stale all the same.
	gitOut(t, "add", "a.txt")
	write(t, "a.txt", "AFTER\n")
	for written := time.Now().Unix(); time.Now().Unix() == written; {
		time.Sleep(20 * time.Millisecond)
	}
	f.fails("decide", "changed since the evidence was prepared", func() (any, error) {
		return Decide(f.cfg, Current(), DecideInput{Review: &LeadReview{Summary: "read the patch as it was"}})
	})
	if again := f.proceed(); runs() != 3 || !slices.Equal(needs(again), []string{"review"}) {
		t.Fatalf("the changed file is checked again and handed to the lead again: %v", again)
	}
	if done := f.decide(DecideInput{Review: &LeadReview{Summary: "read the one-line patch"}}); done["step"] != StepDone {
		t.Fatalf("the lead's review completes a light task: %v", done)
	}
}

func TestBlockedWriterIsAnsweredInItsOwnSession(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && call.Resume == "" {
			return `{"status":"BLOCKED","summary":"need the label","blockers":["Which label?"],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		if kind == "writer" && !strings.Contains(call.Prompt, "The label is answered") {
			t.Errorf("the answer must reach the writer: %s", call.Prompt)
		}
		return "", nil
	}
	blocked := f.start(f.plan("add the label"))
	if blocked["step"] != StepWrite || !slices.Equal(needs(blocked), []string{"answer"}) || offered(blocked)[0] != "answer" {
		t.Fatalf("a blocked writer leaves the task at write, asking for an answer: %v", blocked)
	}
	gateID, session := Current().ID, Current().latest()["core"].Session
	if again := f.proceed(); again["step"] != StepWrite || f.count("writer") != 1 {
		t.Fatalf("running again does not restart a writer that waits for an answer: %v", again)
	}
	if hook := StopHook(Current().Cwd, "fixture-session"); hook["decision"] != "block" {
		t.Fatal("a task that waits for the lead's answer must not let the turn end silently")
	}
	f.do("pause", func() (any, error) { return Pause(f.cfg, Current(), PauseInput{Reason: "the user decides the label"}) })
	if hook := StopHook(Current().Cwd, "fixture-session"); len(hook) != 0 {
		t.Fatal("an honest pause must not block the stop")
	}
	done := f.do("answer", func() (any, error) {
		return Answer(f.cfg, Current(), AnswerInput{Stream: "core", Answer: "The label is answered"})
	})
	writer := Current().latest()["core"]
	if done["step"] != StepDone || Current().ID != gateID || writer.Session != session || writer.Status != "COMPLETED" || Current().Pause != nil {
		t.Fatalf("the answer lifts the pause, continues the same session and runs the task on: %v %+v", done, writer)
	}
}

func TestFindingGoesToItsAuthorAndIsRecheckedInTheSameSessions(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && packet["correction"] != nil {
			must(t, os.WriteFile("a.txt", []byte("corrected\n"), 0o644))
			return `{"status":"COMPLETED","summary":"used the agreed value","blockers":[],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return "", nil
	}
	f.reviewOnce(finding("P1"), strings.Replace(finding("P3"), "wrong value", "naming", 1))
	summary := f.start(f.plan("change the line"))
	if summary["step"] != StepDone {
		t.Fatalf("a blocking finding is fixed by its author without the lead: %v", summary)
	}
	gate := Current()
	writers := gate.Writers
	if len(writers) != 2 || writers[1].Session != writers[0].Session || !gate.Round.Correction || !gate.Round.Auto {
		t.Fatalf("the writer that wrote the code corrects it in its own session: %+v", writers)
	}
	if len(gate.Reviews) != 2 || gate.Reviews[1].Lanes[0].Session != gate.Reviews[0].Lanes[0].Session || gate.Reviews[1].Lanes[0].Route.Effort != "medium" {
		t.Fatalf("the reviewer that raised the finding rechecks it in its own session: %+v", gate.Reviews[1].Lanes[0])
	}
	var blocking, note Finding
	for _, finding := range gate.Findings() {
		if finding.Severity == "P1" {
			blocking = finding
		} else {
			note = finding
		}
	}
	if _, resolved := gate.Resolutions[blocking.ID]; !resolved || !gate.Dispositions[blocking.ID].Auto {
		t.Fatalf("the blocking finding was accepted by the rule and confirmed fixed: %+v", gate.Dispositions[blocking.ID])
	}
	if _, judged := gate.Dispositions[note.ID]; judged || !gate.isNote(f.cfg, note) {
		t.Fatal("polish is a note: listed, never sent to a writer by itself")
	}
}

func TestWavesHandOffContracts(t *testing.T) {
	f := newFixture(t)
	var order []string
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" {
			stream := packet["stream"].(map[string]any)["name"].(string)
			f.mu.Lock()
			order = append(order, stream)
			f.mu.Unlock()
			if stream == "web" && packet["handoffs"].(map[string]any)["api"] != "contract of api" {
				t.Errorf("web must receive api's contract: %v", packet["handoffs"])
			}
		}
		return "", nil
	}
	plan := f.plan("-m build both", Stream{Name: "web", Domain: "frontend", Files: []string{"b.txt"}, After: []string{"api"}},
		Stream{Name: "api", Domain: "backend", Files: []string{"a.txt"}})
	plan.UI = nil
	f.start(plan)
	if !slices.Equal(order, []string{"api", "web"}) {
		t.Fatalf("api must finish before web starts: %v", order)
	}
	writers := Current().latest()
	if writers["api"].Route.Alias != "opus" || writers["web"].Route.Alias != "opus" {
		t.Fatalf("medium writes both domains with opus: %+v %+v", writers["api"].Route, writers["web"].Route)
	}
	cyclic := f.plan("-m x", Stream{Name: "a", Domain: "backend", Files: []string{"a.txt"}, After: []string{"b"}},
		Stream{Name: "b", Domain: "backend", Files: []string{"b.txt"}, After: []string{"a"}})
	if _, err := Resolve(f.cfg, cyclic); err == nil {
		t.Fatal("a dependency cycle must be rejected")
	}
}

func TestHeavyNeedsAcceptedArchitecture(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-h change the contract")
	f.fails("start", "architecture", func() (any, error) { return Start(f.cfg, plan, "", false, RunInput{}) })
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	if f.count("architecture") != 2 || len(receipt["lanes"].([]any)) != 2 {
		t.Fatalf("heavy runs two independent designs: %v", receipt)
	}
	f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	if f.count("architecture") != 2 {
		t.Fatal("a finished design pass must be reused, not paid for again")
	}
	plan.Architecture = &Accepted{ID: receipt["id"].(string), Acceptance: "the first design, with the second one's boundary"}
	changed := *plan
	changed.Brief = "A different contract"
	f.fails("start", "changed since the architecture pass", func() (any, error) { return Start(f.cfg, &changed, "", false, RunInput{}) })
	criteria := *plan
	criteria.Requirements = []Requirement{{ID: "R1", Text: "The contract holds"}}
	if _, err := Resolve(f.cfg, &criteria); err != nil {
		t.Fatal(err)
	}
	resolved, _ := Resolve(f.cfg, &criteria)
	if err := resolved.requireArchitecture(); err != nil {
		t.Fatalf("adding a criterion must not throw an accepted design away: %v", err)
	}
	if summary := f.start(plan); summary["mode"] != "heavy" || summary["step"] != StepDone {
		t.Fatalf("start: %v", summary)
	}
	lanes := Current().Reviews[0].Lanes
	if len(lanes) != 2 || lanes[0].Route.Alias == lanes[1].Route.Alias {
		t.Fatalf("heavy reviews two axes on two models: %+v", lanes)
	}
}

func TestUltraRequiresAnAnswerForEveryBlocker(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "architecture" && packet["round"] == "critique" {
			return `{"summary":"critique","agreements":"","blockers":[{"id":"race","evidence":"a.txt:1","remedy":"lock"}]}`, nil
		}
		return "", nil
	}
	plan := f.plan("-x move the money")
	receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
	if f.count("architecture") != 4 {
		t.Fatalf("ultra runs two designs and two critiques, ran %d", f.count("architecture"))
	}
	sessions := map[string]string{}
	for _, call := range f.calls[:2] {
		sessions[call.Dir] = call.Route.Alias
	}
	for _, call := range f.calls[2:] {
		if call.Resume == "" || sessions[call.Dir] != call.Route.Alias || call.Route.Effort != "high" {
			t.Fatalf("a critique continues the session of the architect that read the code, one step lower: %+v", call.Route)
		}
	}
	plan.Architecture = &Accepted{ID: receipt["id"].(string), Acceptance: "accepted"}
	f.fails("start", "blocker", func() (any, error) { return Start(f.cfg, plan, "", false, RunInput{}) })
	plan.Architecture.Blockers = map[string]string{"fable/race": "resolved by the lock", "astra/race": "same root cause"}
	f.start(plan)
	if writer := Current().latest()["core"]; writer.Route.Alias != "opus" || writer.Route.Effort != "xhigh" {
		t.Fatalf("ultra writes with opus at xhigh by default, never max, got %s", writer.Route.Alias)
	}
	for _, lane := range Current().Reviews[0].Lanes {
		if lane.Route.Model == Current().latest()["core"].Route.Model {
			t.Fatalf("the author's model must never review its code: %+v", lane.Route)
		}
	}
}

func TestExhaustedPoolFallsBackAndIsReported(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" && call.Route.Alias == "astra" {
			return "", provider.ErrLimit
		}
		return "", nil
	}
	summary := f.start(f.plan("change the line"))
	lane := Current().Reviews[0].Lanes[0]
	if receipt := summary["receipt"].(map[string]any); lane.Route.Alias != "fable" || lane.Route.SubstitutedFrom != "astra:medium" || len(receipt["substitutions"].([]any)) != 1 {
		t.Fatalf("the declared alternate must run and be reported: %+v %v", lane.Route, summary["substitutions"])
	}
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		return "", errors.New("ordinary failure")
	}
	if _, _, err := provider.RunRouted(f.cfg, provider.Call{Route: f.cfg.MustRoute("opus:high"), Dir: t.TempDir()}, nil); err == nil ||
		f.calls[len(f.calls)-1].Route.Alias != "opus" {
		t.Fatal("only an exhausted limit triggers a substitute")
	}
}

func TestLightModeIsReviewedByTheLead(t *testing.T) {
	f := newFixture(t)
	corrections := 0
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "writer" || packet["correction"] == nil {
			return "", nil
		}
		correction := packet["correction"].(map[string]any)
		id := correction["findings"].([]any)[0].(map[string]any)["id"].(string)
		if corrections++; corrections == 1 {
			// The author checks the finding against its brief first, and here it objects.
			return `{"status":"COMPLETED","summary":"left the value as the brief has it","blockers":[],"handoff":"","deviations":[],` +
				`"disputes":[{"id":"` + id + `","evidence":"the brief asks for this value: a.txt:1"}]}`, nil
		}
		if upheld := correction["upheld"].([]any); len(upheld) != 1 || upheld[0] != id {
			t.Errorf("a finding its reviewer kept is to be fixed, not disputed again: %v", correction["upheld"])
		}
		must(t, os.WriteFile("a.txt", []byte("corrected\n"), 0o644))
		return `{"status":"COMPLETED","summary":"used the agreed value","blockers":[],"handoff":"","deviations":[],"disputes":[]}`, nil
	}
	plan := f.plan("-l fix the typo")
	plan.Checks = nil
	handed := f.start(plan)
	if handed["step"] != StepDecide || f.count("review") != 0 {
		t.Fatalf("light hands the patch to the lead and starts no reviewer: %v", handed)
	}
	need := handed["needs"].([]any)[0].(map[string]any)
	patch, _ := os.ReadFile(need["detail"].(map[string]any)["patch"].(string))
	if need["kind"] != "review" || !strings.Contains(string(patch), "+after") || !strings.Contains(string(patch), "-before") {
		t.Fatalf("the lead is handed one patch of the change: %v\n%s", need, patch)
	}
	f.fails("decide", "summary", func() (any, error) { return Decide(f.cfg, Current(), DecideInput{Review: &LeadReview{}}) })
	wrong := Finding{Severity: "P1", Confidence: "high", Category: "logic", Path: "a.txt", Line: 1, Evidence: "wrong value",
		Impact: "wrong result", Remedy: "use the agreed value", Preserve: "the format"}
	disputed := f.decide(DecideInput{Review: &LeadReview{Summary: "read the one-line patch", Findings: []Finding{wrong}}})
	need = disputed["needs"].([]any)[0].(map[string]any)
	packet := fileText(t, need["detail"].(map[string]any)["packet"].(string))
	if disputed["step"] != StepDecide || f.count("writer") != 2 || need["kind"] != "review" || !strings.Contains(need["what"].(string), "disputes 1 of your findings") ||
		!strings.Contains(packet, "the brief asks for this value") {
		t.Fatalf("the lead raised the finding, so the lead is shown its author's objection: %v", disputed)
	}
	// Reported again under its id, the finding is upheld and goes back to be fixed; left out, it would be withdrawn.
	wrong.ID = Current().Findings()[0].ID
	found := f.decide(DecideInput{Review: &LeadReview{Summary: "the objection does not hold: the agreed value is another", Findings: []Finding{wrong}}})
	if found["step"] != StepDecide || f.count("writer") != 3 || !slices.Equal(needs(found), []string{"review"}) || fileText(t, "a.txt") != "corrected\n" {
		t.Fatalf("an upheld finding is corrected, and the corrected patch comes back to the lead: %v", found)
	}
	if done := f.decide(DecideInput{Review: &LeadReview{Summary: "the correction is right"}}); done["step"] != StepDone {
		t.Fatalf("the lead's clean review completes the task: %v", done)
	}
}

func TestBaselineFailureIsRecordedButDoesNotWaiveCurrentFailures(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("change the line")
	plan.Checks = []Check{{Argv: []string{"false"}, Baseline: true}, {Argv: []string{"sh", "-c", "exit 3"}}}
	summary := f.start(plan)
	if summary["step"] != StepDecide || !slices.Equal(needs(summary), []string{"stall"}) || f.count("review") != 0 {
		t.Fatalf("failing checks go back to the writer once; a second failure asks for a new diagnosis, and no reviewer is paid: %v", summary)
	}
	gate := Current()
	if f.count("writer") != 2 || len(gate.Round.Failures) != 2 {
		t.Fatalf("the writer got both failing checks in one correction: %+v", gate.Round)
	}
	current := gate.currentReceipts()
	first, second := current[checkID([]string{"false"})], current[checkID([]string{"sh", "-c", "exit 3"})]
	if first == nil || second == nil || !first.Preexisting || second.Preexisting || passed(first) {
		t.Fatalf("only the check that was red before the task is marked preexisting, and it still fails: %+v %+v", first, second)
	}
	f.decide(DecideInput{Work: []WorkOrder{{Stream: "core", Note: "the checks cannot pass here: repair the fixture"}}})
	if gate = Current(); gate.Boost != 1 || len(gate.Corrections[len(gate.Corrections)-1].Work) != 1 {
		t.Fatalf("a new diagnosis earns the writers one step of effort: boost %d", gate.Boost)
	}
}

func TestAnotherSessionCannotClaimOwnedFiles(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-l change the line")
	plan.Checks = nil
	f.start(plan) // light: stays active, waiting for the lead's review
	t.Setenv("AGENTGATE_SESSION", "other-session")
	f.fails("start", "another session", func() (any, error) { return Start(f.cfg, f.plan("change it too"), "", false, RunInput{}) })
	overlapping := f.plan("change by pattern", Stream{Name: "core", Domain: "backend", Files: []string{"*.txt"}})
	f.fails("start", "another session", func() (any, error) { return Start(f.cfg, overlapping, "", false, RunInput{}) })
	other := f.plan("change the neighbour", Stream{Name: "core", Domain: "backend", Files: []string{"b.txt"}})
	if summary := f.start(other); summary["step"] != StepDone {
		t.Fatalf("disjoint files in a parallel session must work: %v", summary)
	}
	t.Setenv(provider.WorkerEnv, "1")
	if _, err := Execute("run", func() (any, error) { return nil, nil }); err == nil {
		t.Fatal("a worker must not drive the task")
	}
}

func TestStopCancelsTheRealProcessAndParksTheTask(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap is not installed")
	}
	f := newFixture(t)
	provider.Run = f.real // the real launcher, sandbox and receipts, with a fake codex that never finishes
	cwd, _ := os.Getwd()
	bin := filepath.Join(cwd, "fixture-bin")
	must(t, os.MkdirAll(bin, 0o755))
	must(t, os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\ncat >/dev/null\necho partial > a.txt\nsleep 60\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	stopped := make(chan any, 1)
	go func() {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if raw, _ := os.ReadFile("a.txt"); string(raw) == "partial\n" {
				break
			}
		}
		value, _ := Stop(f.cfg)
		stopped <- value
	}()
	_, err := Execute("start", func() (any, error) { return Start(f.cfg, f.plan("-l change the line"), "", false, RunInput{}) })
	if !ops.IsCancelled(err) {
		t.Fatalf("want a cancellation, got %v", err)
	}
	if value := (<-stopped).(map[string]any); resultOf(value)["cancellation_requested"] != true {
		t.Fatalf("stop must request cancellation: %v", value)
	}
	gate := Current()
	if gate.Step != StepWrite || gate.Pause == nil || gate.Pause.Kind != PauseUser || gate.latest()["core"].Status != "CANCELLED" {
		t.Fatalf("the task must be parked as stopped by the user with a cancelled writer: %s %+v %+v", gate.Step, gate.Pause, gate.latest()["core"])
	}
	if raw, _ := os.ReadFile("a.txt"); string(raw) != "partial\n" {
		t.Fatal("partial work must be preserved")
	}
	if record := ops.Status(home()); record.State != "cancelled" || record.Live {
		t.Fatalf("the operation receipt must say cancelled: %+v", record)
	}
	refused, err := Execute("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{}) })
	var refusal *Refusal
	if !errors.As(err, &refusal) || refused != nil || !slices.Equal(refusal.Instead, []Action{ActResume, ActStop}) {
		t.Fatalf("after a user stop only an explicit resume continues: %v", err)
	}
}

func TestInstallIsIdempotentAndKeepsOtherHooks(t *testing.T) {
	isolateHomes(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	settings := `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"/old/claude-dev quality-hook"},` +
		`{"type":"command","command":"other-tool stop"}]}]}}`
	must(t, os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600))
	_, err := Install(InstallOptions{Lead: "claude"})
	must(t, err)
	second, err := Install(InstallOptions{})
	must(t, err)
	if second.(map[string]any)["note"] != "already up to date" {
		t.Fatalf("a second install must change nothing: %v", second)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	text := string(raw)
	if strings.Count(text, selfPath()+" hook") != 1 || strings.Contains(text, "quality-hook") || !strings.Contains(text, "other-tool stop") ||
		!strings.Contains(text, `"model": "opus"`) {
		t.Fatalf("exactly one hook of ours, foreign hooks and settings kept: %s", text)
	}
	if command, _ := os.ReadFile(filepath.Join(dir, "commands", "dev.md")); !strings.Contains(string(command), "agentgate guide") {
		t.Fatal("the /dev command must be installed")
	}
	_, err = Install(InstallOptions{Uninstall: true})
	must(t, err)
	if raw, _ := os.ReadFile(filepath.Join(dir, "settings.json")); strings.Contains(string(raw), selfPath()+" hook") {
		t.Fatal("uninstall must remove the hook")
	}
}
