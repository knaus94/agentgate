package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRejectsTyposUnsafePathsAndInvalidTimeoutsWithoutWriting(t *testing.T) {
	isolate(t)
	if _, err := Init(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(UserPath())
	cases := [][2]string{
		{"max_paralell", "3"},
		{"modes.heavy.writer_timout_s", "10"},
		{"modes.hevy.backend", "sol:high"},
		{"version", "2"},
		{"retention_days", "-1"},
		{"modes.heavy.writer_timeout_s", "-1"},
		{"worker_mcp.graph", `{"command":"graph","writable":["."]}`},
		{"worker_mcp.graph", `{"command":"graph","writable":[".git"]}`},
		{"worker_mcp.graph", `{"command":"graph","writable":["../outside"]}`},
		{"worker_mcp.graph", `{"command":"graph","refresh_timeout_s":-1}`},
		{"fallbacks.sol", `["sol"]`},
		{"fallbacks.sol", `["sonnet","sonnet"]`},
		{"profile", "../codex"},
		{"models..model", "x"},
		{"", "x"},
	}
	for _, item := range cases {
		if err := Set(item[0], item[1]); err == nil {
			t.Errorf("accepted %s=%s", item[0], item[1])
		}
		after, _ := os.ReadFile(UserPath())
		if string(after) != string(before) {
			t.Fatalf("rejected setting %q changed the file", item[0])
		}
	}
	if err := Set("worktree.copy", `["..cache/config.json"]`); err != nil {
		t.Fatalf("a legal name starting with two dots is not parent traversal: %v", err)
	}
}

func TestProjectCannotMaskInvalidUserSetting(t *testing.T) {
	isolate(t)
	if err := os.WriteFile(ProjectFile, []byte(`{"max_parallel":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Set("max_parallel", "-1"); err == nil {
		t.Fatal("project override hid an invalid user setting that would break other repositories")
	}
	if _, err := os.Stat(UserPath()); !os.IsNotExist(err) {
		t.Fatal("invalid user config was written")
	}
}

func TestConcurrentConfigEditsAreAtomicAndDoNotLoseKeys(t *testing.T) {
	isolate(t)
	if _, err := Init(); err != nil {
		t.Fatal(err)
	}
	const edits = 12
	var workers sync.WaitGroup
	errors := make(chan error, edits+1)
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			raw, err := os.ReadFile(UserPath())
			if err != nil {
				errors <- err
				return
			}
			var config map[string]any
			if err := json.Unmarshal(raw, &config); err != nil {
				errors <- fmt.Errorf("observed torn write: %w", err)
				return
			}
		}
	}()
	for i := 0; i < edits; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := Set(fmt.Sprintf("models.custom%d", i), `{"provider":"codex","model":"test-model","pool":"test"}`); err != nil {
				errors <- err
			}
		}()
	}
	workers.Wait()
	close(stop)
	<-readerDone
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < edits; i++ {
		if cfg.Models[fmt.Sprintf("custom%d", i)].Model != "test-model" {
			t.Errorf("concurrent edit %d was lost", i)
		}
	}
	info, err := os.Stat(UserPath())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("configuration should have private permissions")
	}
}

func TestMalformedObjectAndUnreadableProfileAreReported(t *testing.T) {
	isolate(t)
	if err := os.MkdirAll(filepath.Dir(UserPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(UserPath(), []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Set("max_parallel", "2"); err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("null user config should be rejected without a panic: %v", err)
	}
	if err := os.WriteFile(UserPath(), []byte(`{"profile":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ProfileDir(), "codex.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "profile codex") {
		t.Fatalf("unreadable personal profile must not silently fall back to builtin: %v", err)
	}
}
