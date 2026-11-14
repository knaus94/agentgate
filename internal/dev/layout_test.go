package dev

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/knaus94/agentgate/internal/provider"
)

func TestDesignAcceptanceRequiresEvidenceBeyondMeasurements(t *testing.T) {
	f := newFixture(t)
	layoutFixture(t)
	plan := f.plan("-m implement the page")
	plan.Design = []DesignRef{screenFixture(t)}
	f.launch(plan)
	f.review()
	input, err := Current().qualityInputs(f.cfg)
	must(t, err)
	if input == nil || len(input.Rules) == 0 {
		t.Fatal("passing measurements were accepted without independent design criteria")
	}
	changeQuality(f, func(report *QualityReport) {
		for i := range report.Rules {
			if strings.HasSuffix(report.Rules[i].ID, ".appearance") {
				report.Rules[i].Status = "block"
				report.Rules[i].Evidence = "The frame dimensions match, but the artwork is cropped differently"
			}
		}
	})
	f.do("review", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{Recheck: true}) })
	f.fails("finish", "artwork is cropped", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "measurement PASS"}) })
	changeQuality(f, func(report *QualityReport) { report.Rules[0].Status = "not_applicable" })
	f.fails("review", "must be assessed", func() (any, error) { return ReviewTask(f.cfg, Current(), ReviewInput{Recheck: true}) })
	f.model = nil
	f.review()
	gate := Current()
	must(t, os.Remove(gate.Plan.Design[0].reviewFiles()["page_context_picture"]))
	f.fails("finish", "task contract changed", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "old context"}) })
	input, err = gate.qualityInputs(f.cfg)
	must(t, err)
	if !slices.ContainsFunc(input.Rules, func(rule reviewRule) bool {
		return strings.HasSuffix(rule.ID, ".placement") && strings.Contains(rule.Unavailable, "page.png")
	}) {
		t.Fatal("a cropped screenshot was allowed to prove placement in the shell")
	}
	f.review()
	f.finish()
}

func TestLegacyDesignContextRefreshDoesNotRebaselineImplementedCode(t *testing.T) {
	f := newFixture(t)
	dir := layoutFixture(t)
	plan := f.plan("-m implement the page")
	plan.Design = []DesignRef{screenFixture(t)}
	f.launch(plan)
	gate := Current()
	design := &gate.Plan.Design[0]
	design.Figma = "https://www.figma.com/design/fixture/example?node-id=1-1"
	sealDesignFixture(t, design.Package, map[string]any{"source": map[string]any{"key": "fixture", "node": "1:1"}, "frame": map[string]any{"id": "1:1"}, "screen": map[string]any{"width": 400, "height": 300}})
	if design.imported() {
		t.Fatal("a legacy crop without screen context was considered sufficient evidence")
	}
	must(t, gate.prepareDesigns(false))
	if !strings.Contains(fileText(t, filepath.Join(dir, "calls")), "layout new") ||
		strings.Count(fileText(t, filepath.Join(dir, "baselines")), "baseline") != 1 {
		t.Fatal("refresh must import context without capturing the implemented page as its baseline")
	}
	write(t, filepath.Join(design.Package, "context.png"), "parent picture")
	sealDesignFixture(t, design.Package, map[string]any{"source": map[string]any{"key": "fixture", "node": "1:1"}, "frame": map[string]any{"id": "1:1"}, "screen": map[string]any{"id": "2:1"}})
	if !design.imported() {
		t.Fatal("a complete contextual package would be imported again on every review")
	}
}

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
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--out" ]; then
    shift
    mkdir -p "$1"
    for name in actual.png page.png diff.png report.md report.json; do printf '%s\n' 'fixture evidence' > "$1/$name"; done
  fi
  shift
done
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
	write(t, filepath.Join(dir, "package", "data", "nodes.json"), `{}`)
	write(t, filepath.Join(dir, "package", "frame.png"), "fixture picture")
	write(t, filepath.Join(dir, "package", "brief.md"), "The screen and its shell")
	sealDesignFixture(t, filepath.Join(dir, "package"), map[string]any{"frame": map[string]any{"id": "1:1"}, "screen": map[string]any{"id": "1:1"}})
	return DesignRef{Screen: dir, Root: "#page", URL: "/page", Stream: "core"}
}

func sealDesignFixture(t *testing.T, dir string, metadata map[string]any) {
	t.Helper()
	files := map[string]string{}
	must(t, filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Base(path) == "manifest.json" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		name, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(name)] = hex.EncodeToString(sum[:])
		return nil
	}))
	metadata["files"], metadata["digest"] = files, packageContentDigest(files)
	raw, err := json.Marshal(metadata)
	must(t, err)
	write(t, filepath.Join(dir, "manifest.json"), string(raw))
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
	plan.Design[0].Device = "mobile"
	plan.Design[0].Viewport = "390x844"
	plan.Update = "Pair the content export with the content root"
	f.do("replan", func() (any, error) { return Replan(f.cfg, Current(), plan) })
	gate := Current()
	if len(gate.Plan.Checks) != 3 || gate.Baseline["a.txt"].text() != "before\n" {
		t.Fatalf("stale generated checks survived replan: %+v", gate.Plan.Checks)
	}
	for _, check := range gate.Plan.Checks {
		if generatedDesignCheck(check) && (!slices.Contains(check.Argv, "#content") || !slices.Contains(check.Argv, "mobile") || !slices.Contains(check.Argv, "390x844")) {
			t.Fatal("screen overrides were lost")
		}
	}
	facts := gate.designContext()
	if len(facts) != 1 || facts[0]["baseline_exists_on_host"] != false ||
		facts[0]["effective"].(map[string]string)["device"] != "mobile" || !strings.Contains(facts[0]["baseline_policy"].(string), "not a prerequisite") {
		t.Fatalf("preparation lacks the host's measurement facts: %+v", facts)
	}
	if strings.Count(fileText(t, filepath.Join(dir, "baselines")), "baseline") != 1 {
		t.Fatal("replan rebaselined the implemented page")
	}
	f.review()
	callsBefore := strings.Count(fileText(t, filepath.Join(dir, "calls")), "\n")
	f.review()
	if strings.Count(fileText(t, filepath.Join(dir, "calls")), "\n") <= callsBefore {
		t.Fatal("a rendered no_reuse check must run on review even when source is unchanged")
	}
	image := filepath.Join(Current().Plan.Design[0].dir(), "last", "actual.png")
	must(t, os.MkdirAll(filepath.Dir(image), 0o755))
	write(t, image, "new rendered evidence")
	f.fails("finish", "task contract changed", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "old screenshot review"}) })
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

func TestPreparationOfAnActiveDesignReusesItsHostContext(t *testing.T) {
	f := newFixture(t)
	dir := layoutFixture(t)
	plan := f.plan("-m implement https://www.figma.com/design/abc/example?node-id=1-2")
	plan.Design = []DesignRef{screenFixture(t)}
	f.launch(plan)
	f.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		if kind == "prepare" {
			if packet["design_context"] == nil || packet["design_source"] == nil {
				t.Fatal("no reusable host context")
			}
			return `{"status":"BLOCKED","summary":"one explicit mapping is needed","questions":[],"setup":["Select the existing artwork element"],"plan_json":"","context_pack":{}}`, nil
		}
		return "", nil
	}
	f.do("prepare", func() (any, error) { return Prepare(f.cfg, PrepareInput{Notes: "repair measurement"}) })
	if strings.Contains(fileText(t, filepath.Join(dir, "calls")), "figma") {
		t.Fatal("an active measurement repair reopened Figma")
	}
}

func TestMalformedDesignURLIsAnErrorNotAPanic(t *testing.T) {
	f := newFixture(t)
	layoutFixture(t)
	design := screenFixture(t)
	design.Screen = ""
	for _, target := range []string{"http", "https:", "http:/broken", "http://", "httpx://localhost", "https://user:password@localhost/", "http://example.com/"} {
		plan := f.plan("-m implement the page")
		design.URL = target
		plan.Design = []DesignRef{design}
		if _, err := Route(f.cfg, plan); err == nil {
			t.Errorf("malformed/nonlocal URL accepted: %s", target)
		}
	}
}
