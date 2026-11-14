package dev

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// earlierState is a task as 0.15 saved it: three stages, the first accepted, the second written and reviewed,
// stopped by its user. Stage one changed a.txt and stage two b.txt; neither change is committed.
func earlierState(f *fixture) map[string]any {
	f.t.Helper()
	cwd, err := os.Getwd()
	must(f.t, err)
	head := strings.TrimSpace(gitOut(f.t, "rev-parse", "HEAD"))
	route, err := json.Marshal(f.cfg.MustRoute("opus:medium"))
	must(f.t, err)
	reviewer, err := json.Marshal(f.cfg.MustRoute("astra:medium"))
	must(f.t, err)
	before := map[string]any{"bytes": base64.StdEncoding.EncodeToString([]byte("before\n")), "mode": 420}
	stream := func(name, file string) map[string]any {
		return map[string]any{"name": name, "domain": "backend", "files": []string{file}}
	}
	stage := func(name, covers, streamName, file string) map[string]any {
		return map[string]any{"name": name, "goal": "deliver " + covers, "covers": []string{covers},
			"streams": []any{stream(streamName, file)}, "checks": []any{map[string]any{"argv": []string{"true"}}}}
	}
	request := "-m build the feature in three parts"
	assessment := map[string]any{"size": "ordinary", "evidence": "three small files"}
	requirements := []any{map[string]any{"id": "R1", "text": "the first part"}, map[string]any{"id": "R2", "text": "the second part"},
		map[string]any{"id": "R3", "text": "the third part"}}
	dir := filepath.Join(home(), strings.Repeat("c", 32))
	return map[string]any{
		"id": strings.Repeat("c", 32), "session": session(), "cwd": cwd, "mode": "medium", "created_at": 1794662327,
		"request": map[string]any{"action": "implement", "mode": "medium", "task": "build the feature in three parts", "grill": "auto"},
		"plan": map[string]any{"request": request, "brief": "Build the feature", "assessment": assessment, "stage_view": true,
			"requirements": []any{map[string]any{"id": "R2", "text": "the second part"}},
			"streams":      []any{stream("two", "b.txt")}, "checks": []any{map[string]any{"argv": []string{"true"}}}},
		"epic": map[string]any{"request": request, "brief": "Build the feature", "assessment": assessment, "requirements": requirements,
			"context": []string{"context.txt", "Legacy: /srv/old/handler.ts:10-20 — the handler this replaces"},
			"stages":  []any{stage("first", "R1", "one", "a.txt"), stage("second", "R2", "two", "b.txt"), stage("third", "R3", "three", "c.txt")}},
		"stage_index": 1,
		"stage_records": []any{map[string]any{"name": "first", "mode": "medium", "summary": "wrote the first part", "accepted_at": 1794668378,
			"correction_rounds": 0, "findings": 0, "handoffs": map[string]string{"one": "contract of one"}, "archive": filepath.Join(dir, "stages", "1-first.json")}},
		"baseline":      map[string]any{"b.txt": before},
		"epic_baseline": map[string]any{"a.txt": before, "b.txt": before, "c.txt": nil},
		"head_before":   head, "epic_head_before": head,
		"phase": "held", "resume_phase": "awaiting-review", "hold_reason": "the user stopped /dev", "iteration": 0,
		"writers": []any{map[string]any{"stream": "two", "route": json.RawMessage(route), "dir": filepath.Join(dir, "writers", "two-0001"),
			"session": "writer-session", "status": "COMPLETED", "summary": "wrote the second part", "blockers": []string{}, "handoff": "contract of two", "batch": "v1"}},
		"author_models": []string{f.cfg.MustRoute("opus:medium").Model},
		"round":         map[string]any{"batch": "v1", "streams": []any{stream("two", "b.txt")}},
		"reviews": []any{map[string]any{"iteration": 0, "digest": strings.Repeat("d", 64), "contract": strings.Repeat("e", 64), "dir": filepath.Join(dir, "review-0-0001"),
			"lanes": []any{map[string]any{"axes": []string{"correctness"}, "scope": "", "files": []string{"b.txt"}, "route": json.RawMessage(reviewer),
				"session": "review-session", "dir": filepath.Join(dir, "review-0-0001", "lane-0", "run"),
				"reports": []any{map[string]any{"axis": "correctness", "status": "PASS", "findings": []any{}, "checked": []string{"read the diff"}, "resolutions": []any{}}}}}}},
		"review_digest": strings.Repeat("d", 64),
		"checks":        []any{map[string]any{"argv": []string{"true"}, "returncode": 0, "digest": strings.Repeat("f", 64), "repository_digest": strings.Repeat("a", 64)}},
		"dispositions":  map[string]any{}, "resolutions": map[string]any{},
	}
}

func saveEarlier(t *testing.T, state map[string]any) (string, []byte) {
	t.Helper()
	raw, err := json.MarshalIndent(state, "", " ")
	must(t, err)
	path := filepath.Join(home(), "state.json")
	must(t, os.MkdirAll(home(), 0o700))
	must(t, os.WriteFile(path, raw, 0o600))
	return path, raw
}

func TestAnEarlierTaskIsReadWithoutBeingRewritten(t *testing.T) {
	f := newFixture(t)
	path, raw := saveEarlier(t, earlierState(f))
	gate, err := LoadCurrent()
	must(t, err)
	if gate.Step != StepVerify || gate.Pause == nil || gate.Pause.Kind != PauseUser || gate.Stage != 1 || len(gate.Accepted) != 1 ||
		len(gate.Plan.Stages) != 3 || !slices.Equal(gate.Modes, []string{"medium", "medium", ""}) || gate.Round == nil || gate.Round.Batch != "v1" {
		t.Fatalf("the position of an earlier task is translated: step=%s pause=%+v stage=%d modes=%q", gate.Step, gate.Pause, gate.Stage, gate.Modes)
	}
	if !slices.Equal(gate.Plan.Context, []string{"context.txt"}) || !strings.Contains(gate.Plan.Brief, "Legacy: /srv/old/handler.ts:10-20") {
		t.Fatalf("free text among the context files becomes a note in the brief: %q\n%s", gate.Plan.Context, gate.Plan.Brief)
	}
	if _, err := Resolve(f.cfg, gate.Plan); err != nil {
		t.Fatalf("the translated plan passes the present validator: %v", err)
	}
	status, err := Status(f.cfg, gate, "")
	must(t, err)
	answer := asMap(status)
	if answer["saved_by_an_earlier_version"] != true || !slices.Equal(offered(answer), []string{"resume", "stop"}) {
		t.Fatalf("status says what the task is and what is possible: %v", answer)
	}
	if text := PanelText(f.cfg, false, false, 120, 0); !strings.Contains(text, "2. second") || !strings.Contains(text, "paused (user)") {
		t.Fatalf("the panel shows an earlier task:\n%s", text)
	}
	if hook := StopHook(gate.Cwd, gate.Session); len(hook) != 0 {
		t.Fatalf("the Stop hook does not hold a session to a task it has not converted: %v", hook)
	}
	// Another session must still see what this task owns.
	t.Setenv("AGENTGATE_SESSION", "another-lead")
	f.fails("start", "owned by another session", func() (any, error) {
		return Start(f.cfg, f.plan("-m change the second part", Stream{Name: "other", Domain: "backend", Files: []string{"b.txt"}}), "", false, RunInput{})
	})
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "state.v1.json")); !os.IsNotExist(err) || fileText(t, path) != string(raw) {
		t.Fatal("reading an earlier task must not write anything")
	}

	// A hold stood for five things; only waiting for a person is still a pause.
	for name, test := range map[string]struct {
		phase, resume, reason string
		final                 bool
		step, pause           string
		refused               bool
	}{
		"writing":                    {phase: "implementing", step: StepWrite},
		"reviewed":                   {phase: "reviewed", step: StepVerify},
		"a blocked writer":           {phase: "held", resume: "implementing", reason: "writer two is blocked: which wording?", step: StepWrite},
		"an interrupted run":         {phase: "held", resume: "correcting", reason: "the operation was interrupted", step: StepWrite},
		"confirm every stage":        {phase: "held", resume: "staged", reason: "stage first is accepted; the user asked to confirm each stage before the next one starts", step: StepWrite, pause: PauseStep},
		"waiting for a decision":     {phase: "held", resume: "reviewed", reason: "the retention period is the user's call", step: StepVerify, pause: PauseDecision},
		"complete":                   {phase: "complete", final: true, step: StepDone},
		"complete without a receipt": {phase: "complete", refused: true},
		"cancelled":                  {phase: "cancelled", resume: "reviewed", step: StepCancelled},
		"an unknown phase":           {phase: "paused", refused: true},
	} {
		state := earlierState(f)
		state["phase"], state["resume_phase"], state["hold_reason"] = test.phase, test.resume, test.reason
		if test.final {
			state["final"] = map[string]any{"summary": "done", "at": 1794670000}
		}
		encoded, err := json.Marshal(state)
		must(t, err)
		translated, err := migrateState(encoded)
		if test.refused {
			if err == nil {
				t.Errorf("%s: a state that cannot be understood is refused, not guessed", name)
			}
			continue
		}
		must(t, err)
		kind := ""
		if translated.Pause != nil {
			kind = translated.Pause.Kind
		}
		if translated.Step != test.step || kind != test.pause || validateGateState(translated) != nil {
			t.Errorf("%s: step %s pause %q, want %s %q (%v)", name, translated.Step, kind, test.step, test.pause, validateGateState(translated))
		}
	}
}

func TestAnEarlierTaskIsConvertedByItsOwnSessionAndRunsToTheEnd(t *testing.T) {
	f := newFixture(t)
	write(t, "a.txt", "first\n")
	write(t, "b.txt", "second\n")
	write(t, "notes.txt", "the user's own uncommitted note\n")
	path, raw := saveEarlier(t, earlierState(f))

	resumed := f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) })
	gate := Current()
	if gate.Version != stateVersion || gate.legacy != nil || fileText(t, filepath.Join(filepath.Dir(path), "state.v1.json")) != string(raw) {
		t.Fatal("the first operation of the task's session converts it and keeps the original beside it")
	}
	if resumed["step"] != StepVerify || resumed["paused"] != nil || offered(resumed)[0] != "run" {
		t.Fatalf("the user's stop is lifted by resume, and the task can run: %v", resumed)
	}
	now, err := gate.snapshot()
	must(t, err)
	owned, foreign, err := gate.changes(now)
	must(t, err)
	if len(owned) != 1 || owned[0].Path != "b.txt" || len(foreign) != 0 {
		t.Fatalf("the present stage is measured from its own before-images; nothing else is its change: %+v %v", owned, foreign)
	}
	whole, err := diffTrees(gate.objects(), gate.BaseTree, now)
	must(t, err)
	var paths []string
	for _, change := range whole {
		paths = append(paths, change.Path)
	}
	stageBefore, _, _ := treeFile(gate.objects(), gate.StageTree, "b.txt")
	taskBefore, _, _ := treeFile(gate.objects(), gate.BaseTree, "a.txt")
	accepted, _, _ := treeFile(gate.objects(), gate.StageTree, "a.txt")
	if !slices.Equal(paths, []string{"a.txt", "b.txt"}) || string(stageBefore) != "before\n" || string(taskBefore) != "before\n" || string(accepted) != "first\n" {
		t.Fatalf("the task is measured from where it began, the stage from where the stage before it ended: %v", paths)
	}

	done := f.proceed()
	gate = Current()
	if done["step"] != StepDone || len(gate.Accepted) != 3 || !slices.Equal(gate.Modes, []string{"medium", "medium", "medium"}) {
		t.Fatalf("a converted task goes on to its end: %v", done)
	}
	if f.count("writer") != 1 || fileText(t, "c.txt") != "after\n" || fileText(t, "b.txt") != "second\n" {
		t.Fatalf("what was written before the upgrade is not written again; only the stage that had not started runs: %d writers", f.count("writer"))
	}
	if f.count("review") != 3 {
		t.Fatalf("the stage in progress is verified once more, the last stage is reviewed, and the whole change once: %d reviews", f.count("review"))
	}
}
