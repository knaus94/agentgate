package provider

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/config"
)

// Substitution records that a lane ran on a declared alternate, and why its route was not used.
type Substitution struct {
	Wanted string `json:"wanted"`
	Used   string `json:"used"`
	Reason string `json:"reason"`
}

// onAccounts runs the call under the best configured account of its provider and moves to the next one when
// that account's limit is exhausted. The model changes only after every account refused it.
func onAccounts(call Call) (Result, error) {
	if !Accounts.Has(call.Route.Provider) {
		return Run(call)
	}
	tried := map[string]bool{}
	for {
		account, ok := Accounts.Pick(call.Route, call.Route.Account, tried)
		if !ok {
			if err := Accounts.Err(); err != nil {
				return Result{}, fmt.Errorf("account selection failed: %w", err)
			}
			reason := fmt.Sprintf("no %s account has room for %s", call.Route.Provider, call.Route)
			if back := Accounts.Back(call.Route); back > 0 {
				reason += ": back " + time.Unix(back, 0).Format("Jan 2 15:04")
			}
			if kept := Accounts.Reserved(call.Route.Provider); len(kept) > 0 {
				reason += "; " + strings.Join(kept, ", ") + " kept for the lead"
			}
			return Result{}, fmt.Errorf("%w: %s", ErrLimit, reason)
		}
		tried[account.Name] = true
		attempt := call
		attempt.Account = account
		result, err := Run(attempt)
		Accounts.Release(account.Name, call.Route, err == nil)
		if err == nil {
			result.Account = account.Name
			return result, nil
		}
		if !errors.Is(err, ErrLimit) {
			if call.Resume != "" && call.Route.Account != "" && account.Name != call.Route.Account {
				err = fmt.Errorf("account %s has no room and the session did not continue on %s: %w", call.Route.Account, account.Name, err)
			}
			return result, err
		}
		Accounts.Limited(account.Name, call.Route)
		if err := Accounts.Err(); err != nil {
			return Result{}, fmt.Errorf("save account cooldown: %w", err)
		}
	}
}

// RunRouted tries the route on every account that has room and, when all are exhausted, the declared
// alternates in order. skip says why the caller may not use a route, such as the model that wrote the code
// under review; "" when it may. A result of an alternate says in Why why the route itself was not used.
func RunRouted(cfg *config.Config, call Call, skip func(config.Route) string) (Result, config.Route, error) {
	candidates := append([]config.Route{call.Route}, cfg.Alternates(call.Route)...)
	base, first, why := call.Dir, error(nil), ""
	lastResult, lastRoute := Result{Dir: call.Dir}, call.Route
	for index, route := range candidates {
		if index > 0 && call.Resume != "" {
			continue
		}
		if skip != nil {
			if reason := skip(route); reason != "" {
				if index == 0 {
					why = reason
				}
				continue
			}
		}
		attempt := call
		attempt.Route = route
		if index > 0 {
			attempt.Dir = fmt.Sprintf("%s-alt%d", base, index)
			// Assignment/schema files remain in the original lane. /tmp is
			// private inside bwrap, so a sibling alternate needs an explicit
			// read-only view of that directory.
			if info, err := os.Stat(base); err == nil && info.IsDir() {
				attempt.ReadDirs = append(append([]string(nil), call.ReadDirs...), base)
			}
		}
		result, err := onAccounts(attempt)
		if result.Dir == "" {
			result.Dir = attempt.Dir
		}
		lastResult, lastRoute = result, route
		if err == nil {
			route.Account = result.Account
			if index > 0 {
				result.Why = why
			}
			return result, route, nil
		}
		if first == nil {
			first = err
		}
		if index == 0 && errors.Is(err, ErrLimit) {
			why = limitReason(route, err)
		}
		if !errors.Is(err, ErrLimit) {
			return result, route, err
		}
	}
	if first == nil {
		return lastResult, lastRoute, errors.New("no permitted route is available for this worker")
	}
	return lastResult, lastRoute, fmt.Errorf("%w; no permitted alternate is available", first)
}

// limitReason says in a few words why a route had no room: what the accounts read, or what its CLI said.
func limitReason(route config.Route, err error) string {
	if !Accounts.Has(route.Provider) {
		return "the " + route.Provider + " CLI reported its usage limit for " + route.String()
	}
	return strings.TrimPrefix(err.Error(), ErrLimit.Error()+": ")
}
