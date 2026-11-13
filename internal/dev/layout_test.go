package dev

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/knaus94/agentgate/internal/provider"
)

func layoutFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LAYOUT_TEST_DIR", dir)
	command := filepath.Join(dir, "layout")
	write(t, command, `#!/bin/sh
printf '%s\n' "$*" >> "$LAYOUT_TEST_DIR/calls"
for arg do
  if [ "$arg" = "--write-baseline" ]; then
    if [ -f "$LAYOUT_TEST_DIR/fail" ]; then echo 'stand unavailable' >&2; exit 2; fi
    printf '%s\n' baseline >> "$LAYOUT_TEST_DIR/baselines"
  fi
done
if [ -f "$LAYOUT_TEST_DIR/setup" ]; then echo 'cannot observe root' >&2; exit 2; fi
exit 0
`)
	must(t, os.Chmod(command, 0o755))
	old := layoutCommand
	layoutCommand = func() string { return command }
	t.Cleanup(func() { layoutCommand = old })
	return dir
}

func screenFixture(t *testing.T) DesignRef {
	t.Helper()
	dir := ".agentgate/design/screen"
	must(t, os.MkdirAll(filepath.Join(dir, "package", "data"), 0o755))
	write(t, filepath.Join(dir, "screen.json"), `{"url":"/page","root":"#old"}`)
	write(t, filepath.Join(dir, "package", "data", "spec.json"), `{}`)
	write(t, filepath.Join(dir, "package", "manifest.json"), `{"digest":"fixture"}`)
	return DesignRef{Screen: dir, Root: "#page", URL: "/page", Stream: "core"}
}

func TestLayoutPreparationRecoversAndReplanReplacesManagedChecks(t *testing.T) {
	f := newFixture(t)
	dir := layoutFixture(t)
	plan := f.plan("-m implement the page")
	plan.Design = []DesignRef{screenFixture(t)}
	write(t, filepath.Join(dir, "fail"), "fail")
	f.fails("launch", "stand unavailable", func() (any, error) { return Launch(f.cfg, plan) })
	original := Current()
	if len(original.Writers) != 0 {
		t.Fatal("writing started before the stand was ready")
	}
	must(t, os.Remove(filepath.Join(dir, "fail")))
	plan.Update = "The stand is available again"
	f.model = func(kind string, packet map[string]any, call provider.Call) (string, error) {
		if kind == "writer" {
			for _, raw := range packet["checks"].([]any) {
				if raw.(map[string]any)["generated"] == "design" {
					t.Fatal("writer was given a gate-owned layout check")
				}
			}
			for _, path := range call.ReadDirs {
				if !filepath.IsAbs(path) {
					t.Fatalf("sandbox mount must be absolute: %s", path)
				}
			}
		}
		return "", nil
	}
	f.launch(plan)
	if Current().ID != original.ID || strings.Count(fileText(t, filepath.Join(dir, "baselines")), "baseline") != 1 {
		t.Fatal("failed preparation did not resume on the same gate")
	}
	legacy := plan.Design[0].check()
	legacy.Generated = ""
	plan.Checks = append(plan.Checks, legacy, Check{Argv: []string{layoutCommand(), "layout", "independent", "--url", "file:///tmp/reference"}})
	plan.Design[0].Root = "#content"
	plan.Update = "Pair the content export with the content root"
	f.do("replan", func() (any, error) { return Replan(f.cfg, Current(), plan) })
	gate := Current()
	if len(gate.Plan.Checks) != 3 || gate.Baseline["a.txt"].text() != "before\n" {
		t.Fatalf("stale generated checks survived replan: %+v", gate.Plan.Checks)
	}
	for _, check := range gate.Plan.Checks {
		if generatedDesignCheck(check) && !slices.Contains(check.Argv, "#content") {
			t.Fatal("screen overrides were lost")
		}
	}
	if strings.Count(fileText(t, filepath.Join(dir, "baselines")), "baseline") != 1 {
		t.Fatal("replan rebaselined the implemented page")
	}
	f.review()
	f.finish()
}

func TestLayoutSetupFailureIsNotSentToAWriter(t *testing.T) {
	f := newFixture(t)
	dir := layoutFixture(t)
	plan := f.plan("-m implement the page")
	plan.Design = []DesignRef{screenFixture(t)}
	f.launch(plan)
	write(t, filepath.Join(dir, "setup"), "failure")
	result := f.review()
	if f.count("review") != 0 || !strings.Contains(result["next"].(string), "could not observe") {
		t.Fatal("tool failure went to a code reviewer")
	}
	count := f.count("writer")
	f.fails("fix", "verification could not run", func() (any, error) { return Fix(f.cfg, Current(), FixInput{}) })
	if f.count("writer") != count {
		t.Fatal("tool failure was sent to a CSS writer")
	}
	must(t, os.Remove(filepath.Join(dir, "setup")))
	f.review()
	f.finish()
}
