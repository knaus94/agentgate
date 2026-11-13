// Package provider runs one model invocation as a fresh, sandboxed CLI process.
//
// Every process sees the host read-only. A reviewer can write only its own lane directory;
// a writer can additionally write the checkout. Writers keep their session, so a blocked
// writer is answered in the same context instead of being restarted.
package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/accounts"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
)

// WorkerEnv marks a spawned model process; such a process may not drive the gate.
const WorkerEnv = "AGENTGATE_WORKER"

// MCPServer is one stdio MCP server exposed to a Codex process with an explicit tool allowlist.
type MCPServer struct {
	Command string
	Args    []string
	Env     map[string]string
	Tools   []string
}

type Call struct {
	Route    config.Route
	Prompt   string
	Dir      string // private lane directory: logs, provider home, result
	Schema   any    // JSON schema of the answer, or nil for free text
	Timeout  time.Duration
	Writer   bool                 // may edit the checkout
	Persist  bool                 // keep the session so it can be continued later
	Resume   string               // session to continue instead of starting a fresh one
	Writable []string             // extra writable directories
	ReadDirs []string             // directories outside the checkout the process must be able to read
	MCP      map[string]MCPServer // Codex only
	Account  config.Account       // profile to run under; empty means the CLI's default profile
}

type Result struct {
	Answer  string
	Session string
	Account string // name of the configured account the call ran under, if any
}

// Accounts chooses the profile each call runs under. Nil, or no account for a provider, means its default profile.
var Accounts *accounts.Manager

// ErrLimit means the provider refused the call because its usage limit is exhausted.
var ErrLimit = errors.New("provider usage limit reached")

var limitPattern = regexp.MustCompile(`(?i)usage limit|rate.?limit|quota|too many requests|\b429\b|overloaded|limit reached`)

// Run is the process launcher; tests replace it.
var Run = run

func run(call Call) (Result, error) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		return Result{}, errors.New("bwrap is not installed; refusing to run a model without the sandbox")
	}
	if err := os.MkdirAll(filepath.Join(call.Dir, "home"), 0o700); err != nil {
		return Result{}, err
	}
	// The worker is given the lane by its real path: what the CLI is told and what the sandbox mounts agree.
	if resolved, err := filepath.EvalSymlinks(call.Dir); err == nil {
		call.Dir = resolved
	}
	invoke := runCodex
	if call.Route.Provider == "claude" {
		invoke = runClaude
	}
	result, err := invoke(call)
	if err != nil && !ops.IsCancelled(err) && !errors.Is(err, ops.ErrTimeout) && limited(call.Dir) {
		return result, fmt.Errorf("%w: %s (%v)", ErrLimit, call.Route, err)
	}
	return result, err
}

// limited reports whether the failed run was refused for an exhausted limit. Only what the CLI said about the
// failure counts: its stderr and the failure events of its stream. Ordinary events mention rate limits too
// (Claude reports the state of the limits in every run) and must not be mistaken for a refusal.
func limited(dir string) bool {
	if raw, err := os.ReadFile(filepath.Join(dir, "stderr.txt")); err == nil && limitPattern.Match(tail(raw, 8192)) {
		return true
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	for _, line := range strings.Split(string(tail(raw, 65536)), "\n") {
		var event struct {
			Type string `json:"type"`
		}
		failure := json.Unmarshal([]byte(line), &event) != nil || event.Type == "" || event.Type == "result" || event.Type == "error" ||
			event.Type == "turn.failed"
		if failure && limitPattern.MatchString(line) {
			return true
		}
	}
	return false
}

func tail(raw []byte, n int) []byte {
	if len(raw) > n {
		return raw[len(raw)-n:]
	}
	return raw
}

// Sandbox wraps argv so that the host is read-only and only the listed directories are writable.
// A private /dev/shm lets a headless browser run; /run/systemd/resolve stays visible for DNS.
// /tmp and /run are replaced by empty private directories, so anything a process needs from there
// is bound back explicitly: readable directories read-only, the lane and the writable ones read-write.
func Sandbox(dir string, writer bool, readable, writable []string, argv []string) []string {
	// A mount cannot be made through a symbolic link, and a path recorded before a directory moved may hold
	// one (the state of an installation under the tool's earlier name is a link to its new place). Mounting
	// the real path serves both: inside, the link still leads to it.
	real := func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		return path
	}
	dir = real(dir)
	for index := range readable {
		readable[index] = real(readable[index])
	}
	for index := range writable {
		writable[index] = real(writable[index])
	}
	cwd, _ := os.Getwd()
	cwd = real(cwd)
	checkout := "--ro-bind"
	if writer {
		checkout = "--bind"
	}
	out := []string{"bwrap", "--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/dev/shm",
		"--unshare-pid", "--unshare-ipc", "--unshare-uts", "--new-session", "--die-with-parent",
		"--tmpfs", "/tmp", "--tmpfs", "/run", "--ro-bind-try", "/run/systemd/resolve", "/run/systemd/resolve"}
	// The host is readable, so what a worker must never read is covered: sessions the tool keeps for the lead.
	if private := PrivateDir(); private != "" {
		if info, err := os.Stat(private); err == nil && info.IsDir() {
			out = append(out, "--tmpfs", private)
		}
	}
	for _, extra := range readable {
		out = append(out, "--ro-bind", extra, extra)
	}
	out = append(out, checkout, cwd, cwd, "--bind", dir, dir)
	for _, extra := range writable {
		out = append(out, "--bind", extra, extra)
	}
	return append(append(out, "--chdir", cwd, "--"), argv...)
}

// PrivateDir holds what belongs to the lead alone, such as a signed-in browser profile. No worker sees inside it.
func PrivateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "agentgate", "private")
}

func environment(extra ...string) []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "TMUX", "TMUX_PANE", "CLAUDE_CODE_SESSION_ID", "AGENTGATE_SESSION", "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CONFIG_DIR":
			continue
		}
		// A worker must not inherit the lead's Codex session: its thread, its sandbox marks, its home.
		if strings.HasPrefix(name, "CODEX_") {
			continue
		}
		env = append(env, entry)
	}
	return append(append(env, WorkerEnv+"=1"), extra...)
}

// record keeps the receipt of the lane's present run, and adds every finished run to the lane's history,
// so a viewer can show how long each attempt took.
func record(call Call, argv []string, cmd *exec.Cmd, state string, started int64) {
	receipt := map[string]any{"state": state, "route": call.Route, "account": call.Account.Name, "argv": argv, "writer": call.Writer,
		"resumed": call.Resume != "", "started_at": started, "observed_at": time.Now().Unix()}
	if cmd.Process != nil {
		receipt["pid"] = cmd.Process.Pid
	}
	if cmd.ProcessState != nil {
		receipt["returncode"] = cmd.ProcessState.ExitCode()
	}
	_ = ops.WriteJSON(filepath.Join(call.Dir, "process.json"), receipt)
	if state != "exited" {
		return
	}
	delete(receipt, "argv")
	if line, err := json.Marshal(receipt); err == nil {
		if history, err := os.OpenFile(filepath.Join(call.Dir, "runs.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = history.Write(append(line, '\n'))
			history.Close()
		}
	}
}

// execute runs the sandboxed process with the prompt on stdin and returns its stdout.
func execute(call Call, argv []string, env []string, stdoutName string) error {
	stdout, err := os.Create(filepath.Join(call.Dir, stdoutName))
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(call.Dir, "stderr.txt"))
	if err != nil {
		return err
	}
	defer stderr.Close()
	wrapped := Sandbox(call.Dir, call.Writer, call.ReadDirs, call.Writable, argv)
	cmd := exec.Command(wrapped[0], wrapped[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = strings.NewReader(call.Prompt), stdout, stderr, env
	// The receipt of a running lane carries the pid, so it is written once the process exists.
	started := time.Now().Unix()
	err = ops.RunStarted(cmd, call.Timeout, func() { record(call, argv, cmd, "starting", started) })
	record(call, argv, cmd, "exited", started)
	if err != nil {
		if ops.IsCancelled(err) || errors.Is(err, ops.ErrTimeout) {
			return err
		}
		return fmt.Errorf("%s failed (%v); inspect %s", call.Route, err, filepath.Join(call.Dir, "stderr.txt"))
	}
	return nil
}

// link points target at source, replacing a link left by a run under another account.
func link(source, target string) {
	if _, err := os.Stat(source); err != nil {
		return
	}
	if current, err := os.Readlink(target); err == nil && current == source {
		return
	}
	_ = os.Remove(target)
	_ = os.Symlink(source, target)
}

// profile is the real CLI home the call authenticates from: the chosen account, else the user's own.
func profile(call Call, variable, fallback string) string {
	if call.Account.Home != "" {
		return accounts.Home(call.Account)
	}
	if actual := os.Getenv(variable); actual != "" {
		return actual
	}
	return filepath.Join(userHome(), fallback)
}

// credential links the login file into the lane home and keeps the real file writable inside the sandbox:
// when the CLI refreshes a token mid-run, the new one must reach the profile, or the profile is logged out.
func credential(call *Call, source, target string) {
	link(source, target)
	if real, err := filepath.EvalSymlinks(source); err == nil {
		call.Writable = append(append([]string{}, call.Writable...), real)
	}
}

func userHome() string { home, _ := os.UserHomeDir(); return home }

func runCodex(call Call) (Result, error) {
	home := filepath.Join(call.Dir, "home")
	actual := profile(call, "CODEX_HOME", ".codex")
	credential(&call, filepath.Join(actual, "auth.json"), filepath.Join(home, "auth.json"))
	link(filepath.Join(actual, "AGENTS.md"), filepath.Join(home, "AGENTS.md"))
	result := filepath.Join(call.Dir, "result.json")
	_ = os.Remove(result)
	// The process already runs inside bwrap. Codex's own sandbox would nest a second bwrap,
	// which hardened kernels deny, so every tool command would fail to start.
	argv := []string{"codex", "--no-daemon", "--disable", "plugins", "--disable", "plugin_discovery_prefetch",
		"--disable", "multi_agent"}
	if call.MCP != nil {
		argv = append(argv, "--disable", "browser_use", "--disable", "in_app_browser")
	}
	argv = append(argv, "exec")
	if call.Resume != "" {
		argv = append(argv, "resume", call.Resume)
	}
	argv = append(argv, "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "--ignore-user-config",
		"--ignore-rules", "-m", call.Route.Model, "-c", "model_reasoning_effort="+quote(call.Route.Effort), "--json", "-o", result)
	if !call.Persist && call.Resume == "" {
		argv = append(argv, "--ephemeral")
	}
	for name, server := range call.MCP {
		prefix := "mcp_servers." + name + "."
		var env []string
		for key, value := range server.Env {
			env = append(env, key+"="+quote(value))
		}
		argv = append(argv, "-c", prefix+"command="+quote(server.Command), "-c", prefix+"args="+quoteList(server.Args),
			"-c", prefix+"env={"+strings.Join(env, ",")+"}", "-c", prefix+"enabled_tools="+quoteList(server.Tools),
			"-c", prefix+`default_tools_approval_mode="approve"`, "-c", prefix+"startup_timeout_sec=60")
	}
	if call.Schema != nil {
		schema := filepath.Join(call.Dir, "schema.json")
		if err := ops.WriteJSON(schema, call.Schema); err != nil {
			return Result{}, err
		}
		argv = append(argv, "--output-schema", schema)
	}
	argv = append(argv, "-")
	if err := execute(call, argv, environment("CODEX_HOME="+home), "events.jsonl"); err != nil {
		return Result{}, err
	}
	events, _ := os.ReadFile(filepath.Join(call.Dir, "events.jsonl"))
	session, completed := call.Resume, false
	for _, line := range strings.Split(string(events), "\n") {
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event.Type == "thread.started" && event.ThreadID != "" {
			session = event.ThreadID
		}
		completed = completed || event.Type == "turn.completed"
	}
	answer, err := os.ReadFile(result)
	if err != nil || !completed || session == "" {
		return Result{}, fmt.Errorf("%s did not complete a turn; inspect %s", call.Route, call.Dir)
	}
	return Result{Answer: string(answer), Session: session}, nil
}

func quote(value string) string { raw, _ := json.Marshal(value); return string(raw) }

func quoteList(values []string) string {
	if values == nil {
		values = []string{}
	}
	raw, _ := json.Marshal(values)
	return string(raw)
}

func runClaude(call Call) (Result, error) {

	// The sandbox hides the real config directory from writes; the process keeps its session in the lane.
	home := filepath.Join(call.Dir, "home")
	credential(&call, filepath.Join(profile(call, "CLAUDE_CONFIG_DIR", ".claude"), ".credentials.json"), filepath.Join(home, ".credentials.json"))
	tools := "Read,Grep,Glob"
	if call.Writer {
		tools += ",Edit,Write,Bash"
	}
	// The stream of events is what a viewer follows while the worker runs; the answer is its last "result" event.
	argv := []string{"claude", "-p", "--model", call.Route.Model, "--effort", call.Route.Effort, "--output-format", "stream-json", "--verbose",
		"--tools", tools, "--strict-mcp-config", "--setting-sources", ""}
	if call.MCP == nil {
		argv = append(argv, "--dangerously-skip-permissions")
	} else {
		// With servers attached the worker may call exactly what is listed: its built-in tools and the named
		// tools of each server. Everything else is refused without a prompt.
		servers, allowed := map[string]any{}, strings.Split(tools, ",")
		for name, server := range call.MCP {
			env := server.Env
			if env == nil {
				env = map[string]string{}
			}
			servers[name] = map[string]any{"type": "stdio", "command": server.Command, "args": append([]string{}, server.Args...), "env": env}
			if len(server.Tools) == 0 {
				allowed = append(allowed, "mcp__"+name)
			}
			for _, tool := range server.Tools {
				allowed = append(allowed, "mcp__"+name+"__"+tool)
			}
		}
		sort.Strings(allowed[len(strings.Split(tools, ",")):])
		config := filepath.Join(call.Dir, "mcp.json")
		if err := ops.WriteJSON(config, map[string]any{"mcpServers": servers}); err != nil {
			return Result{}, err
		}
		argv = append(argv, "--mcp-config", config, "--permission-mode", "dontAsk", "--allowedTools", strings.Join(allowed, ","))
		for _, dir := range call.Writable {
			argv = append(argv, "--add-dir", dir)
		}
	}
	if call.Resume != "" {
		argv = append(argv, "--resume", call.Resume)
	} else if !call.Persist {
		argv = append(argv, "--no-session-persistence")
	}
	for _, dir := range append([]string{call.Dir}, call.ReadDirs...) {
		argv = append(argv, "--add-dir", dir)
	}
	if call.Schema != nil {
		schema, _ := json.Marshal(call.Schema)
		argv = append(argv, "--json-schema", string(schema))
	}
	if err := execute(call, argv, environment("CLAUDE_CONFIG_DIR="+home), "events.jsonl"); err != nil {
		return Result{}, err
	}
	raw, _ := os.ReadFile(filepath.Join(call.Dir, "events.jsonl"))
	type outcome struct {
		Type       string                     `json:"type"`
		IsError    bool                       `json:"is_error"`
		Subtype    string                     `json:"subtype"`
		SessionID  string                     `json:"session_id"`
		Result     string                     `json:"result"`
		Structured json.RawMessage            `json:"structured_output"`
		ModelUsage map[string]json.RawMessage `json:"modelUsage"`
	}
	var out outcome
	for _, line := range strings.Split(string(raw), "\n") {
		var event outcome
		if json.Unmarshal([]byte(line), &event) == nil && event.Type == "result" {
			out = event
		}
	}
	if out.IsError || out.Subtype != "success" || out.SessionID == "" {
		return Result{}, fmt.Errorf("%s did not complete (%s); inspect %s", call.Route, out.Subtype, call.Dir)
	}
	used := false
	for model := range out.ModelUsage {
		used = used || strings.Contains(model, call.Route.Model)
	}
	if !used {
		return Result{}, fmt.Errorf("the answer did not come from %s; inspect %s", call.Route.Model, call.Dir)
	}
	answer := out.Result
	if len(out.Structured) > 0 && string(out.Structured) != "null" {
		answer = string(out.Structured)
	}
	return Result{Answer: answer, Session: out.SessionID}, nil
}

// Substitution records that a lane ran on a declared alternate because the routed pool was exhausted.
type Substitution struct {
	Wanted string `json:"wanted"`
	Used   string `json:"used"`
	Reason string `json:"reason"`
}

// onAccounts runs the call under the best configured account of its provider and moves to the next one when
// that account's limit is exhausted. The model changes only after every account refused it.
func onAccounts(call Call) (Result, error) {
	if !Accounts.Has(call.Route.Provider) {
		return Run(call)
	}
	tried, first := map[string]bool{}, error(nil)
	for {
		account, ok := Accounts.Pick(call.Route, call.Route.Account, tried)
		if !ok {
			if first == nil {
				first = fmt.Errorf("%w: no %s account has room for %s", ErrLimit, call.Route.Provider, call.Route)
			}
			return Result{}, first
		}
		tried[account.Name] = true
		attempt := call
		attempt.Account = account
		result, err := Run(attempt)
		Accounts.Release(account.Name, err == nil)
		if err == nil {
			result.Account = account.Name
			return result, nil
		}
		if !errors.Is(err, ErrLimit) {
			if call.Resume != "" && call.Route.Account != "" && account.Name != call.Route.Account {
				err = fmt.Errorf("account %s has no room and the session did not continue on %s: %w", call.Route.Account, account.Name, err)
			}
			return Result{}, err
		}
		Accounts.Limited(account.Name, call.Route)
		if first == nil {
			first = err
		}
	}
}

// RunRouted tries the route on every account that has room and, when all are exhausted, the declared
// alternates in order. skip rejects alternates the caller may not use, such as the model that wrote the
// code under review.
func RunRouted(cfg *config.Config, call Call, skip func(config.Route) bool) (Result, config.Route, error) {
	candidates := append([]config.Route{call.Route}, cfg.Alternates(call.Route)...)
	base, first := call.Dir, error(nil)
	for index, route := range candidates {
		if index > 0 && (call.Resume != "" || (skip != nil && skip(route))) {
			continue
		}
		attempt := call
		attempt.Route = route
		if index > 0 {
			attempt.Dir = fmt.Sprintf("%s-alt%d", base, index)
		}
		result, err := onAccounts(attempt)
		if err == nil {
			route.Account = result.Account
			return result, route, nil
		}
		if first == nil {
			first = err
		}
		if !errors.Is(err, ErrLimit) {
			return Result{}, route, err
		}
	}
	return Result{}, call.Route, fmt.Errorf("%w; no permitted alternate is available", first)
}
