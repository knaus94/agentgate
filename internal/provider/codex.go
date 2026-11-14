package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/knaus94/agentgate/internal/ops"
)

func runCodex(call Call) (Result, error) {
	home := filepath.Join(call.Dir, "home")
	actual := profile(call, "CODEX_HOME", ".codex")
	if err := link(filepath.Join(actual, "AGENTS.md"), filepath.Join(home, "AGENTS.md")); err != nil {
		return Result{}, fmt.Errorf("prepare provider instructions: %w", err)
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
	for _, name := range sortedKeys(call.MCP) {
		server := call.MCP[name]
		prefix := "mcp_servers." + name + "."
		var env []string
		for _, key := range sortedKeys(server.Env) {
			env = append(env, quote(key)+"="+quote(server.Env[key]))
		}
		argv = append(argv, "-c", prefix+"command="+quote(server.Command), "-c", prefix+"args="+quoteList(server.Args),
			"-c", prefix+"env={"+strings.Join(env, ",")+"}",
			"-c", prefix+`default_tools_approval_mode="approve"`, "-c", prefix+"startup_timeout_sec=60")
		// Omitting enabled_tools exposes the extension's complete tool set.
		// Sending an empty array would silently disable every tool in Codex.
		if len(server.Tools) != 0 {
			argv = append(argv, "-c", prefix+"enabled_tools="+quoteList(server.Tools))
		}
	}
	if call.Schema != nil {
		schema := filepath.Join(call.Dir, "schema.json")
		if err := ops.WriteJSON(schema, call.Schema); err != nil {
			return Result{}, err
		}
		argv = append(argv, "--output-schema", schema)
	}
	argv = append(argv, "-")
	if err := execute(call, argv, workerEnvironment(call, "CODEX_HOME="+home), "events.jsonl"); err != nil {
		return Result{}, err
	}
	session, completed := call.Resume, false
	if err := scanEvents(filepath.Join(call.Dir, "events.jsonl"), func(line []byte) {
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal(line, &event) != nil {
			return
		}
		if event.Type == "thread.started" && event.ThreadID != "" {
			session = event.ThreadID
		}
		switch event.Type {
		case "turn.completed":
			completed = true
		case "turn.failed":
			completed = false
		}
	}); err != nil {
		return Result{Session: session}, fmt.Errorf("read %s event stream: %w", call.Route, err)
	}
	answer, err := os.ReadFile(result)
	if err != nil || !completed || session == "" || strings.TrimSpace(string(answer)) == "" {
		return Result{Session: session}, fmt.Errorf("%s did not complete a turn; inspect %s", call.Route, call.Dir)
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
