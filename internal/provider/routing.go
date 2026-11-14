package provider

import (
	"errors"
	"fmt"
	"os"

	"github.com/knaus94/agentgate/internal/config"
)

// Substitution records that a lane ran on a declared alternate because the routed pool was exhausted.
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
	tried, first := map[string]bool{}, error(nil)
	for {
		account, ok := Accounts.Pick(call.Route, call.Route.Account, tried)
		if !ok {
			if err := Accounts.Err(); err != nil {
				return Result{}, fmt.Errorf("account selection failed: %w", err)
			}
			if first == nil {
				first = fmt.Errorf("%w: no %s account has room for %s", ErrLimit, call.Route.Provider, call.Route)
			}
			return Result{}, first
		}
		tried[account.Name] = true
		attempt := call
		attempt.Account = account
		result, err := Run(attempt)
		Accounts.Release(account.Name, err == nil)
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
		if first == nil {
			first = err
		}
	}
}

// RunRouted tries the route on every account that has room and, when all are exhausted, the declared
// alternates in order. skip rejects alternates the caller may not use, such as the model that wrote the
// code under review.
func RunRouted(cfg *config.Config, call Call, skip func(config.Route) bool) (Result, config.Route, error) {
	candidates := append([]config.Route{call.Route}, cfg.Alternates(call.Route)...)
	base, first := call.Dir, error(nil)
	lastResult, lastRoute := Result{Dir: call.Dir}, call.Route
	for index, route := range candidates {
		if (index > 0 && call.Resume != "") || (skip != nil && skip(route)) {
			continue
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
			return result, route, nil
		}
		if first == nil {
			first = err
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
