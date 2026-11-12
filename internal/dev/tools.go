package dev

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/knaus94/claude-dev/internal/config"
	"github.com/knaus94/claude-dev/internal/ops"
	"github.com/knaus94/claude-dev/internal/provider"
)

// Workers run without the user's MCP servers and plugins, so that a result does not depend on what one
// machine has installed. A repository can hand chosen roles chosen tools all the same: a code graph for the
// scout, a workspace index for the architects. Those servers are named in the configuration (`worker_mcp`),
// started inside the worker's sandbox, and limited to the listed tools.

var refreshed sync.Map // server name -> true once its index was refreshed in this process

// workerTools returns the MCP servers a role gets, the directories they must be able to write, and the lines
// that tell the worker about them.
func workerTools(cfg *config.Config, role string) (map[string]provider.MCPServer, []string, string) {
	names := make([]string, 0, len(cfg.WorkerMCP))
	for name := range cfg.WorkerMCP {
		names = append(names, name)
	}
	sort.Strings(names)
	servers := map[string]provider.MCPServer{}
	var lines, writable []string
	cwd, _ := os.Getwd()
	for _, name := range names {
		server := cfg.WorkerMCP[name]
		if !slices.Contains(server.RolesOrDefault(), role) {
			continue
		}
		// An index is brought up to date once per operation, before its first reader. A refresh that fails
		// leaves an older index, which is still better than none.
		if len(server.Refresh) > 0 {
			if _, done := refreshed.LoadOrStore(name, true); !done {
				_ = ops.Run(exec.Command(server.Refresh[0], server.Refresh[1:]...), seconds(server.RefreshTimeoutS))
			}
		}
		servers[name] = provider.MCPServer{Command: server.Command, Args: server.Args, Env: server.Env, Tools: server.Tools}
		for _, path := range server.Writable {
			// Only a directory that exists can be opened for writing in the sandbox.
			if info, err := os.Stat(filepath.Join(cwd, path)); err == nil && info.IsDir() {
				writable = append(writable, filepath.Join(cwd, path))
			}
		}
		line := fmt.Sprintf("- MCP server `%s`", name)
		if len(server.Tools) > 0 {
			line += " (tools: " + strings.Join(server.Tools, ", ") + ")"
		}
		if server.Hint != "" {
			line += ": " + server.Hint
		}
		lines = append(lines, line)
	}
	if len(servers) == 0 {
		return nil, nil, ""
	}
	return servers, writable, "\nBeyond the usual tools you have these, prepared for this repository. Prefer them to searching by hand " +
		"where they answer the question, and verify in the code what you rely on:\n" + strings.Join(lines, "\n") + "\n"
}
