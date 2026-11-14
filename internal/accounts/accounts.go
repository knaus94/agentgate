// Package accounts spreads model calls over several logged-in CLI profiles of the same provider.
//
// An account is a Claude config directory or a Codex home. Before work starts each account is asked how much of
// its limits is used; a call goes to the account with the most room for the model it needs, and an account
// that reports an exhausted limit is set aside until that limit resets.
package accounts

import (
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/config"
)

// Window is one limit of an account: how much is used and when it resets.
type Window struct {
	Label    string  `json:"label"`
	Scope    string  `json:"scope,omitempty"` // empty: applies to every model; otherwise a model family such as "fable"
	Used     float64 `json:"used_percent"`
	ResetsAt int64   `json:"resets_at,omitempty"`
	LimitID  string  `json:"limit_id,omitempty"`
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
	CheckedAt  int64            `json:"checked_at,omitempty"` // includes failed probes, to avoid retry storms
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
	return s.headroomAt(family, now())
}

func (s Status) headroomAt(family string, at int64) (float64, bool) {
	worst, known := 0.0, false
	for _, window := range s.Windows {
		if window.ResetsAt > 0 && window.ResetsAt <= at {
			continue // a stale exhausted window must not block the account after reset
		}
		if window.ResetsAt == 0 && s.ProbedAt > 0 && at-s.ProbedAt > int64(probeTTL.Seconds()) {
			continue
		}
		if applies(window.Scope, family) {
			known, worst = true, max(worst, min(100, max(0, window.Used)))
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
	if room, known := s.headroomAt(family, at); known && room < minHeadroom {
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

// Home is the directory of the account, expanded.
func Home(account config.Account) string { return config.ExpandHome(account.Home) }
