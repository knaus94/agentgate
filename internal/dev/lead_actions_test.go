package dev

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/knaus94/agentgate/internal/provider"
)

// Actions a lead was missing in real sessions: each test is one of them, end to end.

const completedReport = `{"status":"COMPLETED","summary":"done","blockers":[],"handoff":"","deviations":[],"disputes":[]}`

func TestWorkGoesToTheWriterItNames(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m add the owner guard", Stream{Name: "core", Domain: "backend", Files: []string{"a.txt"}},
		Stream{Name: "docs", Domain: "backend", Files: []string{"b.txt"}})
	plan.Requirements = []Requirement{{ID: "R1", Text: "a foreign owner is refused"}, {ID: "R2", Text: "the docs describe the guard"}}
	changeQuality(f, func(report *QualityReport) {
		if !strings.Contains(fileText(t, "a.txt"), "guard") {
			report.Requirements[0].Status, report.Requirements[0].Cause, report.Requirements[0].Reason = "missing", "code", "a.txt has no guard"
			report.Requirements[0].Evidence = []Proof{{Kind: "code", Ref: "a.txt:1", Detail: "no owner comparison"}}
		}
		if !strings.Contains(fileText(t, "b.txt"), "guard") {
			report.Requirements[1].Status, report.Requirements[1].Cause, report.Requirements[1].Reason = "partial", "proof", "the docs say nothing yet"
		}
	})
	reviewer := f.model
	var corrected []string
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "writer" || packet["correction"] == nil {
			return reviewer(kind, packet, call)
		}
		stream := packet["stream"].(map[string]any)["name"].(string)
		corrected = append(corrected, stream+": "+strings.Join(asStrings(packet["correction"].(map[string]any)["work"]), "; "))
		if call.Resume == "" {
			t.Errorf("work goes into the session of the writer that wrote the stream: %s", stream)
		}
		write(t, map[string]string{"core": "a.txt", "docs": "b.txt"}[stream], "after, with the guard\n")
		return completedReport, nil
	}
	// Code the reviewer finds missing goes to the writer that owns it, without the lead.
	waiting := f.start(plan)
	if !slices.Equal(needs(waiting), []string{"requirement"}) || !slices.Equal(corrected, []string{"core: "}) {
		t.Fatalf("missing code reaches its writer by itself; missing proof waits for the lead: %v %v", corrected, waiting)
	}
	// The lead judges that the docs lack text, not proof, and gives that stream the work.
	f.fails("decide", "streams: core, docs", func() (any, error) {
		return Decide(f.cfg, Current(), DecideInput{Work: []WorkOrder{{Stream: "web", Note: "describe the guard"}}})
	})
	done := f.decide(DecideInput{Work: []WorkOrder{{Stream: "docs", Requirement: "R2", Note: "describe the guard in b.txt"}}})
	if done["step"] != StepDone || !slices.Equal(corrected, []string{"core: ", "docs: describe the guard in b.txt"}) {
		t.Fatalf("the work reaches the named writer alone, and the task finishes once its criterion is met: %v %v", corrected, done)
	}
}

func TestNamedOperationsChangeOnePartOfThePlan(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, _ map[string]any, call provider.Call) (string, error) {
		if kind == "writer" && call.Resume == "" {
			return `{"status":"BLOCKED","summary":"the limit belongs in b.txt","blockers":["b.txt is outside my stream"],"handoff":"","deviations":[],"disputes":[]}`, nil
		}
		return "", nil
	}
	f.start(f.plan("-m enforce the limit"))
	f.fails("amend", "its streams: core", func() (any, error) {
		return AmendOperations(f.cfg, Current(), Operations{Update: "the limit lives in b.txt", Scope: []ScopeChange{{Stream: "api", Add: []string{"b.txt"}}}})
	})
	amended := f.do("amend", func() (any, error) {
		return AmendOperations(f.cfg, Current(), Operations{Update: "the limit lives in b.txt",
			Scope:        []ScopeChange{{Stream: "core", Add: []string{"b.txt"}}},
			Checks:       []CheckChange{{Add: []Check{{Argv: []string{"test", "-f", "b.txt"}}}}},
			Requirements: []RequirementChange{{ID: "R1", Text: "the limit is enforced"}},
			Timeouts:     &Timeouts{WriterS: 5400}})
	})
	gate := Current()
	revision := gate.Revisions[len(gate.Revisions)-1]
	if owner := gate.owner("b.txt"); owner == nil || owner.Name != "core" || gate.findCheck([]string{"test", "-f", "b.txt"}) == nil ||
		!slices.Contains(gate.Plan.Stages[0].Covers, "R1") || gate.Plan.WriterTimeoutS != 5400 || gate.Plan.Update != "" ||
		revision.Reason != "the limit lives in b.txt" || len(revision.Changes) == 0 {
		t.Fatalf("each operation changes its part of the plan and the change is recorded: %+v %v", gate.Plan, amended)
	}
	done := f.do("answer", func() (any, error) {
		return Answer(f.cfg, Current(), AnswerInput{Stream: "core", Answer: "b.txt is yours now"})
	})
	if done["step"] != StepDone || fileText(t, "b.txt") != "after\n" {
		t.Fatalf("the writer finishes with the file it was given: %v", done)
	}
}

func TestTheNotesOfAFinishedTaskAreFixedInIt(t *testing.T) {
	f := newFixture(t)
	f.reviewOnce(finding("P2")) // below the blocking line of medium: a note
	done := f.start(f.plan("-m change the line"))
	notes := asStrings(done["notes"])
	if done["step"] != StepDone || len(notes) != 1 || !strings.Contains(notes[0], "P2 correctness a.txt:1") || !slices.Contains(offered(done), "decide") {
		t.Fatalf("a finished task lists the notes it leaves, one line each: %v", done)
	}
	writers := f.count("writer")
	id := Current().leftNotes()[0].ID
	fixed := f.decide(DecideInput{Findings: []Disposition{{ID: id, Decision: "accepted", Evidence: "the user wants it fixed"}}})
	last := f.calls[len(f.calls)-2] // the writer, then the reviewer that verified it
	if fixed["step"] != StepDone || f.count("writer") != writers+1 || fixed["notes"] != nil || last.Resume == "" || !last.Writer {
		t.Fatalf("an accepted note opens the task where it ended, its writer fixes it, and the task finishes again: %v", fixed)
	}
}

func TestADroppedTaskGivesBackItsFilesAndCanBeForgotten(t *testing.T) {
	f := newFixture(t)
	f.start(f.plan("-m change the line"))
	gate := Current()
	write(t, "a.txt", "the user's own edit\n")
	f.fails("drop", "changed after the task's writers left them", func() (any, error) {
		return Drop(f.cfg, Current(), DropInput{Reason: "the user does not want the change"})
	})
	if fileText(t, "a.txt") != "the user's own edit\n" {
		t.Fatal("a file someone changed after the writers is never overwritten")
	}
	write(t, "a.txt", "after\n")
	dropped := f.do("drop", func() (any, error) {
		return Drop(f.cfg, Current(), DropInput{Reason: "the user does not want the change"})
	})
	if dropped["step"] != StepCancelled || fileText(t, "a.txt") != "before\n" {
		t.Fatalf("dropping takes back what the task changed: %v", dropped)
	}
	f.fails("drop", "already dropped", func() (any, error) { return Drop(f.cfg, Current(), DropInput{Reason: "again"}) })
	forgotten, err := Forget(ForgetInput{Reason: "the user wants it gone"})
	must(t, err)
	if _, statErr := os.Stat(gate.dir()); Current() != nil || !os.IsNotExist(statErr) || asMap(forgotten)["forgot"] != gate.ID[:8] {
		t.Fatalf("a forgotten task leaves no state behind: %v", forgotten)
	}
}
