package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigReadsDoNotRequireOrMigrateState(t *testing.T) {
	s := newSandbox(t)
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	for _, state := range []string{filepath.Join(locked, "state"), filepath.Join(t.TempDir(), "absent")} {
		reader := &sandbox{t: t, repo: s.repo, env: append(append([]string{}, s.env...), "CODEX_THREAD_ID=config-reader", "XDG_STATE_HOME="+state)}
		for _, subcommand := range []string{"show", "validate", "path", "profiles", "extensions"} {
			if _, stderr, code := reader.run("", "config", subcommand); code != 0 {
				t.Fatalf("config %s needs no writable state: %s", subcommand, stderr)
			}
		}
		if _, err := os.Stat(state); !os.IsNotExist(err) {
			t.Fatalf("config read created state: %s", state)
		}
	}
}

func TestHelpCannotInstallUpdateOrStop(t *testing.T) {
	s := newSandbox(t)
	// Configuration errors cannot prevent help, and help cannot create/update
	// state or lead integrations before explaining what a command does.
	for _, variable := range s.env {
		if strings.HasPrefix(variable, "XDG_CONFIG_HOME=") {
			path := filepath.Join(strings.TrimPrefix(variable, "XDG_CONFIG_HOME="), "agentgate", "config.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, command := range []string{"install", "update", "stop", "prune", "launch", "finish", "land", "doctor", "config"} {
		for _, option := range []string{"--help", "-h"} {
			stdout, stderr, code := s.run("", command, option)
			if code != 0 || !strings.Contains(stdout, "Usage") || stderr != "" {
				t.Errorf("%s %s must show help without executing: code=%d stdout=%q stderr=%q", command, option, code, stdout, stderr)
			}
		}
	}
	for _, args := range [][]string{{"help", "task"}, {"task", "--help"}, {"task"}, {"setup"}, {"help", "setup", "install"}, {"task", "launch", "--file", "missing.json", "--help"}} {
		stdout, stderr, code := s.run("", args...)
		if code != 0 || !strings.Contains(stdout, "agentgate") || stderr != "" {
			t.Errorf("%v: %d %q %q", args, code, stdout, stderr)
		}
	}
}

func TestCommandGrammarRejectsIgnoredOptionsAndArguments(t *testing.T) {
	for _, input := range [][]string{
		{"launch", "--fle", "plan.json"}, {"stop", "--once"}, {"update", "--lead", "codex"},
		{"install", "--lead", "invalid"}, {"install", "--lead"}, {"launch", "--file", "--all"},
		{"launch", "--file", "a", "--file", "b"}, {"launch", "--file", "a", "--plan", "b"},
		{"config", "show", "extra"}, {"plan", "list", "extra"}, {"plan", "show"},
		{"accounts", "list", "--save"}, {"accounts", "remove", "a", "--paid"}, {"accounts", "discover", "--json"},
		{"status", "unexpected"}, {"--unknown"}, {"task", "instal"}, {"setup", "stop"},
		{"parse", "task", "--file", "-"}, {"plan", "list", "--file", "-"},
	} {
		if _, err := parseArgs(input); err == nil {
			t.Errorf("must reject %v", input)
		}
	}
	for _, input := range [][]string{
		{"launch", "--file=plan.json"}, {"task", "launch", "--plan", "wallet"},
		{"setup", "install", "--lead=both"}, {"--file", "-", "check"},
		{"parse", "--", "-h", "fix totals"}, {"status", "--all"}, {"watch", "--json"},
		{"--version"}, {"-v"}, {"limits", "--json"},
	} {
		if _, err := parseArgs(input); err != nil {
			t.Errorf("%v: %v", input, err)
		}
	}
}

func TestGroupedCommandsAndPositionalRequest(t *testing.T) {
	s := newSandbox(t)
	parsed := s.ok("", "task", "parse", "--", "-h", "fix totals")
	if parsed["mode"] != "heavy" || parsed["task"] != "totals" {
		t.Fatalf("parsed: %v", parsed)
	}
	if status := s.ok("", "task", "status"); status["read_only"] != true {
		t.Fatal(status)
	}
	if snapshot := s.ok("", "task", "watch", "--json"); snapshot["read_only"] != true || snapshot["schema_version"] != float64(1) || len(snapshot["tasks"].([]any)) != 0 {
		t.Fatal(snapshot)
	}
}

func TestSavedPlanShowAndConfigurationValidationDispatch(t *testing.T) {
	s := newSandbox(t)
	plan := `{"request":"fix the file","assessment":{"size":"ordinary","evidence":"one text file"},"brief":"a.txt must say fixed","streams":[{"name":"core","domain":"backend","files":["a.txt"]}]}`
	saved := s.ok(plan, "plan", "save", "example", "--file", "-")
	briefPath := saved["brief"].(string)
	if !filepath.IsAbs(briefPath) {
		briefPath = filepath.Join(s.repo, briefPath)
	}
	brief, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := s.run("", "plan", "show", "example")
	if code != 0 || stdout != string(brief) || stderr != "" {
		t.Fatalf("plan show: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, command := range [][]string{{"config", "validate"}, {"setup", "config", "validate"}} {
		if result := s.ok("", command...); result["valid"] != true {
			t.Fatalf("config validate: %v", result)
		}
	}
	if err := os.WriteFile(filepath.Join(s.repo, ".agentgate.json"), []byte(`{"unknown_field":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := s.run("", "config", "validate"); code != 1 || !strings.Contains(stderr, "unknown_field") {
		t.Fatalf("invalid configuration was accepted: code=%d stderr=%q", code, stderr)
	}
}

func TestJSONInputIsExactlyOneObject(t *testing.T) {
	var target struct {
		Reason string `json:"reason"`
	}
	for _, raw := range []string{"null", "[]", `"value"`, `{"reason":"ok"} {"reason":"ignored"}`, `{"reason":"ok"} trailing`, `{"reasn":"typo"}`} {
		if err := decodeInput([]byte(raw), &target); err == nil {
			t.Errorf("must reject %s", raw)
		}
	}
	if err := decodeInput([]byte("  {\"reason\":\"ok\"}\n \t"), &target); err != nil || target.Reason != "ok" {
		t.Fatalf("valid object: %+v %v", target, err)
	}
	s := newSandbox(t)
	for _, input := range []string{"null", `{"reason":"ok"} {}`} {
		_, stderr, code := s.run(input, "hold", "--file", "-")
		if code != 1 || !strings.Contains(stderr, "hold input") {
			t.Fatalf("%s: code %d, stderr %s", input, code, stderr)
		}
	}
	if _, err := readInput(io.LimitReader(strings.NewReader(strings.Repeat("x", maxInputBytes+1)), maxInputBytes+1)); err == nil {
		t.Fatal("oversized input accepted")
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestOutputErrorsAreNotSuccess(t *testing.T) {
	for _, value := range []any{"hello", map[string]any{"ok": true}} {
		if err := writeOutput(failedWriter{}, value); err == nil {
			t.Fatal("write failure swallowed")
		}
	}
	if err := writeOutput(&bytes.Buffer{}, make(chan int)); err == nil {
		t.Fatal("serialization failure swallowed")
	}
}

func TestInteractiveInstallLeadChoice(t *testing.T) {
	for answer, want := range map[string]string{
		"": "claude", "\n": "claude", "1": "claude", "claude": "claude", "  CLAUDE \n": "claude",
		"2": "codex", "codex": "codex", "3": "both", "both": "both", "unknown": "unknown", "2 extra": "2 extra",
	} {
		if got := installLeadChoice(answer); got != want {
			t.Errorf("choice %q: got %q, want %q", answer, got, want)
		}
	}
}
