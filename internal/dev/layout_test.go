package dev

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/knaus94/agentgate/internal/provider"
)

// leadQuality answers the criteria of the evidence the lead was handed the way the fixture's reviewer would.
func leadQuality(t *testing.T, summary map[string]any) *QualityReport {
	t.Helper()
	list, _ := summary["needs"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["kind"] != "review" {
		t.Fatalf("the lead's own review is not what the task waits for: %v", summary)
	}
	packet := map[string]any{}
	must(t, json.Unmarshal([]byte(fileText(t, list[0].(map[string]any)["detail"].(map[string]any)["packet"].(string))), &packet))
	return fixtureQuality(packet)
}

func lineCount(t *testing.T, path string) int {
	t.Helper()
	return strings.Count(fileText(t, path), "\n")
}

func TestDesignAcceptanceRequiresEvidenceBeyondMeasurements(t *testing.T) {
	f := newFixture(t)
	layoutFixture(t)
	plan := f.plan("-m implement the page")
	plan.Design = []DesignRef{screenFixture(t)}
	verdict := "cropped"
	changeQuality(f, func(report *QualityReport) {
		for i := range report.Rules {
			switch {
			case verdict == "cropped" && strings.HasSuffix(report.Rules[i].ID, ".appearance"):
				report.Rules[i].Status = "block"
				report.Rules[i].Evidence = "The frame dimensions match, but the artwork is cropped differently"
			case verdict == "skipped" && i == 0:
				report.Rules[i].Status = "not_applicable"
			}
		}
	})
	blocked := f.start(plan)
	input, err := Current().qualityInputs(f.cfg)
	must(t, err)
	if input == nil || len(input.Rules) == 0 {
		t.Fatal("passing measurements were accepted without independent design criteria")
	}
	if !slices.Equal(needs(blocked), []string{"rule"}) || !strings.Contains(blocked["needs"].([]any)[0].(map[string]any)["what"].(string), "artwork is cropped") {
		t.Fatalf("a measurement PASS completed a design whose picture was judged different: %v", blocked)
	}
	verdict = "skipped"
	f.fails("run", "must be assessed", func() (any, error) { return Run(f.cfg, Current(), RunInput{Recheck: true}) })
	gate := Current()
	reviewed := gate.Reviews[len(gate.Reviews)-1].Contract
	must(t, os.Remove(gate.Plan.Design[0].reviewFiles()["page_context_picture"]))
	if gate.codeContract() == reviewed {
		t.Fatal("a review stayed current after a picture it read was removed")
	}
	input, err = gate.qualityInputs(f.cfg)
	must(t, err)
	if !slices.ContainsFunc(input.Rules, func(rule reviewRule) bool {
		return strings.HasSuffix(rule.ID, ".placement") && strings.Contains(rule.Unavailable, "page.png")
	}) {
		t.Fatal("a cropped screenshot was allowed to prove placement in the shell")
	}
	verdict = "passes"
	if done := f.do("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{Recheck: true}) }); done["step"] != StepDone {
		t.Fatalf("the page is measured again and the reviewer's fresh reading completes the task: %v", done)
	}
}

func TestLegacyDesignContextRefreshDoesNotRebaselineImplementedCode(t *testing.T) {
	f := newFixture(t)
	dir := layoutFixture(t)
	plan := f.plan("-l implement the page")
	plan.Design = []DesignRef{screenFixture(t)}
	f.start(plan)
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

func TestLayoutPreparationRecoversAndAnAmendmentReplacesManagedChecks(t *testing.T) {
	f := newFixture(t)
	dir := layoutFixture(t)
	plan := f.plan("-l implement the page")
	plan.Design = []DesignRef{screenFixture(t)}
	write(t, filepath.Join(dir, "fail"), "fail")
	// A stand that cannot be reached is the lead's to repair: the task waits with that need, it does not fail.
	if started := f.start(plan); !slices.Equal(needs(started), []string{"setup"}) || !strings.Contains(fmt.Sprint(started["needs"]), "stand unavailable") {
		t.Fatalf("a design that cannot be measured is a setup need naming the cause: %v", started)
	}
	original := Current()
	if original == nil || len(original.Writers) != 0 {
		t.Fatal("writing started before the stand was ready")
	}
	must(t, os.Remove(filepath.Join(dir, "fail")))
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
	f.proceed()
	if Current().ID != original.ID || strings.Count(fileText(t, filepath.Join(dir, "baselines")), "baseline") != 1 {
		t.Fatal("failed preparation did not resume on the same task")
	}
	revised := clonePlan(t, plan)
	legacy := Current().Plan.Design[0].check()
	legacy.Generated = ""
	revised.Checks = append(revised.Checks, legacy, Check{Argv: []string{layoutCommand(), "layout", "independent", "--url", "file:///tmp/reference"}})
	revised.Design[0].Root = "#content"
	revised.Design[0].Device = "mobile"
	revised.Design[0].Viewport = "390x844"
	revised.Update = "Pair the content export with the content root"
	f.amend(revised)
	gate := Current()
	before, _, _ := treeFile(gate.objects(), gate.BaseTree, "a.txt")
	checks := gate.checks()
	if len(checks) != 3 || string(before) != "before\n" || gate.BaseTree != original.BaseTree {
		t.Fatalf("stale generated checks survived the amendment, or the task lost its origin: %+v", checks)
	}
	managed := 0
	for _, check := range checks {
		if !generatedDesignCheck(check) {
			continue
		}
		if managed++; !slices.Contains(check.Argv, "#content") || !slices.Contains(check.Argv, "mobile") || !slices.Contains(check.Argv, "390x844") {
			t.Fatal("screen overrides were lost")
		}
	}
	if managed != 1 {
		t.Fatalf("a design has one layout check, built from its present description: %+v", checks)
	}
	facts := gate.designContext()
	if len(facts) != 1 || facts[0]["baseline_exists_on_host"] != false ||
		facts[0]["effective"].(map[string]string)["device"] != "mobile" || !strings.Contains(facts[0]["baseline_policy"].(string), "not a prerequisite") {
		t.Fatalf("preparation lacks the host's measurement facts: %+v", facts)
	}
	handed := f.proceed()
	if strings.Count(fileText(t, filepath.Join(dir, "baselines")), "baseline") != 1 {
		t.Fatal("the amendment rebaselined the implemented page")
	}
	callsBefore := lineCount(t, filepath.Join(dir, "calls"))
	handed = f.do("run", func() (any, error) { return Run(f.cfg, Current(), RunInput{Recheck: true}) })
	if lineCount(t, filepath.Join(dir, "calls")) <= callsBefore {
		t.Fatal("a rendered no_reuse check must run when verification is asked again, even when the source is unchanged")
	}
	quality := leadQuality(t, handed)
	image := filepath.Join(Current().Plan.Design[0].dir(), "last", "actual.png")
	write(t, image, "new rendered evidence")
	f.fails("decide", "changed since the evidence was prepared", func() (any, error) {
		return Decide(f.cfg, Current(), DecideInput{Review: &LeadReview{Summary: "old screenshot review", Quality: quality}})
	})
	handed = f.proceed()
	if done := f.decide(DecideInput{Review: &LeadReview{Summary: "read the page next to its design", Quality: leadQuality(t, handed)}}); done["step"] != StepDone {
		t.Fatalf("a reading of the present evidence completes the task: %v", done)
	}
}

func TestLayoutSetupFailureIsNotSentToAWriter(t *testing.T) {
	f := newFixture(t)
	dir := layoutFixture(t)
	plan := f.plan("-m implement the page")
	plan.Design = []DesignRef{screenFixture(t)}
	f.model = func(kind string, _ map[string]any, _ provider.Call) (string, error) {
		if kind == "writer" {
			write(t, filepath.Join(dir, "setup"), "failure") // the stand breaks once the page is written
		}
		return "", nil
	}
	stopped := f.start(plan)
	if f.count("review") != 0 || !slices.Equal(needs(stopped), []string{"setup"}) ||
		!strings.Contains(stopped["needs"].([]any)[0].(map[string]any)["what"].(string), "could not observe") {
		t.Fatalf("tool failure went to a code reviewer: %v", stopped)
	}
	count := f.count("writer")
	if again := f.proceed(); f.count("writer") != count || f.count("review") != 0 || !slices.Equal(needs(again), []string{"setup"}) {
		t.Fatalf("tool failure was sent to a CSS writer: %v", again)
	}
	must(t, os.Remove(filepath.Join(dir, "setup")))
	if done := f.proceed(); done["step"] != StepDone || f.count("writer") != count {
		t.Fatalf("once the stand is repaired, running again measures the page and goes on: %v", done)
	}
}

func TestPreparationOfAnActiveDesignReusesItsHostContext(t *testing.T) {
	f := newFixture(t)
	dir := layoutFixture(t)
	plan := f.plan("-l implement https://www.figma.com/design/abc/example?node-id=1-2")
	plan.Design = []DesignRef{screenFixture(t)}
	f.start(plan)
	f.model = func(kind string, packet map[string]any, _ provider.Call) (string, error) {
		if kind == "prepare" {
			if packet["design_context"] == nil || packet["design_source"] == nil {
				t.Fatal("no reusable host context")
			}
			return `{"status":"BLOCKED","summary":"one explicit mapping is needed","questions":[],"setup":{"authorizations":[],"notes":["Select the existing artwork element"]},"plan":null,"context_pack":null}`, nil
		}
		return "", nil
	}
	f.prepare(PrepareInput{Notes: "repair measurement"})
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
