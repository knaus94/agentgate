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
	"strings"
	"time"

	"github.com/knaus94/claude-dev/internal/config"
	"github.com/knaus94/claude-dev/internal/ops"
)

// WorkerEnv marks a spawned model process; such a process may not drive the gate.
const WorkerEnv = "CLAUDE_DEV_WORKER"

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
}

type Result struct {
	Answer  string
	Session string
}

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

func limited(dir string) bool {
	for _, name := range []string{"stderr.txt", "events.jsonl", "events.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil && limitPattern.Match(tail(raw, 8192)) {
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
	cwd, _ := os.Getwd()
	checkout := "--ro-bind"
	if writer {
		checkout = "--bind"
	}
	out := []string{"bwrap", "--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/dev/shm",
		"--unshare-pid", "--unshare-ipc", "--unshare-uts", "--new-session", "--die-with-parent",
		"--tmpfs", "/tmp", "--tmpfs", "/run", "--ro-bind-try", "/run/systemd/resolve", "/run/systemd/resolve"}
	for _, extra := range readable {
		out = append(out, "--ro-bind", extra, extra)
	}
	out = append(out, checkout, cwd, cwd, "--bind", dir, dir)
	for _, extra := range writable {
		out = append(out, "--bind", extra, extra)
	}
	return append(append(out, "--chdir", cwd, "--"), argv...)
}

func environment(extra ...string) []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "TMUX", "TMUX_PANE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_DEV_SESSION", "CLAUDE_CODE_EFFORT_LEVEL",
			"CODEX_HOME", "CLAUDE_CONFIG_DIR":
			continue
		}
		env = append(env, entry)
	}
	return append(append(env, WorkerEnv+"=1"), extra...)
}

func record(call Call, argv []string, cmd *exec.Cmd, state string) {
	receipt := map[string]any{"state": state, "route": call.Route, "argv": argv, "writer": call.Writer,
		"resumed": call.Resume != "", "observed_at": time.Now().Unix()}
	if cmd.Process != nil {
		receipt["pid"] = cmd.Process.Pid
	}
	if cmd.ProcessState != nil {
		receipt["returncode"] = cmd.ProcessState.ExitCode()
	}
	_ = ops.WriteJSON(filepath.Join(call.Dir, "process.json"), receipt)
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
	record(call, argv, cmd, "starting")
	err = ops.Run(cmd, call.Timeout)
	record(call, argv, cmd, "exited")
	if err != nil {
		if ops.IsCancelled(err) || errors.Is(err, ops.ErrTimeout) {
			return err
		}
		return fmt.Errorf("%s failed (%v); inspect %s", call.Route, err, filepath.Join(call.Dir, "stderr.txt"))
	}
	return nil
}

func link(source, target string) {
	if _, err := os.Stat(source); err == nil {
		if _, err := os.Lstat(target); err != nil {
			_ = os.Symlink(source, target)
		}
	}
}

func userHome() string { home, _ := os.UserHomeDir(); return home }

func runCodex(call Call) (Result, error) {
	home := filepath.Join(call.Dir, "home")
	actual := os.Getenv("CODEX_HOME")
	if actual == "" {
		actual = filepath.Join(userHome(), ".codex")
	}
	for _, name := range []string{"auth.json", "AGENTS.md"} {
		link(filepath.Join(actual, name), filepath.Join(home, name))
	}
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
	if call.MCP != nil {
		return Result{}, errors.New("MCP tools are only wired for Codex routes")
	}
	// The sandbox hides the real config directory from writes; the process keeps its session in the lane.
	home := filepath.Join(call.Dir, "home")
	actual := os.Getenv("CLAUDE_CONFIG_DIR")
	if actual == "" {
		actual = filepath.Join(userHome(), ".claude")
	}
	link(filepath.Join(actual, ".credentials.json"), filepath.Join(home, ".credentials.json"))
	tools := "Read,Grep,Glob"
	if call.Writer {
		tools += ",Edit,Write,Bash"
	}
	argv := []string{"claude", "-p", "--model", call.Route.Model, "--effort", call.Route.Effort, "--output-format", "json",
		"--tools", tools, "--strict-mcp-config", "--setting-sources", "", "--dangerously-skip-permissions"}
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
	if err := execute(call, argv, environment("CLAUDE_CONFIG_DIR="+home), "events.json"); err != nil {
		return Result{}, err
	}
	raw, _ := os.ReadFile(filepath.Join(call.Dir, "events.json"))
	var out struct {
		IsError    bool                       `json:"is_error"`
		Subtype    string                     `json:"subtype"`
		SessionID  string                     `json:"session_id"`
		Result     string                     `json:"result"`
		Structured json.RawMessage            `json:"structured_output"`
		ModelUsage map[string]json.RawMessage `json:"modelUsage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.IsError || out.Subtype != "success" || out.SessionID == "" {
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

// RunRouted tries the route and, when its usage limit is exhausted, the declared alternates in order.
// skip rejects alternates the caller may not use, such as the model that wrote the code under review.
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
		result, err := Run(attempt)
		if err == nil {
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
