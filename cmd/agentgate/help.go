package main

import (
	"fmt"
	"slices"
	"strings"
)

// commandSpec is the CLI's contract: the parser and help renderer share it, so
// an option cannot be silently accepted by a command which does not implement it.
type commandSpec struct {
	name, group, summary, syntax string
	flags                        []string
	input                        bool
	plan                         bool
	example                      string
}

var commandSpecs = []commandSpec{
	{name: "watch", group: "Tasks", summary: "Live terminal dashboard; open workers, checks and findings", syntax: "watch [--all] [--once] [--json] [--no-color]", flags: []string{"all", "once", "json", "no-color"}, example: "agentgate task watch"},
	{name: "status", group: "Tasks", summary: "Current task, or the checkout's task overview, as JSON", syntax: "status [--all]", flags: []string{"all"}, example: "agentgate task status"},
	{name: "stop", group: "Tasks", summary: "Stop workers in this session, or this checkout from outside", syntax: "stop", example: "agentgate task stop"},
	{name: "plan", group: "Tasks", summary: "Save, list, inspect and share reusable plans", syntax: "plan save [name] --file <path>|- | list | show <name> | link [directory]", input: true, plan: true, example: "agentgate plan save wallet --file plan.json"},
	{name: "install", group: "Setup", summary: "Install lead commands and editor integration", syntax: "install [--lead claude|codex|both] [--zed] [--uninstall]", flags: []string{"lead", "zed", "uninstall"}, example: "agentgate setup install --lead both"},
	{name: "doctor", group: "Setup", summary: "Check required executables, sandbox and configuration", syntax: "doctor", example: "agentgate setup doctor"},
	{name: "config", group: "Setup", summary: "Read, validate and edit layered configuration", syntax: "config [show|path|init|validate|set <key> <value>|unset <key>|extensions|profiles|profile <name>]", example: "agentgate config set modes.medium.backend sol:high"},
	{name: "accounts", group: "Setup", summary: "Manage CLI profiles, subscriptions and remaining limits", syntax: "accounts [list [--json]|discover [--save]|add <name> <claude|codex> <home> [--paid]|remove <name>|reset <name>]", flags: []string{"json", "save", "paid"}, example: "agentgate accounts discover --save"},
	{name: "modes", group: "Setup", summary: "Show the effective model and effort table", syntax: "modes", example: "agentgate modes"},
	{name: "update", group: "Setup", summary: "Check for or install a release", syntax: "update [--check]", flags: []string{"check"}, example: "agentgate setup update --check"},
	{name: "prune", group: "Setup", summary: "Remove expired inactive task state", syntax: "prune", example: "agentgate prune"},
	{name: "figma", group: "Design tools", summary: "Sign in, inspect Figma frames and export design packages", syntax: "figma login|status|frames|pull|node|import-cookies", example: "agentgate figma --help"},
	{name: "layout", group: "Design tools", summary: "Compare a local page with an exported design", syntax: "layout <screen> | <package> --url <page>", example: "agentgate layout --help"},
	{name: "fixtures", group: "Design tools", summary: "Serve or record GraphQL fixtures for a screen", syntax: "fixtures <dir> [--port 5999] [--record <url>]", example: "agentgate fixtures --help"},
	{name: "parse", group: "Lead protocol", summary: "Parse a /dev request without starting work", syntax: "parse [request] [--file <path>|-]", input: true, example: "agentgate parse -- '-h move invoice totals to integer cents'"},
	{name: "preview", group: "Lead protocol", summary: "Preview routing and work without starting workers", syntax: "preview [request] [--file <path>|-]", input: true, example: "agentgate preview -- '-m add pagination'"},
	{name: "prepare", group: "Lead protocol", summary: "Prepare a plan with a read-only discovery worker", syntax: "prepare --file <path>|-", input: true},
	{name: "route", group: "Lead protocol", summary: "Validate a plan and resolve model routing", syntax: "route --file <path>|- | --plan <name>", input: true, plan: true},
	{name: "architecture", group: "Lead protocol", summary: "Run independent architecture proposals and critiques", syntax: "architecture --file <path>|- | --plan <name>", input: true, plan: true},
	{name: "design", group: "Lead protocol", summary: "Prepare or revise an interface specification", syntax: "design --file <path>|-", input: true},
	{name: "consult", group: "Lead protocol", summary: "Ask a read-only consultant for scoped advice", syntax: "consult --file <path>|-", input: true},
	{name: "launch", group: "Lead protocol", summary: "Launch a validated plan and its worker streams", syntax: "launch --file <path>|- | --plan <name>", input: true, plan: true},
	{name: "replan", group: "Lead protocol", summary: "Revise the current task's plan", syntax: "replan --file <path>|- | --plan <name>", input: true, plan: true},
	{name: "answer", group: "Lead protocol", summary: "Answer a blocked worker in its existing session", syntax: "answer --file <path>|-", input: true},
	{name: "tell", group: "Lead protocol", summary: "Send additional instructions to a running worker", syntax: "tell --file <path>|-", input: true},
	{name: "check", group: "Lead protocol", summary: "Run declared verification checks", syntax: "check --all | --file <path>|-", input: true, flags: []string{"all"}},
	{name: "review", group: "Lead protocol", summary: "Review the current change with independent workers", syntax: "review [--file <path>|-]", input: true},
	{name: "visual", group: "Lead protocol", summary: "Record visual acceptance evidence", syntax: "visual --file <path>|-", input: true},
	{name: "triage", group: "Lead protocol", summary: "Accept or reject review findings with reasons", syntax: "triage --file <path>|-", input: true},
	{name: "fix", group: "Lead protocol", summary: "Return accepted findings and failed checks to their writers", syntax: "fix [--file <path>|-]", input: true},
	{name: "finish", group: "Lead protocol", summary: "Close a task only when its evidence satisfies the gate", syntax: "finish --file <path>|-", input: true},
	{name: "land", group: "Lead protocol", summary: "Apply or drop a completed isolated worktree", syntax: "land [--file <path>|-]", input: true},
	{name: "hold", group: "Lead protocol", summary: "Pause a task with a recorded reason", syntax: "hold --file <path>|-", input: true},
	{name: "resume", group: "Lead protocol", summary: "Resume a held task", syntax: "resume"},
	{name: "continue", group: "Lead protocol", summary: "Continue unfinished streams after an interrupted writer round", syntax: "continue", example: "agentgate task continue"},
	{name: "guide", group: "Reference", summary: "Print the lead's workflow guide", syntax: "guide"},
	{name: "reference", group: "Reference", summary: "Print the full JSON protocol and schemas", syntax: "reference"},
	{name: "hook", group: "Reference", summary: "Read a Claude Stop-hook payload from stdin", syntax: "hook"},
	{name: "version", group: "Reference", summary: "Print the installed version", syntax: "version"},
}

var taskCommands = []string{"status", "watch", "stop", "parse", "preview", "prepare", "route", "architecture", "design", "consult", "launch", "replan", "answer", "tell", "check", "review", "visual", "triage", "fix", "finish", "land", "hold", "resume", "continue"}
var setupCommands = []string{"install", "doctor", "update", "config", "accounts", "modes"}

func command(name string) (commandSpec, bool) {
	for _, spec := range commandSpecs {
		if spec.name == name {
			return spec, true
		}
	}
	return commandSpec{}, false
}

func groupCommands(name string) []string {
	switch name {
	case "task":
		return taskCommands
	case "setup":
		return setupCommands
	}
	return nil
}

func rootHelp() string {
	var out strings.Builder
	out.WriteString("agentgate — a quality gate around sandboxed model workers\n\n")
	out.WriteString("Start in Claude Code: /dev [-l|-m|-h|-x] [-w] <task>\nStart in Codex:       $dev [-l|-m|-h|-x] [-w] <task>\n\n")
	out.WriteString("Quick start\n  agentgate setup install --lead both\n  agentgate setup doctor\n  agentgate task watch\n\n")
	for _, group := range []string{"Tasks", "Setup", "Design tools", "Lead protocol", "Reference"} {
		out.WriteString(group + "\n")
		for _, spec := range commandSpecs {
			if spec.group == group {
				fmt.Fprintf(&out, "  %-14s %s\n", spec.name, spec.summary)
			}
		}
		out.WriteByte('\n')
	}
	out.WriteString("Use agentgate help <command> for syntax and examples. Every command accepts --help.\n")
	out.WriteString("Groups: task <command>, setup <command>. Existing flat commands remain supported.\n")
	out.WriteString("JSON inputs: --file <path>, --file - (stdin), or --plan <name> for saved plans.\n")
	out.WriteString("JSON results go to stdout; errors go to stderr. Exit 0: success, 1: command error,\n2: failed checks. Design tools document their own exit codes.\n")
	return out.String()
}

// Keep the shared root help as a value for documentation tests and consistent output.
var usage = rootHelp()

func helpFor(parts []string) (string, error) {
	if len(parts) == 0 || parts[0] == "help" {
		return usage, nil
	}
	if parts[0] == "limits" {
		parts = append([]string{"accounts"}, parts[1:]...)
	}
	if commands := groupCommands(parts[0]); commands != nil {
		if len(parts) > 1 {
			if !slices.Contains(commands, parts[1]) {
				return "", fmt.Errorf("unknown %s command %q; run `agentgate help %s`", parts[0], parts[1], parts[0])
			}
			return helpFor(parts[1:])
		}
		var out strings.Builder
		fmt.Fprintf(&out, "agentgate %s <command>\n\n", parts[0])
		for _, name := range commands {
			spec, _ := command(name)
			fmt.Fprintf(&out, "  %-14s %s\n", name, spec.summary)
		}
		fmt.Fprintf(&out, "\nUse agentgate help %s <command> for details. Flat command names also work.\n", parts[0])
		return out.String(), nil
	}
	spec, ok := command(parts[0])
	if !ok {
		return "", fmt.Errorf("unknown command %q; run `agentgate help`", parts[0])
	}
	var out strings.Builder
	fmt.Fprintf(&out, "agentgate %s\n\n%s.\n\nUsage\n  agentgate %s\n", spec.name, spec.summary, spec.syntax)
	if spec.input {
		out.WriteString("\nInput\n")
		if spec.name == "parse" || spec.name == "preview" {
			out.WriteString("  --file <path>  Read request text from a file; use - for stdin.\n")
			out.WriteString("  Request text can also be passed as an argument or read from stdin.\n")
		} else {
			out.WriteString("  --file <path>  Read JSON from a file; use - for stdin.\n")
			out.WriteString("  Exactly one JSON object is accepted; unknown fields and trailing input are errors.\n")
		}
		if spec.plan {
			out.WriteString("  --plan <name>  Read a saved plan instead of --file.\n")
		}
	}
	if spec.example != "" {
		fmt.Fprintf(&out, "\nExample\n  %s\n", spec.example)
	}
	if spec.group == "Lead protocol" {
		out.WriteString("\nThis is a lead protocol command. Run `agentgate reference` for JSON fields.\n")
	}
	out.WriteString("\n-h, --help  Show help without performing the command.\n")
	return out.String(), nil
}
