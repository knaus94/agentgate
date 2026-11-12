package accounts

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/knaus94/claude-dev/internal/config"
)

const commandUsage = `usage:
  claude-dev accounts [--json]                       limits of the configured accounts
  claude-dev accounts discover [--save]              find CLI profiles in the home directory; --save adds the usable ones
  claude-dev accounts add <name> <claude|codex> <home> [--paid]
  claude-dev accounts remove <name>
  claude-dev accounts reset <name>                   forget that the account hit a limit`

// LoginHint is the command that logs a profile in. It is interactive, so the user runs it in a terminal.
func LoginHint(account config.Account) string {
	if account.Provider == "claude" {
		return "CLAUDE_CONFIG_DIR=" + account.Home + " claude auth login"
	}
	return "CODEX_HOME=" + account.Home + " codex login"
}

// Command implements `claude-dev accounts ...`.
func Command(cfg *config.Config, manager *Manager, rest []string, flags map[string]bool) (any, error) {
	action := ""
	if len(rest) > 0 {
		action = rest[0]
	}
	switch action {
	case "", "list", "limits":
		if len(cfg.Accounts) == 0 {
			found := Discover()
			return "No accounts are configured: every worker uses the default profile of its CLI.\n" +
				"Profiles found on this machine (add the usable ones with `claude-dev accounts discover --save`):\n\n" +
				Render(found, nil, manager.leadHome), nil
		}
		statuses := manager.Statuses(true)
		if flags["json"] {
			return statuses, nil
		}
		return Render(statuses, nil, manager.leadHome), nil
	case "discover":
		found := Discover()
		if !flags["save"] {
			return Render(found, nil, manager.leadHome) + "\nAdd the usable ones with `claude-dev accounts discover --save`.\n", nil
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
		if len(rest) != 4 || (rest[2] != "claude" && rest[2] != "codex") {
			return nil, errors.New(commandUsage)
		}
		account := config.Account{Name: rest[1], Provider: rest[2], Home: rest[3], Paid: flags["paid"]}
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
	case "remove", "reset":
		if len(rest) != 2 || !slices.ContainsFunc(cfg.Accounts, func(other config.Account) bool { return other.Name == rest[1] }) {
			return nil, fmt.Errorf("unknown account; %s", commandUsage)
		}
		if action == "reset" {
			manager.Reset(rest[1])
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
		if !status.LoggedIn || status.APIKey || homes[clean(status.Home)] || names[status.Name] || identities[status.Identity] {
			continue
		}
		identities[status.Identity] = true
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
	var out strings.Builder
	for _, provider := range []string{"claude", "codex"} {
		first := map[string]string{}
		header := false
		for _, status := range statuses {
			if status.Provider != provider {
				continue
			}
			if !header {
				fmt.Fprintf(&out, "%s\n", provider)
				header = true
			}
			var notes []string
			var windows []string
			for _, window := range status.Windows {
				text := fmt.Sprintf("%s %.0f%%", window.Label, window.Used)
				if window.Used >= 100-minHeadroom && window.ResetsAt > moment {
					text += " (back " + time.Unix(window.ResetsAt, 0).Format("Jan 2 15:04") + ")"
				}
				windows = append(windows, text)
			}
			switch original, duplicate := first[status.Identity]; {
			case !status.LoggedIn:
				notes = append(notes, "not logged in: "+LoginHint(status.Account))
			case status.APIKey && !status.Paid:
				notes = append(notes, "API key, billed per token: not used unless added with --paid")
			case duplicate && status.Identity != "":
				notes = append(notes, "same subscription as "+original+": adds no capacity")
			default:
				if status.Identity != "" {
					first[status.Identity] = status.Name
				}
				for scope, until := range status.Cooling {
					if until > moment {
						what := "all models"
						if scope != "*" {
							what = scope
						}
						notes = append(notes, fmt.Sprintf("resting for %s until %s", what, time.Unix(until, 0).Format("Jan 2 15:04")))
					}
				}
			}
			if status.LoggedIn && status.Note != "" {
				notes = append(notes, status.Note)
			}
			if provider == "claude" && clean(status.Home) == leadHome {
				notes = append(notes, "lead session runs here")
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
			for _, note := range notes {
				fmt.Fprintf(&out, "  %-14s   %s\n", "", note)
			}
		}
	}
	if out.Len() == 0 {
		return "No CLI profiles found.\n"
	}
	return out.String()
}
