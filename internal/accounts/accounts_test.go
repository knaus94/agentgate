package accounts

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/knaus94/claude-dev/internal/config"
)

var (
	fable = config.Route{Alias: "fable", Provider: "claude", Model: "claude-fable-5-2", Effort: "high"}
	opus  = config.Route{Alias: "opus", Provider: "claude", Model: "claude-opus-5-5", Effort: "high"}
	sol   = config.Route{Alias: "sol", Provider: "codex", Model: "gpt-6.1-sol", Effort: "medium"}
)

// world scripts what each account reports and freezes the clock.
type world struct {
	mu       sync.Mutex
	statuses map[string]Status
	probes   int
	path     string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "lead"))
	w := &world{statuses: map[string]Status{}, path: filepath.Join(root, "state", "accounts.json")}
	probe := func(account config.Account) Status {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.probes++
		status := w.statuses[account.Name]
		status.Account, status.ProbedAt = account, now()
		return status
	}
	previousClaude, previousCodex, previousNow := ProbeClaude, ProbeCodex, now
	ProbeClaude, ProbeCodex = probe, probe
	now = func() int64 { return 1_000_000 }
	t.Cleanup(func() { ProbeClaude, ProbeCodex, now = previousClaude, previousCodex, previousNow })
	return w
}

func (w *world) set(name string, status Status) {
	w.mu.Lock()
	w.statuses[name] = status
	w.mu.Unlock()
}

func used(week float64, scoped ...Window) Status {
	return Status{LoggedIn: true, Windows: append([]Window{{Label: "week", Used: week, ResetsAt: 2_000_000}}, scoped...)}
}

func claude(names ...string) []config.Account {
	var out []config.Account
	for _, name := range names {
		out = append(out, config.Account{Name: name, Provider: "claude", Home: "~/" + name})
	}
	return out
}

// Order is the ranking a call would be offered, without reserving an account.
func (m *Manager) Order(route config.Route, prefer string) []config.Account {
	m.refresh(false)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.order(route, prefer, nil)
}

func names(accounts []config.Account) string {
	var out []string
	for _, account := range accounts {
		out = append(out, account.Name)
	}
	return strings.Join(out, ",")
}

func TestParseClaudeUsage(t *testing.T) {
	windows := ParseClaudeUsage([]byte(`{"five_hour":{"utilization":null,"resets_at":null},
		"seven_day":{"utilization":41.0,"resets_at":"2026-11-15T10:00:00.000000+00:00"},"seven_day_opus":null,"something_new":{"a":1},
		"limits":[{"kind":"weekly_all","percent":41,"resets_at":"2026-11-15T10:00:00Z"},
			{"kind":"weekly_scoped","percent":38,"resets_at":"2026-11-15T10:00:00Z","scope":{"model":{"display_name":"Fable 5"}}}]}`))
	if len(windows) != 2 || windows[0].Label != "week" || windows[0].Used != 41 || windows[0].ResetsAt == 0 ||
		windows[1].Scope != "fable" || windows[1].Used != 38 {
		t.Fatalf("the weekly limit and the Fable bucket are read, an empty window and unknown fields are skipped: %+v", windows)
	}
	if ParseClaudeUsage([]byte(`not json`)) != nil {
		t.Fatal("an unreadable reply gives no windows")
	}
}

func TestParseCodexLimits(t *testing.T) {
	windows, plan := ParseCodexLimits([]byte(`{"rateLimits":{"primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":1700},
		"secondary":{"usedPercent":22,"windowDurationMins":10080,"resetsAt":1800},"planType":"prolite","rateLimitReachedType":null}}`))
	if plan != "prolite" || len(windows) != 2 || windows[0].Label != "5 hours" || windows[1].Label != "week" || windows[1].Used != 22 {
		t.Fatalf("codex windows: %+v %s", windows, plan)
	}
	reached, _ := ParseCodexLimits([]byte(`{"rateLimits":{"primary":{"usedPercent":97,"windowDurationMins":300,"resetsAt":1700},"rateLimitReachedType":"primary"}}`))
	if len(reached) != 1 || reached[0].Used != 100 {
		t.Fatalf("a reached limit counts as fully used: %+v", reached)
	}
}

func TestProbeClaudeNeverRefreshesAToken(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	asked := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		if r.Header.Get("Authorization") != "Bearer token-1" || r.Header.Get("anthropic-beta") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"seven_day":{"utilization":10,"resets_at":"2026-11-15T10:00:00Z"}}`))
	}))
	defer server.Close()
	previousURL, previousNow := usageURL, now
	usageURL, now = server.URL, func() int64 { return 1000 }
	defer func() { usageURL, now = previousURL, previousNow }()

	account := config.Account{Name: "second", Provider: "claude", Home: "~/second"}
	if status := probeClaude(account); status.LoggedIn || status.Note == "" {
		t.Fatalf("a profile without credentials is not logged in: %+v", status)
	}
	home := filepath.Join(root, "second")
	_ = os.MkdirAll(home, 0o700)
	write := func(expires string) {
		_ = os.WriteFile(filepath.Join(home, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"token-1","refreshToken":"r","expiresAt":`+
			expires+`,"rateLimitTier":"default_claude_max_20x"}}`), 0o600)
	}
	write("5000000")
	status := probeClaude(account)
	if !status.LoggedIn || status.Plan != "max 20x" || len(status.Windows) != 1 || status.Windows[0].Used != 10 || status.Identity == "" {
		t.Fatalf("a live token reads the limits: %+v", status)
	}
	write("900000") // expired: 900 s < now
	status = probeClaude(account)
	if asked != 1 || !status.LoggedIn || len(status.Windows) != 0 || !strings.Contains(status.Note, "stale") {
		t.Fatalf("an expired token is left to the CLI, the account stays usable with unknown limits: %+v (asked %d)", status, asked)
	}
}

func TestPickPrefersRoomSparesTheLeadAndSpreads(t *testing.T) {
	w := newWorld(t)
	w.set("lead", used(30))
	w.set("second", used(35))
	w.set("third", used(80))
	manager := New(claude("lead", "second", "third"), w.path)
	if got := names(manager.Order(opus, "")); got != "second,lead,third" {
		t.Fatalf("with similar room the lead's own account is spared: %s", got)
	}
	if got := names(manager.Order(opus, "third")); !strings.HasPrefix(got, "third,") {
		t.Fatalf("a continued session prefers the account that holds it: %s", got)
	}
	first, _ := manager.Pick(opus, "", nil)
	second, _ := manager.Pick(opus, "", nil)
	third, _ := manager.Pick(opus, "", nil)
	if first.Name != "second" || second.Name != "lead" || third.Name != "second" {
		t.Fatalf("calls started together spread over the accounts: %s %s %s", first.Name, second.Name, third.Name)
	}
	manager.Release(first.Name, true)
	manager.Release(third.Name, true)
	manager.Release(second.Name, true)
	if next, _ := manager.Pick(opus, "", map[string]bool{"second": true}); next.Name != "lead" {
		t.Fatalf("an account this call already failed on is not offered again: %s", next.Name)
	}
	if w.probes != 3 {
		t.Fatalf("a fresh reading is reused, not asked for on every call: %d probes", w.probes)
	}
	if manager.Has("codex") || !manager.Has("claude") || (*Manager)(nil).Has("claude") {
		t.Fatal("a provider without accounts keeps its default profile")
	}
}

func TestScopedLimitRemovesTheAccountOnlyForThatModel(t *testing.T) {
	w := newWorld(t)
	w.set("lead", used(20, Window{Label: "week, fable", Scope: "fable", Used: 99, ResetsAt: 2_000_000}))
	w.set("second", used(60))
	manager := New(claude("lead", "second"), w.path)
	if got := names(manager.Order(fable, "")); got != "second" {
		t.Fatalf("an exhausted Fable bucket takes the account out for Fable: %s", got)
	}
	if got := names(manager.Order(opus, "")); got != "lead,second" {
		t.Fatalf("the same account still serves other models: %s", got)
	}
}

func TestSameSubscriptionCountsOnceAndAPIKeysNeedConsent(t *testing.T) {
	w := newWorld(t)
	one := used(22)
	one.Identity = "same"
	w.set("codex", one)
	w.set("codex-lean", one)
	w.set("codex-api", Status{LoggedIn: true, APIKey: true})
	w.set("empty", Status{})
	accounts := []config.Account{{Name: "codex", Provider: "codex", Home: "~/a"}, {Name: "codex-lean", Provider: "codex", Home: "~/b"},
		{Name: "codex-api", Provider: "codex", Home: "~/c"}, {Name: "empty", Provider: "codex", Home: "~/d"}}
	if got := names(New(accounts, w.path).Order(sol, "")); got != "codex" {
		t.Fatalf("a second directory of one subscription, an API key and a logged-out profile add nothing: %s", got)
	}
	accounts[2].Paid = true
	if got := names(New(accounts, w.path).Order(sol, "")); got != "codex,codex-api" {
		t.Fatalf("a paid profile is used once the user allows it, after the subscriptions: %s", got)
	}
	text := Render(New(accounts[:3], w.path).Statuses(false), nil, "")
	for _, want := range []string{"same subscription as codex", "week 22%"} {
		if !strings.Contains(text, want) {
			t.Errorf("the table lacks %q:\n%s", want, text)
		}
	}
}

func TestLimitedAccountRestsUntilItsLimitResets(t *testing.T) {
	w := newWorld(t)
	w.set("lead", used(40))
	w.set("second", used(50))
	manager := New(claude("lead", "second"), w.path)
	manager.Order(opus, "")

	// The provider refused, and the fresh reading shows the weekly limit is gone: rest until it resets.
	w.set("second", used(100))
	if until := manager.Limited("second", opus); until != 2_000_000 {
		t.Fatalf("the account rests until the exhausted limit resets, got %d", until)
	}
	if got := names(manager.Order(opus, "")); got != "lead" {
		t.Fatalf("a resting account takes no calls: %s", got)
	}
	// Another run on this machine sees the same cooldown, even when the reading looks fine again.
	w.set("second", used(45))
	if got := names(New(claude("lead", "second"), w.path).Order(fable, "")); got != "lead" {
		t.Fatalf("the cooldown is shared between runs and covers every model of an all-model limit: %s", got)
	}
	manager.Reset("second")
	if got := names(New(claude("lead", "second"), w.path).Order(opus, "")); got != "second,lead" {
		t.Fatalf("reset brings the account back: %s", got)
	}

	// A refusal while the limits still show room is an overload: a short pause for that model only.
	if until := manager.Limited("lead", fable); until != now()+int64(transientPause.Seconds()) {
		t.Fatalf("a transient refusal pauses briefly, got %d", until)
	}
	if names(manager.Order(fable, "")) != "second" || names(manager.Order(opus, "")) != "second,lead" {
		t.Fatal("the short pause covers the refused model only")
	}
	previous := now
	now = func() int64 { return previous() + 600 }
	defer func() { now = previous }()
	if got := names(manager.Order(fable, "")); got != "second,lead" {
		t.Fatalf("the pause ends by itself: %s", got)
	}
}

func TestDiscoverSaveAddsOnlyNewCapacity(t *testing.T) {
	configured := claude("main")
	known := []Status{{Account: configured[0], LoggedIn: true, Identity: "a"}}
	found := []Status{
		{Account: config.Account{Name: "claude", Provider: "claude", Home: "~/main"}, LoggedIn: true, Identity: "a"},
		{Account: config.Account{Name: "claude-2", Provider: "claude", Home: "~/claude-2"}, LoggedIn: true, Identity: "b"},
		{Account: config.Account{Name: "claude-work", Provider: "claude", Home: "~/claude-work"}},
		{Account: config.Account{Name: "codex", Provider: "codex", Home: "~/codex"}, LoggedIn: true, Identity: "c"},
		{Account: config.Account{Name: "codex-lean", Provider: "codex", Home: "~/codex-lean"}, LoggedIn: true, Identity: "c"},
		{Account: config.Account{Name: "codex-api", Provider: "codex", Home: "~/codex-api"}, LoggedIn: true, APIKey: true, Identity: "d"},
	}
	list, added := merge(configured, known, found)
	if strings.Join(added, ",") != "claude-2,codex" || len(list) != 3 {
		t.Fatalf("only logged-in subscriptions that are not configured yet are added: %v", added)
	}
}
