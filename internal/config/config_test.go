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
	if cfg.Modes["heavy"].Backend != "sol:xhigh" || cfg.Models["haiku"].Model != "claude-haiku-5-5" || cfg.Modes["heavy"].Frontend != "astra:high" {
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
	if cfg, _ = Load(); cfg.Modes["heavy"].Backend != "opus:high" {
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

func TestExtensionsPlugInWithoutEditingTheConfiguration(t *testing.T) {
	root := isolate(t)
	_ = root
	user := filepath.Join(filepath.Dir(UserPath()), "extensions")
	repo := filepath.Join(filepath.Dir(PersonalFile), "extensions")
	for _, dir := range []string{user, repo} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, text string) {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(PersonalFile, `{"worktree": {"copy": ["env/.env.local"]}}`)
	write(filepath.Join(repo, "graph.json"), `{"about": "code graph", "accounts": [{"name": "x", "provider": "claude", "home": "/x"}],
		"worker_mcp": {"graph": {"command": "graph-mcp", "tools": ["query"], "roles": ["scout", "reviewer"], "refresh": ["graph", "update"]}},
		"worktree": {"copy": ["graph-out", "env/.env.local"]}}`)
	write(filepath.Join(user, "docs.json"), `{"worker_mcp": {"docs": {"command": "docs-mcp"}}, "worktree": {"copy": ["docs-cache"]}}`)
	write(filepath.Join(repo, "off.json.off"), `{"max_parallel": 99}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.WorkerMCP) != 2 || cfg.WorkerMCP["graph"].RefreshTimeoutS != 300 || strings.Join(cfg.WorkerMCP["docs"].RolesOrDefault(), ",") != "scout" {
		t.Fatalf("each file adds its server; a server without roles is the scout's: %+v", cfg.WorkerMCP)
	}
	if got := strings.Join(cfg.Worktree.Copy, ","); got != "env/.env.local,docs-cache,graph-out" {
		t.Fatalf("lists of extensions add to the list that is there, without repeats: %s", got)
	}
	if len(cfg.Accounts) != 0 || cfg.MaxParallel == 99 {
		t.Fatal("an extension never brings an account, and a file that is not *.json is off")
	}
	loaded := Extensions()
	if len(loaded) != 2 || loaded[1].About != "code graph" || strings.Join(loaded[1].Adds, ",") != "worker_mcp.graph,worktree.copy" {
		t.Fatalf("the loaded extensions are listed with what they add: %+v", loaded)
	}
	write(filepath.Join(repo, "bad.json"), `{"worker_mcp": {"Bad Name": {"command": "x"}}}`)
	if _, err := Load(); err == nil {
		t.Fatal("an invalid extension is rejected like any configuration")
	}
	write(filepath.Join(repo, "bad.json"), `{broken`)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "bad.json") {
		t.Fatalf("a broken extension is named in the error: %v", err)
	}
}

func TestProfilesAreLaidUnderTheUsersSettings(t *testing.T) {
	isolate(t)
	if err := Set("profile", "no-such-profile"); err == nil {
		t.Fatal("an unknown profile is rejected")
	}
	if err := Set("profile", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := Set("modes.medium.backend", "sol:high"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Modes["medium"].Backend != "sol:high" || cfg.Modes["heavy"].Backend != "astra:high" || cfg.Models["terra"].Model == "" {
		t.Fatalf("the profile replaces the default table and the user's own setting still wins: %+v", cfg.Modes["medium"])
	}
	if _, known := cfg.Models["fable"]; known {
		t.Fatal("null in a layer removes what a lower layer defined")
	}
	// A profile of the user's own is a file; it shadows a built-in one of the same name.
	if err := os.MkdirAll(ProfileDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ProfileDir(), "mine.json"), []byte(`{"about": "my table", "max_parallel": 7, "accounts": [{"name": "x", "provider": "claude", "home": "/x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Set("profile", "mine"); err != nil {
		t.Fatal(err)
	}
	_ = Unset("modes.medium.backend")
	cfg, err = Load()
	if err != nil || cfg.MaxParallel != 7 || len(cfg.Accounts) != 0 || cfg.Modes["medium"].Backend != "opus:medium" {
		t.Fatalf("a user profile is loaded, without accounts: %+v %v", cfg, err)
	}
	listed := Profiles()
	if listed["mine"] != "my table" || !strings.Contains(listed["codex"], "Codex only") {
		t.Fatalf("profiles are listed with what they say about themselves: %v", listed)
	}
}
