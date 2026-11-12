// agentgate is a quality gate around sandboxed model workers, led from a Claude Code or a Codex session.
package main

import (
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

const usage = `agentgate — a quality gate around sandboxed model workers, led from Claude Code or Codex

In a session:     /dev [-l|-m|-h|-x] [-w] <task>   in Claude Code, $dev ... in Codex
                  (the mode is chosen automatically without a flag; -w works in an isolated worktree
                  until you land it)

Setup
    install [--lead claude|codex|both] [--zed] [--uninstall]
                                  put the lead command and the Stop hook into Claude Code (/dev), Codex ($dev)
                                  or both; "codex" also makes every worker a Codex model
  doctor                          check the environment
  update [--check]                install the latest release
  modes                           show which model does what in each mode
        config show|path|init|set <key> <value>|unset <key>|extensions|profiles|profile <name>
  accounts [discover [--save] | add <name> <claude|codex> <home> | remove <name> | reset <name>]
                                  several subscriptions: limits, load spreading, failover
      plan save [name] | list | show <name> | link [synced directory]
                                  keep a plan and its readable brief in .agentgate/plans/;
                                  link moves .agentgate into a synced folder
    watch [--all] [--once]          interactive panel: tasks, stages and timings, workers, checks, findings,
                                  and what each worker is doing; --all shows every checkout
  status | stop | prune | version

For the lead (used by /dev)
  guide | reference | parse | preview | route | architecture | design | consult | launch | answer | tell
    check | review | visual | triage | fix | finish | land | hold | resume | hook

Inputs are JSON from --file <path>, --file - (stdin) or --plan <name> (a saved plan).
`

type arguments struct {
	action string
	file   string
	flags  map[string]bool
	rest   []string
	lead   string
}

func parseArgs(args []string) (arguments, error) {
	out := arguments{flags: map[string]bool{}}
	for index := 0; index < len(args); index++ {
		switch value := args[index]; {
		case value == "--file":
			if index+1 >= len(args) {
				return out, errors.New("--file needs a path or -")
			}
			index++
			out.file = args[index]
		case value == "--lead":
			if index+1 >= len(args) {
				return out, errors.New("--lead needs claude, codex or both")
			}
			index++
			out.lead = args[index]
		case value == "--plan":
			if index+1 >= len(args) {
				return out, errors.New("--plan needs the name of a saved plan")
			}
			index++
			path, err := dev.PlanFile(args[index])
			if err != nil {
				return out, err
			}
			out.file = path
		case strings.HasPrefix(value, "--") && len(value) > 2:
			out.flags[value[2:]] = true
		case out.action == "":
			out.action = value
		default:
			out.rest = append(out.rest, value)
		}
	}
	return out, nil
}

func (a arguments) read() ([]byte, error) {
	switch a.file {
	case "":
		return nil, nil
	case "-":
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(a.file)
}

// input decodes the JSON input strictly, so a misspelled field is an error instead of a silent default.
func (a arguments) input(into any, required bool) error {
	raw, err := a.read()
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		if required {
			return fmt.Errorf("%s needs --file <path> or --file -", a.action)
		}
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("%s input: %w", a.action, err)
	}
	return nil
}

func (a arguments) raw() (string, error) {
	if a.file == "" {
		a.file = "-"
	}
	raw, err := a.read()
	return string(raw), err
}

func emit(value any) {
	if text, ok := value.(string); ok {
		fmt.Print(text)
		return
	}
	raw, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(raw))
}

func gated(cfg *config.Config, action string, run func(gate *dev.Gate) (any, error)) (any, error) {
	return dev.Execute(action, func() (any, error) {
		gate := dev.Current()
		if gate == nil {
			return nil, errors.New("there is no /dev task for this session in this checkout")
		}
		return run(gate)
	})
}

// sandboxSafe are the actions that only read or print: they work inside a caller's sandbox too.
var sandboxSafe = []string{"", "help", "--help", "-h", "version", "guide", "reference", "parse", "preview", "route", "modes", "status", "watch", "hook"}

func run(args arguments) (any, int, error) {
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
		dev.Watch(cfg, args.flags["once"], args.flags["all"])
		return nil, 0, nil
	case "prune":
		return map[string]any{"removed": dev.Prune(cfg.RetentionDays)}, 0, nil
	case "status":
		if gate := dev.Current(); gate != nil {
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
		gate := dev.Current()
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
		if err := args.input(&input, true); err != nil {
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
		return map[string]any{"directories": config.ExtensionDirs(), "loaded": config.Extensions(),
			"note": "One *.json file per integration. Objects merge into the configuration; lists add their items. Remove or rename a file to switch it off."}, 0, nil
	case "show":
	default:
		return nil, 0, errors.New("usage: agentgate config show|path|init|set <key> <value>|unset <key>|extensions|profiles|profile <name>")
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
		var answer string
		_, _ = fmt.Fscanln(os.Stdin, &answer)
		return map[string]string{"2": "codex", "codex": "codex", "3": "both", "both": "both"}[strings.TrimSpace(answer)]
	}
}

func selfUpdate(checkOnly bool) (any, int, error) {
	tag, err := update.Latest()
	if err != nil {
		return nil, 0, err
	}
	if update.Current(tag) {
		return map[string]any{"version": update.Version, "up_to_date": true}, 0, nil
	}
	if checkOnly {
		return map[string]any{"version": update.Version, "latest": tag, "up_to_date": false}, 0, nil
	}
	path, err := update.Apply(tag)
	if err != nil {
		return nil, 0, err
	}
	// The new binary refreshes the /dev command it ships with.
	refreshed, _ := exec.Command(path, "install").CombinedOutput()
	return map[string]any{"updated_from": update.Version, "to": tag, "binary": path, "install": strings.TrimSpace(string(refreshed))}, 0, nil
}

func main() {
	args, err := parseArgs(os.Args[1:])
	if err == nil {
		var value any
		var code int
		if value, code, err = run(args); err == nil {
			if value != nil {
				emit(value)
			}
			os.Exit(code)
		}
	}
	fmt.Fprintln(os.Stderr, "agentgate: "+err.Error())
	os.Exit(1)
}
