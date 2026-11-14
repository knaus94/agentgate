package accounts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/knaus94/agentgate/internal/config"
)

func TestExpiredWindowNeverBlocksAfterItsReset(t *testing.T) {
	s := Status{LoggedIn: true, ProbedAt: 100, Windows: []Window{{Used: 100, ResetsAt: 200}}}
	if ok, _ := s.Available("opus", 199); ok {
		t.Fatal("a current exhausted window must block")
	}
	if ok, _ := s.Available("opus", 200); !ok {
		t.Fatal("a stale reading must stop blocking at its reset timestamp")
	}
	s.Windows = []Window{{Used: 100}}
	if ok, _ := s.Available("opus", 100+int64(probeTTL.Seconds())+1); !ok {
		t.Fatal("an old unknown-reset reading must not block forever")
	}
}

func TestModelSpecificCodexBucketsDoNotBlockUnrelatedModels(t *testing.T) {
	newWorld(t)
	windows, _ := ParseCodexLimits([]byte(`{"rateLimitsByLimitId":{
		"codex":{"primary":{"usedPercent":20,"windowDurationMins":300,"resetsAt":2000000}},
		"gpt-mini":{"primary":{"usedPercent":100,"windowDurationMins":300,"resetsAt":2000000}}
	}}`))
	s := Status{LoggedIn: true, Windows: windows}
	if ok, _ := s.Available("sol gpt-6.1-sol", now()); !ok {
		t.Fatal("an unrelated model-specific bucket blocked Sol")
	}
	if ok, _ := s.Available("mini gpt-mini", now()); ok {
		t.Fatal("the actual exhausted model bucket must block its model")
	}
	windows, _ = ParseCodexLimits([]byte(`{"rateLimits":{"primary":{"usedPercent":97,"windowDurationMins":300},"secondary":{"usedPercent":20,"windowDurationMins":10080},"rateLimitReachedType":"primary"}}`))
	if len(windows) != 2 || windows[0].Used != 100 || windows[1].Used != 20 {
		t.Fatalf("a reached five-hour limit must not exhaust the healthy weekly window: %+v", windows)
	}
	windows, _ = ParseCodexLimits([]byte(`{"ordinaryUsageAllowed":false}`))
	if len(windows) != 1 || windows[0].Used != 100 {
		t.Fatal("explicit denied usage without windows must remain denied")
	}
}

func TestMalformedClaudeModelLabelDoesNotPanic(t *testing.T) {
	windows := ParseClaudeUsage([]byte(`{"limits":[{"kind":"weekly_scoped","percent":50,"scope":{"model":{"display_name":"   "}}}]}`))
	if len(windows) != 0 {
		t.Fatal("blank model labels should be ignored")
	}
}

func TestConcurrentAccountSelectionSharesOneProbe(t *testing.T) {
	w := newWorld(t)
	w.set("one", used(10))
	manager := New(claude("one"), w.path)
	var wait sync.WaitGroup
	for i := 0; i < 24; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			account, ok := manager.Pick(opus, "", nil)
			if !ok || account.Name != "one" {
				t.Error("selection unexpectedly failed")
				return
			}
			manager.Release(account.Name, false)
		}()
	}
	wait.Wait()
	if w.probes != 1 {
		t.Fatalf("parallel lanes caused %d provider requests instead of sharing one fresh reading", w.probes)
	}
}

func TestFailedProbeIsCachedAndExpiredUsageDoesNotPersist(t *testing.T) {
	w := newWorld(t)
	w.set("one", used(100))
	manager := New(claude("one"), w.path)
	manager.Statuses(false)
	// The only recorded limit has reset; the provider is unavailable now.
	previousNow, previousProbe := now, ProbeClaude
	now = func() int64 { return 2_000_001 }
	count := 0
	ProbeClaude = func(account config.Account) Status {
		count++
		return Status{Account: account, LoggedIn: true, Note: "offline"}
	}
	t.Cleanup(func() { now, ProbeClaude = previousNow, previousProbe })
	for i := 0; i < 4; i++ {
		account, ok := manager.Pick(opus, "", nil)
		if !ok {
			t.Fatal("expired usage blocked an account indefinitely after a failed refresh")
		}
		manager.Release(account.Name, false)
	}
	if count != 1 {
		t.Fatalf("failed probes were retried on every lane (%d requests)", count)
	}
}

func TestDuplicateProfilesDoNotAddRetryCapacity(t *testing.T) {
	w := newWorld(t)
	status := used(10)
	status.Identity = "one-subscription"
	w.set("one", status)
	w.set("alias", status)
	manager := New(claude("one", "alias"), w.path)
	if _, ok := manager.Pick(opus, "", map[string]bool{"one": true}); ok {
		t.Fatal("the same subscription was offered under another name after it refused")
	}
	manager.Statuses(false)
	manager.Limited("one", opus)
	if got := manager.Order(opus, ""); len(got) != 0 {
		t.Fatalf("an alias bypassed a cooldown: %+v", got)
	}
}

func TestPaidAPIProfilesRemainAfterUsableSubscriptions(t *testing.T) {
	w := newWorld(t)
	w.set("subscription", used(90))
	w.set("api", Status{LoggedIn: true, APIKey: true, Windows: []Window{{Used: 0}}})
	list := claude("subscription", "api")
	list[1].Paid = true
	if got := names(New(list, w.path).Order(opus, "")); got != "subscription,api" {
		t.Fatalf("remaining subscription capacity should be used before token billing: %s", got)
	}
}

func TestCorruptAccountCacheDoesNotDiscardCooldownsSilently(t *testing.T) {
	w := newWorld(t)
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := New(claude("one"), w.path)
	if _, ok := manager.Pick(opus, "", nil); ok || manager.Err() == nil {
		t.Fatal("corrupt shared account state was treated as empty")
	}
	raw, _ := os.ReadFile(w.path)
	if string(raw) != "broken" {
		t.Fatal("corrupt state was overwritten before it could be recovered")
	}
}

func TestStatusesDoNotExposeMutableSharedMaps(t *testing.T) {
	w := newWorld(t)
	w.set("one", used(10))
	manager := New(claude("one"), w.path)
	manager.Limited("one", opus)
	statuses := manager.Statuses(false)
	statuses[0].Cooling["opus"] = 0
	statuses[0].Windows[0].Used = 100
	again := manager.Statuses(false)
	if again[0].Cooling["opus"] == 0 || again[0].Windows[0].Used == 100 {
		t.Fatal("a returned snapshot changed internal account state")
	}
}

func TestCodexQuotaRPCIsScopedAndHandlesNoisyNotifications(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "profile")
	bin := filepath.Join(root, "bin")
	for _, path := range []string{home, bin} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
set -eu
[ -z "${OPENAI_API_KEY+x}" ] || exit 2
[ -z "${CODEX_THREAD_ID+x}" ] || exit 3
IFS= read -r line
printf '%s\n' "$line" > "$CODEX_HOME/initialize.json"
printf '{"id":1,"result":{}}\n'
IFS= read -r line
IFS= read -r line
printf '%s\n' "$line" > "$CODEX_HOME/request.json"
printf '{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":10,"windowDurationMins":300}}}}\n'
i=0
while [ "$i" -lt 1000 ]; do printf '{"method":"notification"}\n'; i=$((i+1)); done
sleep 60
`
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPENAI_API_KEY", "test-only-key")
	t.Setenv("CODEX_THREAD_ID", "another-account-thread")
	start := time.Now()
	body, err := codexRateLimits(home)
	if err != nil || !strings.Contains(string(body), "usedPercent") {
		t.Fatalf("quota RPC failed: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("notification backlog delayed process shutdown")
	}
	raw, err := os.ReadFile(filepath.Join(home, "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if request["method"] != "account/rateLimits/read" || request["params"] != nil {
		t.Fatalf("quota request sent initialization-only parameters: %s", raw)
	}
}
