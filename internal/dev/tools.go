package dev

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

// Workers run without the user's MCP servers and plugins, so that a result does not depend on what one
// machine has installed. A repository can hand chosen roles chosen tools all the same: a code graph for the
// scout, a workspace index for the architects. Those servers are named in the configuration (`worker_mcp`),
// started inside the worker's sandbox, and limited to the listed tools.

type refreshResult struct {
	once sync.Once
	err  error
}

var refreshed sync.Map // repository + operation + server config -> completed refresh

// repositoryWritableDirectory resolves a cache grant without following any
// repository-controlled symlink. A lexical "inside the repo" check is not
// sufficient: bubblewrap binds the target of a symlink, including an ancestor.
// Missing directories are never created or granted implicitly.
func repositoryWritableDirectory(checkout, relative string) (string, error) {
	clean := filepath.Clean(relative)
	if relative == "" || filepath.IsAbs(relative) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.ContainsRune(relative, '\x00') {
		return "", errors.New("writable directory must be a subdirectory of the checkout")
	}
	root, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		return "", fmt.Errorf("resolve checkout: %w", err)
	}
	current := root
	for _, component := range strings.Split(clean, string(filepath.Separator)) {
		if component == ".git" {
			return "", errors.New("git metadata cannot be granted writable")
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return "", fmt.Errorf("inspect writable directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("writable directory contains a symlink")
		}
		if !info.IsDir() {
			return "", errors.New("writable path is not a directory")
		}
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", fmt.Errorf("resolve writable directory: %w", err)
	}
	inside, err := filepath.Rel(root, resolved)
	if err != nil || inside == "." || inside == ".." || filepath.IsAbs(inside) || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || resolved != current {
		return "", errors.New("writable directory resolves outside its approved checkout path")
	}
	return resolved, nil
}

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
	operation := "direct"
	if active := ops.Status(home()); active != nil && active.Live {
		operation = active.ID
	}
	for _, name := range names {
		server := cfg.WorkerMCP[name]
		if !slices.Contains(server.RolesOrDefault(), role) {
			continue
		}
		// Concurrent readers wait for the same refresh. A failed refresh cannot
		// masquerade as a prepared index; that optional server is omitted.
		if len(server.Refresh) > 0 {
			key := digest(map[string]any{"cwd": cwd, "operation": operation, "name": name, "config": server})
			value, _ := refreshed.LoadOrStore(key, &refreshResult{})
			refresh := value.(*refreshResult)
			refresh.once.Do(func() {
				refresh.err = ops.Run(exec.Command(server.Refresh[0], server.Refresh[1:]...), seconds(server.RefreshTimeoutS))
			})
			if refresh.err != nil {
				lines = append(lines, fmt.Sprintf("- MCP server `%s` is unavailable: its refresh failed (%v). Read repository source directly; do not trust a stale index.", name, refresh.err))
				continue
			}
		}
		servers[name] = provider.MCPServer{Command: server.Command, Args: server.Args, Env: server.Env, Tools: server.Tools}
		for _, path := range server.Writable {
			if directory, err := repositoryWritableDirectory(cwd, path); err == nil {
				writable = append(writable, directory)
			} else {
				lines = append(lines, fmt.Sprintf("- MCP server `%s`: writable path %q was not granted (%v).", name, path, err))
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
	if len(lines) == 0 {
		return nil, nil, ""
	}
	return servers, writable, "\nOptional repository tools and their preparation status. Use available tools where they answer the question, " +
		"and verify in source code what you rely on:\n" + strings.Join(lines, "\n") + "\n"
}
