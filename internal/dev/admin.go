package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/knaus94/claude-dev/internal/assets"
	"github.com/knaus94/claude-dev/internal/config"
	"github.com/knaus94/claude-dev/internal/ops"
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
	for _, tool := range [][]string{{"bwrap", "--version"}, {"git", "--version"}, {"diff", "--version"}, {"claude", "--version"}, {"codex", "--version"}} {
		detail, ok := version(tool[0], tool[1:]...)
		add(tool[0], ok, true, detail)
	}
	_, loggedIn := version("codex", "login", "status")
	add("codex login", loggedIn, true, "codex login status")
	cfg, err := config.Load()
	add("config", err == nil, true, fmt.Sprintf("user file %s, project file %s%s", config.UserPath(), config.ProjectFile, suffix(err)))
	err = os.MkdirAll(Root(), 0o700)
	add("state", err == nil, true, Root()+suffix(err))
	installed, _ := os.ReadFile(filepath.Join(claudeDir(), "commands", "dev.md"))
	add("/dev command", string(installed) == assets.Command, true, "run `claude-dev install` when this is not ok")
	settings, _ := os.ReadFile(filepath.Join(claudeDir(), "settings.json"))
	add("Stop hook", strings.Contains(string(settings), hookMarker), true, "run `claude-dev install` when this is not ok")
	if cfg != nil {
		_, statErr := os.Stat(expandHome(cfg.PlaywrightMCP))
		add("browser tooling", statErr == nil, false, "optional, for browser verification: "+expandHome(cfg.PlaywrightMCP))
		home, _ := os.UserHomeDir()
		cache, _ := os.ReadFile(filepath.Join(home, ".codex", "models_cache.json"))
		for alias, model := range cfg.Models {
			if model.Provider == "codex" && len(cache) > 0 {
				add("model "+alias, strings.Contains(string(cache), `"`+model.Model+`"`), false, model.Model+" in the local Codex model list")
			}
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

const hookMarker = "claude-dev hook"

func selfPath() string {
	if path, err := exec.LookPath("claude-dev"); err == nil {
		return path
	}
	path, _ := os.Executable()
	return path
}

func backup(path string) {
	if raw, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(fmt.Sprintf("%s.bak-claude-dev-%s", path, time.Now().Format("20060102-150405")), raw, 0o600)
	}
}

// Install puts the /dev command and the Stop hook into Claude Code. Running it again changes nothing.
func Install(uninstall, zed bool) (any, error) {
	done := []string{}
	command := filepath.Join(claudeDir(), "commands", "dev.md")
	settingsPath := filepath.Join(claudeDir(), "settings.json")
	if uninstall {
		if os.Remove(command) == nil {
			done = append(done, "removed "+command)
		}
	} else if existing, _ := os.ReadFile(command); string(existing) != assets.Command || isSymlink(command) {
		if err := os.MkdirAll(filepath.Dir(command), 0o700); err != nil {
			return nil, err
		}
		if len(existing) > 0 && !isSymlink(command) {
			backup(command)
		}
		_ = os.Remove(command) // never write through a symlink into someone else's file
		if err := os.WriteFile(command, []byte(assets.Command), 0o600); err != nil {
			return nil, err
		}
		done = append(done, "installed "+command)
	}
	note, err := installHook(settingsPath, uninstall)
	if err != nil {
		return nil, err
	}
	if note != "" {
		done = append(done, note)
	}
	out := map[string]any{"changed": done, "claude_config": claudeDir()}
	if zed && !uninstall {
		out["zed"] = installZed()
	}
	if len(done) == 0 {
		out["note"] = "already up to date"
	}
	out["next"] = "Start a new Claude Code session and run `/dev help`. `claude-dev doctor` verifies the setup."
	return out, nil
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
			ours := strings.Contains(command, hookMarker) || strings.Contains(command, "claude-dev quality-hook")
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
	{"label": "claude-dev: watch", "command": "claude-dev watch", "use_new_terminal": false, "allow_concurrent_runs": false, "reveal": "always"},
	{"label": "claude-dev: status", "command": "claude-dev status", "use_new_terminal": false, "reveal": "always"},
	{"label": "claude-dev: stop", "command": "claude-dev stop", "use_new_terminal": false, "reveal": "always"},
}

// installZed adds tasks to Zed's global tasks file. A file with comments is left alone: rewriting it would lose them.
func installZed() map[string]any {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	path := filepath.Join(base, "zed", "tasks.json")
	keymap := `{"context": "Workspace", "bindings": {"ctrl-alt-d": ["task::Spawn", {"task_name": "claude-dev: watch"}]}}`
	var tasks []any
	raw, err := os.ReadFile(path)
	if err == nil && json.Unmarshal(raw, &tasks) != nil {
		snippet, _ := json.MarshalIndent(zedTasks, "", "  ")
		side := filepath.Join(base, "zed", "claude-dev.tasks.json")
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
	return map[string]any{"changed": added > 0, "tasks_file": path, "run": "command palette → task: spawn → claude-dev: watch",
		"optional_keymap_entry": keymap}
}

// newest returns the most recently touched gate of this checkout, for viewers that run outside the lead session.
func newest() *Gate {
	var found *Gate
	var stamp time.Time
	paths, _ := filepath.Glob(filepath.Join(checkoutDir(""), "*", "state.json"))
	for _, path := range paths {
		info, err := os.Stat(path)
		raw, _ := os.ReadFile(path)
		gate := &Gate{}
		if err == nil && json.Unmarshal(raw, gate) == nil && info.ModTime().After(stamp) {
			found, stamp = gate, info.ModTime()
		}
	}
	return found
}

// Visible is the gate a read-only viewer should show: the session's own, or the newest of the checkout.
func Visible() *Gate {
	if session() != "" {
		if gate := Current(); gate != nil {
			return gate
		}
	}
	return newest()
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
			gates = append(gates, map[string]any{"session": gate.Session, "id": gate.ID, "mode": gate.Mode, "phase": gate.Phase,
				"task": gate.Request.Task, "operation_live": live})
		}
	}
	return map[string]any{"gates": gates, "read_only": true,
		"note": "Recorded state only. The owning Claude Code session drives the task."}
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
		return map[string]any{"cancellation_requested": true, "next": "The task will be put on hold; `claude-dev watch` shows it."}, nil
	}
	return nil, errors.New("several sessions are running here; stop from the owning Claude Code session")
}
