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

func write(t *testing.T, name, content string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(name), 0o755))
	must(t, os.WriteFile(name, []byte(content), 0o644))
}

// changeQuality lets a scripted reviewer pass the code and alter its verdict on the criteria.
func changeQuality(f *fixture, change func(*QualityReport)) {
	f.raw = true
	f.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		if kind != "review" || packet["quality"] == nil {
			return "", nil
		}
		var answer map[string]any
		_ = json.Unmarshal([]byte(f.defaultAnswer(kind, packet)), &answer)
		report := fixtureQuality(packet)
		change(report)
		answer["quality"] = report
		raw, _ := json.Marshal(answer)
		return string(raw), nil
	}
}

func clonePlan(t *testing.T, plan *Plan) *Plan {
	t.Helper()
	raw, err := json.Marshal(plan)
	must(t, err)
	out := &Plan{}
	must(t, json.Unmarshal(raw, out))
	return out
}

// revisedFoundation corrects a plan whose first stage was held to the whole of R1: the foundation gets its own
// criterion, and R1 is completed by the last stage.
func revisedFoundation(t *testing.T, plan *Plan) *Plan {
	revised := clonePlan(t, plan)
	revised.Update = "Correct foundation coverage; retain the end-to-end requirement for final delivery"
	revised.Requirements = append(revised.Requirements, Requirement{ID: "foundation", Text: "The foundation exists", Method: "code"})
	revised.Stages[0].Covers = []string{"foundation"}
	revised.Stages[len(revised.Stages)-1].Covers = append(revised.Stages[len(revised.Stages)-1].Covers, "R1")
	return revised
}

func TestAReviewedStageIsAmendedWithoutLosingTheTaskOrWaivingARequirement(t *testing.T) {
	f := newFixture(t)
	plan := stagedPlan("-m build the feature")
	// The writer's generator also produced two files nobody could have named when the plan was written.
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" {
			write(t, "context.txt", "generated tracked output\n")
			write(t, "migrations/0001/up.sql", "new generated migration\n")
		}
		return "", nil
	}
	writers := f.model
	changeQuality(f, func(q *QualityReport) { q.Requirements[0].Status, q.Requirements[0].Cause = "partial", "plan" })
	reviewers := f.model
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" {
			return writers(kind, packet, call)
		}
		return reviewers(kind, packet, call)
	}
	stopped := f.start(plan)
	if stopped["step"] != StepDecide || !slices.Equal(needs(stopped), []string{"requirement"}) {
		t.Fatalf("the reviewer's partial verdict keeps the stage open: %v", stopped)
	}
	if foreign := asStrings(stopped["foreign_changes"]); !slices.Equal(foreign, []string{"context.txt", "migrations/0001/up.sql"}) {
		t.Fatalf("what the writer changed outside its scope is reported: %v", foreign)
	}
	f.do("pause", func() (any, error) { return Pause(f.cfg, Current(), PauseInput{Reason: "the plan needs an amendment"}) })
	before := Current()

	revised := revisedFoundation(t, plan)
	revised.Stages[0].Streams[0].Files = append(revised.Stages[0].Streams[0].Files, "context.txt", "migrations/")
	revised.Generated = []string{"migrations/**"}
	amended := f.amend(revised)
	after := Current()
	if after.Pause != nil || amended["step"] != StepVerify {
		t.Fatalf("the amendment is the decision the pause waited for; no resume is needed: %v", amended)
	}
	if after.ID != before.ID || after.Stage != before.Stage || len(after.Accepted) != 0 || after.BaseTree != before.BaseTree || after.StageTree != before.StageTree ||
		digest(after.Writers) != digest(before.Writers) || digest(after.Checks) != digest(before.Checks) || digest(after.Reviews) != digest(before.Reviews) {
		t.Fatal("an amendment discarded the task's identity, origin, sessions or evidence")
	}
	if content, _, ok := treeFile(after.objects(), after.origin(), "context.txt"); !ok || string(content) != "before\n" {
		t.Fatalf("a file admitted later keeps the content it had before the task: %q", content)
	}
	if _, _, ok := treeFile(after.objects(), after.origin(), "migrations/0001/up.sql"); ok {
		t.Fatal("a file the task created must not become its own before-image")
	}
	if !strings.Contains(strings.Join(asStrings(amended["did"]), "\n"), "the code is reviewed again") {
		t.Fatalf("widening the scope makes the code review stale, and the answer says so: %v", amended["did"])
	}

	f.raw, f.model = false, nil
	var packet map[string]any
	f.model = func(kind string, seen map[string]any, _ provider.Call) (string, error) {
		if kind == "review" && packet == nil {
			packet = seen
		}
		return "", nil
	}
	f.proceed()
	gate := Current()
	if len(gate.Accepted) != 3 || gate.Step != StepDone {
		t.Fatalf("the amended task runs on to its end: %+v", gate.position())
	}
	changes := map[string]map[string]any{}
	for _, raw := range packet["changes"].([]any) {
		change := raw.(map[string]any)
		changes[change["path"].(string)] = change
	}
	if len(changes) != 3 || changes["context.txt"]["status"] != "modified" || changes["migrations/0001/up.sql"]["status"] != "added" ||
		!strings.Contains(changes["migrations/0001/up.sql"]["collapsed"].(string), "generated") || changes["a.txt"]["collapsed"] != nil {
		t.Fatalf("the reviewer is shown the admitted files as changes, and generator output as one line: %v", changes)
	}
	patch, _ := os.ReadFile(packet["patch"].(string))
	if !strings.Contains(string(patch), "+generated tracked output") || strings.Contains(string(patch), "new generated migration") {
		t.Fatalf("the patch holds the reviewable change and leaves generator output out:\n%s", patch)
	}

	// The requirement that was moved is still owed: the last stage must leave it verified.
	f2 := newFixture(t)
	moved := revisedFoundation(t, stagedPlan("-m build the feature"))
	moved.Update = ""
	changeQuality(f2, func(q *QualityReport) {
		for index := range q.Requirements {
			if q.Requirements[index].ID == "R1" {
				q.Requirements[index].Status, q.Requirements[index].Cause = "partial", "plan"
			}
		}
	})
	owed := f2.start(moved)
	if owed["step"] != StepDecide || Current().Stage != 2 || !slices.Equal(needs(owed), []string{"requirement"}) {
		t.Fatalf("moving a requirement does not waive it: %v", owed)
	}
}

func TestAnAmendmentThatIsRefusedLeavesTheTaskAsItWas(t *testing.T) {
	for _, kind := range []string{"weaken", "drop-changed-file", "rename-stream", "accepted-requirement", "no-reason"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			plan := stagedPlan("-m --step build the feature")
			f.start(plan) // stage one is accepted; the task waits before stage two
			f.reviewOnce(finding("P1"))
			f.model = func(previous func(string, map[string]any, provider.Call) (string, error)) func(string, map[string]any, provider.Call) (string, error) {
				return func(kind string, packet map[string]any, call provider.Call) (string, error) {
					if kind == "writer" && packet["correction"] != nil {
						return `{"status":"BLOCKED","summary":"the remedy is unclear","blockers":["Which value?"],"handoff":"","deviations":[],"disputes":[]}`, nil
					}
					return previous(kind, packet, call)
				}
			}(f.model)
			f.proceed() // stage two is written and stops in its correction
			revised := clonePlan(t, plan)
			revised.Update = "revise the present stage"
			want := ""
			switch kind {
			case "weaken":
				revised.Requirements[1].Text, want = "A weaker result", "never weakened"
			case "drop-changed-file":
				revised.Stages[1].Streams[0].Files, want = []string{"a.txt"}, "cannot leave its scope" // b.txt is what the stage changed
			case "rename-stream":
				revised.Stages[1].Streams[0].Name, want = "replacement", "already has a writer"
			case "accepted-requirement":
				revised.Requirements[0].Text, revised.Authorized, want = "A different base", "the user", "belongs to an accepted stage"
			case "no-reason":
				revised.Update, want = " ", "needs update"
			}
			gate := Current()
			before := digest(gate)
			disk := fileText(t, filepath.Join(home(), "state.json"))
			if _, err := Amend(f.cfg, gate, revised); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("want a refusal containing %q, got %v", want, err)
			}
			if digest(gate) != before || fileText(t, filepath.Join(home(), "state.json")) != disk {
				t.Fatal("a refusal partly changed the task")
			}
		})
	}
	// What the user really changed is allowed, on the record.
	f := newFixture(t)
	plan := stagedPlan("-m --step build the feature")
	f.start(plan)
	revised := clonePlan(t, plan)
	revised.Requirements[1].Text, revised.Update, revised.Authorized = "The feature works for guests too", "the user widened the feature", "user: \"guests must see it as well\""
	f.amend(revised)
	if revision := Current().Revisions[1]; !slices.Contains(revision.Changes, "requirement R2 changed") {
		t.Fatalf("an authorized change of a requirement is recorded: %+v", revision)
	}
}

func TestEveryFileHasABeforeImageWheneverItIsAdmitted(t *testing.T) {
	f := newFixture(t)
	write(t, "context.txt", "the user's uncommitted edit\n")
	plan := stagedPlan("-m --step build the feature")
	f.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		if kind == "writer" && strings.Contains(packet["brief"].(string), "## This stage: base") {
			write(t, "context.txt", "output of the first stage\n")
		}
		return "", nil
	}
	f.start(plan)
	f.model = nil
	revised := clonePlan(t, plan)
	revised.Update = "Register the output of the first stage for the second one, without hiding it from the pass over the whole change"
	revised.Stages[1].Streams[0].Files = append(revised.Stages[1].Streams[0].Files, "context.txt")
	f.amend(revised)
	gate := Current()
	stage, _, _ := treeFile(gate.objects(), gate.StageTree, "context.txt")
	task, _, _ := treeFile(gate.objects(), gate.BaseTree, "context.txt")
	if string(stage) != "output of the first stage\n" || string(task) != "the user's uncommitted edit\n" {
		t.Fatalf("the stage and the task each keep their own starting point: stage %q, task %q", stage, task)
	}
}

func TestAnAmendmentKeepsAnUnfinishedRoundAndCanAddWork(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m implement")
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind == "writer" {
			return "", errors.New("the provider went away")
		}
		return "", nil
	}
	failed := f.start(plan)
	if failed["step"] != StepWrite || !slices.Equal(needs(failed), []string{"failed"}) || offered(failed)[0] != "run" {
		t.Fatalf("a failed writer leaves the round open, and running again retries it: %v", failed)
	}
	before := Current()
	revised := clonePlan(t, plan)
	revised.Update = "Add the neighbour that turned out to be needed"
	revised.Streams = append(revised.Streams, Stream{Name: "neighbour", Domain: "backend", Files: []string{"b.txt"}})
	amended := f.amend(revised)
	gate := Current()
	if amended["step"] != StepWrite || gate.Round.Batch != before.Round.Batch || len(gate.Round.Streams) != 2 {
		t.Fatalf("an amendment keeps the unfinished round and adds the new stream to it: %+v", gate.Round)
	}
	f.model = nil
	done := f.proceed()
	if done["step"] != StepDone || fileText(t, "b.txt") != "after\n" || f.count("writer") != 3 {
		t.Fatalf("the round is finished under the amended plan: %v", done)
	}
}

func TestAnAmendmentCanSendNewWorkToAStreamThatAlreadyReported(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-l implement")
	plan.Checks = nil
	f.start(plan) // light: stops for the lead's review
	session := Current().latest()["core"].Session
	revised := clonePlan(t, plan)
	revised.Update = "The label has to be upper case as well"
	revised.Streams[0].Brief = "Write the label in upper case"
	revised.Rework = []string{"core"}
	var seen map[string]any
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" {
			seen = packet
			if call.Resume != session {
				t.Errorf("the amendment goes into the session of the writer that wrote the stream")
			}
			write(t, "a.txt", "AFTER\n")
		}
		return "", nil
	}
	amended := f.amend(revised)
	if amended["step"] != StepWrite || f.count("writer") != 1 {
		t.Fatalf("an amendment names new work and starts no worker itself: %v", amended)
	}
	reviewed := f.proceed()
	if seen == nil || seen["update"] != revised.Update || seen["stream"].(map[string]any)["brief"] != "Write the label in upper case" {
		t.Fatalf("the writer is told what the amendment asks: %v", seen)
	}
	if reviewed["step"] != StepDecide || !slices.Equal(needs(reviewed), []string{"review"}) {
		t.Fatalf("the reworked change comes back for review: %v", reviewed)
	}
}
