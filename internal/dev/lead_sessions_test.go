package dev

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/knaus94/agentgate/internal/provider"
)

// Regressions found in real lead sessions of 0.16: each test is one defect a lead ran into.

func TestARequestKeepsItsFlagsBehindTheCommandName(t *testing.T) {
	for raw, want := range map[string]Request{
		"/dev -m --grill study the brief": {Action: "implement", Mode: "medium", Grill: "force", Task: "study the brief"},
		"$dev -h plan move the totals":    {Action: "plan", Mode: "heavy", Grill: "auto", Task: "move the totals"},
		"-m --plan adapt the roulette":    {Action: "plan", Mode: "medium", Grill: "auto", Task: "adapt the roulette"},
		"/dev --review":                   {Action: "review", Grill: "auto", Task: "current task changes"},
	} {
		got, err := ParseRequest(raw)
		must(t, err)
		if got.Action != want.Action || got.Mode != want.Mode || got.Grill != want.Grill || got.Task != want.Task {
			t.Errorf("%q: want %+v, got %+v", raw, want, got)
		}
	}
	f := newFixture(t)
	resolved, err := Resolve(f.cfg, f.plan("/dev -m --grill study the brief"))
	must(t, err)
	if resolved.Mode != "medium" || resolved.Decision.Source != "flag" {
		t.Fatalf("a plan whose request repeats the command keeps the user's mode: %+v", resolved.Decision)
	}
	if name := slug(&Plan{Request: "/dev -m --grill add Double to calc"}); name != "add-double-calc" {
		t.Fatalf("a plan is named after its task, not after the command and its flags: %s", name)
	}
}

func TestStreamsThatCannotShareAFileAreNotRefused(t *testing.T) {
	for _, pair := range [][2]string{
		{"pages/random/graphql/**", "pages/random/*"},
		{"pages/random/graphql/**", "pages/random/+*"},
		{"src/*.ts", "src/*.tsx"},
		{"api/[a-m]*.go", "api/[n-z]*.go"},
	} {
		if left, right, collide := firstOverlap([]string{pair[0]}, []string{pair[1]}); collide {
			t.Errorf("%s and %s cannot own the same file, yet were refused", left, right)
		}
	}
	for _, pair := range [][2]string{
		{"a/**", "a/b/*.ts"},
		{"src/**/*.tsx", "src/pages/x.tsx"},
		{"migrations/*_orders", "migrations/2026_orders/migration.sql"},
		{"api/[a-m]*.go", "api/k*.go"},
	} {
		if _, _, collide := firstOverlap([]string{pair[0]}, []string{pair[1]}); !collide {
			t.Errorf("%s and %s can own the same file and must be refused", pair[0], pair[1])
		}
	}
	f := newFixture(t)
	plan := f.plan("-m split the page", Stream{Name: "codegen", Domain: "frontend", Files: []string{"pages/random/graphql/**"}},
		Stream{Name: "page", Domain: "frontend", Files: []string{"pages/random/*"}, After: []string{"codegen"}})
	plan.UI = &UI{Skip: "fixture"}
	if _, err := Resolve(f.cfg, plan); err != nil {
		t.Fatalf("two streams of disjoint directories are one plan: %v", err)
	}
}

func TestAPatternThatNamesADirectoryOwnsWhatIsInside(t *testing.T) {
	generated := compilePattern("prisma/migrations/*_giveaway")
	if !generated.match("prisma/migrations/20261115_giveaway/migration.sql") {
		t.Fatal("a migration directory named by a pattern owns the file a generator writes inside it")
	}
	if compilePattern("pages/random/*").match("pages/random/graphql/query.ts") {
		t.Fatal("`*` names the files of a directory, not what is below its subdirectories")
	}
	if compilePattern("src/*.ts").match("src/a.ts/b.ts") || compilePattern("src/**").match("src") {
		t.Fatal("a name with an extension, or the contents of a directory, owns no directory")
	}
	scope, err := normalizeScope([]string{"prisma/migrations/*_giveaway/"})
	must(t, err)
	if !slices.Equal(scope, []string{"prisma/migrations/*_giveaway/**"}) {
		t.Fatalf("a pattern written as a directory owns everything below it: %v", scope)
	}
}

func TestARequirementSentAsWorkIsNotAStall(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m cover the access rule")
	plan.Requirements = []Requirement{{ID: "R1", Text: "a foreign owner is refused"}}
	reviews := 0
	f.raw = true
	f.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		if kind != "review" || packet["axes"] == nil {
			return "", nil
		}
		reviews++
		quality := fixtureQuality(packet)
		var reports []map[string]any
		for _, axis := range packet["axes"].([]any) {
			report := map[string]any{"axis": axis, "status": "PASS", "findings": []any{}, "checked": []string{"read the diff"}, "resolutions": []any{}}
			switch {
			case reviews == 1 && axis == "correctness":
				var blocking map[string]any
				must(t, json.Unmarshal([]byte(finding("P1")), &blocking))
				report["status"], report["findings"] = "NEEDS_FIX", []any{blocking}
			case reviews == 2:
				// The correction changed no byte and resolved the finding; the criterion still lacks code.
				var resolutions []any
				for _, accepted := range packet["accepted_findings"].([]any) {
					resolutions = append(resolutions, map[string]any{"id": accepted.(map[string]any)["id"], "resolved": true, "evidence": "fixed"})
				}
				report["resolutions"] = resolutions
				quality.Requirements[0].Status, quality.Requirements[0].Cause, quality.Requirements[0].Reason = "partial", "proof", "the foreign owner is not exercised"
			}
			reports = append(reports, report)
		}
		raw, err := json.Marshal(map[string]any{"reports": reports, "quality": quality})
		return string(raw), err
	}
	waiting := f.start(plan)
	if !slices.Equal(needs(waiting), []string{"requirement"}) {
		t.Fatalf("the criterion is unmet after a correction that changed nothing: %v", waiting)
	}
	writers := f.count("writer")
	sent := f.decide(DecideInput{Work: []WorkOrder{{Stream: "core", Requirement: "R1", Note: "Add the foreign owner"}}})
	if slices.Contains(needs(sent), "stall") || f.count("writer") != writers+1 {
		t.Fatalf("work the lead sends is given to the writer, never read as a stalled correction: %v", sent)
	}
}

func TestAFailingCheckIsRunAgainAfterTheCorrectionAimedAtIt(t *testing.T) {
	f := newFixture(t)
	outside := filepath.Join(t.TempDir(), "repaired")
	plan := f.plan("-m make the check pass")
	plan.Checks = []Check{{Argv: []string{"test", "-f", outside}}}
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind == "writer" && f.count("writer") > 1 {
			// The correction repairs what the check needs without changing a file of the repository.
			write(t, outside, "repaired\n")
			return `{"status":"COMPLETED","summary":"repaired what the check needs","blockers":[],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return "", nil
	}
	if done := f.start(plan); done["step"] != StepDone || f.count("writer") != 2 {
		t.Fatalf("the check that failed before the correction runs again after it, and passes: %v", done)
	}
}

func TestACheckThatFailsOnUnchangedFilesGoesToTheLead(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m keep the service check green")
	plan.Checks = []Check{{Argv: []string{"sh", "-c", "test ! -f service-down"}}}
	f.start(plan)
	gate := Current()
	// A finished task is reopened; the service the check needs has stopped meanwhile.
	write(t, "service-down", "stopped\n")
	must(t, gate.reopen())
	must(t, gate.save())
	writers := f.count("writer")
	waiting := f.proceed()
	if !slices.Equal(needs(waiting), []string{"setup"}) || f.count("writer") != writers ||
		!strings.Contains(fmt.Sprint(waiting["needs"]), "passed on these same files before") {
		t.Fatalf("a check that fails on the same files goes to the lead, not to the writers: %v", waiting)
	}
	must(t, os.Remove("service-down"))
	if done := f.proceed(); done["step"] != StepDone || f.count("writer") != writers {
		t.Fatalf("once the service is back the check runs again and the task finishes without a writer: %v", done)
	}
}

func TestABlockedStreamDoesNotHoldBackTheOthers(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && call.Resume == "" && packet["stream"].(map[string]any)["name"] == "page" {
			return `{"status":"BLOCKED","summary":"need the query","blockers":["Which query feeds the page?"],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return "", nil
	}
	plan := f.plan("-m build the page", Stream{Name: "page", Domain: "backend", Files: []string{"a.txt"}},
		Stream{Name: "docs", Domain: "backend", Files: []string{"context.txt"}})
	f.start(plan)
	if gate := Current(); gate.latest()["docs"].Status != "COMPLETED" || gate.latest()["page"].Status != "BLOCKED" {
		t.Fatalf("an independent stream completes while another waits for an answer: %+v", gate.latest())
	}
	// The lead adds the stream the blocked one turns out to need; answering runs it first.
	revised := clonePlan(t, Current().Plan)
	revised.Stages[0].Streams = append(revised.Stages[0].Streams, Stream{Name: "query", Domain: "backend", Files: []string{"b.txt"}})
	revised.Stages[0].Streams[0].After = []string{"query"}
	revised.Update = "the page needs a query of its own"
	f.amend(revised)
	done := f.do("answer", func() (any, error) {
		return Answer(f.cfg, Current(), AnswerInput{Stream: "page", Answer: "use the new query"})
	})
	if done["step"] != StepDone || fileText(t, "b.txt") != "after\n" {
		t.Fatalf("the stream the blocked one waits for runs before the answer is delivered: %v", done)
	}
}

func TestADesignScreenIsMadeWhereScreensAreKept(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m implement the result screen")
	plan.Design = []DesignRef{{Figma: "https://www.figma.com/design/key/File?node-id=1-2", Screen: "result-participants",
		URL: "http://localhost:4200/ru/murz", Root: "[data-result]", Stream: "core"}}
	resolved := &Resolved{Plan: plan, Config: f.cfg}
	if problems := resolved.validateDesign(); len(problems) > 0 {
		t.Fatalf("a design link with a bare screen name is a valid design: %v", problems)
	}
	if screen := plan.Design[0].Screen; screen != filepath.Join(".agentgate", "design", "result-participants") {
		t.Fatalf("a screen the tool makes goes under the screens directory, not into the repository root: %s", screen)
	}
	gate := &Gate{Plan: plan}
	if !gate.designFile(".agentgate/design/result-participants/package/assets/copy.svg") || gate.designFile("apps/client/page.tsx") {
		t.Fatal("the tool's own design files are not changes of the task, and nothing else is excused")
	}
}

func TestALayoutCheckThatFindsNoRootGoesToTheWriter(t *testing.T) {
	f := newFixture(t)
	dir := layoutFixture(t)
	plan := f.plan("-m implement the page")
	plan.Design = []DesignRef{screenFixture(t)}
	// The page opens where it is asked to and does not show the element: the writer named it differently.
	missing := filepath.Join(dir, "missing")
	script := fileText(t, filepath.Join(dir, "layout"))
	write(t, filepath.Join(dir, "layout"), strings.Replace(script, "exit 0\n", `if [ -f "`+missing+`" ]; then echo 'check: no element matches #page at http://localhost/x; the page carries data-state="x"' >&2; exit 3; fi
exit 0
`, 1))
	must(t, os.Chmod(filepath.Join(dir, "layout"), 0o755))
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind == "writer" {
			if f.count("writer") == 1 {
				write(t, missing, "")
			} else {
				_ = os.Remove(missing)
			}
		}
		return "", nil
	}
	answer := f.start(plan)
	if f.count("writer") != 2 || slices.Contains(needs(answer), "setup") {
		t.Fatalf("a page without the design's element goes back to the writer as a failing check: %d writers, %v", f.count("writer"), answer)
	}
}

func TestTheLastStageRunsEveryDeclaredCheck(t *testing.T) {
	plan := &Plan{Checks: []Check{{Argv: []string{"plan-check"}}}, Stages: []Stage{
		{Name: "schema", Checks: []Check{{Argv: []string{"migrate"}}}},
		{Name: "page", Checks: []Check{{Argv: []string{"typecheck"}}}},
	}}
	names := func(checks []Check) []string {
		var out []string
		for _, check := range checks {
			out = append(out, check.Argv[0])
		}
		slices.Sort(out)
		return out
	}
	if got := names(plan.checksFor(0)); !slices.Equal(got, []string{"migrate", "plan-check"}) {
		t.Fatalf("an earlier stage runs its own checks and the plan's: %v", got)
	}
	if got := names(plan.checksFor(1)); !slices.Equal(got, []string{"migrate", "plan-check", "typecheck"}) {
		t.Fatalf("the last stage runs every declared check: %v", got)
	}
}

func TestACommitLandingTakesTheLeadsMessage(t *testing.T) {
	f := newFixture(t)
	identity(t)
	f.start(f.plan("-w -m implement"))
	f.fails("land", "needs message", func() (any, error) { return Land(f.cfg, Current(), LandInput{How: "commit"}) })
	f.do("land", func() (any, error) {
		return Land(f.cfg, Current(), LandInput{How: "commit", Message: "fix(core): correct the specified line"})
	})
	if subject := strings.TrimSpace(gitOut(t, "log", "-1", "--format=%s")); subject != "fix(core): correct the specified line" {
		t.Fatalf("the commit carries the lead's message: %q", subject)
	}
}

func TestACheckIsNamedByAPartOfItsCommand(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-l implement criteria")
	plan.Checks = []Check{{Argv: []string{"sh", "-c", "echo client:test"}}, {Argv: []string{"sh", "-c", "echo server:test"}}}
	plan.Requirements = []Requirement{{ID: "R1", Text: "behavior works", Method: "check"}}
	f.start(plan)
	gate := Current()
	input, err := gate.qualityInputs(f.cfg)
	must(t, err)
	report := func(ref string) *QualityReport {
		return &QualityReport{Rules: []RuleResult{}, Requirements: []RequirementResult{{ID: "R1", Status: "verified", Reason: "passes",
			Evidence: []Proof{{Kind: "check", Ref: ref, Detail: "the check passes"}}}}}
	}
	named := report("client:test")
	must(t, gate.validateQuality(named, input))
	if named.Requirements[0].Evidence[0].Ref != checkID(plan.Checks[0].Argv) {
		t.Fatalf("a check named by its command is recorded by its id: %+v", named.Requirements[0].Evidence[0])
	}
	if err := gate.validateQuality(report(":test"), input); err == nil || !strings.Contains(err.Error(), checkID(plan.Checks[1].Argv)) {
		t.Fatalf("an ambiguous name is refused with every check that can be named: %v", err)
	}
}

func TestAnAmendmentLeavesNoInstructionsInThePlan(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, _ map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && call.Resume == "" {
			return `{"status":"BLOCKED","summary":"need a value","blockers":["Which value?"],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return "", nil
	}
	f.start(f.plan("-m implement"))
	revised := clonePlan(t, Current().Plan)
	revised.Brief += " Use the agreed value."
	revised.Update, revised.Rework = "the value is agreed", []string{"core"}
	f.amend(revised)
	shown := asMap(f.do("show", func() (any, error) { return Show(f.cfg, Current(), "plan") }))
	plan := resultOf(shown)["plan"].(map[string]any)
	revisions := Current().Revisions
	last := revisions[len(revisions)-1]
	if plan["update"] != nil || plan["rework"] != nil || last.Reason != "the value is agreed" || !slices.Equal(last.Rework, []string{"core"}) {
		t.Fatalf("an amendment's own fields live in its revision, not in the plan read back: %v %+v", plan, revisions)
	}
}

func TestTheStopHookSaysWhatWasRefused(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, _ map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && call.Resume == "" {
			return `{"status":"BLOCKED","summary":"need a value","blockers":["Which value?"],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return "", nil
	}
	f.start(f.plan("-m implement"))
	f.fails("decide", "is not a finding of this task", func() (any, error) {
		return Decide(f.cfg, Current(), DecideInput{Findings: []Disposition{{ID: "nope", Decision: "accepted", Evidence: "e"}}})
	})
	hook := StopHook(Current().Cwd, "fixture-session")
	reason, _ := hook["reason"].(string)
	if hook["decision"] != "block" || !strings.Contains(reason, "`decide`, was refused: finding \"nope\" is not a finding of this task") || !strings.Contains(reason, "It needs: answer") {
		t.Fatalf("the hook names the refusal the lead may have missed and what the task needs: %q", reason)
	}
}
