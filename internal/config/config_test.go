package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Chdir(dir)
	return dir
}

func TestBuiltInTableIsValidAndComplete(t *testing.T) {
	isolate(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range ModeNames {
		mode := cfg.Modes[name]
		if mode.Backend == "" || mode.Frontend == "" || len(mode.Blocking) == 0 {
			t.Errorf("mode %s is incomplete: %+v", name, mode)
		}
	}
	if len(cfg.Modes["light"].Review) != 0 || len(cfg.Modes["ultra"].Review) != 4 || !cfg.Modes["ultra"].Critique {
		t.Fatal("light is reviewed by the lead; ultra has four lanes and a critique")
	}
	route, err := cfg.Route("fable:xhigh")
	if err != nil || route.Provider != "claude" || route.Pool != "fable" || route.String() != "fable:xhigh" {
		t.Fatalf("route: %+v %v", route, err)
	}
	for _, bad := range []string{"fable", "nobody:high", "fable:extreme", ""} {
		if _, err := cfg.Route(bad); err == nil {
			t.Errorf("route %q must be rejected", bad)
		}
	}
	alternates := cfg.Alternates(route)
	if len(alternates) != 2 || alternates[0].Alias != "opus" || alternates[0].Effort != "xhigh" || alternates[0].SubstitutedFrom != "fable:xhigh" {
		t.Fatalf("alternates keep the effort and remember what they replace: %+v", alternates)
	}
	if !cfg.Modes["heavy"].Blocks("P2") || cfg.Modes["medium"].Blocks("P2") {
		t.Fatal("P2 blocks in heavy and not in medium")
	}
	if Lower("xhigh") != "high" || Lower("medium") != "medium" || Lower("low") != "low" {
		t.Fatal("a recheck drops one step and never below medium")
	}
	if Raise("high", 1, "max") != "xhigh" || Raise("xhigh", 3, "max") != "max" || Raise("high", 2, "high") != "high" || Raise("xhigh", 1, "medium") != "xhigh" {
		t.Fatal("escalation rises step by step and stops at the ceiling")
	}
}

func TestSetUnsetAndRollback(t *testing.T) {
	isolate(t)
	if err := Set("modes.heavy.backend", "sol:xhigh"); err != nil {
		t.Fatal(err)
	}
	if err := Set("models.haiku", `{"provider":"claude","model":"claude-haiku-5-5","pool":"claude"}`); err != nil {
		t.Fatal(err)
	}
	cfg, _ := Load()
	if cfg.Modes["heavy"].Backend != "sol:xhigh" || cfg.Models["haiku"].Model != "claude-haiku-5-5" || cfg.Modes["heavy"].Frontend != "fable:high" {
		t.Fatalf("a set key changes and its siblings stay: %+v", cfg.Modes["heavy"])
	}
	before, _ := os.ReadFile(UserPath())
	for key, value := range map[string]string{"modes.heavy.backend": "nobody:high", "max_parallel": "0", "modes.heavy.escalate_to": "extreme",
		"models.sol": `{"provider":"gemini","model":"x","pool":"y"}`, "modes.ultra.architecture": `["fable:high"]`} {
		if err := Set(key, value); err == nil || !strings.Contains(err.Error(), "nothing changed") {
			t.Errorf("%s=%s must be rejected, got %v", key, value, err)
		}
	}
	if after, _ := os.ReadFile(UserPath()); string(after) != string(before) {
		t.Fatal("a rejected edit must leave the file untouched")
	}
	if err := Unset("modes.heavy.backend"); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = Load(); cfg.Modes["heavy"].Backend != "fable:high" {
		t.Fatal("unset restores the built-in value")
	}
}

func TestProjectFileOverridesTheUserFile(t *testing.T) {
	isolate(t)
	if err := Set("max_parallel", "2"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProjectFile, []byte(`{"max_parallel": 3, "modes": {"light": {"backend": "astra:low"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil || cfg.MaxParallel != 3 || cfg.Modes["light"].Backend != "astra:low" || cfg.Modes["light"].Frontend != "sonnet:medium" {
		t.Fatalf("project values win and merge key by key: %+v %v", cfg, err)
	}
	_ = os.WriteFile(ProjectFile, []byte(`{broken`), 0o644)
	if _, err := Load(); err == nil {
		t.Fatal("a broken project file must be reported, not ignored")
	}
}

func TestInitWritesAStarterOnce(t *testing.T) {
	isolate(t)
	path, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"models"`) || !strings.Contains(string(raw), `"fallbacks"`) || strings.Contains(string(raw), `"modes"`) {
		t.Fatalf("the starter holds models and fallbacks only: %s", raw)
	}
	if _, err := Init(); err == nil {
		t.Fatal("init must not overwrite an existing file")
	}
}

func TestAccountsBelongToTheUserNotToARepository(t *testing.T) {
	isolate(t)
	if err := SetValue("accounts", []Account{{Name: "main", Provider: "claude", Home: "~/.claude"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProjectFile, []byte(`{"accounts": [{"name": "theirs", "provider": "claude", "home": "/somewhere"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil || len(cfg.Accounts) != 1 || cfg.Accounts[0].Name != "main" {
		t.Fatalf("a repository must not choose whose subscription pays: %+v %v", cfg.Accounts, err)
	}
	_ = os.Remove(ProjectFile)
	if err := SetValue("accounts", []Account{{Name: "main", Provider: "claude", Home: "~/a"}, {Name: "main", Provider: "codex", Home: "~/b"}}); err == nil {
		t.Fatal("two accounts with one name must be rejected")
	}
	if cfg, _ := Load(); len(cfg.Accounts) != 1 {
		t.Fatal("a rejected change leaves the file as it was")
	}
}
