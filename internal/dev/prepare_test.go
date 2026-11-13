package dev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knaus94/agentgate/internal/provider"
)

func TestPreparationDraftsWithoutWritingAndReusesDiscovery(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-l the worker must not change the requested mode")
	f.cfg.Design.Stand = json.RawMessage(`{"command":["yarn","dev"],"ready":"http://127.0.0.1:4200","cookies":[{"value":"private-cookie"}],"env":{"TOKEN":"private-value"}}`)
	var attempts int
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind != "prepare" {
			return "", nil
		}
		attempts++
		if call.Writer || (attempts == 2 && call.Resume == "") {
			t.Fatal("preparation must read only and repair its draft in the same session")
		}
		raw, _ := json.Marshal(packet)
		if strings.Contains(string(raw), "private-cookie") || strings.Contains(string(raw), "private-value") {
			t.Fatal("stand credentials reached the preparation worker")
		}
		proposal, _ := json.Marshal(plan)
		if attempts == 1 {
			proposal = []byte(`{"brief":"missing streams"}`)
		}
		answer := map[string]any{"status": "READY", "summary": "one line change", "questions": []string{}, "setup": []string{}, "plan_json": string(proposal),
			"context_pack": map[string]any{"summary": "fixture map", "files": []packFile{{Path: "a.txt", Role: "owned"}}}}
		out, _ := json.Marshal(answer)
		return string(out), nil
	}
	result := f.do("prepare", func() (any, error) {
		return Prepare(f.cfg, PrepareInput{Request: "-m change the line", Name: "prepared"})
	})
	if result["status"] != "READY" || attempts != 2 || fileText(t, "a.txt") != "before\n" || Current() != nil {
		t.Fatalf("preparation executed work: %v", result)
	}
	path, err := PlanFile("prepared")
	must(t, err)
	var saved Plan
	must(t, json.Unmarshal([]byte(fileText(t, path)), &saved))
	if saved.Request != "-m change the line" {
		t.Fatal("the original request was weakened")
	}
	f.model = nil
	f.launch(&saved)
	if f.count("scout") != 0 || Current().ContextPack == "" {
		t.Fatal("preparation's discovery must be handed to the writer without another scout")
	}
	f.review()
	f.finish()
}

func TestPreparationKeepsMissingDecisionsAndToolFailuresOutOfTheLead(t *testing.T) {
	f := newFixture(t)
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind == "prepare" {
			return `{"status":"BLOCKED","summary":"no business contract","questions":["Which API supplies the balance?"],"setup":[],"plan_json":"","context_pack":{}}`, nil
		}
		return "", nil
	}
	result := f.do("prepare", func() (any, error) { return Prepare(f.cfg, PrepareInput{Request: "-m add a page", Name: "blocked"}) })
	if result["status"] != "BLOCKED" || Current() != nil {
		t.Fatal(result)
	}
	if _, err := os.Stat(filepath.Join(PlansDir, "blocked", "plan.json")); !os.IsNotExist(err) {
		t.Fatal("a blocked draft was saved as executable")
	}
	command := filepath.Join(t.TempDir(), "catalogue")
	write(t, command, "#!/bin/sh\nprintf '%s\\n' 'profile unavailable' >&2\nexit 2\n")
	must(t, os.Chmod(command, 0o755))
	old := layoutCommand
	layoutCommand = func() string { return command }
	t.Cleanup(func() { layoutCommand = old })
	f.fails("prepare", "profile unavailable", func() (any, error) {
		return Prepare(f.cfg, PrepareInput{Request: "-m build https://www.figma.com/design/abc/example?node-id=1-2"})
	})
	if f.count("prepare") != 1 {
		t.Fatal("do not spend a worker when the design catalogue is unavailable")
	}
}

func TestReplanKeepsTheTaskAndRequiresFreshReview(t *testing.T) {
	f := newFixture(t)
	plan := f.plan("-m change the line")
	f.launch(plan)
	f.review()
	before := Current()
	plan.Update = "Add a precise check without changing the implementation"
	plan.Checks = append(plan.Checks, Check{Argv: []string{"test", "-s", "a.txt"}})
	f.do("replan", func() (any, error) { return Replan(f.cfg, Current(), plan) })
	after := Current()
	if after.ID != before.ID || digest(after.Baseline) != digest(before.Baseline) || len(after.Writers) != len(before.Writers) || after.Phase != "awaiting-review" {
		t.Fatal("replan reset the task or its original snapshot")
	}
	f.fails("finish", "no current independent review", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "not yet"}) })
	f.do("hold", func() (any, error) { return Hold(f.cfg, Current(), HoldInput{Reason: "user pause"}) })
	f.fails("replan", "explicitly resume", func() (any, error) { return Replan(f.cfg, Current(), plan) })
	f.do("resume", func() (any, error) { return Resume(f.cfg, Current()) })
	f.review()
	f.finish()
}
