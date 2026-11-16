package accounts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/config"
)

const commandUsage = `usage:
  agentgate accounts [--json]                       limits of the configured accounts
  agentgate accounts discover [--save]              find CLI profiles in the home directory; --save adds the usable ones
  agentgate accounts add <name> <claude|codex> <home> [--paid] [--lead-only]
  agentgate accounts remove <name>
  agentgate accounts reserve <name>                 keep the account for the lead: no worker runs on it
  agentgate accounts unreserve <name>               let workers use it again
  agentgate accounts reset <name>                   forget that the account hit a limit and what was learned of its limits`

// LoginHint is the command that logs a profile in. It is interactive, so the user runs it in a terminal.
func LoginHint(account config.Account) string {
	home := "'" + strings.ReplaceAll(Home(account), "'", "'\"'\"'") + "'"
	if account.Provider == "claude" {
		return "CLAUDE_CONFIG_DIR=" + home + " claude auth login"
	}
	return "CODEX_HOME=" + home + " codex login"
}

// Command implements `agentgate accounts ...`.
func Command(cfg *config.Config, manager *Manager, rest []string, flags map[string]bool) (any, error) {
	action := ""
	if len(rest) > 0 {
		action = rest[0]
	}
	switch action {
	case "", "list", "limits":
		if len(cfg.Accounts) == 0 {
			found := Discover()
			if flags["json"] {
				return found, nil
			}
			return "No accounts are configured: every worker uses the default profile of its CLI.\n" +
				"Profiles found on this machine (add the usable ones with `agentgate accounts discover --save`):\n\n" +
				Render(found, nil, manager.leadHome), nil
		}
		statuses := manager.Statuses(true)
		if err := manager.Err(); err != nil {
			return nil, fmt.Errorf("account state unavailable: %w", err)
		}
		if flags["json"] {
			return statuses, nil
		}
		text := Render(statuses, nil, manager.leadHome)
		for _, warning := range Unserved(cfg) {
			text += "\n" + warning + "\n"
		}
		return text, nil
	case "discover":
		found := Discover()
		if !flags["save"] {
			return Render(found, nil, manager.leadHome) + "\nAdd the usable ones with `agentgate accounts discover --save`.\n", nil
		}
		list, added := merge(cfg.Accounts, manager.Statuses(false), found)
		if len(added) == 0 {
			return "Nothing to add: every usable profile is already configured.\n", nil
		}
		if err := config.SetValue("accounts", list); err != nil {
			return nil, err
		}
		return "Added: " + strings.Join(added, ", ") + ".\n\n" + Render(New(list, manager.path).Statuses(true), nil, manager.leadHome), nil
	case "add":
		if len(rest) != 4 || strings.TrimSpace(rest[1]) == "" || (rest[2] != "claude" && rest[2] != "codex") {
			return nil, errors.New(commandUsage)
		}
		account := config.Account{Name: rest[1], Provider: rest[2], Home: rest[3], Paid: flags["paid"], LeadOnly: flags["lead-only"]}
		if !filepath.IsAbs(Home(account)) {
			absolute, err := filepath.Abs(Home(account))
			if err != nil {
				return nil, err
			}
			account.Home = absolute
		}
		if slices.ContainsFunc(cfg.Accounts, func(other config.Account) bool { return other.Name == account.Name }) {
			return nil, fmt.Errorf("account %q already exists", account.Name)
		}
		if err := os.MkdirAll(Home(account), 0o700); err != nil {
			return nil, err
		}
		if err := config.SetValue("accounts", append(slices.Clone(cfg.Accounts), account)); err != nil {
			return nil, err
		}
		status := Probe(account)
		text := Render([]Status{status}, nil, manager.leadHome)
		if !status.LoggedIn {
			text += "\nLog it in from a terminal:  " + LoginHint(account) + "\n"
		}
		return text, nil
	case "reserve", "unreserve":
		position := -1
		if len(rest) == 2 {
			position = slices.IndexFunc(cfg.Accounts, func(other config.Account) bool { return other.Name == rest[1] })
		}
		if position < 0 {
			return nil, fmt.Errorf("unknown account; %s", commandUsage)
		}
		list := slices.Clone(cfg.Accounts)
		list[position].LeadOnly = action == "reserve"
		if err := config.SetValue("accounts", list); err != nil {
			return nil, err
		}
		cfg.Accounts = list
		text := Render(New(list, manager.path).Statuses(false), nil, manager.leadHome)
		for _, warning := range Unserved(cfg) {
			text += "\n" + warning + "\n"
		}
		return text, nil
	case "remove", "reset":
		if len(rest) != 2 || !slices.ContainsFunc(cfg.Accounts, func(other config.Account) bool { return other.Name == rest[1] }) {
			return nil, fmt.Errorf("unknown account; %s", commandUsage)
		}
		if action == "reset" {
			if err := manager.Reset(rest[1]); err != nil {
				return nil, err
			}
			return Render(manager.Statuses(true), nil, manager.leadHome), nil
		}
		list := slices.DeleteFunc(slices.Clone(cfg.Accounts), func(other config.Account) bool { return other.Name == rest[1] })
		if err := config.SetValue("accounts", list); err != nil {
			return nil, err
		}
		return "Removed " + rest[1] + ". The profile directory is untouched.\n", nil
	}
	return nil, errors.New(commandUsage)
}

// merge adds the discovered profiles that bring new capacity: logged in, on a subscription, and not the
// same subscription as an account already in the list.
func merge(configured []config.Account, known, found []Status) ([]config.Account, []string) {
	list := slices.Clone(configured)
	names, homes, identities := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, account := range configured {
		names[account.Name], homes[clean(account.Home)] = true, true
	}
	for _, status := range known {
		if status.Identity != "" {
			identities[status.Identity] = true
		}
	}
	var added []string
	for _, status := range found {
		if !status.LoggedIn || status.APIKey || homes[clean(status.Home)] || names[status.Name] || (status.Identity != "" && identities[status.Identity]) {
			continue
		}
		if status.Identity != "" {
			identities[status.Identity] = true
		}
		names[status.Name], homes[clean(status.Home)] = true, true
		list, added = append(list, status.Account), append(added, status.Name)
	}
	return list, added
}

// Render draws accounts as a table grouped by provider.
func Render(statuses []Status, at *int64, leadHome string) string {
	moment := now()
	if at != nil {
		moment = *at
	}
	notes := Notes(statuses, moment, leadHome, false)
	var out strings.Builder
	for _, provider := range []string{"claude", "codex"} {
		header := false
		for index, status := range statuses {
			if status.Provider != provider {
				continue
			}
			if !header {
				fmt.Fprintf(&out, "%s\n", provider)
				header = true
			}
			var windows []string
			for _, window := range status.Windows {
				text := fmt.Sprintf("%s %.0f%%", status.Label(window, false), window.Used)
				if window.Used >= 100-minHeadroom && window.ResetsAt > moment {
					text += " (back " + time.Unix(window.ResetsAt, 0).Format("Jan 2 15:04") + ")"
				}
				windows = append(windows, text)
			}
			plan := status.Plan
			if plan == "" {
				plan = "-"
			}
			usage := strings.Join(windows, " · ")
			if usage == "" {
				usage = "-"
			}
			fmt.Fprintf(&out, "  %-14s %-18s %-12s %s\n", status.Name, status.Home, plan, usage)
			for _, note := range notes[index] {
				fmt.Fprintf(&out, "  %-14s   %s\n", "", note)
			}
		}
	}
	if out.Len() == 0 {
		return "No CLI profiles found.\n"
	}
	return out.String()
}

// Label names a window as a person reads it. Long, for the table: the general weekly limit is "week, other
// models" once the subscription's own limits are known to be apart from it. Short, for the panel: "all" for the
// general weekly limit beside a model's own, the model's name for its own, "5h" for the session window.
func (s Status) Label(window Window, short bool) string {
	scoped := ""
	for _, other := range s.Windows {
		if other.Scope != "" && other.period() == window.period() {
			scoped = other.Scope
		}
	}
	general := s.Provider == "claude" && window.Scope == "" && window.period() == "week" && scoped != ""
	caps, known := s.GeneralCaps[scoped]
	switch {
	case !short && general && known && !caps:
		return "week, other models"
	case !short:
		return window.Label
	case general:
		return "all"
	case s.Provider == "claude" && window.Scope != "":
		return window.Scope
	case window.Label == "5 hours":
		return "5h"
	}
	return window.Label
}

// Notes says what the tool does with each account, by its position in statuses: kept for the lead, resting,
// not logged in, the same subscription as another, and what is known of a model's own limit. short is the
// wording for the panel, where one line holds them all.
func Notes(statuses []Status, moment int64, leadHome string, short bool) [][]string {
	out := make([][]string, len(statuses))
	first, kept := map[string]string{}, map[string]string{}
	for _, status := range statuses {
		if status.LeadOnly && status.Identity != "" {
			kept[status.Provider+status.Identity] = status.Name
		}
	}
	for index, status := range statuses {
		var notes []string
		say := func(long, brief string) {
			if short {
				long = brief
			}
			notes = append(notes, long)
		}
		keeper := kept[status.Provider+status.Identity]
		switch {
		case status.LeadOnly:
			say("kept for the lead: workers do not use it", "kept for the lead")
		case keeper != "":
			say("same subscription as "+keeper+", kept for the lead: workers do not use it", "kept for the lead, as "+keeper)
		}
		switch original, duplicate := first[status.Provider+status.Identity]; {
		case !status.LoggedIn:
			say("not logged in: "+LoginHint(status.Account), "not logged in")
		case status.APIKey && !status.Paid:
			say("API key, billed per token: not used unless added with --paid", "API key: not used")
		case duplicate && status.Identity != "" && (keeper == "" || status.LeadOnly):
			say("same subscription as "+original+": adds no capacity", "same subscription as "+original)
		case duplicate && status.Identity != "":
			// said above: its subscription is kept for the lead
		default:
			if status.Identity != "" {
				first[status.Provider+status.Identity] = status.Name
			}
			scopes := make([]string, 0, len(status.Cooling))
			for scope := range status.Cooling {
				scopes = append(scopes, scope)
			}
			sort.Strings(scopes)
			for _, scope := range scopes {
				if until := status.Cooling[scope]; until > moment {
					what, brief := scope, scope+" "
					switch scope {
					case "*":
						what, brief = "all models", ""
					case "all":
						what = "the models without a limit of their own"
					}
					back := time.Unix(until, 0).Format("Jan 2 15:04")
					say("resting for "+what+" until "+back, brief+"resting until "+back)
				}
			}
		}
		if status.Provider == "claude" {
			seen := map[string]bool{}
			for _, window := range status.Windows {
				if window.Scope == "" || seen[window.Scope] {
					continue
				}
				seen[window.Scope] = true
				caps, known := status.GeneralCaps[window.Scope]
				switch {
				case !known:
					say(window.Scope+": not yet known whether the general limit caps it", window.Scope+": unknown if all caps it")
				case caps:
					say(window.Scope+": also capped by the general limit (seen)", window.Scope+": capped by all too")
				default:
					say(window.Scope+": its own limit only (seen)", window.Scope+": own limit only")
				}
			}
		}
		if status.Note != "" {
			notes = append(notes, status.Note)
		}
		if status.Provider == config.Lead() && clean(status.Home) == leadHome {
			say("lead session runs here", "lead runs here")
		}
		out[index] = notes
	}
	return out
}

// Unserved warns about a provider whose every account is kept for the lead while workers are routed to it:
// those workers will all run on fallback models.
func Unserved(cfg *config.Config) []string {
	used := map[string]bool{}
	use := func(spec string) {
		if route, err := cfg.Route(spec); err == nil {
			used[route.Provider] = true
		}
	}
	for _, mode := range cfg.Modes {
		for _, spec := range append([]string{mode.Scout, mode.Backend, mode.Frontend}, mode.Architecture...) {
			use(spec)
		}
		for _, lane := range mode.Review {
			use(lane.Route)
		}
	}
	for _, spec := range []string{cfg.UIDesigner, cfg.VisualReviewer, cfg.Consultant} {
		use(spec)
	}
	var out []string
	for _, provider := range []string{"claude", "codex"} {
		count, kept := 0, 0
		for _, account := range cfg.Accounts {
			if account.Provider == provider {
				count++
				if account.LeadOnly {
					kept++
				}
			}
		}
		if used[provider] && count > 0 && kept == count {
			out = append(out, fmt.Sprintf("Every %s account is kept for the lead: %s workers will all run on fallback models.", provider, provider))
		}
	}
	return out
}
