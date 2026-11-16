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
	Scope    string  `json:"scope,omitempty"`  // empty: applies to every model; otherwise a model family such as "fable"
	Period   string  `json:"period,omitempty"` // session (a few hours) or week
	Used     float64 `json:"used_percent"`
	ResetsAt int64   `json:"resets_at,omitempty"`
	LimitID  string  `json:"limit_id,omitempty"`
}

// period is the window's period; a reading saved before periods were recorded is told by its label.
func (w Window) period() string {
	switch {
	case w.Period != "":
		return w.Period
	case strings.HasPrefix(w.Label, "week"):
		return "week"
	}
	return "session"
}

// Status is what is known about an account right now.
type Status struct {
	config.Account
	Identity  string           `json:"identity,omitempty"` // same identity under two homes is one subscription
	Plan      string           `json:"plan,omitempty"`
	LoggedIn  bool             `json:"logged_in"`
	APIKey    bool             `json:"api_key,omitempty"`
	Windows   []Window         `json:"windows,omitempty"`
	ProbedAt  int64            `json:"probed_at,omitempty"`
	CheckedAt int64            `json:"checked_at,omitempty"` // includes failed probes, to avoid retry storms
	Note      string           `json:"note,omitempty"`
	Cooling   map[string]int64 `json:"cooling,omitempty"` // model family, "all" (the general limit) or "*" -> unix time it is expected back
	// GeneralCaps says, for a model with a limit of its own, whether the general limit of the same period caps it
	// too. Absent: not seen yet; the first call made while the general limit is exhausted finds out.
	GeneralCaps map[string]bool `json:"general_caps,omitempty"`
	LastUsed    int64           `json:"last_used,omitempty"`
	Configured  bool            `json:"configured"`
}

// Family is what a limit scope is matched against: the alias and the model name of a route.
func Family(route config.Route) string { return strings.ToLower(route.Alias + " " + route.Model) }

func applies(scope, family string) bool { return scope == "" || strings.Contains(family, scope) }

// Headroom is the free share of the tightest limit that applies to the model, and whether it is known. The
// general limit counts for a model with a limit of its own unless it was seen not to cap it.
func (s Status) Headroom(family string) (float64, bool) {
	own := s.ownScope(family)
	caps, seen := s.GeneralCaps[own]
	return s.headroomAt(family, now(), own != "" && seen && !caps)
}

// current says whether a window still describes the account: one whose reset has passed does not.
func (s Status) current(window Window, at int64) bool {
	if window.ResetsAt > 0 && window.ResetsAt <= at {
		return false // a stale exhausted window must not block the account after reset
	}
	return window.ResetsAt != 0 || s.ProbedAt == 0 || at-s.ProbedAt <= int64(probeTTL.Seconds())
}

// headroomAt is the room for a model. separate leaves out a general window where the model has a window of its
// own for the same period.
func (s Status) headroomAt(family string, at int64, separate bool) (float64, bool) {
	own := s.ownScope(family)
	worst, known := 0.0, false
	for _, window := range s.Windows {
		if !s.current(window, at) || !applies(window.Scope, family) {
			continue
		}
		if separate && window.Scope == "" && s.hasOwn(own, window.period()) {
			continue
		}
		known, worst = true, max(worst, min(100, max(0, window.Used)))
	}
	return 100 - worst, known
}

// ownScope is the scope of the limit a Claude model has of its own on this account, or "". Codex buckets keep
// the rule they always had: every window that applies counts.
func (s Status) ownScope(family string) string {
	if s.Provider != "claude" {
		return ""
	}
	for _, window := range s.Windows {
		if window.Scope != "" && strings.Contains(family, window.Scope) {
			return window.Scope
		}
	}
	return ""
}

// hasOwn says whether the account reports a limit of the scope for the period.
func (s Status) hasOwn(scope, period string) bool {
	if scope == "" {
		return false
	}
	for _, window := range s.Windows {
		if window.Scope == scope && window.period() == period {
			return true
		}
	}
	return false
}

// generalOut is the reset of a general window of the period used to at least the given share, or 0 when there
// is none.
func (s Status) generalOut(period string, at int64, least float64) int64 {
	for _, window := range s.Windows {
		if window.Scope == "" && window.period() == period && s.current(window, at) && window.Used >= least {
			return max(window.ResetsAt, at+1)
		}
	}
	return 0
}

// Available says whether the account may take a call for the model now, and why not otherwise.
func (s Status) Available(family string, at int64) (bool, string) {
	switch {
	case !s.LoggedIn:
		return false, "not logged in"
	case s.APIKey && !s.Paid:
		return false, "API-key billing; add it with --paid to allow it"
	}
	own := s.ownScope(family)
	caps := s.GeneralCaps[own] // false while it is not known: the call is tried
	for scope, until := range s.Cooling {
		if until <= at {
			continue
		}
		// A rest of the general limit holds a model with a limit of its own only when that limit is capped by it.
		if scope == "*" || scope == "all" && (own == "" || caps) || scope != "all" && strings.Contains(family, scope) {
			return false, "limit reached, back " + time.Unix(until, 0).Format("Jan 2 15:04")
		}
	}
	if room, known := s.headroomAt(family, at, own != "" && !caps); known && room < minHeadroom {
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

// back is when the account is expected to take a call for the model again, by what was read: the latest of the
// rests and exhausted limits that keep it from the model. 0 when nothing read keeps it.
func (s Status) back(family string, at int64) int64 {
	own := s.ownScope(family)
	caps := s.GeneralCaps[own]
	out := int64(0)
	for scope, until := range s.Cooling {
		if until > at && (scope == "*" || scope == "all" && (own == "" || caps) || scope != "all" && strings.Contains(family, scope)) {
			out = max(out, until)
		}
	}
	for _, window := range s.Windows {
		if !s.current(window, at) || !applies(window.Scope, family) || window.Used < 100-minHeadroom {
			continue
		}
		if own != "" && !caps && window.Scope == "" && s.hasOwn(own, window.period()) {
			continue
		}
		out = max(out, window.ResetsAt)
	}
	return out
}

// Tightest is the most used limit that still describes the account.
func (s Status) Tightest() (Window, bool) {
	var out Window
	found := false
	for _, window := range s.Windows {
		if s.current(window, now()) && (!found || window.Used > out.Used) {
			out, found = window, true
		}
	}
	return out, found
}

// Current says whether a window still describes the account.
func (s Status) Current(window Window) bool { return s.current(window, now()) }
