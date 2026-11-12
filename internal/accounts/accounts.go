// Package accounts spreads model calls over several logged-in CLI profiles of the same provider.
//
// An account is a Claude config directory or a Codex home. Before work starts each account is asked how much of
// its limits is used; a call goes to the account with the most room for the model it needs, and an account
// that reports an exhausted limit is set aside until that limit resets.
package accounts

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/knaus94/claude-dev/internal/config"
)

// Window is one limit of an account: how much is used and when it resets.
type Window struct {
	Label    string  `json:"label"`
	Scope    string  `json:"scope,omitempty"` // empty: applies to every model; otherwise a model family such as "fable"
	Used     float64 `json:"used_percent"`
	ResetsAt int64   `json:"resets_at,omitempty"`
}

// Status is what is known about an account right now.
type Status struct {
	config.Account
	Identity   string           `json:"identity,omitempty"` // same identity under two homes is one subscription
	Plan       string           `json:"plan,omitempty"`
	LoggedIn   bool             `json:"logged_in"`
	APIKey     bool             `json:"api_key,omitempty"`
	Windows    []Window         `json:"windows,omitempty"`
	ProbedAt   int64            `json:"probed_at,omitempty"`
	Note       string           `json:"note,omitempty"`
	Cooling    map[string]int64 `json:"cooling,omitempty"` // model family -> unix time the limit is expected back
	LastUsed   int64            `json:"last_used,omitempty"`
	Configured bool             `json:"configured"`
}

// Family is what a limit scope is matched against: the alias and the model name of a route.
func Family(route config.Route) string { return strings.ToLower(route.Alias + " " + route.Model) }

func applies(scope, family string) bool { return scope == "" || strings.Contains(family, scope) }

// Headroom is the free share of the tightest limit that applies to the model, and whether it is known.
func (s Status) Headroom(family string) (float64, bool) {
	worst, known := 0.0, false
	for _, window := range s.Windows {
		if applies(window.Scope, family) {
			known, worst = true, max(worst, window.Used)
		}
	}
	return 100 - worst, known
}

// Available says whether the account may take a call for the model now, and why not otherwise.
func (s Status) Available(family string, at int64) (bool, string) {
	switch {
	case !s.LoggedIn:
		return false, "not logged in"
	case s.APIKey && !s.Paid:
		return false, "API-key billing; add it with --paid to allow it"
	}
	for scope, until := range s.Cooling {
		if until > at && (scope == "*" || strings.Contains(family, scope)) {
			return false, "limit reached, back " + time.Unix(until, 0).Format("Jan 2 15:04")
		}
	}
	if room, known := s.Headroom(family); known && room < minHeadroom {
		return false, "limit exhausted"
	}
	return true, ""
}

const (
	minHeadroom     = 2.0              // percent left below which an account is treated as exhausted
	probeTTL        = 2 * time.Minute  // how long a limits reading is trusted
	defaultCooldown = 15 * time.Minute // when a refusal does not say when the limit resets
	transientPause  = 2 * time.Minute  // a refusal while the limits still show room: overload or a burst limit
	inflightWeight  = 8.0              // each call already running on an account counts as this much used percent
	leadWeight      = 10.0             // the lead's own account is spared when another has similar room
)

// Probes ask a provider about an account; tests replace them.
var (
	ProbeClaude = probeClaude
	ProbeCodex  = probeCodex
	usageURL    = "https://api.anthropic.com/api/oauth/usage"
	now         = func() int64 { return time.Now().Unix() }
)

func identity(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:6])
}

func parseTime(value string) int64 {
	if stamp, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return stamp.Unix()
	}
	return 0
}

// probeClaude reads the profile's OAuth token and asks the usage endpoint. The token is never refreshed here:
// the CLI owns it, and rotating it from a second place would log the profile out.
func probeClaude(account config.Account) Status {
	status := Status{Account: account}
	home := config.ExpandHome(account.Home)
	raw, err := os.ReadFile(filepath.Join(home, ".credentials.json"))
	var credentials struct {
		OAuth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"`
			Plan         string `json:"subscriptionType"`
			Tier         string `json:"rateLimitTier"`
		} `json:"claudeAiOauth"`
	}
	if err != nil || json.Unmarshal(raw, &credentials) != nil || credentials.OAuth.AccessToken == "" {
		status.Note = "no subscription login in " + account.Home
		return status
	}
	status.LoggedIn = true
	status.Plan = strings.ReplaceAll(strings.TrimPrefix(strings.TrimPrefix(credentials.OAuth.Tier, "default_"), "claude_"), "_", " ")
	if status.Plan == "" {
		status.Plan = credentials.OAuth.Plan
	}
	status.Identity = identity("claude", credentials.OAuth.RefreshToken)
	var profile struct {
		Account struct {
			UUID string `json:"accountUuid"`
		} `json:"oauthAccount"`
	}
	if raw, err := os.ReadFile(filepath.Join(home, ".claude.json")); err == nil && json.Unmarshal(raw, &profile) == nil && profile.Account.UUID != "" {
		status.Identity = identity("claude", profile.Account.UUID)
	}
	if credentials.OAuth.ExpiresAt/1000 < now()+60 {
		status.Note = "limits unknown: the token is stale until this profile is used once"
		return status
	}
	request, _ := http.NewRequest(http.MethodGet, usageURL, nil)
	request.Header.Set("Authorization", "Bearer "+credentials.OAuth.AccessToken)
	request.Header.Set("anthropic-beta", "oauth-2025-04-20")
	request.Header.Set("User-Agent", "claude-dev")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		status.Note = "limits unknown: " + err.Error()
		return status
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		status.Note = "limits unknown: " + response.Status
		return status
	}
	status.Windows, status.ProbedAt = ParseClaudeUsage(body), now()
	return status
}

// ParseClaudeUsage turns the usage payload into windows. Unknown fields are ignored: the payload grows often.
func ParseClaudeUsage(body []byte) []Window {
	type bucket struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
	}
	var usage struct {
		FiveHour *bucket `json:"five_hour"`
		Week     *bucket `json:"seven_day"`
		Opus     *bucket `json:"seven_day_opus"`
		Sonnet   *bucket `json:"seven_day_sonnet"`
		Limits   []struct {
			Kind     string   `json:"kind"`
			Percent  *float64 `json:"percent"`
			ResetsAt string   `json:"resets_at"`
			Scope    *struct {
				Model *struct {
					Name string `json:"display_name"`
				} `json:"model"`
			} `json:"scope"`
		} `json:"limits"`
	}
	if json.Unmarshal(body, &usage) != nil {
		return nil
	}
	var out []Window
	add := func(label, scope string, value *bucket) {
		if value != nil && value.Utilization != nil {
			out = append(out, Window{Label: label, Scope: scope, Used: *value.Utilization, ResetsAt: parseTime(value.ResetsAt)})
		}
	}
	add("5 hours", "", usage.FiveHour)
	add("week", "", usage.Week)
	add("week, opus", "opus", usage.Opus)
	add("week, sonnet", "sonnet", usage.Sonnet)
	seen := map[string]bool{"opus": usage.Opus != nil && usage.Opus.Utilization != nil, "sonnet": usage.Sonnet != nil && usage.Sonnet.Utilization != nil}
	for _, limit := range usage.Limits {
		if limit.Percent == nil {
			continue
		}
		if limit.Kind == "weekly_all" && (usage.Week == nil || usage.Week.Utilization == nil) {
			out = append(out, Window{Label: "week", Used: *limit.Percent, ResetsAt: parseTime(limit.ResetsAt)})
		}
		if limit.Kind != "weekly_scoped" || limit.Scope == nil || limit.Scope.Model == nil || limit.Scope.Model.Name == "" {
			continue
		}
		scope := strings.ToLower(strings.Fields(limit.Scope.Model.Name)[0])
		if !seen[scope] {
			seen[scope] = true
			out = append(out, Window{Label: "week, " + scope, Scope: scope, Used: *limit.Percent, ResetsAt: parseTime(limit.ResetsAt)})
		}
	}
	return out
}

// probeCodex asks the Codex app server of that home for its rate limits.
func probeCodex(account config.Account) Status {
	status := Status{Account: account}
	home := config.ExpandHome(account.Home)
	raw, err := os.ReadFile(filepath.Join(home, "auth.json"))
	var auth struct {
		Mode   string `json:"auth_mode"`
		Key    string `json:"OPENAI_API_KEY"`
		Tokens *struct {
			AccountID string `json:"account_id"`
		} `json:"tokens"`
	}
	if err != nil || json.Unmarshal(raw, &auth) != nil {
		status.Note = "no login in " + account.Home
		return status
	}
	if auth.Tokens == nil || auth.Tokens.AccountID == "" {
		if auth.Key == "" {
			status.Note = "no login in " + account.Home
			return status
		}
		status.LoggedIn, status.APIKey, status.Plan = true, true, "api key"
		status.Identity = identity("codex-key", auth.Key)
		return status
	}
	status.LoggedIn, status.Identity = true, identity("codex", auth.Tokens.AccountID)
	body, err := codexRateLimits(home)
	if err != nil {
		status.Note = "limits unknown: " + err.Error()
		return status
	}
	status.Windows, status.Plan = ParseCodexLimits(body)
	status.ProbedAt = now()
	return status
}

func codexRateLimits(home string) ([]byte, error) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binary, "app-server", "--stdio")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	}()
	replies := make(chan []byte, 8)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1<<20), 1<<24)
		for scanner.Scan() {
			replies <- append([]byte{}, scanner.Bytes()...)
		}
		close(replies)
	}()
	call := func(id int, method string) ([]byte, error) {
		fmt.Fprintf(stdin, `{"id":%d,"method":%q,"params":{"clientInfo":{"name":"claude_dev","version":"1"}}}`+"\n", id, method)
		deadline := time.After(12 * time.Second)
		for {
			select {
			case line, open := <-replies:
				if !open {
					return nil, errors.New("codex app-server closed")
				}
				var message struct {
					ID     int             `json:"id"`
					Result json.RawMessage `json:"result"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if json.Unmarshal(line, &message) != nil || message.ID != id {
					continue
				}
				if message.Error != nil {
					return nil, errors.New(message.Error.Message)
				}
				return message.Result, nil
			case <-deadline:
				return nil, errors.New("codex app-server timed out")
			}
		}
	}
	if _, err := call(1, "initialize"); err != nil {
		return nil, err
	}
	fmt.Fprintln(stdin, `{"method":"initialized","params":{}}`)
	return call(2, "account/rateLimits/read")
}

// ParseCodexLimits turns the rate-limit reply into windows and the plan name.
func ParseCodexLimits(body []byte) ([]Window, string) {
	type window struct {
		Used     *float64 `json:"usedPercent"`
		Minutes  int      `json:"windowDurationMins"`
		ResetsAt int64    `json:"resetsAt"`
	}
	type bucket struct {
		Primary   *window `json:"primary"`
		Secondary *window `json:"secondary"`
		Plan      string  `json:"planType"`
		Reached   *string `json:"rateLimitReachedType"`
	}
	var reply struct {
		Allowed *bool             `json:"ordinaryUsageAllowed"`
		Limits  *bucket           `json:"rateLimits"`
		ByID    map[string]bucket `json:"rateLimitsByLimitId"`
	}
	if json.Unmarshal(body, &reply) != nil {
		return nil, ""
	}
	buckets := []bucket{}
	ids := make([]string, 0, len(reply.ByID))
	for id := range reply.ByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		buckets = append(buckets, reply.ByID[id])
	}
	if len(buckets) == 0 && reply.Limits != nil {
		buckets = append(buckets, *reply.Limits)
	}
	var out []Window
	plan := ""
	for _, item := range buckets {
		if item.Plan != "" {
			plan = item.Plan
		}
		for _, entry := range []*window{item.Primary, item.Secondary} {
			if entry == nil || entry.Used == nil {
				continue
			}
			label := fmt.Sprintf("%d min", entry.Minutes)
			switch {
			case entry.Minutes == 10080:
				label = "week"
			case entry.Minutes >= 1440:
				label = fmt.Sprintf("%d days", entry.Minutes/1440)
			case entry.Minutes >= 60:
				label = fmt.Sprintf("%d hours", entry.Minutes/60)
			}
			used := *entry.Used
			if item.Reached != nil || (reply.Allowed != nil && !*reply.Allowed) {
				used = 100
			}
			out = append(out, Window{Label: label, Used: used, ResetsAt: entry.ResetsAt})
		}
	}
	return out, plan
}

// Probe asks the account's provider.
func Probe(account config.Account) Status {
	if account.Provider == "claude" {
		return ProbeClaude(account)
	}
	return ProbeCodex(account)
}

// Manager chooses accounts for calls and remembers which ones are resting.
type Manager struct {
	accounts []config.Account
	path     string
	leadHome string
	mu       sync.Mutex
	known    map[string]*Status
	inflight map[string]int
}

// New makes a manager for the configured accounts. path is where readings and cooldowns are kept between runs.
func New(accounts []config.Account, path string) *Manager {
	lead := os.Getenv("CLAUDE_CONFIG_DIR")
	if lead == "" {
		home, _ := os.UserHomeDir()
		lead = filepath.Join(home, ".claude")
	}
	return &Manager{accounts: accounts, path: path, leadHome: clean(lead), known: map[string]*Status{}, inflight: map[string]int{}}
}

func clean(path string) string {
	path = config.ExpandHome(path)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}

type saved struct {
	Statuses map[string]*Status `json:"accounts"`
}

// locked runs fn on the saved state under a file lock, so parallel sessions do not lose each other's cooldowns.
func (m *Manager) locked(fn func(state *saved)) {
	_ = os.MkdirAll(filepath.Dir(m.path), 0o700)
	lock, err := os.OpenFile(m.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err == nil {
		defer lock.Close()
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
	}
	state := &saved{Statuses: map[string]*Status{}}
	if raw, err := os.ReadFile(m.path); err == nil {
		_ = json.Unmarshal(raw, state)
	}
	if state.Statuses == nil {
		state.Statuses = map[string]*Status{}
	}
	fn(state)
	raw, _ := json.MarshalIndent(state, "", "  ")
	tmp := m.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, m.path)
	}
}

// refresh brings every reading up to date, probing in parallel the accounts whose reading is old.
func (m *Manager) refresh(force bool) {
	var stale []config.Account
	m.locked(func(state *saved) {
		for _, account := range m.accounts {
			previous := state.Statuses[account.Name]
			if force || previous == nil || previous.Home != account.Home || now()-previous.ProbedAt > int64(probeTTL.Seconds()) {
				stale = append(stale, account)
			}
		}
	})
	fresh := make([]Status, len(stale))
	var wait sync.WaitGroup
	for index, account := range stale {
		wait.Add(1)
		go func() {
			defer wait.Done()
			fresh[index] = Probe(account)
		}()
	}
	wait.Wait()
	m.locked(func(state *saved) {
		for _, status := range fresh {
			status := status
			if previous := state.Statuses[status.Name]; previous != nil {
				status.LastUsed = previous.LastUsed
				for scope, until := range previous.Cooling {
					if until > now() {
						if status.Cooling == nil {
							status.Cooling = map[string]int64{}
						}
						status.Cooling[scope] = until
					}
				}
				if status.ProbedAt == 0 && previous.Home == status.Home {
					status.Windows, status.ProbedAt = previous.Windows, previous.ProbedAt // keep the last good reading
				}
			}
			status.Configured = true
			state.Statuses[status.Name] = &status
		}
		m.mu.Lock()
		for _, account := range m.accounts {
			if status := state.Statuses[account.Name]; status != nil {
				copy := *status
				copy.Account = account // the configuration, not the saved reading, says what the account may do
				m.known[account.Name] = &copy
			}
		}
		m.mu.Unlock()
	})
}

// Statuses returns the current reading of every configured account.
func (m *Manager) Statuses(force bool) []Status {
	m.refresh(force)
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.accounts))
	for _, account := range m.accounts {
		if status := m.known[account.Name]; status != nil {
			out = append(out, *status)
		}
	}
	return out
}

// Has says whether any account is configured for the provider. Without one the CLI's default profile is used.
func (m *Manager) Has(provider string) bool {
	if m == nil {
		return false
	}
	for _, account := range m.accounts {
		if account.Provider == provider {
			return true
		}
	}
	return false
}

// Order lists the accounts that may take a call for the route, best first: most room for that model, fewest
// calls already running, the lead's own account spared, and the same subscription never counted twice.
// prefer names the account that holds the session being continued; it goes first while it is available.
func (m *Manager) Order(route config.Route, prefer string) []config.Account {
	m.refresh(false)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.order(route, prefer, nil)
}

func (m *Manager) order(route config.Route, prefer string, tried map[string]bool) []config.Account {
	type candidate struct {
		account config.Account
		score   float64
		used    int64
	}
	family := Family(route)
	best := map[string]candidate{}
	var keys []string
	for _, account := range m.accounts {
		status := m.known[account.Name]
		if account.Provider != route.Provider || status == nil || tried[account.Name] {
			continue
		}
		if ok, _ := status.Available(family, now()); !ok {
			continue
		}
		room, known := status.Headroom(family)
		if !known {
			room = 50
		}
		score := room - inflightWeight*float64(m.inflight[account.Name])
		if account.Provider == "claude" && clean(account.Home) == m.leadHome {
			score -= leadWeight
		}
		if account.Name == prefer {
			score += 1000
		}
		key := status.Identity // the same subscription under a second directory adds no capacity
		if key == "" {
			key = "name:" + account.Name
		}
		previous, seen := best[key]
		if !seen {
			keys = append(keys, key)
		}
		if !seen || score > previous.score {
			best[key] = candidate{account, score, status.LastUsed}
		}
	}
	candidates := make([]candidate, 0, len(keys))
	for _, key := range keys {
		candidates = append(candidates, best[key])
	}
	sort.SliceStable(candidates, func(a, b int) bool {
		if candidates[a].score != candidates[b].score {
			return candidates[a].score > candidates[b].score
		}
		return candidates[a].used < candidates[b].used
	})
	out := make([]config.Account, len(candidates))
	for index, item := range candidates {
		out[index] = item.account
	}
	return out
}

// Pick reserves the best account for a call, so lanes started together spread out instead of all choosing
// the same one. tried lists accounts this call already failed on. Release must follow.
func (m *Manager) Pick(route config.Route, prefer string, tried map[string]bool) (config.Account, bool) {
	m.refresh(false)
	m.mu.Lock()
	defer m.mu.Unlock()
	order := m.order(route, prefer, tried)
	if len(order) == 0 {
		return config.Account{}, false
	}
	m.inflight[order[0].Name]++
	return order[0], true
}

// Release ends a reservation made by Pick.
func (m *Manager) Release(name string, succeeded bool) {
	m.mu.Lock()
	m.inflight[name]--
	m.mu.Unlock()
	if succeeded {
		m.locked(func(state *saved) {
			if status := state.Statuses[name]; status != nil {
				status.LastUsed = now()
			}
		})
	}
}

// Limited records that the account refused a call for the route. The limits are read again: when one that
// applies is exhausted the account rests until it resets; when they still show room the refusal was an
// overload or a burst limit and the pause is short.
func (m *Manager) Limited(name string, route config.Route) int64 {
	var account config.Account
	for _, item := range m.accounts {
		if item.Name == name {
			account = item
		}
	}
	family, scope := Family(route), strings.ToLower(route.Alias)
	until := now() + int64(defaultCooldown.Seconds())
	fresh := Probe(account)
	if fresh.ProbedAt != 0 {
		until = now() + int64(transientPause.Seconds())
		for _, window := range fresh.Windows {
			if applies(window.Scope, family) && window.Used >= 100-minHeadroom && window.ResetsAt > until {
				until = window.ResetsAt
				scope = window.Scope
				if scope == "" {
					scope = "*"
				}
			}
		}
	}
	m.locked(func(state *saved) {
		status := state.Statuses[name]
		if status == nil {
			status = &Status{Account: account, Configured: true}
			state.Statuses[name] = status
		}
		if status.Cooling == nil {
			status.Cooling = map[string]int64{}
		}
		status.Cooling[scope] = until
		if fresh.ProbedAt != 0 {
			status.Windows, status.ProbedAt = fresh.Windows, fresh.ProbedAt
		}
		m.mu.Lock()
		copy := *status
		m.known[name] = &copy
		m.mu.Unlock()
	})
	return until
}

// Reset clears the cooldowns of an account, for when the user knows the limit is back.
func (m *Manager) Reset(name string) {
	m.locked(func(state *saved) {
		if status := state.Statuses[name]; status != nil {
			status.Cooling, status.ProbedAt = nil, 0 // and read its limits again
		}
	})
}

// Home is the directory of the account, expanded.
func Home(account config.Account) string { return config.ExpandHome(account.Home) }

// Discover finds CLI profiles in the home directory: ~/.claude, ~/.claude-*, ~/.codex, ~/.codex-*.
func Discover() []Status {
	home, _ := os.UserHomeDir()
	var out []Status
	for _, provider := range []string{"claude", "codex"} {
		paths, _ := filepath.Glob(filepath.Join(home, "."+provider+"*"))
		sort.Strings(paths)
		for _, path := range paths {
			name := strings.TrimPrefix(filepath.Base(path), ".")
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() || (name != provider && !strings.HasPrefix(name, provider+"-")) {
				continue
			}
			out = append(out, Probe(config.Account{Name: name, Provider: provider, Home: "~/" + filepath.Base(path)}))
		}
	}
	return out
}
