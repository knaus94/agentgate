package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/knaus94/agentgate/internal/accounts"
	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/dev"
	"github.com/knaus94/agentgate/internal/provider"
	"github.com/knaus94/agentgate/internal/update"
)

func gated(cfg *config.Config, action string, run func(gate *dev.Gate) (any, error)) (any, error) {
	return dev.Execute(action, func() (any, error) {
		gate, err := dev.LoadCurrent()
		if err != nil {
			return nil, err
		}
		if gate == nil {
			return nil, errors.New("there is no /dev task for this session in this checkout")
		}
		return run(gate)
	})
}

// sandboxSafe are the actions that only read or print: they work inside a caller's sandbox too.
var sandboxSafe = []string{"", "help", "--help", "-h", "version", "guide", "reference", "parse", "preview", "route", "modes", "status", "watch", "hook"}

func run(args arguments) (any, int, error) {
	// Help and version are pure: even an invalid configuration or an old state
	// directory must not make them mutate files or fail to explain the command.
	if args.action == "" || args.action == "help" || args.action == "-h" || args.action == "--help" {
		text, err := helpFor(args.help)
		return text, 0, err
	}
	if args.action == "version" {
		return update.Version + "\n", 0, nil
	}
	if args.action == "config" && (len(args.rest) == 0 || slices.Contains([]string{"show", "validate", "path", "extensions", "profiles"}, args.rest[0])) {
		return configCommand(args.rest)
	}
	if config.Lead() == "codex" && !slices.Contains(sandboxSafe, args.action) && dev.Confined() {
		return nil, 0, errors.New("running inside the Codex sandbox, where agentgate cannot start its own sandbox, reach a provider or " +
			"write its state. Call it as one plain command — `agentgate " + args.action + " --file <path>` — with no pipe, heredoc, `&&`, `;` or " +
			"redirection, so the rule installed by `agentgate install --lead codex` applies; write the input to a file first")
	}
	dev.AdoptLegacy()
	switch args.action {
	case "", "help", "--help", "-h":
		return usage, 0, nil
	case "version":
		return update.Version + "\n", 0, nil
	case "guide":
		if config.Lead() == "codex" {
			return assets.Guide + assets.GuideCodex, 0, nil
		}
		return assets.Guide, 0, nil
	case "reference":
		return assets.Reference, 0, nil
	case "doctor":
		result := dev.Doctor()
		if result["healthy"] != true {
			return result, 1, nil
		}
		return result, 0, nil
	case "install":
		value, err := dev.Install(dev.InstallOptions{Uninstall: args.flags["uninstall"], Zed: args.flags["zed"], Lead: args.lead, Ask: askLead()})
		return value, 0, err
	case "update":
		return selfUpdate(args.flags["check"])
	case "hook":
		var payload struct {
			Cwd       string `json:"cwd"`
			SessionID string `json:"session_id"`
		}
		raw, _ := io.ReadAll(os.Stdin)
		if json.Unmarshal(raw, &payload) != nil {
			return map[string]any{}, 0, nil
		}
		return dev.StopHook(payload.Cwd, payload.SessionID), 0, nil
	case "parse":
		text, err := args.raw()
		if err != nil {
			return nil, 0, err
		}
		request, err := dev.ParseRequest(text)
		if err == nil && strings.Contains(text, "figma.com/") {
			// The lead meets the link here first: it is told at once what the link is for.
			out := map[string]any{}
			raw, _ := json.Marshal(request)
			_ = json.Unmarshal(raw, &out)
			out["design"] = "The request holds a Figma link. Use `agentgate prepare --file <request JSON>`: the tool reads its catalogue and a read-only worker prepares the plan. " +
				"Do not explore the repository, export frames, parse design data or inspect pictures as the lead."
			return out, 0, nil
		}
		return request, 0, err
	}

	if args.action == "config" {
		return configCommand(args.rest)
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, 0, err
	}
	provider.Accounts = accounts.New(cfg.Accounts, filepath.Join(dev.Root(), "accounts.json"))
	if args.action == "accounts" || args.action == "limits" {
		value, err := accounts.Command(cfg, provider.Accounts, args.rest, args.flags)
		return value, 0, err
	}
	loadPlan := func() (*dev.Plan, error) {
		plan := &dev.Plan{}
		return plan, args.input(plan, true)
	}
	switch args.action {
	case "prepare":
		input := dev.PrepareInput{}
		if err := args.input(&input, true); err != nil {
			return nil, 0, err
		}
		value, err := dev.Execute("prepare", func() (any, error) { return dev.Prepare(cfg, input) })
		return value, 0, err
	case "replan":
		plan, err := loadPlan()
		if err != nil {
			return nil, 0, err
		}
		value, err := gated(cfg, "replan", func(gate *dev.Gate) (any, error) { return dev.Replan(cfg, gate, plan) })
		return value, 0, err
	case "plan":
		return planCommand(cfg, args)
	case "modes":
		return dev.Modes(cfg), 0, nil
	case "preview":
		text, err := args.raw()
		if err != nil {
			return nil, 0, err
		}
		value, err := dev.Preview(cfg, text)
		return value, 0, err
	case "route":
		plan, err := loadPlan()
		if err != nil {
			return nil, 0, err
		}
		value, err := dev.Route(cfg, plan)
		return value, 0, err
	case "watch":
		if args.flags["json"] {
			return dev.WatchSnapshot(cfg, args.flags["all"]), 0, nil
		}
		dev.WatchWithOptions(cfg, args.flags["once"], args.flags["all"], args.flags["no-color"])
		return nil, 0, nil
	case "prune":
		return map[string]any{"removed": dev.Prune(cfg.RetentionDays)}, 0, nil
	case "status":
		gate, err := dev.LoadCurrent()
		if err != nil && !args.flags["all"] {
			return nil, 0, err
		}
		if gate != nil && !args.flags["all"] {
			var summary map[string]any
			gate.Inside(func() { summary = gate.Summary(cfg) })
			return summary, 0, nil
		}
		return dev.Overview(), 0, nil
	case "stop":
		if dev.HasSession() {
			value, err := dev.Stop(cfg)
			return value, 0, err
		}
		value, err := dev.StopFromOutside()
		return value, 0, err
	case "tell":
		input := dev.TellInput{}
		if err := args.input(&input, true); err != nil {
			return nil, 0, err
		}
		gate, err := dev.LoadCurrent()
		if err != nil {
			return nil, 0, err
		}
		if gate == nil {
			return nil, 0, errors.New("there is no /dev task for this session in this checkout")
		}
		value, err := dev.Tell(gate, input)
		return value, 0, err
	case "architecture":
		plan, err := loadPlan()
		if err != nil {
			return nil, 0, err
		}
		value, err := dev.Execute("architecture", func() (any, error) { return dev.Architecture(cfg, plan) })
		return value, 0, err
	case "launch":
		plan, err := loadPlan()
		if err != nil {
			return nil, 0, err
		}
		value, err := dev.Execute("launch", func() (any, error) { return dev.Launch(cfg, plan) })
		return value, 0, err
	case "design":
		input := dev.DesignInput{}
		if err := args.input(&input, true); err != nil {
			return nil, 0, err
		}
		value, err := dev.Execute("design", func() (any, error) { return dev.Design(cfg, input) })
		return value, 0, err
	case "consult":
		input := dev.ConsultInput{}
		if err := args.input(&input, true); err != nil {
			return nil, 0, err
		}
		value, err := dev.Execute("consult", func() (any, error) { return dev.Consult(cfg, input) })
		return value, 0, err
	case "answer":
		input := dev.AnswerInput{}
		if err := args.input(&input, true); err != nil {
			return nil, 0, err
		}
		value, err := gated(cfg, "answer", func(gate *dev.Gate) (any, error) { return dev.Answer(cfg, gate, input) })
		return value, 0, err
	case "check":
		input := dev.CheckInput{All: args.flags["all"]}
		if err := args.input(&input, !input.All); err != nil {
			return nil, 0, err
		}
		code := 0
		value, err := gated(cfg, "check", func(gate *dev.Gate) (any, error) {
			result, err := dev.RunChecks(gate, input)
			if err == nil && result.Failed {
				code = 2
			}
			return result, err
		})
		return value, code, err
	case "review":
		input := dev.ReviewInput{}
		if err := args.input(&input, false); err != nil {
			return nil, 0, err
		}
		value, err := gated(cfg, "review", func(gate *dev.Gate) (any, error) { return dev.ReviewTask(cfg, gate, input) })
		return value, 0, err
	case "visual":
		input := dev.VisualInput{}
		if err := args.input(&input, true); err != nil {
			return nil, 0, err
		}
		value, err := gated(cfg, "visual", func(gate *dev.Gate) (any, error) { return dev.VisualPass(cfg, gate, input) })
		return value, 0, err
	case "triage":
		input := dev.TriageInput{}
		if err := args.input(&input, true); err != nil {
			return nil, 0, err
		}
		value, err := gated(cfg, "triage", func(gate *dev.Gate) (any, error) { return dev.Triage(cfg, gate, input) })
		return value, 0, err
	case "fix":
		input := dev.FixInput{}
		if err := args.input(&input, false); err != nil {
			return nil, 0, err
		}
		value, err := gated(cfg, "fix", func(gate *dev.Gate) (any, error) { return dev.Fix(cfg, gate, input) })
		return value, 0, err
	case "land":
		input := dev.LandInput{}
		if err := args.input(&input, false); err != nil {
			return nil, 0, err
		}
		value, err := gated(cfg, "land", func(gate *dev.Gate) (any, error) { return dev.Land(cfg, gate, input) })
		return value, 0, err
	case "finish":
		input := dev.FinishInput{}
		if err := args.input(&input, true); err != nil {
			return nil, 0, err
		}
		value, err := gated(cfg, "finish", func(gate *dev.Gate) (any, error) { return dev.Finish(cfg, gate, input) })
		return value, 0, err
	case "hold":
		input := dev.HoldInput{}
		if err := args.input(&input, true); err != nil {
			return nil, 0, err
		}
		value, err := gated(cfg, "hold", func(gate *dev.Gate) (any, error) { return dev.Hold(cfg, gate, input) })
		return value, 0, err
	case "resume":
		value, err := gated(cfg, "resume", func(gate *dev.Gate) (any, error) { return dev.Resume(cfg, gate) })
		return value, 0, err
	case "continue":
		value, err := gated(cfg, "continue", func(gate *dev.Gate) (any, error) { return dev.Continue(cfg, gate) })
		return value, 0, err
	}
	return nil, 0, fmt.Errorf("unknown command %q; run `agentgate help`", args.action)
}

func planCommand(cfg *config.Config, args arguments) (any, int, error) {
	action, name := "list", ""
	if len(args.rest) > 0 {
		action = args.rest[0]
	}
	if len(args.rest) > 1 {
		name = args.rest[1]
	}
	switch action {
	case "save":
		plan := &dev.Plan{}
		if err := args.input(plan, true); err != nil {
			return nil, 0, err
		}
		value, err := dev.PlanSave(cfg, plan, name)
		return value, 0, err
	case "list":
		value, err := dev.PlanList()
		return value, 0, err
	case "show":
		value, err := dev.PlanShow(name)
		return value, 0, err
	case "link":
		value, err := dev.PlanLink(cfg, name)
		return value, 0, err
	}
	return nil, 0, errors.New("usage: agentgate plan save [name] --file <path>|- | plan list | plan show <name> | plan link [synced directory]")
}

func configCommand(rest []string) (any, int, error) {
	if len(rest) == 0 {
		rest = []string{"show"}
	}
	switch rest[0] {
	case "path":
		return config.UserPath() + "\n", 0, nil
	case "init":
		path, err := config.Init()
		return map[string]any{"created": path, "next": "Edit models and fallbacks there, or use `agentgate config set`."}, 0, err
	case "set":
		if len(rest) != 3 {
			return nil, 0, errors.New("usage: agentgate config set <key> <value>, e.g. config set modes.medium.backend sonnet:high")
		}
		if err := config.Set(rest[1], rest[2]); err != nil {
			return nil, 0, err
		}
	case "unset":
		if len(rest) != 2 {
			return nil, 0, errors.New("usage: agentgate config unset <key>")
		}
		if err := config.Unset(rest[1]); err != nil {
			return nil, 0, err
		}
	case "profiles":
		cfg, _ := config.Load()
		active := ""
		if cfg != nil {
			active = cfg.Profile
		}
		return map[string]any{"active": active, "available": config.Profiles(), "your_own": config.ProfileDir(),
			"note": "A profile is a complete mode table laid under your settings: `agentgate config profile <name>` selects one, `config profile default` returns to the built-in table."}, 0, nil
	case "profile":
		if len(rest) != 2 {
			return nil, 0, errors.New("usage: agentgate config profile <name>|default")
		}
		if rest[1] == "default" {
			if err := config.Unset("profile"); err != nil {
				return nil, 0, err
			}
		} else if err := config.Set("profile", rest[1]); err != nil {
			return nil, 0, err
		}
	case "extensions":
		info := map[string]any{"directories": config.ExtensionDirs(), "loaded": config.Extensions(),
			"note": "Each level loads extensions before its explicit config: user, repository, then personal checkout config last. Extension lists add items; explicit config lists replace them. rule_sources lists definitions in precedence order."}
		cfg, err := config.Load()
		if err != nil {
			info["configuration_error"] = err.Error()
		} else {
			info["review_rules"], info["rule_sources"] = cfg.ReviewRules, cfg.ReviewRuleSources
		}
		return info, 0, nil
	case "validate":
		if _, err := config.Load(); err != nil {
			return nil, 0, err
		}
		return map[string]any{"valid": true, "configuration": config.UserPath()}, 0, nil
	case "show":
	default:
		return nil, 0, errors.New("usage: agentgate config show|path|init|validate|set <key> <value>|unset <key>|extensions|profiles|profile <name>")
	}
	cfg, err := config.Load()
	return cfg, 0, err
}

// askLead asks which lead to install when a person is at the terminal; otherwise there is nobody to ask.
func askLead() func() string {
	in, _ := os.Stdin.Stat()
	out, _ := os.Stderr.Stat()
	if in == nil || out == nil || in.Mode()&os.ModeCharDevice == 0 || out.Mode()&os.ModeCharDevice == 0 {
		return nil
	}
	return func() string {
		fmt.Fprint(os.Stderr, `Which sessions lead the work?
  1) Claude Code leads; Claude and Codex models work   (default)
  2) Codex only: Codex leads, Codex models work
  3) both: /dev in Claude Code and $dev in Codex, Claude and Codex models work
Choose 1, 2 or 3: `)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		return installLeadChoice(answer)
	}
}

func installLeadChoice(answer string) string {
	answer = strings.ToLower(strings.TrimSpace(answer))
	switch answer {
	case "", "1", "claude":
		return "claude"
	case "2", "codex":
		return "codex"
	case "3", "both":
		return "both"
	default:
		return answer // Install reports an invalid choice instead of silently picking a lead.
	}
}

func selfUpdate(checkOnly bool) (any, int, error) {
	tag, err := update.Latest()
	if err != nil {
		return nil, 0, err
	}
	if !update.Available(tag) {
		return map[string]any{"version": update.Version, "latest": tag, "up_to_date": true}, 0, nil
	}
	if checkOnly {
		return map[string]any{"version": update.Version, "latest": tag, "up_to_date": false}, 0, nil
	}
	path, err := update.Apply(tag)
	if err != nil {
		return nil, 0, err
	}
	// The new binary refreshes the /dev command it ships with.
	refreshed, err := exec.Command(path, "install").CombinedOutput()
	if err != nil {
		return nil, 0, fmt.Errorf("binary updated to %s, but lead integration refresh failed: %w: %s; run `agentgate install` to retry", tag, err, strings.TrimSpace(string(refreshed)))
	}
	return map[string]any{"updated_from": update.Version, "to": tag, "binary": path, "install": strings.TrimSpace(string(refreshed))}, 0, nil
}
