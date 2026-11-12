// Package config loads the mode table: which model does which job in each mode.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/knaus94/claude-dev/internal/assets"
)

// ModeNames lists the modes from cheapest to most thorough.
var ModeNames = []string{"light", "medium", "heavy", "ultra"}

// Efforts are the reasoning efforts a route may request.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

// Severities a mode may treat as blocking.
var Severities = []string{"P0", "P1", "P2", "P3"}

// Axes a review lane may cover.
var Axes = []string{"correctness", "architecture", "security"}

type Model struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Pool     string `json:"pool"`
}

type Lane struct {
	Axes  []string `json:"axes"`
	Route string   `json:"route"`
}

type Mode struct {
	Scout                string   `json:"scout"`       // cheap route that gathers the context pack once; empty: the lead's context is enough
	EscalateTo           string   `json:"escalate_to"` // highest effort a stalled correction or a hard stream may be raised to
	Architecture         []string `json:"architecture"`
	Critique             bool     `json:"critique"`
	ArchitectureTimeoutS int      `json:"architecture_timeout_s"`
	WriterTimeoutS       int      `json:"writer_timeout_s"`   // ceiling of one writer process; 0 means the default
	ReviewerTimeoutS     int      `json:"reviewer_timeout_s"` // ceiling of one reviewer process; 0 means the default
	Backend              string   `json:"backend"`
	Frontend             string   `json:"frontend"`
	Review               []Lane   `json:"review"`
	Blocking             []string `json:"blocking"`
}

// Account is one logged-in CLI profile: a Claude config directory or a Codex home. Several accounts of one
// provider share the work, and one that hit its limit is skipped until it resets.
type Account struct {
	Name     string `json:"name"`
	Provider string `json:"provider"` // claude or codex
	Home     string `json:"home"`
	Paid     bool   `json:"paid,omitempty"` // an API-key profile bills per token; it is used only when this is set
}

// Worktree configures isolated tasks (`/dev -w`): what a fresh worktree needs before code in it can be checked.
type Worktree struct {
	Default       bool       `json:"default"`         // isolate every writing task, without the flag
	Copy          []string   `json:"copy"`            // untracked files and directories to bring from the checkout, e.g. env files
	Setup         [][]string `json:"setup"`           // commands run once in the new worktree, e.g. installing dependencies
	SetupTimeoutS int        `json:"setup_timeout_s"` // limit for each setup command
}

type Config struct {
	Version           int                 `json:"version"`
	MaxParallel       int                 `json:"max_parallel"`
	WorkerMemoryMiB   int                 `json:"worker_memory_mib"`
	RetentionDays     int                 `json:"retention_days"`
	Models            map[string]Model    `json:"models"`
	Fallbacks         map[string][]string `json:"fallbacks"`
	ReviewersExcluded []string            `json:"reviewers_excluded"`
	UIDesigner        string              `json:"ui_designer"`
	VisualReviewer    string              `json:"visual_reviewer"`
	Consultant        string              `json:"consultant"`
	PlaywrightMCP     string              `json:"playwright_mcp"`
	Accounts          []Account           `json:"accounts"`
	PlansHome         string              `json:"plans_home"` // synced directory that holds the .claude-dev of each repository
	Worktree          Worktree            `json:"worktree"`
	Modes             map[string]Mode     `json:"modes"`
}

// Route is one resolved model invocation.
type Route struct {
	Alias           string `json:"alias"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	Effort          string `json:"effort"`
	Pool            string `json:"pool"`
	SubstitutedFrom string `json:"substituted_from,omitempty"`
	Account         string `json:"account,omitempty"` // configured account the call ran under; a continued session prefers it
}

func (r Route) String() string { return r.Alias + ":" + r.Effort }

// UserPath is the optional override file merged over the built-in table.
func UserPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, "claude-dev", "config.json")
}

// ProjectFile is the optional per-repository override, read from the working directory.
const ProjectFile = ".claude-dev.json"

// PersonalFile is the user's own override for one repository. It lives next to the saved plans, so it is
// never committed with the team's file and travels with the plans when that directory is synced.
const PersonalFile = ".claude-dev/config.json"

// Load returns the built-in table with the user's and then the repository's overrides merged in.
func Load() (*Config, error) {
	var merged map[string]any
	if err := json.Unmarshal(assets.Config, &merged); err != nil {
		return nil, fmt.Errorf("built-in config: %w", err)
	}
	for _, path := range []string{UserPath(), ProjectFile, PersonalFile} {
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var layer map[string]any
		if err := json.Unmarshal(raw, &layer); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if path != UserPath() {
			delete(layer, "accounts") // a repository does not choose whose subscription pays for its work
		}
		merge(merged, layer)
	}
	raw, _ := json.Marshal(merged)
	cfg := &Config{}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return cfg, cfg.validate()
}

func readUser() (map[string]any, error) {
	layer := map[string]any{}
	raw, err := os.ReadFile(UserPath())
	if errors.Is(err, os.ErrNotExist) {
		return layer, nil
	}
	if err != nil {
		return nil, err
	}
	return layer, json.Unmarshal(raw, &layer)
}

// writeUser saves the user file and rolls back when the merged result would be invalid.
func writeUser(layer map[string]any) error {
	previous, readErr := os.ReadFile(UserPath())
	if err := os.MkdirAll(filepath.Dir(UserPath()), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(layer, "", "  ")
	if err := os.WriteFile(UserPath(), append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if _, err := Load(); err != nil {
		if readErr == nil {
			_ = os.WriteFile(UserPath(), previous, 0o600)
		} else {
			_ = os.Remove(UserPath())
		}
		return fmt.Errorf("rejected, nothing changed: %w", err)
	}
	return nil
}

// Set writes one dotted key of the user file, for example modes.medium.backend = sonnet:high.
// The value is JSON when it parses as JSON, otherwise a string.
func Set(key, value string) error {
	layer, err := readUser()
	if err != nil {
		return err
	}
	var parsed any
	if json.Unmarshal([]byte(value), &parsed) != nil {
		parsed = value
	}
	parts := strings.Split(key, ".")
	node := layer
	for _, part := range parts[:len(parts)-1] {
		child, ok := node[part].(map[string]any)
		if !ok {
			child = map[string]any{}
			node[part] = child
		}
		node = child
	}
	node[parts[len(parts)-1]] = parsed
	return writeUser(layer)
}

// Unset removes one dotted key of the user file, restoring the built-in value.
func Unset(key string) error {
	layer, err := readUser()
	if err != nil {
		return err
	}
	parts := strings.Split(key, ".")
	node := layer
	for _, part := range parts[:len(parts)-1] {
		child, ok := node[part].(map[string]any)
		if !ok {
			return nil
		}
		node = child
	}
	delete(node, parts[len(parts)-1])
	return writeUser(layer)
}

// SetValue writes one top-level key of the user file to a structured value.
func SetValue(key string, value any) error {
	layer, err := readUser()
	if err != nil {
		return err
	}
	layer[key] = value
	return writeUser(layer)
}

// ExpandHome resolves a leading "~/".
func ExpandHome(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, rest)
	}
	return path
}

// Init creates the user file with the models and fallbacks, the part people actually change.
func Init() (string, error) {
	if _, err := os.Stat(UserPath()); err == nil {
		return UserPath(), errors.New("the file already exists")
	}
	var builtin map[string]any
	_ = json.Unmarshal(assets.Config, &builtin)
	return UserPath(), writeUser(map[string]any{"models": builtin["models"], "fallbacks": builtin["fallbacks"]})
}

// merge overlays src on dst: objects merge key by key, everything else is replaced.
func merge(dst, src map[string]any) {
	for key, value := range src {
		if child, ok := value.(map[string]any); ok {
			if existing, ok := dst[key].(map[string]any); ok {
				merge(existing, child)
				continue
			}
		}
		dst[key] = value
	}
}

func (c *Config) validate() error {
	if c.MaxParallel < 1 || c.WorkerMemoryMiB < 1 {
		return errors.New("config: max_parallel and worker_memory_mib must be positive")
	}
	for alias, model := range c.Models {
		if (model.Provider != "claude" && model.Provider != "codex") || model.Model == "" || model.Pool == "" {
			return fmt.Errorf("config: model %q needs provider claude|codex, model and pool", alias)
		}
	}
	for alias, list := range c.Fallbacks {
		for _, other := range append([]string{alias}, list...) {
			if _, ok := c.Models[other]; !ok {
				return fmt.Errorf("config: fallbacks reference unknown model %q", other)
			}
		}
	}
	for _, path := range c.Worktree.Copy {
		if path == "" || filepath.IsAbs(path) || strings.HasPrefix(filepath.Clean(path), "..") {
			return fmt.Errorf("config: worktree.copy takes paths inside the repository: %q", path)
		}
	}
	for _, argv := range c.Worktree.Setup {
		if len(argv) == 0 || argv[0] == "" {
			return errors.New("config: worktree.setup is a list of commands, each a non-empty argv array")
		}
	}
	if c.Worktree.SetupTimeoutS < 1 {
		return errors.New("config: worktree.setup_timeout_s must be positive")
	}
	names := map[string]bool{}
	for _, account := range c.Accounts {
		if account.Name == "" || names[account.Name] || account.Home == "" || (account.Provider != "claude" && account.Provider != "codex") {
			return fmt.Errorf("config: account %q needs a unique name, provider claude|codex and a home directory", account.Name)
		}
		names[account.Name] = true
	}
	specs := []string{c.UIDesigner, c.VisualReviewer, c.Consultant}
	for _, name := range ModeNames {
		mode, ok := c.Modes[name]
		if !ok {
			return fmt.Errorf("config: mode %q is missing", name)
		}
		specs = append(specs, mode.Backend, mode.Frontend)
		if mode.Scout != "" {
			specs = append(specs, mode.Scout)
		}
		if mode.EscalateTo != "" && !slices.Contains(Efforts, mode.EscalateTo) {
			return fmt.Errorf("config: mode %q escalate_to must be an effort level", name)
		}
		specs = append(specs, mode.Architecture...)
		if mode.Critique && len(mode.Architecture) != 2 {
			return fmt.Errorf("config: mode %q critique needs exactly two architecture routes", name)
		}
		for _, lane := range mode.Review {
			specs = append(specs, lane.Route)
			for _, axis := range lane.Axes {
				if !slices.Contains(Axes, axis) {
					return fmt.Errorf("config: mode %q has unknown review axis %q", name, axis)
				}
			}
			if len(lane.Axes) == 0 {
				return fmt.Errorf("config: mode %q has a review lane without axes", name)
			}
		}
		for _, severity := range mode.Blocking {
			if !slices.Contains(Severities, severity) {
				return fmt.Errorf("config: mode %q has unknown severity %q", name, severity)
			}
		}
	}
	for _, spec := range specs {
		if _, err := c.Route(spec); err != nil {
			return err
		}
	}
	return nil
}

// Route resolves "alias:effort".
func (c *Config) Route(spec string) (Route, error) {
	alias, effort, ok := strings.Cut(spec, ":")
	model, known := c.Models[alias]
	if !ok || !known || !slices.Contains(Efforts, effort) {
		return Route{}, fmt.Errorf("config: route %q must be <model alias>:<effort>", spec)
	}
	return Route{Alias: alias, Provider: model.Provider, Model: model.Model, Effort: effort, Pool: model.Pool}, nil
}

// MustRoute resolves a spec already checked by validate.
func (c *Config) MustRoute(spec string) Route {
	route, err := c.Route(spec)
	if err != nil {
		panic(err)
	}
	return route
}

// Alternates lists the declared substitutes for a route, same effort, in order.
func (c *Config) Alternates(route Route) []Route {
	var out []Route
	for _, alias := range c.Fallbacks[route.Alias] {
		model := c.Models[alias]
		out = append(out, Route{Alias: alias, Provider: model.Provider, Model: model.Model, Effort: route.Effort,
			Pool: model.Pool, SubstitutedFrom: route.String()})
	}
	return out
}

// Lower returns the effort one step down, never below medium: a recheck reads a delta, not the whole change.
func Lower(effort string) string {
	if index := slices.Index(Efforts, effort); index > 1 {
		return Efforts[index-1]
	}
	return effort
}

// Raise returns the effort the given number of steps up, never above the ceiling.
func Raise(effort string, steps int, ceiling string) string {
	index, limit := slices.Index(Efforts, effort), slices.Index(Efforts, ceiling)
	if limit < index {
		limit = index // a missing or lower ceiling never reduces the configured effort
	}
	return Efforts[min(index+max(steps, 0), limit)]
}

// Blocks reports whether a severity blocks completion in the mode.
func (m Mode) Blocks(severity string) bool { return slices.Contains(m.Blocking, severity) }
