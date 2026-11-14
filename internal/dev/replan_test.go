package dev

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/knaus94/agentgate/internal/provider"
)

func revisedFoundation(t *testing.T, plan *Plan) *Plan {
	t.Helper()
	raw, err := json.Marshal(plan)
	must(t, err)
	var revised Plan
	must(t, json.Unmarshal(raw, &revised))
	revised.Update = "Correct foundation coverage; retain the end-to-end requirement for final delivery"
	revised.Requirements = append(revised.Requirements, Requirement{ID: "foundation", Text: "The foundation exists", Method: "code"})
	revised.Stages[0].Covers = []string{"foundation"}
	revised.Stages[len(revised.Stages)-1].Covers = append(revised.Stages[len(revised.Stages)-1].Covers, "R1")
	return &revised
}

func TestReviewedStageCanBeAmendedWithoutWaivingWholeTask(t *testing.T) {
	f := newFixture(t)
	plan := stagedPlan("-m build the feature")
	f.launch(plan)
	write(t, "context.txt", "generated tracked output\n")
	write(t, "migration.txt", "new generated migration\n")
	changeQuality(f, func(q *QualityReport) { q.Requirements[0].Status = "partial" })
	f.review()
	f.fails("finish", "R1: partial", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "foundation only"}) })
	before := Current()
	// A task started by an older binary has snapshots but no retained whole-task Git origin yet.
	before.EpicHead, before.EpicManifest = "", nil
	must(t, before.save())
	revised := revisedFoundation(t, plan)
	revised.Stages[0].Streams[0].Files = append(revised.Stages[0].Streams[0].Files, "context.txt", "migration.txt")
	f.do("hold", func() (any, error) { return Hold(f.cfg, Current(), HoldInput{Reason: "amendment needed"}) })
	f.fails("replan", "explicitly resume", func() (any, error) { return Replan(f.cfg, Current(), revised) })
	f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) })
	f.do("replan", func() (any, error) { return Replan(f.cfg, Current(), revised) })
	after := Current()
	if after.EpicHead != before.HeadBefore || !maps.Equal(after.EpicManifest, before.Manifest) {
		t.Fatal("legacy stage zero lost its original source origin")
	}
	if after.ID != before.ID || after.StageIndex != before.StageIndex || len(after.StageRecords) != 0 || after.Phase != "awaiting-review" ||
		digest(after.Writers) != digest(before.Writers) || digest(after.Round) != digest(before.Round) || digest(after.Checks) != digest(before.Checks) ||
		digest(after.Reviews) != digest(before.Reviews) || digest(after.Dispositions) != digest(before.Dispositions) || !same(after.Baseline["a.txt"], before.Baseline["a.txt"]) {
		t.Fatal("amendment discarded stage identity, evidence, sessions or the original snapshot")
	}
	if after.Baseline["context.txt"].text() != "before\n" || after.EpicBaseline["context.txt"].text() != "before\n" || after.Baseline["migration.txt"] != nil || after.EpicBaseline["migration.txt"] != nil {
		t.Fatal("generated outputs became their own before-images")
	}
	f.fails("finish", "no current independent review", func() (any, error) { return Finish(f.cfg, after, FinishInput{Summary: "old review"}) })
	f.model = nil
	f.review()
	f.finish()
	accepted := Current()
	invalid := revisedFoundation(t, revised)
	invalid.Stages[0].Goal = "Rewrite an accepted stage"
	if err := accepted.replan(invalid); err == nil {
		t.Fatal("accepted stage changed")
	}
	var briefs []string
	stageWriter(f, &briefs)
	f.launch(revised)
	f.review()
	f.finish()
	f.launch(revised)
	changeQuality(f, func(q *QualityReport) {
		for i := range q.Requirements {
			if q.Requirements[i].ID == "R1" {
				q.Requirements[i].Status = "partial"
			}
		}
	})
	f.review()
	f.fails("finish", "R1: partial", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "runtime still missing"}) })
}

func TestStageAmendmentRefusalsPreserveTheGate(t *testing.T) {
	for _, kind := range []string{"weaken", "drop-file", "new-stream", "dirty-before"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			if kind == "dirty-before" {
				write(t, "context.txt", "user's original edit\n")
			}
			plan := stagedPlan("-m build the feature")
			f.launch(plan)
			f.review()
			revised := revisedFoundation(t, plan)
			switch kind {
			case "weaken":
				revised.Requirements[0].Text = "A weaker result"
			case "drop-file":
				revised.Stages[0].Streams[0].Files = []string{"b.txt"}
			case "new-stream":
				revised.Stages[0].Streams[0].Name = "replacement"
			case "dirty-before":
				write(t, "context.txt", "generated over the user edit\n")
				revised.Stages[0].Streams[0].Files = append(revised.Stages[0].Streams[0].Files, "context.txt")
			}
			gate := Current()
			before := digest(gate)
			disk := fileText(t, filepath.Join(home(), "state.json"))
			if _, err := Replan(f.cfg, gate, revised); err == nil {
				t.Fatal("unsafe amendment passed")
			}
			if digest(gate) != before || fileText(t, filepath.Join(home(), "state.json")) != disk {
				t.Fatal("a refusal partly changed the gate")
			}
		})
	}
}

func TestLateStageOutputsKeepDistinctStageAndTaskBaselines(t *testing.T) {
	f := newFixture(t)
	plan := stagedPlan("-m build the feature")
	f.launch(plan)
	write(t, "context.txt", "earlier generated output\n")
	f.review()
	f.finish()
	f.launch(plan)
	f.review()
	before := Current()
	raw, err := json.Marshal(plan)
	must(t, err)
	var revised Plan
	must(t, json.Unmarshal(raw, &revised))
	revised.Update = "Register an earlier generated output for this stage without hiding the whole-task diff"
	revised.Stages[1].Streams[0].Files = append(revised.Stages[1].Streams[0].Files, "context.txt")
	f.do("replan", func() (any, error) { return Replan(f.cfg, Current(), &revised) })
	after := Current()
	if after.Baseline["context.txt"].text() != "earlier generated output\n" || after.EpicBaseline["context.txt"].text() != "before\n" || digest(after.StageRecords) != digest(before.StageRecords) {
		t.Fatal("late ownership confused the stage's starting point with the original task")
	}
}

func TestReplanPreservesAnUnfinishedRoundsContinuation(t *testing.T) {
	for _, staged := range []bool{false, true} {
		name := "single"
		if staged {
			name = "staged"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			plan := f.plan("-m implement")
			if staged {
				plan = stagedPlan("-m implement")
			}
			f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
				if kind == "writer" {
					return "", errors.New("interrupted")
				}
				return "", nil
			}
			f.fails("launch", "interrupted", func() (any, error) { return Launch(f.cfg, plan) })
			f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) })
			before := Current()
			plan.Update = "Clarify verification without changing the writer assignment"
			f.do("replan", func() (any, error) { return Replan(f.cfg, Current(), plan) })
			if Current().Phase != "implementing" || Current().Round.Batch != before.Round.Batch {
				t.Fatal("replan stranded the unfinished round")
			}
			f.model = nil
			f.do("continue", func() (any, error) { return Continue(f.cfg, Current()) })
			f.review()
			f.finish()
		})
	}
}

func TestStagedPreparationReusesDiscoveryForTheFirstStage(t *testing.T) {
	f := newFixture(t)
	plan := stagedPlan("-m build the feature")
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind != "prepare" {
			return "", nil
		}
		proposal, _ := json.Marshal(plan)
		answer, _ := json.Marshal(map[string]any{"status": "READY", "summary": "Three stages", "questions": []string{}, "setup": []string{}, "plan_json": string(proposal), "context_pack": map[string]any{"summary": "Existing backend contracts", "files": []packFile{{Path: "a.txt", Role: "foundation"}}}})
		return string(answer), nil
	}
	f.do("prepare", func() (any, error) { return Prepare(f.cfg, PrepareInput{Request: plan.Request, Name: "staged"}) })
	path, err := PlanFile("staged")
	must(t, err)
	var saved Plan
	raw, err := os.ReadFile(path)
	must(t, err)
	must(t, json.Unmarshal(raw, &saved))
	f.model = nil
	f.launch(&saved)
	if f.count("scout") != 0 || Current().ContextPack == "" {
		t.Fatal("staged launch repeated the preparation's discovery")
	}
}
