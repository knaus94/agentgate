package provider

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/knaus94/agentgate/internal/ops"
)

func runClaude(call Call) (Result, error) {

	// The sandbox hides the real config directory from writes; the process keeps its session in the lane.
	home := filepath.Join(call.Dir, "home")
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
		for _, name := range sortedKeys(call.MCP) {
			server := call.MCP[name]
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
		schema, err := json.Marshal(call.Schema)
		if err != nil {
			return Result{}, fmt.Errorf("encode worker output schema: %w", err)
		}
		argv = append(argv, "--json-schema", string(schema))
	}
	if err := execute(call, argv, workerEnvironment(call, "CLAUDE_CONFIG_DIR="+home), "events.jsonl"); err != nil {
		return Result{}, err
	}
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
	if err := scanEvents(filepath.Join(call.Dir, "events.jsonl"), func(line []byte) {
		var event outcome
		if json.Unmarshal(line, &event) == nil && event.Type == "result" {
			out = event
		}
	}); err != nil {
		return Result{Session: out.SessionID}, fmt.Errorf("read %s event stream: %w", call.Route, err)
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
