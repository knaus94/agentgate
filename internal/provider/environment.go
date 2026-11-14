package provider

import (
	"os"
	"sort"
	"strings"
)

func environment(extra ...string) []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "TMUX", "TMUX_PANE", "CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_PARENT_SESSION_ID", "AGENTGATE_SESSION", "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CONFIG_DIR", "FIGMA_ACCESS_TOKEN", "AGENTGATE_FIGMA_HOME":
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

// Selecting an account also selects its authentication source. Ambient API
// keys/OAuth tokens must not charge a different account or switch providers.
func workerEnvironment(call Call, extra ...string) []string {
	env := environment()
	if call.Account.Home != "" {
		filtered := make([]string, 0, len(env))
		for _, entry := range env {
			name, _, _ := strings.Cut(entry, "=")
			switch name {
			case "OPENAI_API_KEY", "OPENAI_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
				"CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY":
				continue
			}
			filtered = append(filtered, entry)
		}
		env = filtered
	}
	return append(env, extra...)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
