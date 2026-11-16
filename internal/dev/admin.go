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
		review = []string{"the lead reads the patch"}
	}
	design := "the plan (its brief is the design)"
	if len(mode.Architecture) > 0 {
		design = strings.Join(mode.Architecture, " ∥ ")
		if mode.Critique {
			design += ", then cross-critique"
		}
	}
	// The discovery route prepares the plan and draws the map of the code its workers start from.
	scout := mode.Scout
	if scout == "" {
		scout = cfg.Modes["medium"].Scout + " (as in medium)"
	}
	return map[string]any{"scout": scout, "design": design, "backend": mode.Backend, "frontend": mode.Frontend, "review": review,
		"blocking": mode.Blocking, "escalate_to": mode.EscalateTo, "ui_designer": cfg.UIDesignerFor(name)}
}

// Modes renders the effective mode table for people.
func Modes(cfg *config.Config) string {
	var out strings.Builder
	for _, name := range config.ModeNames {
		table := modeTable(cfg, name)
		fmt.Fprintf(&out, "%s\n  planning  %s\n  architect %s\n  backend   %s\n  frontend  %s\n  review    %s\n  ui design %s\n  blocking  %s\n  escalate  up to %s when a correction stalls\n",
			name, table["scout"], table["design"], table["backend"], table["frontend"], strings.Join(table["review"].([]string), " ∥ "),
			table["ui_designer"], strings.Join(cfg.Modes[name].Blocking, ", "), table["escalate_to"])
	}
	fmt.Fprintf(&out, "\nbrowser check %s · consultant %s\n", cfg.VisualReviewer, cfg.Consultant)
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
	limit, err := resolved.Parallelism()
	if err != nil {
		return nil, err
	}
	describe := func(index int) map[string]any {
		stage := resolved.Plan.Stages[index]
		writers := map[string]string{}
		for _, stream := range stage.Streams {
			writers[stream.Name] = writerRoute(cfg, resolved.Modes[index], stream, 0).String()
		}
		order, _ := waves(stage.Streams)
		var names [][]string
		for _, wave := range order {
			var row []string
			for _, stream := range wave {
				row = append(row, stream.Name)
			}
			names = append(names, row)
		}
		table := modeTable(cfg, resolved.Modes[index])
		return map[string]any{"decision": resolved.Decisions[index], "writers": writers, "waves": names, "review": table["review"], "blocking": table["blocking"]}
	}
	table := modeTable(cfg, resolved.Mode)
	out := map[string]any{"decision": resolved.Decision, "planning": table["scout"], "design": table["design"], "parallel": limit, "read_only": true}
	if err := resolved.independent(); err != nil {
		out["problem"] = err.Error()
	}
	if len(resolved.Plan.Stages) == 1 {
		for key, value := range describe(0) {
			if key != "decision" {
				out[key] = value
			}
		}
		return out, nil
	}
	// A plan of several stages is shown stage by stage: each has its own mode and so its own models.
	var stages []map[string]any
	for index, stage := range resolved.Plan.Stages {
		item := describe(index)
		item["stage"] = stage.Name
		stages = append(stages, item)
	}
	out["stages"] = stages
	out["integration_review"] = integrationNeeded(resolved.Mode, len(resolved.Plan.Stages))
	out["isolated"] = resolved.Request.Worktree || isolateStaged(cfg)
	return out, nil
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
		add("lead pages", pagesInstalled("claude"), false, "the skill agentgate in "+pagesDir("claude")+"; "+hint)
		add("Stop hook", hooked(filepath.Join(claudeDir(), "settings.json")), true, hint)
	}
	if slices.Contains(leads, "codex") {
		installed, _ := os.ReadFile(filepath.Join(skillDir(), "SKILL.md"))
		add("$dev skill", string(installed) == assets.Skill, true, hint)
		add("lead pages", pagesInstalled("codex"), false, "the skill agentgate in "+pagesDir("codex")+"; "+hint)
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
		for _, warning := range accounts.Unserved(cfg) {
			add("accounts for workers", false, false, warning)
		}
	}
	healthy := true
	for _, item := range probes {
		healthy = healthy && (item.OK || !item.Required)
	}
	return map[string]any{"healthy": healthy, "checks": probes}
}

// pagesInstalled says whether every page of the lead's skill is installed as this version has it.
func pagesInstalled(lead string) bool {
	for path, content := range pages(pagesDir(lead)) {
		if installed, err := os.ReadFile(path); err != nil || string(installed) != content {
			return false
		}
	}
	return true
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

// pagesDir is where a lead's client keeps the skill with the lead's pages.
func pagesDir(lead string) string {
	if lead == "claude" {
		return filepath.Join(claudeDir(), "skills", "agentgate")
	}
	return filepath.Join(filepath.Dir(skillDir()), "agentgate")
}

// pages are the files of that skill: its index and every page `agentgate reference` prints.
func pages(dir string) map[string]string {
	out := map[string]string{filepath.Join(dir, "SKILL.md"): assets.SkillPage("SKILL")}
	for _, topic := range assets.ReferenceTopics() {
		out[filepath.Join(dir, topic+".md")] = assets.SkillPage(topic)
	}
	return out
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
			if err := placePages(lead, wanted, explicit, &done); err != nil {
				return nil, err
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
				if err := placePages(lead, true, explicit, &done); err != nil {
					return nil, err
				}
				next = append(next, "Codex: start a new session and type $dev help. Run /hooks there once and trust the agentgate Stop hook; "+
					"Codex does not run a hook until you have reviewed it.")
			} else if explicit {
				drop(skill, &done)
				drop(policy, &done)
				drop(codexRulePath(), &done)
				_ = os.Remove(filepath.Join(skillDir(), "agents"))
				_ = os.Remove(skillDir())
				if err := placePages(lead, false, explicit, &done); err != nil {
					return nil, err
				}
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

// placePages installs the lead's pages as a skill of the lead's client, or removes them on an explicit choice.
// A page an earlier version installed and this one no longer has is removed with it.
func placePages(lead string, wanted, explicit bool, done *[]string) error {
	dir := pagesDir(lead)
	current := pages(dir)
	if wanted {
		for path, content := range current {
			if err := place(path, content, done); err != nil {
				return err
			}
		}
	}
	installed, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	for _, path := range installed {
		if _, ours := current[path]; !wanted && explicit || !ours && wanted {
			drop(path, done)
		}
	}
	if !wanted && explicit {
		_ = os.Remove(dir)
	}
	return nil
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

// Overview lists the tasks of this checkout without touching any of them.
func Overview() map[string]any {
	var gates []map[string]any
	paths, _ := filepath.Glob(filepath.Join(checkoutDir(""), "*", "state.json"))
	for _, path := range paths {
		gate, err := loadGateFile(path)
		if err != nil || gate == nil {
			continue
		}
		live := false
		if record := ops.Status(filepath.Dir(path)); record != nil {
			live = record.Live
		}
		item := map[string]any{"session": gate.Session, "id": gate.ID, "mode": gate.Mode, "step": gate.Step,
			"task": gate.Request.Task, "operation_live": live}
		if gate.Pause != nil {
			item["paused"] = gate.Pause.Reason
		}
		if gate.Worktree != "" {
			item["isolated"] = gate.landing()
		}
		gates = append(gates, item)
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
		return map[string]any{"cancellation_requested": true, "next": "The task will be parked as stopped by the user; `agentgate watch` shows it."}, nil
	}
	return nil, errors.New("several sessions are running here; stop from the owning lead session")
}
