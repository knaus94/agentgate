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
	os.Unsetenv(provider.WorkerEnv)
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
	}
	if json.Unmarshal([]byte(answer), &scripted) != nil {
		return answer
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
	raw, _ := json.Marshal(map[string]any{"reports": mine})
	return string(raw)
}

func kindOf(call provider.Call) string {
	switch {
	case call.Writer:
		return "writer"
	case call.MCP["playwright"].Command != "":
		return "visual"
	case strings.Contains(call.Dir, "/scout/"):
		return "scout"
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
	case "scout":
		return `{"summary":"a.txt holds one line","files":[{"path":"a.txt","role":"owned","symbols":[]}],"callers":[],` +
			`"invariants":["keep the line format"],"conventions":[],"unknowns":[]}`
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
			_ = os.WriteFile(file.(string), []byte("after\n"), 0o644)
		}
		return `{"status":"COMPLETED","summary":"implemented","blockers":[],"handoff":"contract of ` + stream["name"].(string) + `","deviations":[]}`
	case "architecture":
		if packet["round"] == "critique" {
			return `{"summary":"critique","agreements":"sound","blockers":[]}`
		}
		return `{"summary":"design","design":"keep the contract","risks":[],"open_questions":[]}`
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
	raw, _ := json.Marshal(map[string]any{"reports": reports})
	return string(raw)
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

func (f *fixture) do(action string, run func() (any, error)) map[string]any {
	f.t.Helper()
	value, err := Execute(action, run)
	must(f.t, err)
	raw, _ := json.Marshal(value)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func (f *fixture) fails(action, want string, run func() (any, error)) {
	f.t.Helper()
	if _, err := Execute(action, run); err == nil || !strings.Contains(err.Error(), want) {
		f.t.Fatalf("%s: want an error containing %q, got %v", action, want, err)
	}
}

func (f *fixture) launch(plan *Plan) map[string]any {
	return f.do("launch", func() (any, error) { return Launch(f.cfg, plan) })
}

func (f *fixture) review() map[string]any {
	return f.do("review", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{}) })
}

func (f *fixture) finish() map[string]any {
	return f.do("finish", func() (any, error) {
		return Finish(f.cfg, Current(), FinishInput{Summary: "done", ArchitectureEvidence: "matches the accepted design"})
	})
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
		if got := decide(&c.assessment, c.files, 1); got.Mode != c.want || len(got.Because) == 0 {
			t.Errorf("%+v with %d files: want %s, got %s", c.assessment, c.files, c.want, got.Mode)
		}
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

func TestMediumFlowReusesCurrentEvidence(t *testing.T) {
	f := newFixture(t)
	counter := filepath.Join(t.TempDir(), "runs")
	plan := f.plan("change the line")
	plan.Checks = []Check{{Argv: []string{"sh", "-c", "echo x >> " + counter}, Inputs: []string{"a.txt", "deps/**"}}}
	if summary := f.launch(plan); summary["phase"] != "awaiting-review" || summary["mode"] != "medium" {
		t.Fatalf("launch: %v", summary)
	}
	if summary := f.review(); summary["phase"] != "reviewed" {
		t.Fatalf("review: %v", summary)
	}
	f.review() // identical content and contract: the reviewer is not paid twice
	if f.count("review") != 2 {
		t.Fatalf("review ran %d times for identical content", f.count("review"))
	}
	lane := Current().Reviews[0].Lanes[0]
	if lane.Route.Alias != "fable" || !slices.Equal(lane.Axes, []string{"architecture", "simplicity"}) || len(Current().Reviews[0].Lanes) != 2 {
		t.Fatalf("medium reviews design and simplicity on one model and correctness on another, got %+v", lane)
	}
	runs := func() int { raw, _ := os.ReadFile(counter); return strings.Count(string(raw), "x") }
	if runs() != 1 {
		t.Fatalf("the outstanding check must run alongside the review, ran %d times", runs())
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
	if summary := f.finish(); summary["phase"] != "complete" {
		t.Fatalf("finish: %v", summary)
	}
	if hook := StopHook(Current().Cwd, "fixture-session"); len(hook) != 0 {
		t.Fatalf("a finished, unchanged task must not block the stop: %v", hook)
	}
}

func TestBlockedWriterIsAnsweredInItsOwnSession(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && call.Resume == "" {
			return `{"status":"BLOCKED","summary":"need the label","blockers":["Which label?"],"handoff":"","deviations":[]}`, nil
		}
		if kind == "writer" && !strings.Contains(call.Prompt, "The label is answered") {
			t.Errorf("the answer must reach the writer: %s", call.Prompt)
		}
		return "", nil
	}
	held := f.launch(f.plan("add the label"))
	if held["phase"] != "held" || held["resume_phase"] != "implementing" {
		t.Fatalf("a blocked writer holds the task: %v", held)
	}
	gateID, session := Current().ID, Current().latest()["core"].Session
	f.fails("launch", "on hold", func() (any, error) { return Launch(f.cfg, f.plan("add the label")) })
	if hook := StopHook(Current().Cwd, "fixture-session"); len(hook) != 0 {
		t.Fatal("an honest hold must not block the stop")
	}
	done := f.do("answer", func() (any, error) {
		return Answer(f.cfg, Current(), AnswerInput{Stream: "core", Answer: "The label is answered"})
	})
	writer := Current().latest()["core"]
	if done["phase"] != "awaiting-review" || Current().ID != gateID || writer.Session != session || writer.Status != "COMPLETED" {
		t.Fatalf("the same gate and session must complete: %v %+v", done, writer)
	}
	if hook := StopHook(Current().Cwd, "fixture-session"); hook["decision"] != "block" {
		t.Fatal("an unreviewed task must block the stop")
	}
}

func TestFindingLifecycleUsesTheSameSessions(t *testing.T) {
	f := newFixture(t)
	reviews := 0
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "review" {
			return "", nil
		}
		if packet["axes"].([]any)[0] != "correctness" {
			return "", nil
		}
		reviews++
		if reviews == 1 {
			return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + finding("P1") + `,` +
				strings.Replace(finding("P3"), "wrong value", "naming", 1) + `],"checked":["read"],"resolutions":[]}]}`, nil
		}
		return "", nil
	}
	f.launch(f.plan("change the line"))
	summary := f.review()
	if len(summary["untriaged"].([]any)) != 1 || len(summary["notes"].([]any)) != 1 {
		t.Fatalf("P1 needs triage, P3 is a note: %v", summary)
	}
	id := summary["untriaged"].([]any)[0].(string)
	f.fails("finish", "triage", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "done"}) })
	f.do("triage", func() (any, error) {
		return Triage(f.cfg, Current(), TriageInput{[]Disposition{{ID: id, Decision: "accepted", Evidence: "verified in a.txt"}}})
	})
	f.fails("finish", "block", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "done"}) })
	writerSession, reviewSession := Current().latest()["core"].Session, Current().Reviews[0].Lanes[1].Session
	fixed := f.do("fix", func() (any, error) { return Fix(f.cfg, Current(), FixInput{Brief: "use the agreed value"}) })
	if fixed["phase"] != "awaiting-review" || Current().latest()["core"].Session != writerSession {
		t.Fatalf("the writer that wrote the code corrects it in its session: %v", fixed)
	}
	must(t, os.WriteFile("a.txt", []byte("corrected\n"), 0o644))
	if summary := f.review(); len(summary["blocking"].([]any)) != 0 {
		t.Fatalf("the resolved finding must not block: %v", summary)
	}
	recheck := Current().Reviews[1].Lanes
	if len(recheck) != 1 || recheck[0].Axes[0] != "correctness" || recheck[0].Session != reviewSession {
		t.Fatalf("only the lane that raised the finding rechecks, in its own session: %+v", recheck)
	}
	if _, resolved := Current().Resolutions[id]; !resolved {
		t.Fatal("the accepted finding must be recorded as resolved")
	}
	f.finish()
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
	f.launch(plan)
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
	f.fails("launch", "architecture", func() (any, error) { return Launch(f.cfg, plan) })
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
	f.fails("launch", "changed since the architecture pass", func() (any, error) { return Launch(f.cfg, &changed) })
	if summary := f.launch(plan); summary["mode"] != "heavy" {
		t.Fatalf("launch: %v", summary)
	}
	f.review()
	lanes := Current().Reviews[0].Lanes
	if len(lanes) != 2 || lanes[0].Route.Alias == lanes[1].Route.Alias {
		t.Fatalf("heavy reviews two axes on two models: %+v", lanes)
	}
	f.fails("finish", "architecture_evidence", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "done"}) })
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
	plan.Architecture = &Accepted{ID: receipt["id"].(string), Acceptance: "accepted"}
	f.fails("launch", "blocker", func() (any, error) { return Launch(f.cfg, plan) })
	plan.Architecture.Blockers = map[string]string{"fable/race": "resolved by the lock", "astra/race": "same root cause"}
	f.launch(plan)
	if writer := Current().latest()["core"]; writer.Route.Alias != "fable" || writer.Route.Effort != "xhigh" {
		t.Fatalf("ultra writes with fable at xhigh by default, never max, got %s", writer.Route.Alias)
	}
	f.review()
	for _, lane := range Current().Reviews[0].Lanes {
		if lane.Route.Model == Current().latest()["core"].Route.Model {
			t.Fatalf("the author's model must never review its code: %+v", lane.Route)
		}
	}
}

func TestExhaustedPoolFallsBackAndIsReported(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "review" && call.Route.Alias == "fable" {
			return "", provider.ErrLimit
		}
		return "", nil
	}
	f.launch(f.plan("change the line"))
	summary := f.review()
	lane := Current().Reviews[0].Lanes[0]
	if lane.Route.Alias != "astra" || lane.Route.SubstitutedFrom != "fable:medium" || len(summary["substitutions"].([]any)) != 1 {
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
	plan := f.plan("-l fix the typo")
	plan.Checks = nil
	f.launch(plan)
	handed := f.review()
	if handed["evidence"] == nil || f.count("review") != 0 {
		t.Fatalf("light hands the diff to the lead and starts no reviewer: %v", handed)
	}
	f.do("review", func() (any, error) {
		return ReviewTask(f.cfg, Current(), ReviewInput{LeadReview: &LeadReview{Summary: "read the one-line diff"}})
	})
	if summary := f.finish(); summary["phase"] != "complete" {
		t.Fatalf("finish: %v", summary)
	}
}

func TestBaselineFailureDoesNotHoldTheTaskHostage(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("change the line")
	plan.Checks = []Check{{Argv: []string{"false"}, Baseline: true}, {Argv: []string{"sh", "-c", "exit 3"}}}
	f.launch(plan)
	result, err := Execute("check", func() (any, error) { return RunChecks(Current(), CheckInput{All: true}) })
	must(t, err)
	receipts := result.(*CheckResult)
	if !receipts.Failed || !receipts.Receipts[0].Preexisting || receipts.Receipts[1].Preexisting {
		t.Fatalf("only the check that was red before the task is excused: %+v", receipts)
	}
	f.do("review", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{SkipChecks: true}) })
	f.fails("finish", "exit 3", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "done"}) })
}

func TestAnotherSessionCannotClaimOwnedFiles(t *testing.T) {
	f := newFixture(t)
	f.launch(f.plan("change the line"))
	t.Setenv("AGENTGATE_SESSION", "other-session")
	f.fails("launch", "another session", func() (any, error) { return Launch(f.cfg, f.plan("change it too")) })
	other := f.plan("change the neighbour", Stream{Name: "core", Domain: "backend", Files: []string{"b.txt"}})
	if summary := f.launch(other); summary["phase"] != "awaiting-review" {
		t.Fatalf("disjoint files in a parallel session must work: %v", summary)
	}
	t.Setenv(provider.WorkerEnv, "1")
	if _, err := Execute("finish", func() (any, error) { return nil, nil }); err == nil {
		t.Fatal("a worker must not drive the gate")
	}
}

func TestStopCancelsTheRealProcessAndHoldsTheGate(t *testing.T) {
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
	_, err := Execute("launch", func() (any, error) { return Launch(f.cfg, f.plan("-l change the line")) })
	if !ops.IsCancelled(err) {
		t.Fatalf("want a cancellation, got %v", err)
	}
	if value := (<-stopped).(map[string]any); value["cancellation_requested"] != true {
		t.Fatalf("stop must request cancellation: %v", value)
	}
	gate := Current()
	if gate.Phase != "held" || gate.latest()["core"].Status != "CANCELLED" {
		t.Fatalf("the gate must be held with a cancelled writer: %s %+v", gate.Phase, gate.latest()["core"])
	}
	if raw, _ := os.ReadFile("a.txt"); string(raw) != "partial\n" {
		t.Fatal("partial work must be preserved")
	}
	if record := ops.Status(home()); record.State != "cancelled" || record.Live {
		t.Fatalf("the operation receipt must say cancelled: %+v", record)
	}
}

func TestInstallIsIdempotentAndKeepsOtherHooks(t *testing.T) {
	isolateHomes(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	settings := `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"/old/claude-dev quality-hook"},` +
		`{"type":"command","command":"other-tool stop"}]}]}}`
	must(t, os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600))
	_, err := Install(InstallOptions{})
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
