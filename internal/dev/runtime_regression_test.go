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

func TestReviewExcludesFrontendAndSupersededWriters(t *testing.T) {
	f := newFixture(t)
	mode := f.cfg.Modes["medium"]
	mode.Frontend = "astra:medium"
	f.cfg.Modes["medium"] = mode
	f.launch(f.plan("-m implement both", Stream{Name: "api", Domain: "backend", Files: []string{"a.txt"}},
		Stream{Name: "ui", Domain: "frontend", Files: []string{"b.txt"}}))
	f.review()
	gate := Current()
	for _, lane := range gate.Reviews[0].Lanes {
		if slices.Contains(gate.authors(), lane.Route.Model) {
			t.Fatalf("author reviewed its own work: %s", lane.Route.Model)
		}
	}
	gate.Writers = append(gate.Writers, &Writer{Stream: "api", Route: f.cfg.MustRoute("sonnet:medium"), Status: "COMPLETED"})
	if !slices.Contains(gate.authors(), f.cfg.MustRoute("opus:medium").Model) {
		t.Fatal("a later writer erased the model that authored the earlier implementation")
	}
}

func TestInterruptedCorrectionResumesTheSameRound(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m fix the content")
	plan.Checks = []Check{{Argv: []string{"sh", "-c", "grep -q fixed a.txt"}}}
	f.launch(plan)
	f.review() // failing check is sent to the writer before review
	failed := false
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "writer" {
			return "", nil
		}
		if packet["correction"] == nil {
			t.Error("resumed correction lost its assignment")
		}
		if !failed {
			failed = true
			return "", errors.New("interrupted correction")
		}
		write(t, "a.txt", "fixed\n")
		return `{"status":"COMPLETED","summary":"fixed","blockers":[],"handoff":"","deviations":[]}`, nil
	}
	f.fails("fix", "interrupted correction", func() (any, error) { return Fix(f.cfg, Current(), FixInput{}) })
	before := Current()
	if before.Phase != "held" || before.ResumePhase != "correcting" {
		t.Fatalf("correction did not hold honestly: %s / %s", before.Phase, before.ResumePhase)
	}
	f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) })
	f.do("continue", func() (any, error) { return Continue(f.cfg, Current()) })
	if after := Current(); after.Phase != "awaiting-review" || after.Round.Batch != before.Round.Batch || after.Iteration != before.Iteration {
		t.Fatalf("continuation lost the saved correction: %+v", after.Round)
	}
	f.review()
	f.finish()
}

func TestFailedWriterCannotBeReviewedAfterResume(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" {
			write(t, "a.txt", "partial\n")
			return "", errors.New("writer failed")
		}
		return "", nil
	}
	f.fails("launch", "writer failed", func() (any, error) { return Launch(f.cfg, f.plan("-m implement")) })
	f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) })
	f.fails("review", "writer round", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{}) })
	if f.count("review") != 0 {
		t.Fatal("an incomplete implementation started a reviewer")
	}
}

func TestInterruptedWritersCannotResumeUnderChangedArchitectureRules(t *testing.T) {
	for _, action := range []string{"continue", "launch", "answer"} {
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
						return `{"status":"BLOCKED","summary":"need a decision","blockers":["Which wording?"],"handoff":"","deviations":[]}`, nil
					}
					return "", errors.New("interrupted writer")
				}
				return "", nil
			}
			if action == "answer" {
				f.launch(plan)
			} else {
				f.fails("launch", "interrupted writer", func() (any, error) { return Launch(f.cfg, plan) })
			}
			before := Current()
			write(t, "policy.md", "Keep the line contract and its terminator.")
			if action != "answer" {
				f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) })
			}
			calls := f.count("writer")
			f.fails(action, "project rules changed", func() (any, error) {
				switch action {
				case "launch":
					return Launch(f.cfg, plan)
				case "answer":
					return Answer(f.cfg, Current(), AnswerInput{Stream: "core", Answer: "Use the approved wording"})
				default:
					return Continue(f.cfg, Current())
				}
			})
			if f.count("writer") != calls || Current().Round.Batch != before.Round.Batch || digest(Current().Baseline) != digest(before.Baseline) {
				t.Fatal("rejected continuation changed the round or started a writer")
			}
		})
	}
}

func TestCorruptPeerStateDoesNotReleaseItsFileOwnership(t *testing.T) {
	f := newFixture(t)
	f.launch(f.plan("-m own the line"))
	state := filepath.Join(home(), "state.json")
	original := fileText(t, state)
	before := fileText(t, "a.txt")
	write(t, state, "{broken")
	t.Setenv("AGENTGATE_SESSION", "another-lead")
	calls := f.count("writer")
	f.fails("launch", "ownership", func() (any, error) { return Launch(f.cfg, f.plan("-m change the owned line")) })
	if f.count("writer") != calls || fileText(t, "a.txt") != before {
		t.Fatal("a corrupt peer allowed an overlapping writer")
	}
	write(t, state, original)
	f.launch(f.plan("-m change another file", Stream{Name: "other", Domain: "backend", Files: []string{"b.txt"}}))
}

func TestInputFingerprintErrorsCannotReusePassingReceipts(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m implement")
	plan.Checks = []Check{{Argv: []string{"true"}, Inputs: []string{"context.txt"}}}
	f.launch(plan)
	f.review()
	must(t, os.Remove("context.txt"))
	must(t, os.Symlink("b.txt", "context.txt"))
	if Current().currentReceipt(&Current().Plan.Checks[0]) != nil {
		t.Fatal("an unreadable/symlinked input fell back to a passing task digest")
	}
	f.fails("finish", "check has no current passing result", func() (any, error) {
		return Finish(f.cfg, Current(), FinishInput{Summary: "done"})
	})
}

func TestImplicitCheckInputsIncludeSiblingDependencies(t *testing.T) {
	f := newFixture(t)
	f.launch(f.plan("-m implement"))
	f.review()
	write(t, "b.txt", "dependency changed\n")
	gate := Current()
	if gate.currentReceipt(&gate.Plan.Checks[0]) != nil {
		t.Fatal("check remained current after an undeclared sibling dependency changed")
	}
}

func TestInputGlobsSupportClassesAndRejectAncestorSymlinks(t *testing.T) {
	newFixture(t)
	must(t, os.Mkdir("source", 0o755))
	write(t, "source/a.go", "a")
	write(t, "source/b.go", "b")
	files, err := inputFiles([]string{"source/[ab].go"})
	must(t, err)
	if !slices.Equal(files, []string{"source/a.go", "source/b.go"}) {
		t.Fatalf("character class did not match inputs: %v", files)
	}
	must(t, os.Symlink("source", "alias"))
	if _, err := inputFiles([]string{"alias/**/*.go"}); err == nil {
		t.Fatal("symlinked input root silently produced an empty fingerprint")
	}
	if _, err := inputFiles([]string{"source/[broken"}); err == nil {
		t.Fatal("malformed input glob was accepted")
	}
}

func TestCommitLandingPreservesTheUsersIndex(t *testing.T) {
	f := newFixture(t)
	identity(t)
	f.launch(f.plan("-w -m implement"))
	f.review()
	f.finish()
	write(t, "b.txt", "user's staged change\n")
	gitOut(t, "add", "b.txt")
	staged := gitOut(t, "diff", "--cached", "--binary")
	f.fails("land", "staged changes", func() (any, error) { return Land(f.cfg, Current(), LandInput{How: "commit"}) })
	if fileText(t, "a.txt") != "before\n" || gitOut(t, "diff", "--cached", "--binary") != staged || Current().Landed != "" {
		t.Fatal("refused landing changed the checkout or staged user work")
	}
	f.land("apply")
	if fileText(t, "a.txt") != "after\n" || gitOut(t, "diff", "--cached", "--binary") != staged {
		t.Fatal("apply did not preserve the user's index")
	}
}

func TestPrivateCommitNeverIncludesUnreviewedFiles(t *testing.T) {
	f := newFixture(t)
	identity(t)
	f.launch(f.plan("-w -m implement"))
	write(t, filepath.Join(Current().Worktree, "b.txt"), "outside the accepted scope\n")
	f.review()
	f.fails("finish", "outside its reviewed scope", func() (any, error) {
		return Finish(f.cfg, Current(), FinishInput{Summary: "done"})
	})
	if Current().Phase != "reviewed" || fileText(t, "b.txt") != "before\n" {
		t.Fatal("unreviewed foreign files were committed or applied")
	}
}

func TestCorruptStateCannotBeMistakenForNoTask(t *testing.T) {
	f := newFixture(t)
	f.launch(f.plan("-m implement"))
	state := filepath.Join(home(), "state.json")
	write(t, state, "{broken")
	called := false
	_, err := Execute("launch", func() (any, error) { called = true; return nil, nil })
	if err == nil || !strings.Contains(err.Error(), "corrupt") || called || fileText(t, state) != "{broken" {
		t.Fatalf("corruption was not safely reported: called=%v err=%v", called, err)
	}
}

func TestDeadOperationRecoversIntoExplicitHold(t *testing.T) {
	f := newFixture(t)
	f.launch(f.plan("-m implement"))
	gate := Current()
	gate.Phase = "implementing"
	gate.Writers[0].Status = "RUNNING"
	must(t, gate.save())
	must(t, ops.WriteJSON(filepath.Join(home(), "operation.json"), ops.Record{State: "running", PID: 99999999, Identity: "dead"}))
	f.do("status", func() (any, error) { return Current().Summary(f.cfg), nil })
	gate = Current()
	if gate.Phase != "held" || gate.ResumePhase != "implementing" || gate.Writers[0].Status != "FAILED" {
		t.Fatal("interrupted operation was not recovered into a resumable hold")
	}
}

func TestCorrectionContinuesInTheActualFallbackDirectory(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && call.Route.Alias == "opus" {
			return "", provider.ErrLimit
		}
		if kind == "review" && packet["axes"].([]any)[0] == "correctness" && len(packet["accepted_findings"].([]any)) == 0 {
			return `{"reports":[{"axis":"correctness","status":"NEEDS_FIX","findings":[` + finding("P1") + `],"checked":["read"],"resolutions":[]}]}`, nil
		}
		if kind == "writer" && packet["correction"] != nil {
			write(t, "a.txt", "corrected by fallback\n")
			return `{"status":"COMPLETED","summary":"corrected","blockers":[],"handoff":"","deviations":[]}`, nil
		}
		return "", nil
	}
	f.launch(f.plan("-m implement"))
	first := *Current().latest()["core"]
	if first.Route.Alias != "fable" || !strings.Contains(first.Dir, "-alt") {
		t.Fatalf("the winning fallback did not own its actual lane: %+v", first)
	}
	f.review()
	id := Current().Findings()[0].ID
	f.do("triage", func() (any, error) {
		return Triage(f.cfg, Current(), TriageInput{Dispositions: []Disposition{{ID: id, Decision: "accepted", Evidence: "confirmed"}}})
	})
	f.do("fix", func() (any, error) { return Fix(f.cfg, Current(), FixInput{}) })
	last := Current().latest()["core"]
	if last.Dir != first.Dir || last.Session != first.Session || last.Route.Model != first.Route.Model {
		t.Fatalf("fallback continuation lost its original session: before=%+v after=%+v", first, last)
	}
	f.review()
	f.finish()
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

func TestDeadStageAndMissingCompletionReceiptsAreRejected(t *testing.T) {
	f := newFixture(t)
	f.launch(f.plan("-m implement"))
	gate := Current()
	gate.Phase = "staged"
	if err := validateGateState(gate); err == nil {
		t.Fatal("staged phase without a staged plan was accepted")
	}
	gate.Phase = "complete"
	if err := validateGateState(gate); err == nil {
		t.Fatal("completed phase without a final receipt was accepted")
	}
}

func TestAnsweringOneStreamDoesNotRestartAnotherBlockedWriter(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, _ map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && call.Resume == "" {
			return `{"status":"BLOCKED","summary":"need a decision","blockers":["Choose the behavior"],"handoff":"","deviations":[]}`, nil
		}
		return "", nil
	}
	f.launch(f.plan("-m implement", Stream{Name: "first", Domain: "backend", Files: []string{"a.txt"}},
		Stream{Name: "second", Domain: "backend", Files: []string{"b.txt"}}))
	before := f.count("writer")
	f.do("answer", func() (any, error) {
		return Answer(f.cfg, Current(), AnswerInput{Stream: "first", Answer: "use the first behavior"})
	})
	gate := Current()
	if f.count("writer") != before+1 || gate.Phase != "held" || gate.latest()["first"].Status != "COMPLETED" || gate.latest()["second"].Status != "BLOCKED" {
		t.Fatal("answering one stream restarted a different unanswered writer")
	}
	f.do("answer", func() (any, error) {
		return Answer(f.cfg, Current(), AnswerInput{Stream: "second", Answer: "use the second behavior"})
	})
	if Current().Phase != "awaiting-review" || f.count("writer") != before+2 {
		t.Fatal("the round did not finish after both decisions were answered")
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
			return `{"status":"COMPLETED","summary":"first end","blockers":[],"handoff":"","deviations":[]}`, nil
		}
		return "", nil
	}
	f.launch(f.plan("-w -m change first end"))
	f.review()
	f.finish()
	userContent := "first\none\ntwo\nthree\nfour\nfive\nuser\n"
	write(t, "a.txt", userContent)
	head := gitOut(t, "rev-parse", "HEAD")
	f.fails("land", "uncommitted changes", func() (any, error) { return Land(f.cfg, Current(), LandInput{How: "commit"}) })
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
	f.launch(f.plan("-w -m implement"))
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
	f.launch(f.plan("-w -m implement"))
	f.review()
	f.finish()
	head := gitOut(t, "rev-parse", "HEAD")
	hook := filepath.Join(".git", "hooks", "pre-commit")
	must(t, os.WriteFile(hook, []byte("#!/bin/sh\necho 'fixture hook refused' >&2\nexit 1\n"), 0o755))
	f.fails("land", "change applied, but commit failed", func() (any, error) { return Land(f.cfg, Current(), LandInput{How: "commit"}) })
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
