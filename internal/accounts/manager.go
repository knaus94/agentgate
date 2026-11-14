package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
)

// Manager chooses accounts for calls and remembers which ones are resting.
type Manager struct {
	accounts  []config.Account
	path      string
	leadHome  string
	mu        sync.Mutex
	refreshMu sync.Mutex
	lastErr   error
	known     map[string]*Status
	inflight  map[string]int
}

// New makes a manager for the configured accounts. path is where readings and cooldowns are kept between runs.
func New(accounts []config.Account, path string) *Manager {
	// The lead session draws on the profile of its own CLI: Claude's config directory, or Codex's home.
	variable, fallback := "CLAUDE_CONFIG_DIR", ".claude"
	if config.Lead() == "codex" {
		variable, fallback = "CODEX_HOME", ".codex"
	}
	lead := os.Getenv(variable)
	if lead == "" {
		home, _ := os.UserHomeDir()
		lead = filepath.Join(home, fallback)
	}
	return &Manager{accounts: append([]config.Account(nil), accounts...), path: path, leadHome: clean(lead), known: map[string]*Status{}, inflight: map[string]int{}}
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
func (m *Manager) locked(write bool, fn func(state *saved)) error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(m.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	state := &saved{Statuses: map[string]*Status{}}
	if raw, err := os.ReadFile(m.path); err == nil {
		if err := json.Unmarshal(raw, state); err != nil {
			return fmt.Errorf("read account state %s: %w", m.path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if state.Statuses == nil {
		state.Statuses = map[string]*Status{}
	}
	fn(state)
	if write {
		return ops.WriteJSON(m.path, state)
	}
	return nil
}

// refresh brings every reading up to date, probing in parallel the accounts whose reading is old.
func (m *Manager) refresh(force bool) (err error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	defer func() {
		m.mu.Lock()
		m.lastErr = err
		m.mu.Unlock()
	}()
	var stale []config.Account
	if err := m.locked(false, func(state *saved) {
		for _, account := range m.accounts {
			previous := state.Statuses[account.Name]
			if force || needsRefresh(previous, account) {
				stale = append(stale, account)
			}
		}
	}); err != nil {
		return err
	}
	fresh := make([]Status, len(stale))
	var wait sync.WaitGroup
	for index, account := range stale {
		wait.Add(1)
		go func() {
			defer wait.Done()
			fresh[index] = Probe(account)
			fresh[index].CheckedAt = now()
		}()
	}
	wait.Wait()
	return m.locked(len(fresh) > 0, func(state *saved) {
		for _, status := range fresh {
			status := status
			if previous := state.Statuses[status.Name]; previous != nil && previous.Home == status.Home && previous.Provider == status.Provider {
				status.LastUsed = previous.LastUsed
				for scope, until := range previous.Cooling {
					if until > now() {
						if status.Cooling == nil {
							status.Cooling = map[string]int64{}
						}
						status.Cooling[scope] = until
					}
				}
				if status.ProbedAt == 0 && status.LoggedIn && (status.Identity == "" || status.Identity == previous.Identity) {
					status.Windows, status.ProbedAt = previous.Windows, previous.ProbedAt // keep the last good reading
				}
			}
			status.Configured = true
			state.Statuses[status.Name] = &status
		}
		m.mu.Lock()
		for _, account := range m.accounts {
			if status := state.Statuses[account.Name]; status != nil {
				copy := cloneStatus(*status)
				copy.Account = account // the configuration, not the saved reading, says what the account may do
				m.known[account.Name] = &copy
			}
		}
		m.mu.Unlock()
	})
}

func needsRefresh(previous *Status, account config.Account) bool {
	if previous == nil || previous.Home != account.Home || previous.Provider != account.Provider {
		return true
	}
	checked := max(previous.CheckedAt, previous.ProbedAt)
	if checked == 0 || now()-checked >= int64(probeTTL.Seconds()) {
		return true
	}
	for _, window := range previous.Windows {
		if window.ResetsAt > checked && window.ResetsAt <= now() {
			return true
		}
	}
	return false
}

func cloneStatus(status Status) Status {
	status.Windows = append([]Window(nil), status.Windows...)
	if status.Cooling != nil {
		cooling := make(map[string]int64, len(status.Cooling))
		for key, value := range status.Cooling {
			cooling[key] = value
		}
		status.Cooling = cooling
	}
	return status
}

// Err exposes a persistence/probe-cache failure to the router. It must not be
// mistaken for a provider quota refusal and trigger a different paid model.
func (m *Manager) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

// Statuses returns the current reading of every configured account.
func (m *Manager) Statuses(force bool) []Status {
	if err := m.refresh(force); err != nil {
		out := make([]Status, len(m.accounts))
		for i, account := range m.accounts {
			out[i] = Status{Account: account, Configured: true, Note: "account state unavailable: " + err.Error()}
		}
		return out
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.accounts))
	for _, account := range m.accounts {
		if status := m.known[account.Name]; status != nil {
			out = append(out, cloneStatus(*status))
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

// order lists the accounts that may take a call for the route, best first: most room for that model, fewest
// calls already running, the lead's own account spared, and the same subscription never counted twice.
// prefer names the account that holds the session being continued; it goes first while it is available.
func (m *Manager) order(route config.Route, prefer string, tried map[string]bool) []config.Account {
	type candidate struct {
		account   config.Account
		score     float64
		used      int64
		preferred bool
		paid      bool
	}
	family := Family(route)
	best := map[string]candidate{}
	identityOf := func(account config.Account) string {
		if status := m.known[account.Name]; status != nil && status.Identity != "" {
			return account.Provider + ":" + status.Identity
		}
		return account.Provider + ":home:" + clean(account.Home)
	}
	triedIdentity, inflightIdentity := map[string]bool{}, map[string]int{}
	for _, account := range m.accounts {
		key := identityOf(account)
		triedIdentity[key] = triedIdentity[key] || tried[account.Name]
		inflightIdentity[key] += m.inflight[account.Name]
	}
	var keys []string
	for _, account := range m.accounts {
		status := m.known[account.Name]
		key := identityOf(account)
		if account.Provider != route.Provider || status == nil || triedIdentity[key] {
			continue
		}
		if ok, _ := status.Available(family, now()); !ok {
			continue
		}
		room, known := status.Headroom(family)
		if !known {
			room = 50
		}
		score := room - inflightWeight*float64(inflightIdentity[key])
		if account.Provider == config.Lead() && clean(account.Home) == m.leadHome {
			score -= leadWeight
		}
		if account.Name == prefer {
			score += 1000
		}
		previous, seen := best[key]
		if !seen {
			keys = append(keys, key)
		}
		if !seen || score > previous.score {
			best[key] = candidate{account, score, status.LastUsed, account.Name == prefer, status.APIKey}
		}
	}
	candidates := make([]candidate, 0, len(keys))
	for _, key := range keys {
		candidates = append(candidates, best[key])
	}
	sort.SliceStable(candidates, func(a, b int) bool {
		if candidates[a].preferred != candidates[b].preferred {
			return candidates[a].preferred
		}
		if candidates[a].paid != candidates[b].paid {
			return !candidates[a].paid
		}
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
	if m.refresh(false) != nil {
		return config.Account{}, false
	}
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
	if m.inflight[name] > 0 {
		m.inflight[name]--
	}
	m.mu.Unlock()
	if succeeded {
		m.locked(true, func(state *saved) {
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
	found := false
	for _, item := range m.accounts {
		if item.Name == name {
			account = item
			found = true
		}
	}
	if !found {
		return 0
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
	err := m.locked(true, func(state *saved) {
		status := state.Statuses[name]
		if status == nil {
			status = &Status{Account: account, Configured: true}
			state.Statuses[name] = status
		}
		if status.Cooling == nil {
			status.Cooling = map[string]int64{}
		}
		until = max(until, status.Cooling[scope])
		status.Cooling[scope] = until
		if fresh.ProbedAt != 0 {
			status.Windows, status.ProbedAt = fresh.Windows, fresh.ProbedAt
		}
		status.CheckedAt = now()
		// A second profile of this subscription cannot bypass a cooldown.
		if status.Identity != "" {
			for _, other := range state.Statuses {
				if other != nil && other.Provider == status.Provider && other.Identity == status.Identity {
					if other.Cooling == nil {
						other.Cooling = map[string]int64{}
					}
					other.Cooling[scope] = max(other.Cooling[scope], until)
				}
			}
		}
		m.mu.Lock()
		copy := cloneStatus(*status)
		m.known[name] = &copy
		m.mu.Unlock()
	})
	m.mu.Lock()
	m.lastErr = err
	m.mu.Unlock()
	return until
}

// Reset clears the cooldowns of an account, for when the user knows the limit is back.
func (m *Manager) Reset(name string) error {
	return m.locked(true, func(state *saved) {
		if status := state.Statuses[name]; status != nil {
			status.Cooling, status.ProbedAt, status.CheckedAt, status.Windows = nil, 0, 0, nil // and read its limits again
		}
	})
}
