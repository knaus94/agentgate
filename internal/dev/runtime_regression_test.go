package dev

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

func TestStreamIdentifiersCannotEscapeStateDirectories(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"../outside", "a/b", ".", "..", "a\\n", "a\nb", "/absolute"} {
		plan := f.plan("-m change", Stream{Name: name, Domain: "backend", Files: []string{"a.txt"}})
		if _, err := Resolve(f.cfg, plan); err == nil {
			t.Errorf("unsafe stream name %q was accepted", name)
		}
	}
	t.Setenv("AGENTGATE_SESSION", "../../outside")
	relative, err := filepath.Rel(checkoutDir(""), home())
	must(t, err)
	if strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		t.Fatalf("session escaped its checkout state: %s", relative)
	}
}

func TestAnInterruptedCorrectionIsRetriedInTheSameRound(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m fix the content")
	plan.Checks = []Check{{Argv: []string{"sh", "-c", "grep -q fixed a.txt"}}}
	failed := false
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "writer" || packet["correction"] == nil {
			return "", nil
		}
		if !failed {
			failed = true
			return "", errors.New("interrupted correction")
		}
		write(t, "a.txt", "fixed\n")
		return `{"status":"COMPLETED","summary":"fixed","blockers":[],"handoff":"","deviations":[],"disputes":[]}`, nil
	}
	stopped := f.start(plan) // the failing check goes to the writer, whose process then dies
	before := Current()
	if stopped["step"] != StepWrite || !slices.Equal(needs(stopped), []string{"failed"}) || !before.Round.Correction || f.count("review") != 0 {
		t.Fatalf("an interrupted correction is reported as it is: %v", stopped)
	}
	done := f.proceed()
	after := Current()
	if done["step"] != StepDone || after.Round.Batch != before.Round.Batch || after.Iteration != before.Iteration {
		t.Fatalf("the retry lost the saved correction: %v %+v", done, after.Round)
	}
}

func TestAFailedWriterIsRetriedAndNeverReviewed(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" {
			write(t, "a.txt", "partial\n")
			return "", errors.New("writer failed")
		}
		return "", nil
	}
	stopped := f.start(f.plan("-m implement"))
	if stopped["step"] != StepWrite || !slices.Equal(needs(stopped), []string{"failed"}) {
		t.Fatalf("a failed writer leaves its round open: %v", stopped)
	}
	again := f.do("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{Recheck: true}) })
	if again["step"] != StepWrite || f.count("writer") != 2 || f.count("review") != 0 {
		t.Fatalf("an incomplete implementation started a reviewer: %v", again)
	}
}

func TestInterruptedWritersCannotResumeUnderChangedArchitectureRules(t *testing.T) {
	for _, action := range []string{"run", "answer"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t)
			write(t, "policy.md", "Keep the line contract.")
			f.cfg.ReviewRules = map[string]config.ReviewRule{"contract": {Instructions: []string{"policy.md"}, Files: []string{"a.txt"}}}
			plan := f.plan("-h implement the line")
			receipt := f.do("architecture", func() (any, error) { return Architecture(f.cfg, plan) })
			plan.Architecture = &Accepted{ID: receipt["id"].(string), Acceptance: "Keep the contract"}
			f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
				if kind == "writer" {
					if action == "answer" {
						return `{"status":"BLOCKED","summary":"need a decision","blockers":["Which wording?"],"handoff":"","deviations":[],"disputes":[]}`, nil
					}
					return "", errors.New("interrupted writer")
				}
				return "", nil
			}
			f.start(plan)
			before := Current()
			write(t, "policy.md", "Keep the line contract and its terminator.")
			calls := f.count("writer")
			f.fails(action, "project rules changed", func() (any, error) {
				if action == "answer" {
					return Answer(f.cfg, Current(), AnswerInput{Stream: "core", Answer: "Use the approved wording"})
				}
				return Run(f.cfg, Current(), RunInput{})
			})
			if after := Current(); f.count("writer") != calls || after.Round.Batch != before.Round.Batch || after.StageTree != before.StageTree {
				t.Fatal("rejected continuation changed the round or started a writer")
			}
		})
	}
}

func TestCorruptPeerStateDoesNotReleaseItsFileOwnership(t *testing.T) {
	f := newFixture(t)
	f.waiting("own the line")
	state := filepath.Join(home(), "state.json")
	original := fileText(t, state)
	before := fileText(t, "a.txt")
	write(t, state, "{broken")
	t.Setenv("AGENTGATE_SESSION", "another-lead")
	calls := f.count("writer")
	f.fails("start", "ownership", func() (any, error) { return Start(f.cfg, f.plan("-m change the owned line"), "", false, RunInput{}) })
	if f.count("writer") != calls || fileText(t, "a.txt") != before {
		t.Fatal("a corrupt peer allowed an overlapping writer")
	}
	write(t, state, original)
	f.start(f.plan("-m change another file", Stream{Name: "other", Domain: "backend", Files: []string{"b.txt"}}))
}

func TestInputFingerprintErrorsCannotReusePassingReceipts(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-l implement")
	plan.Checks = []Check{{Argv: []string{"true"}, Inputs: []string{"context.txt"}}}
	f.start(plan) // light: written and checked, the lead's review is owed
	gate := Current()
	if !passed(gate.currentReceipt(&gate.checks()[0])) {
		t.Fatal("the check must have a current passing receipt first")
	}
	must(t, os.Remove("context.txt"))
	must(t, os.Symlink("../outside.txt", "context.txt"))
	if gate.currentReceipt(&gate.checks()[0]) != nil {
		t.Fatal("an input leading outside the repository fell back to a passing task digest")
	}
	f.fails("run", "outside the repository", func() (any, error) { return Run(f.cfg, Current(), RunInput{}) })
	if Current().Step == StepDone {
		t.Fatal("the task finished on a check whose inputs cannot be read")
	}
}

func TestImplicitCheckInputsIncludeSiblingDependencies(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-l implement")
	f.start(plan)
	gate := Current()
	if !passed(gate.currentReceipt(&gate.checks()[0])) {
		t.Fatal("the check must have a current passing receipt first")
	}
	write(t, "b.txt", "dependency changed\n")
	if gate.currentReceipt(&gate.checks()[0]) != nil {
		t.Fatal("check remained current after an undeclared sibling dependency changed")
	}
}

func TestInputGlobsSupportClassesAndFollowLinkedDirectories(t *testing.T) {
	newFixture(t)
	must(t, os.Mkdir("source", 0o755))
	write(t, "source/a.go", "a")
	write(t, "source/b.go", "b")
	files, err := inputsIn(t, "source/[ab].go")
	must(t, err)
	if !slices.Equal(files, []string{"source/a.go", "source/b.go"}) {
		t.Fatalf("character class did not match inputs: %v", files)
	}
	must(t, os.Symlink("source", "alias"))
	if files, err := inputsIn(t, "alias/**/*.go"); err != nil || !slices.Contains(files, "alias") || !slices.Contains(files, "source/a.go") {
		t.Fatalf("a linked input root must count as the files it leads to, not as nothing: %v (%v)", files, err)
	}
	if _, err := inputsIn(t, "source/[broken"); err == nil {
		t.Fatal("malformed input glob was accepted")
	}
}

func TestCommitLandingPreservesTheUsersIndex(t *testing.T) {
	f := newFixture(t)
	identity(t)
	f.start(f.plan("-w -m implement"))
	write(t, "b.txt", "user's staged change\n")
	gitOut(t, "add", "b.txt")
	staged := gitOut(t, "diff", "--cached", "--binary")
	f.fails("land", "staged changes", func() (any, error) {
		return Land(f.cfg, Current(), LandInput{How: "commit", Message: "feat: land the fixture"})
	})
	if fileText(t, "a.txt") != "before\n" || gitOut(t, "diff", "--cached", "--binary") != staged || Current().Landed != "" {
		t.Fatal("refused landing changed the checkout or staged user work")
	}
	f.land("apply")
	if fileText(t, "a.txt") != "after\n" || gitOut(t, "diff", "--cached", "--binary") != staged {
		t.Fatal("apply did not preserve the user's index")
	}
}

func TestAnIsolatedTaskNeverLandsAFileNoStreamOwns(t *testing.T) {
	f := newFixture(t)
	identity(t)
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind == "writer" {
			write(t, "b.txt", "outside the accepted scope\n")
		}
		return "", nil
	}
	stopped := f.start(f.plan("-w -m implement"))
	if !slices.Equal(needs(stopped), []string{"scope"}) || f.count("review") != 0 || Current().Step == StepDone {
		t.Fatalf("a file outside the plan is settled before anything is paid to review the rest: %v", stopped)
	}
	need := stopped["needs"].([]any)[0].(map[string]any)
	if !strings.Contains(need["what"].(string), "b.txt") || !slices.Contains(offered(stopped), "amend") {
		t.Fatalf("the answer names the file and what can be done about it: %v", stopped)
	}
	worktree := Current().Worktree
	if again := f.proceed(); !slices.Equal(needs(again), []string{"scope"}) || f.count("review") != 0 {
		t.Fatalf("running again changes nothing while the file is there: %v", again)
	}
	write(t, filepath.Join(worktree, "b.txt"), "before\n")
	done := f.proceed()
	f.land("apply")
	if done["step"] != StepDone || fileText(t, "a.txt") != "after\n" || fileText(t, "b.txt") != "before\n" {
		t.Fatalf("unreviewed foreign files were committed or applied: %v", done)
	}
}

func TestCorruptStateCannotBeMistakenForNoTask(t *testing.T) {
	f := newFixture(t)
	f.waiting("implement")
	state := filepath.Join(home(), "state.json")
	write(t, state, "{broken")
	called := false
	_, err := Execute("start", func() (any, error) { called = true; return nil, nil })
	if err == nil || !strings.Contains(err.Error(), "corrupt") || called || fileText(t, state) != "{broken" {
		t.Fatalf("corruption was not safely reported: called=%v err=%v", called, err)
	}
}

func TestADeadOperationLeavesNoWriterRunning(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, _ map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && call.Resume == "" {
			return `{"status":"BLOCKED","summary":"need a decision","blockers":["Choose the behavior"],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return "", nil
	}
	f.start(f.plan("-m implement"))
	gate := Current()
	gate.Writers[0].Status = "RUNNING" // as the process that owned it left it when it died
	must(t, gate.save())
	must(t, ops.WriteJSON(filepath.Join(home(), "operation.json"), ops.Record{State: "running", PID: 99999999, Identity: "dead"}))
	status := f.do("status", func() (any, error) { return Status(f.cfg, Current(), "") })
	gate = Current()
	if gate.Writers[0].Status != "FAILED" || gate.Step != StepWrite || gate.Pause != nil ||
		!slices.Equal(needs(status), []string{"failed"}) || offered(status)[0] != "run" {
		t.Fatalf("an interrupted operation must leave a writer that `run` retries: %v", status)
	}
	if done := f.proceed(); done["step"] != StepDone || f.count("writer") != 2 {
		t.Fatalf("the retry continues the task: %v", done)
	}
}

func TestCorrectionContinuesInTheActualFallbackDirectory(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && call.Route.Alias == "opus" {
			return "", provider.ErrLimit
		}
		if kind == "writer" && packet["correction"] != nil {
			write(t, "a.txt", "corrected by fallback\n")
			return `{"status":"COMPLETED","summary":"corrected","blockers":[],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return "", nil
	}
	f.reviewOnce(finding("P1"))
	done := f.start(f.plan("-m implement"))
	writers := Current().Writers
	if done["step"] != StepDone || len(writers) != 2 {
		t.Fatalf("the finding goes back to its author once: %v", done)
	}
	first, last := writers[0], writers[1]
	if first.Route.Alias != "fable" || !strings.Contains(first.Dir, "-alt") {
		t.Fatalf("the winning fallback did not own its actual lane: %+v", first)
	}
	if last.Dir != first.Dir || last.Session != first.Session || last.Route.Model != first.Route.Model {
		t.Fatalf("fallback continuation lost its original session: before=%+v after=%+v", first, last)
	}
}

func TestFailedToolRefreshIsReportedAndServerIsOmitted(t *testing.T) {
	f := newFixture(t)
	f.cfg.WorkerMCP = map[string]config.WorkerMCP{
		"graph": {Command: "unused-command", Roles: []string{"reviewer"}, Refresh: []string{"false"}, RefreshTimeoutS: 2},
	}
	servers, _, note := workerTools(f.cfg, "reviewer")
	if len(servers) != 0 || !strings.Contains(note, "refresh failed") || !strings.Contains(note, "stale index") {
		t.Fatalf("stale MCP index was presented as prepared: %v %s", servers, note)
	}
}

func TestImpossiblePositionsAreRejectedWhenStateIsLoaded(t *testing.T) {
	f := newFixture(t)
	f.waiting("implement")
	for name, damage := range map[string]func(*Gate){
		"an unknown step":                     func(gate *Gate) { gate.Step = "staged" },
		"done without a completion receipt":   func(gate *Gate) { gate.Step, gate.Final = StepDone, nil },
		"every stage accepted but not closed": func(gate *Gate) { gate.Stage = len(gate.Plan.Stages) },
		"an unknown kind of pause":            func(gate *Gate) { gate.Pause = &Paused{Kind: "held", Reason: "old"} },
		"no recorded origin":                  func(gate *Gate) { gate.BaseTree = "" },
	} {
		gate := Current()
		must(t, validateGateState(gate))
		damage(gate)
		if err := validateGateState(gate); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAnsweringOneStreamDoesNotRestartAnotherBlockedWriter(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, _ map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && call.Resume == "" {
			return `{"status":"BLOCKED","summary":"need a decision","blockers":["Choose the behavior"],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return "", nil
	}
	f.start(f.plan("-m implement", Stream{Name: "first", Domain: "backend", Files: []string{"a.txt"}},
		Stream{Name: "second", Domain: "backend", Files: []string{"b.txt"}}))
	before := f.count("writer")
	one := f.do("answer", func() (any, error) {
		return Answer(f.cfg, Current(), AnswerInput{Stream: "first", Answer: "use the first behavior"})
	})
	gate := Current()
	if f.count("writer") != before+1 || gate.Step != StepWrite || gate.latest()["first"].Status != "COMPLETED" || gate.latest()["second"].Status != "BLOCKED" ||
		!slices.Equal(needs(one), []string{"answer"}) || slices.Contains(offered(one), "run") {
		t.Fatalf("answering one stream restarted a different unanswered writer: %v", one)
	}
	two := f.do("answer", func() (any, error) {
		return Answer(f.cfg, Current(), AnswerInput{Stream: "second", Answer: "use the second behavior"})
	})
	if two["step"] != StepDone || f.count("writer") != before+2 {
		t.Fatalf("the task did not run on after both decisions were answered: %v", two)
	}
}

func TestCommitLandingDoesNotIncludeCompatibleUserHunks(t *testing.T) {
	f := newFixture(t)
	identity(t)
	write(t, "a.txt", "first\none\ntwo\nthree\nfour\nfive\nlast\n")
	gitOut(t, "add", "a.txt")
	gitOut(t, "commit", "-qm", "two independent ends")
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind == "writer" {
			write(t, "a.txt", "task\none\ntwo\nthree\nfour\nfive\nlast\n")
			return `{"status":"COMPLETED","summary":"first end","blockers":[],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return "", nil
	}
	f.start(f.plan("-w -m change first end"))
	userContent := "first\none\ntwo\nthree\nfour\nfive\nuser\n"
	write(t, "a.txt", userContent)
	head := gitOut(t, "rev-parse", "HEAD")
	f.fails("land", "uncommitted changes", func() (any, error) {
		return Land(f.cfg, Current(), LandInput{How: "commit", Message: "feat: land the fixture"})
	})
	if fileText(t, "a.txt") != userContent || gitOut(t, "rev-parse", "HEAD") != head {
		t.Fatal("commit landing swept the user's compatible unstaged hunk into a commit")
	}
	f.land("apply")
	if fileText(t, "a.txt") != "task\none\ntwo\nthree\nfour\nfive\nuser\n" {
		t.Fatal("apply did not preserve the compatible user hunk")
	}
}

func TestWorktreeCleanupRefusesStatePathOutsideManagedDirectory(t *testing.T) {
	f := newFixture(t)
	identity(t)
	f.start(f.plan("-w -m implement"))
	gate := Current()
	outside := t.TempDir()
	marker := filepath.Join(outside, "keep.txt")
	write(t, marker, "must remain")
	gate.Worktree = outside
	if err := gate.discard(); err == nil {
		t.Fatal("cleanup accepted a state path outside its managed directory")
	}
	if fileText(t, marker) != "must remain" {
		t.Fatal("cleanup removed unrelated data")
	}
	f.land("drop") // remove the real fixture worktree
}

func TestCommitHookFailureReportsAppliedOutcome(t *testing.T) {
	f := newFixture(t)
	identity(t)
	f.start(f.plan("-w -m implement"))
	head := gitOut(t, "rev-parse", "HEAD")
	hook := filepath.Join(".git", "hooks", "pre-commit")
	must(t, os.WriteFile(hook, []byte("#!/bin/sh\necho 'fixture hook refused' >&2\nexit 1\n"), 0o755))
	f.fails("land", "change applied, but commit failed", func() (any, error) {
		return Land(f.cfg, Current(), LandInput{How: "commit", Message: "feat: land the fixture"})
	})
	if gate := Current(); gate.Landed != "apply" || gitOut(t, "rev-parse", "HEAD") != head || fileText(t, "a.txt") != "after\n" {
		t.Fatal("failed commit was misreported as committed or discarded the applied change")
	}
}

func TestWorkerCacheMountsCannotFollowRepositorySymlinks(t *testing.T) {
	f := newFixture(t)
	checkout, err := os.Getwd()
	must(t, err)
	outside := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(outside, "existing"), 0o755))
	must(t, os.MkdirAll("cache/valid", 0o755))
	must(t, os.Symlink(outside, "leaf-link"))
	must(t, os.Symlink(outside, "parent-link"))
	must(t, os.Symlink(filepath.Join(checkout, "cache"), "internal-link"))
	for _, unsafe := range []string{"leaf-link", "parent-link/existing", "parent-link/not-created-yet", "internal-link/valid", "missing/suffix", "../outside", outside, ".", ".git"} {
		if directory, err := repositoryWritableDirectory(checkout, unsafe); err == nil {
			t.Errorf("unsafe cache grant %q resolved to %s", unsafe, directory)
		}
	}
	f.cfg.WorkerMCP = map[string]config.WorkerMCP{
		"cache": {Command: "unused-command", Roles: []string{"reviewer"}, Writable: []string{"cache/valid", "leaf-link", "parent-link/existing", "parent-link/not-created-yet"}},
	}
	_, grants, note := workerTools(f.cfg, "reviewer")
	if !slices.Equal(grants, []string{filepath.Join(checkout, "cache", "valid")}) {
		t.Fatalf("MCP mount preparation granted an external target: %v", grants)
	}
	if strings.Count(note, "was not granted") != 3 || !strings.Contains(note, "symlink") {
		t.Fatalf("refused mounts were not reported to the worker: %s", note)
	}
	if _, err := os.Stat(filepath.Join(outside, "not-created-yet")); !os.IsNotExist(err) {
		t.Fatal("mount preparation created an external cache directory")
	}
}

func TestReviewerSandboxCannotWriteThroughRejectedMCPCache(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap is not installed")
	}
	f := newFixture(t)
	outside := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(outside, "existing"), 0o755))
	must(t, os.MkdirAll("cache/valid", 0o755))
	must(t, os.Symlink(outside, "parent-link"))
	f.cfg.WorkerMCP = map[string]config.WorkerMCP{
		"cache": {Command: "unused-command", Roles: []string{"reviewer"}, Writable: []string{"cache/valid", "parent-link/existing"}},
	}
	_, grants, _ := workerTools(f.cfg, "reviewer")
	argv := provider.Sandbox(t.TempDir(), false, nil, grants, []string{"sh", "-c",
		"printf allowed > cache/valid/allowed.txt; if printf escaped > parent-link/existing/escaped.txt; then exit 70; fi; test -s cache/valid/allowed.txt"})
	if output, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("reviewer sandbox did not enforce the prepared cache grants: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(outside, "existing", "escaped.txt")); !os.IsNotExist(err) {
		t.Fatal("reviewer wrote through a symlink into an external cache")
	}
	if fileText(t, "cache/valid/allowed.txt") != "allowed" {
		t.Fatal("valid internal cache was not writable")
	}
}
