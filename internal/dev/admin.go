package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/accounts"
	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
)

// Preview describes how a request would be handled. It creates nothing and starts nothing.
func Preview(cfg *config.Config, raw string) (any, error) {
	request, err := ParseRequest(raw)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"request": request, "side_effects": "none"}
	if request.Mode != "" {
		out["mode"], out["routes"] = request.Mode, modeTable(cfg, request.Mode)
	} else {
		out["mode"] = "decided from the plan's assessment: size, named risks, reversibility, uncertainty"
		table := map[string]any{}
		for _, name := range config.ModeNames {
			table[name] = modeTable(cfg, name)
		}
		out["routes"] = table
	}
	return out, nil
}

func modeTable(cfg *config.Config, name string) map[string]any {
	mode := cfg.Modes[name]
	review := []string{}
	for _, lane := range mode.Review {
		review = append(review, strings.Join(lane.Axes, "+")+" → "+lane.Route)
	}
	if len(review) == 0 {
		review = []string{"the lead reads the diff"}
	}
	design := "the lead (its brief is the design)"
	if len(mode.Architecture) > 0 {
		design = strings.Join(mode.Architecture, " ∥ ")
		if mode.Critique {
			design += ", then cross-critique"
		}
	}
	scout := mode.Scout
	if scout == "" {
		scout = "the lead's own context"
	}
	return map[string]any{"scout": scout, "design": design, "backend": mode.Backend, "frontend": mode.Frontend, "review": review,
		"blocking": mode.Blocking, "escalate_to": mode.EscalateTo}
}

// Modes renders the effective mode table for people.
func Modes(cfg *config.Config) string {
	var out strings.Builder
	for _, name := range config.ModeNames {
		table := modeTable(cfg, name)
		fmt.Fprintf(&out, "%s\n  scout     %s\n  architect %s\n  backend   %s\n  frontend  %s\n  review    %s\n  blocking  %s\n  escalate  up to %s when a correction stalls\n",
			name, table["scout"], table["design"], table["backend"], table["frontend"], strings.Join(table["review"].([]string), " ∥ "),
			strings.Join(cfg.Modes[name].Blocking, ", "), table["escalate_to"])
	}
	fmt.Fprintf(&out, "\nmodels\n")
	aliases := make([]string, 0, len(cfg.Models))
	for alias := range cfg.Models {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		model := cfg.Models[alias]
		fmt.Fprintf(&out, "  %-8s %-7s %-22s pool %-7s fallback %s\n", alias, model.Provider, model.Model, model.Pool,
			strings.Join(cfg.Fallbacks[alias], ", "))
	}
	return out.String()
}

// Route shows what a plan would run on, without starting anything.
func Route(cfg *config.Config, plan *Plan) (any, error) {
	resolved, err := Resolve(cfg, plan)
	if err != nil {
		return nil, err
	}
	writers := map[string]string{}
	for _, stream := range plan.Streams {
		writers[stream.Name] = resolved.WriterRoute(stream, 0).String()
	}
	order, _ := waves(plan.Streams)
	var names [][]string
	for _, wave := range order {
		var row []string
		for _, stream := range wave {
			row = append(row, stream.Name)
		}
		names = append(names, row)
	}
	limit, err := resolved.Parallelism()
	if err != nil {
		return nil, err
	}
	table := modeTable(cfg, resolved.Mode)
	return map[string]any{"decision": resolved.Decision, "scout": table["scout"], "design": table["design"], "writers": writers, "waves": names,
		"review": table["review"], "blocking": table["blocking"], "parallel": limit, "read_only": true}, nil
}

type probe struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Required bool   `json:"required"`
	Detail   string `json:"detail"`
}

func claudeDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func version(binary string, args ...string) (string, bool) {
	out, err := exec.Command(binary, args...).CombinedOutput()
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line, err == nil
}

// Doctor checks everything a run depends on. It changes nothing.
func Doctor() map[string]any {
	var probes []probe
	add := func(name string, ok, required bool, detail string) {
		probes = append(probes, probe{name, ok, required, detail})
	}
	cfg, err := config.Load()
	// Which CLIs are needed follows from the configuration: the providers of the models in use, and the
	// sessions the lead command is installed into.
	leads, providers := []string{"claude"}, map[string]bool{}
	if cfg != nil {
		if len(cfg.Leads) > 0 {
			leads = cfg.Leads
		}
		for _, model := range cfg.Models {
			providers[model.Provider] = true
		}
	}
	for _, tool := range [][]string{{"bwrap", "--version"}, {"git", "--version"}, {"diff", "--version"}} {
		detail, ok := version(tool[0], tool[1:]...)
		add(tool[0], ok, true, detail)
	}
	for _, cli := range []string{"claude", "codex"} {
		detail, ok := version(cli, "--version")
		add(cli, ok, providers[cli] || slices.Contains(leads, cli), detail)
	}
	if providers["codex"] {
		_, loggedIn := version("codex", "login", "status")
		add("codex login", loggedIn, true, "codex login status")
	}
	add("config", err == nil, true, fmt.Sprintf("user file %s, project file %s%s", config.UserPath(), config.ProjectFile, suffix(err)))
	if cfg != nil && cfg.Profile != "" {
		add("profile", true, false, cfg.Profile)
	}
	err = os.MkdirAll(Root(), 0o700)
	add("state", err == nil, true, Root()+suffix(err))
	hint := "run `agentgate install` when this is not ok"
	hooked := func(path string) bool {
		raw, _ := os.ReadFile(path)
		return strings.Contains(string(raw), hookMarker) || strings.Contains(string(raw), selfPath()+" hook")
	}
	if slices.Contains(leads, "claude") {
		installed, _ := os.ReadFile(filepath.Join(claudeDir(), "commands", "dev.md"))
		add("/dev command", string(installed) == assets.Command, true, hint)
		add("Stop hook", hooked(filepath.Join(claudeDir(), "settings.json")), true, hint)
	}
	if slices.Contains(leads, "codex") {
		installed, _ := os.ReadFile(filepath.Join(skillDir(), "SKILL.md"))
		add("$dev skill", string(installed) == assets.Skill, true, hint)
		rule, _ := os.ReadFile(codexRulePath())
		add("Codex rule", string(rule) == codexRule(), true, "lets Codex run agentgate outside its sandbox; "+hint)
		add("Codex Stop hook", hooked(filepath.Join(codexDir(), "hooks.json")), false, "trust it once in Codex with /hooks")
	}
	if cfg != nil {
		_, statErr := os.Stat(config.ExpandHome(cfg.PlaywrightMCP))
		add("browser tooling", statErr == nil, false, "optional, for browser verification: "+config.ExpandHome(cfg.PlaywrightMCP))
		home, _ := os.UserHomeDir()
		cache, _ := os.ReadFile(filepath.Join(home, ".codex", "models_cache.json"))
		for alias, model := range cfg.Models {
			if model.Provider == "codex" && len(cache) > 0 {
				add("model "+alias, strings.Contains(string(cache), `"`+model.Model+`"`), false, model.Model+" in the local Codex model list")
			}
		}
	}
	if cfg != nil {
		manager := accounts.New(cfg.Accounts, filepath.Join(Root(), "accounts.json"))
		for _, status := range manager.Statuses(false) {
			ok, why := status.Available(status.Provider, time.Now().Unix())
			if why == "" {
				why = "ready"
			}
			add("account "+status.Name, ok, false, status.Home+": "+why)
		}
	}
	healthy := true
	for _, item := range probes {
		healthy = healthy && (item.OK || !item.Required)
	}
	return map[string]any{"healthy": healthy, "checks": probes}
}

func suffix(err error) string {
	if err != nil {
		return ": " + err.Error()
	}
	return ""
}

const hookMarker = "agentgate hook"

// ourHook recognises a Stop hook of this tool: the current binary under whatever name it runs, any earlier
// install of it, and the hook of the Python tool it replaces.
func ourHook(command string) bool {
	return command == selfPath()+" hook" || strings.Contains(command, hookMarker) || strings.Contains(command, "claude-dev hook") ||
		strings.Contains(command, "claude-dev quality-hook")
}

func selfPath() string {
	if path, err := exec.LookPath("agentgate"); err == nil {
		return path
	}
	path, _ := os.Executable()
	return path
}

func backup(path string) {
	if raw, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(fmt.Sprintf("%s.bak-agentgate-%s", path, time.Now().Format("20060102-150405")), raw, 0o600)
	}
}

func codexDir() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

// skillDir is where Codex looks for the user's skills.
func skillDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agents", "skills", "dev")
}

func codexRulePath() string { return filepath.Join(codexDir(), "rules", "agentgate.rules") }

// codexRule lets a Codex session run this tool outside Codex's own sandbox: the tool starts its own sandbox
// for the workers, which cannot be nested in Codex's. The rule names the command and nothing else.
func codexRule() string {
	rule := "prefix_rule(pattern=[\"agentgate\"], decision=\"allow\")\n"
	if path := selfPath(); path != "" && path != "agentgate" {
		rule += fmt.Sprintf("prefix_rule(pattern=[%q], decision=\"allow\")\n", path)
	}
	return rule
}

// place writes a file the tool owns, keeping a copy of whatever was there under another content.
func place(path, content string, done *[]string) error {
	if existing, _ := os.ReadFile(path); string(existing) == content && !isSymlink(path) {
		return nil
	} else if len(existing) > 0 && !isSymlink(path) {
		backup(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Remove(path) // never write through a symlink into someone else's file
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	*done = append(*done, "installed "+path)
	return nil
}

func drop(path string, done *[]string) {
	if os.Remove(path) == nil {
		*done = append(*done, "removed "+path)
	}
}

// InstallOptions says what to install and for whom.
type InstallOptions struct {
	Uninstall bool
	Zed       bool
	Lead      string        // claude, codex or both; empty: what was installed before, or ask
	Ask       func() string // asks the user which lead to install, when it can
}

// LeadChoices are the answers Install accepts for the lead.
var LeadChoices = map[string][]string{"claude": {"claude"}, "codex": {"codex"}, "both": {"claude", "codex"}}

// Install puts the lead command and the Stop hook into Claude Code, into Codex, or into both. A Codex-only
// install also selects the profile in which every worker is a Codex model. Running it again changes nothing.
func Install(options InstallOptions) (any, error) {
	done := []string{}
	adoptName(&done)
	cfg, _ := config.Load()
	choice := options.Lead
	// Switching the lead removes the other one's install, but only on an explicit choice: a default taken
	// because nobody was asked must never take away what is installed.
	explicit := options.Uninstall || choice != ""
	var leads []string
	switch {
	case options.Uninstall:
		leads = []string{"claude", "codex"}
	case choice != "":
	case cfg != nil && len(cfg.Leads) > 0:
		leads = cfg.Leads
	case options.Ask != nil:
		choice, explicit = options.Ask(), true
	default:
		// Nobody to ask: keep to the CLI that is there.
		choice = "claude"
		if _, err := exec.LookPath("claude"); err != nil {
			if _, err := exec.LookPath("codex"); err == nil {
				choice = "codex"
			}
		}
	}
	if leads == nil {
		chosen, ok := LeadChoices[choice]
		if !ok {
			return nil, fmt.Errorf("--lead is claude, codex or both, not %q", choice)
		}
		leads = chosen
		if err := config.SetValue("leads", leads); err != nil {
			return nil, err
		}
		// "Codex only" means the workers too; picking Claude again leaves that profile.
		profile := ""
		if cfg != nil {
			profile = cfg.Profile
		}
		switch {
		case choice == "codex" && profile == "":
			if err := config.Set("profile", "codex"); err != nil {
				return nil, err
			}
			done = append(done, "selected the codex profile: every worker is a Codex model")
		case choice != "codex" && profile == "codex":
			if err := config.Unset("profile"); err != nil {
				return nil, err
			}
			done = append(done, "left the codex profile: workers use the default table again")
		}
	}
	out := map[string]any{"leads": leads}
	var next []string
	for _, lead := range []string{"claude", "codex"} {
		wanted := slices.Contains(leads, lead) && !options.Uninstall
		var hooks string
		if lead == "claude" {
			command := filepath.Join(claudeDir(), "commands", "dev.md")
			hooks = filepath.Join(claudeDir(), "settings.json")
			if wanted {
				if err := place(command, assets.Command, &done); err != nil {
					return nil, err
				}
				next = append(next, "Claude Code: start a new session and type /dev help.")
			} else if explicit {
				drop(command, &done)
			}
		} else {
			skill, policy := filepath.Join(skillDir(), "SKILL.md"), filepath.Join(skillDir(), "agents", "openai.yaml")
			hooks = filepath.Join(codexDir(), "hooks.json")
			if wanted {
				for path, content := range map[string]string{skill: assets.Skill, policy: assets.SkillPolicy, codexRulePath(): codexRule()} {
					if err := place(path, content, &done); err != nil {
						return nil, err
					}
				}
				next = append(next, "Codex: start a new session and type $dev help. Run /hooks there once and trust the agentgate Stop hook; "+
					"Codex does not run a hook until you have reviewed it.")
			} else if explicit {
				drop(skill, &done)
				drop(policy, &done)
				drop(codexRulePath(), &done)
				_ = os.Remove(filepath.Join(skillDir(), "agents"))
				_ = os.Remove(skillDir())
			}
		}
		if !wanted && !explicit {
			continue
		}
		if _, err := os.Stat(hooks); err != nil && !wanted {
			continue // nothing was ever installed there
		}
		note, err := installHook(hooks, !wanted)
		if err != nil {
			return nil, err
		}
		if note != "" {
			done = append(done, note)
		}
	}
	sort.Strings(done)
	out["changed"] = done
	if options.Zed && !options.Uninstall {
		out["zed"] = installZed()
	}
	if len(done) == 0 {
		out["note"] = "already up to date"
	}
	if !options.Uninstall {
		out["next"] = strings.Join(append(next, "`agentgate doctor` verifies the setup."), " ")
	}
	return out, nil
}

// adoptName gives a binary that still runs under the tool's earlier name its present one, next to itself.
func adoptName(done *[]string) {
	self, err := os.Executable()
	if err != nil || filepath.Base(self) != "claude-dev" {
		return
	}
	target := filepath.Join(filepath.Dir(self), "agentgate")
	if _, err := os.Stat(target); err == nil {
		return
	}
	raw, err := os.ReadFile(self)
	if err != nil || os.WriteFile(target, raw, 0o755) != nil {
		return
	}
	*done = append(*done, "the tool is now called agentgate: installed "+target+"; `claude-dev` can be removed")
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func installHook(path string, uninstall bool) (string, error) {
	settings := map[string]any{}
	raw, err := os.ReadFile(path)
	if err == nil && json.Unmarshal(raw, &settings) != nil {
		return "", fmt.Errorf("%s is not plain JSON; add the Stop hook `%s hook` by hand", path, selfPath())
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	var kept []any
	present := false
	wanted := selfPath() + " hook"
	groups, _ := hooks["Stop"].([]any)
	for _, group := range groups {
		entry, _ := group.(map[string]any)
		var inner []any
		list, _ := entry["hooks"].([]any)
		for _, item := range list {
			command, _ := item.(map[string]any)["command"].(string)
			ours := ourHook(command)
			if ours && command == wanted && !uninstall && !present {
				present = true
				inner = append(inner, item)
			} else if !ours {
				inner = append(inner, item)
			}
		}
		if len(inner) > 0 {
			entry["hooks"] = inner
			kept = append(kept, entry)
		}
	}
	if !uninstall && !present {
		kept = append(kept, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": wanted, "timeout": 5}}})
	}
	if len(kept) > 0 {
		hooks["Stop"] = kept
	} else {
		delete(hooks, "Stop")
	}
	settings["hooks"] = hooks
	updated, _ := json.MarshalIndent(settings, "", "  ")
	var before, after any
	_ = json.Unmarshal(raw, &before)
	_ = json.Unmarshal(updated, &after)
	if digest(before) == digest(after) {
		return "", nil
	}
	backup(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(updated, '\n'), 0o600); err != nil {
		return "", err
	}
	if uninstall {
		return "removed the Stop hook from " + path, nil
	}
	return "added the Stop hook to " + path, nil
}

var zedTasks = []map[string]any{
	{"label": "agentgate: watch", "command": "agentgate watch", "use_new_terminal": false, "allow_concurrent_runs": false, "reveal": "always"},
	{"label": "agentgate: status", "command": "agentgate status", "use_new_terminal": false, "reveal": "always"},
	{"label": "agentgate: stop", "command": "agentgate stop", "use_new_terminal": false, "reveal": "always"},
}

// installZed adds tasks to Zed's global tasks file. A file with comments is left alone: rewriting it would lose them.
func installZed() map[string]any {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	path := filepath.Join(base, "zed", "tasks.json")
	keymap := `{"context": "Workspace", "bindings": {"ctrl-alt-d": ["task::Spawn", {"task_name": "agentgate: watch"}]}}`
	var tasks []any
	raw, err := os.ReadFile(path)
	if err == nil && json.Unmarshal(raw, &tasks) != nil {
		snippet, _ := json.MarshalIndent(zedTasks, "", "  ")
		side := filepath.Join(base, "zed", "agentgate.tasks.json")
		_ = os.WriteFile(side, append(snippet, '\n'), 0o600)
		return map[string]any{"changed": false, "reason": path + " has comments or is not plain JSON; it was not touched",
			"paste_from": side, "optional_keymap_entry": keymap}
	}
	added := 0
	for _, task := range zedTasks {
		exists := false
		for _, existing := range tasks {
			if entry, _ := existing.(map[string]any); entry["label"] == task["label"] {
				exists = true
			}
		}
		if !exists {
			tasks = append(tasks, task)
			added++
		}
	}
	if added > 0 {
		backup(path)
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		updated, _ := json.MarshalIndent(tasks, "", "  ")
		if err := os.WriteFile(path, append(updated, '\n'), 0o600); err != nil {
			return map[string]any{"changed": false, "reason": err.Error()}
		}
	}
	return map[string]any{"changed": added > 0, "tasks_file": path, "run": "command palette → task: spawn → agentgate: watch",
		"optional_keymap_entry": keymap}
}

// Overview lists the gates of this checkout without touching any of them.
func Overview() map[string]any {
	var gates []map[string]any
	paths, _ := filepath.Glob(filepath.Join(checkoutDir(""), "*", "state.json"))
	for _, path := range paths {
		raw, _ := os.ReadFile(path)
		gate := &Gate{}
		if json.Unmarshal(raw, gate) == nil {
			live := false
			if record := ops.Status(filepath.Dir(path)); record != nil {
				live = record.Live
			}
			item := map[string]any{"session": gate.Session, "id": gate.ID, "mode": gate.Mode, "phase": gate.Phase,
				"task": gate.Request.Task, "operation_live": live}
			if gate.Worktree != "" {
				item["isolated"] = gate.landing()
			}
			gates = append(gates, item)
		}
	}
	return map[string]any{"gates": gates, "read_only": true,
		"note": "Recorded state only. The owning lead session drives the task."}
}

// StopFromOutside lets the user stop the single live operation of this checkout from any terminal.
func StopFromOutside() (any, error) {
	var live []string
	paths, _ := filepath.Glob(filepath.Join(checkoutDir(""), "*", "operation.json"))
	for _, path := range paths {
		if record := ops.Status(filepath.Dir(path)); record != nil && record.Live {
			live = append(live, filepath.Dir(path))
		}
	}
	switch len(live) {
	case 0:
		return map[string]any{"stopped": false, "note": "no operation is running in this checkout"}, nil
	case 1:
		ops.RequestStop(live[0], "the user stopped /dev")
		return map[string]any{"cancellation_requested": true, "next": "The task will be put on hold; `agentgate watch` shows it."}, nil
	}
	return nil, errors.New("several sessions are running here; stop from the owning lead session")
}
