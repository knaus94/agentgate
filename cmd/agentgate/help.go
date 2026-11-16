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
	{name: "status", group: "Tasks", summary: "Where the task is, what it needs and which actions are legal; the checkout's overview with --all", syntax: "status [--full] [--all]", flags: []string{"all", "full"}, example: "agentgate task status"},
	{name: "stop", group: "Tasks", summary: "Stop workers in this session, or this checkout from outside", syntax: "stop", example: "agentgate task stop"},
	{name: "plan", group: "Tasks", summary: "Save, list, inspect and share reusable plans", syntax: "plan save [name] --file <path>|- | list | show <name> | link [directory]", input: true, plan: true, example: "agentgate plan save wallet --file plan.json"},
	{name: "install", group: "Setup", summary: "Install lead commands and editor integration", syntax: "install [--lead claude|codex|both] [--zed] [--uninstall]", flags: []string{"lead", "zed", "uninstall"}, example: "agentgate setup install --lead both"},
	{name: "doctor", group: "Setup", summary: "Check required executables, sandbox and configuration", syntax: "doctor", example: "agentgate setup doctor"},
	{name: "config", group: "Setup", summary: "Read, validate and edit layered configuration", syntax: "config [show|path|init|validate|set <key> <value>|unset <key>|extensions|profiles|profile <name>]", example: "agentgate config set modes.medium.backend sol:high"},
	{name: "accounts", group: "Setup", summary: "Manage CLI profiles, subscriptions and remaining limits", syntax: "accounts [list [--json]|discover [--save]|add <name> <claude|codex> <home> [--paid] [--lead-only]|remove <name>|reserve <name>|unreserve <name>|reset <name>]", flags: []string{"json", "save", "paid", "lead-only"}, example: "agentgate accounts discover --save"},
	{name: "modes", group: "Setup", summary: "Show the effective model and effort table", syntax: "modes", example: "agentgate modes"},
	{name: "update", group: "Setup", summary: "Check for or install a release", syntax: "update [--check]", flags: []string{"check"}, example: "agentgate setup update --check"},
	{name: "prune", group: "Setup", summary: "Remove expired inactive task state", syntax: "prune", example: "agentgate prune"},
	{name: "figma", group: "Design tools", summary: "Sign in, inspect Figma frames and export design packages", syntax: "figma login|status|frames|pull|node|import-cookies", example: "agentgate figma --help"},
	{name: "layout", group: "Design tools", summary: "Compare a local page with an exported design", syntax: "layout <screen> | <package> --url <page>", example: "agentgate layout --help"},
	{name: "fixtures", group: "Design tools", summary: "Serve or record GraphQL fixtures for a screen", syntax: "fixtures <dir> [--port 5999] [--record <url>]", example: "agentgate fixtures --help"},
	{name: "parse", group: "Lead protocol", summary: "Parse a /dev request without starting work", syntax: "parse [request] [--file <path>|-]", input: true, example: "agentgate parse -- '-h move invoice totals to integer cents'"},
	{name: "preview", group: "Lead protocol", summary: "Preview routing and work without starting workers", syntax: "preview [request] [--file <path>|-]", input: true, example: "agentgate preview -- '-m add pagination'"},
	{name: "prepare", group: "Lead protocol", summary: "Draft or revise a plan with a read-only worker; the same worker continues with answers and notes", syntax: "prepare [--file <path>|-]", input: true},
	{name: "route", group: "Lead protocol", summary: "Validate a plan and resolve model routing", syntax: "route --file <path>|- | --plan <name>", input: true, plan: true},
	{name: "architecture", group: "Lead protocol", summary: "Run independent architecture proposals and critiques", syntax: "architecture --file <path>|- | --plan <name>", input: true, plan: true},
	{name: "design", group: "Lead protocol", summary: "Prepare or revise an interface specification", syntax: "design --file <path>|-", input: true},
	{name: "consult", group: "Lead protocol", summary: "Ask read-only consultants scoped questions", syntax: "consult --file <path>|-", input: true},
	{name: "start", group: "Lead protocol", summary: "Open a task from a plan and run it", syntax: "start --plan <name> | --file <path>|- [--implement]", input: true, plan: true, flags: []string{"implement"}, example: "agentgate start --plan wallet"},
	{name: "run", group: "Lead protocol", summary: "Take every determined step — write, check, review, correct, accept — until a person is needed", syntax: "run [--file <path>|-]", input: true, example: "agentgate run"},
	{name: "answer", group: "Lead protocol", summary: "Answer a blocked writer in its own session and run on", syntax: "answer --file <path>|-", input: true},
	{name: "decide", group: "Lead protocol", summary: "Rule on findings, give the lead's own review, give work to writers; then run on", syntax: "decide --file <path>|-", input: true},
	{name: "amend", group: "Lead protocol", summary: "Change the plan of the active task: named operations or the complete plan, with a reason", syntax: "amend --file <path>|- | --plan <name>", input: true, plan: true},
	{name: "show", group: "Lead protocol", summary: "Print one part of the task in full", syntax: "show [findings|plan|stage|checks|review|architecture|receipt|report|all]", example: "agentgate show review"},
	{name: "tell", group: "Lead protocol", summary: "Leave a note on the writers' shared board", syntax: "tell --file <path>|-", input: true},
	{name: "check", group: "Lead protocol", summary: "Run declared verification checks", syntax: "check --all | --file <path>|-", input: true, flags: []string{"all"}},
	{name: "visual", group: "Lead protocol", summary: "Record the browser choice or a skip, or run a browser pass", syntax: "visual --file <path>|-", input: true},
	{name: "land", group: "Lead protocol", summary: "Apply or drop a completed isolated worktree", syntax: "land [--file <path>|-]", input: true},
	{name: "pause", group: "Lead protocol", summary: "Park the task with the true reason", syntax: "pause --file <path>|-", input: true},
	{name: "resume", group: "Lead protocol", summary: "Lift a pause", syntax: "resume"},
	{name: "drop", group: "Lead protocol", summary: "End the task and take back what it changed, on the user's word", syntax: "drop --file <path>|-", input: true},
	{name: "forget", group: "Lead protocol", summary: "Remove a finished or dropped task from the record, on the user's word", syntax: "forget --file <path>|-", input: true},
	{name: "guide", group: "Reference", summary: "Print the lead's workflow guide", syntax: "guide"},
	{name: "reference", group: "Reference", summary: "Print the protocol: commands and inputs, or one page", syntax: "reference [page]", example: "agentgate reference needs"},
	{name: "hook", group: "Reference", summary: "Read a Claude Stop-hook payload from stdin", syntax: "hook"},
	{name: "version", group: "Reference", summary: "Print the installed version", syntax: "version"},
}

var taskCommands = []string{"status", "show", "watch", "stop", "parse", "preview", "prepare", "route", "architecture", "design", "consult", "start", "run", "answer", "decide", "amend", "tell", "check", "visual", "land", "pause", "resume", "drop", "forget"}
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
	out.WriteString("JSON results go to stdout. A command about the task answers with the task as it is, also when it\nis refused (`refused`) or fails (`error`); other errors go to stderr. Exit 0: success, 1: a command\nrefused or failed, 2: failed checks. Design tools document their own exit codes.\n")
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
		} else if spec.name == "prepare" {
			out.WriteString("  --file <path>  Read the input from a file; use - for stdin.\n")
			out.WriteString("  The first call takes the user's request as it was typed. Later calls take one JSON\n")
			out.WriteString("  object with answers or notes; unknown fields and trailing input are errors.\n")
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
		out.WriteString("\nThis is a lead protocol command. Run `agentgate reference` for its JSON fields.\n")
	}
	out.WriteString("\n-h, --help  Show help without performing the command.\n")
	return out.String(), nil
}
