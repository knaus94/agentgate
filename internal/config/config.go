// Package config loads the mode table: which model does which job in each mode.
package config

import "encoding/json"

// ModeNames lists the modes from cheapest to most thorough.
var ModeNames = []string{"light", "medium", "heavy", "ultra"}

// Efforts are the reasoning efforts a route may request.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

// Severities a mode may treat as blocking.
var Severities = []string{"P0", "P1", "P2", "P3"}

// Axes a review lane may cover.
var Axes = []string{"correctness", "architecture", "simplicity", "security", "design"}

type Model struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Pool     string `json:"pool"`
}

type Lane struct {
	Axes  []string `json:"axes"`
	Route string   `json:"route"`
}

// ReviewRule adds a repository's review instructions to an existing reviewer, without granting tools.
type ReviewRule struct {
	Instructions []string `json:"instructions"`
	Files        []string `json:"files,omitempty"`
	Mode         string   `json:"mode,omitempty"` // blocking (default) or advisory
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

// WorkerMCP is an MCP server handed to workers of chosen roles. Workers otherwise run without any.
type WorkerMCP struct {
	Command         string            `json:"command"`
	Args            []string          `json:"args"`
	Env             map[string]string `json:"env"`
	Tools           []string          `json:"tools"`             // the only tools a worker may call; empty: all of the server's
	Roles           []string          `json:"roles"`             // scout (default), architect, writer, reviewer, consultant, designer
	Writable        []string          `json:"writable"`          // directories of the checkout the server must write, e.g. its cache; read-only workers get them writable
	Hint            string            `json:"hint"`              // when to use it, told to the worker
	Refresh         []string          `json:"refresh"`           // command that brings the server's index up to date, run before its first use
	RefreshTimeoutS int               `json:"refresh_timeout_s"` // default 300
}

// Roles a worker tool may be given to.
var Roles = []string{"scout", "architect", "writer", "reviewer", "consultant", "designer"}

// RolesOrDefault is who gets the server: the scout alone unless the configuration says otherwise, because
// what the scout learns reaches the writers and the reviewers through the context pack.
func (w WorkerMCP) RolesOrDefault() []string {
	if len(w.Roles) == 0 {
		return []string{"scout"}
	}
	return w.Roles
}

// Staging configures when work has to be split into stages.
type Staging struct {
	MaxFiles   int    `json:"max_files"`   // a single-pass plan may change this many files; 0: no limit
	MaxStreams int    `json:"max_streams"` // and have this many streams; 0: no limit
	Isolate    string `json:"isolate"`     // staged tasks in their own worktree: auto (when the repository prepares one), always, never
}

// Worktree configures isolated tasks (`/dev -w`): what a fresh worktree needs before code in it can be checked.
type Worktree struct {
	Default       bool       `json:"default"`         // isolate every writing task, without the flag
	Copy          []string   `json:"copy"`            // untracked files and directories to bring from the checkout, e.g. env files
	Setup         [][]string `json:"setup"`           // commands run once in the new worktree, e.g. installing dependencies
	SetupTimeoutS int        `json:"setup_timeout_s"` // limit for each setup command
}

type Config struct {
	Version           int                   `json:"version"`
	MaxParallel       int                   `json:"max_parallel"`
	WorkerMemoryMiB   int                   `json:"worker_memory_mib"`
	RetentionDays     int                   `json:"retention_days"`
	Models            map[string]Model      `json:"models"`
	Fallbacks         map[string][]string   `json:"fallbacks"`
	ReviewersExcluded []string              `json:"reviewers_excluded"`
	UIDesigner        string                `json:"ui_designer"`
	VisualReviewer    string                `json:"visual_reviewer"`
	Consultant        string                `json:"consultant"`
	PlaywrightMCP     string                `json:"playwright_mcp"`
	Accounts          []Account             `json:"accounts"`
	PlansHome         string                `json:"plans_home"` // synced directory that holds the .agentgate of each repository
	WorkerMCP         map[string]WorkerMCP  `json:"worker_mcp"`
	ReviewRules       map[string]ReviewRule `json:"review_rules,omitempty"`
	ReviewRuleSources map[string][]string   `json:"-"`       // definition sources in precedence order, derived at load time
	Profile           string                `json:"profile"` // a named alternative mode table laid under the user's settings, e.g. "codex"
	Leads             []string              `json:"leads"`   // which CLIs the lead command is installed into: claude, codex
	Worktree          Worktree              `json:"worktree"`
	Staging           Staging               `json:"staging"`
	Modes             map[string]Mode       `json:"modes"`
	Design            Design                `json:"design"`
}

// Design says, once for a repository, where its design screens are kept and how its front end is started to
// show one. The stand is read by the layout check itself.
type Design struct {
	Screens string          `json:"screens"` // directory of the screens; default .agentgate/design
	Stand   json.RawMessage `json:"stand"`   // {command, ready, env_file, env, graphql_env, cookies}
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
