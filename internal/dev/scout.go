package dev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/knaus94/claude-dev/internal/assets"
	"github.com/knaus94/claude-dev/internal/config"
	"github.com/knaus94/claude-dev/internal/ops"
	"github.com/knaus94/claude-dev/internal/provider"
)

type packFile struct {
	Path    string   `json:"path"`
	Role    string   `json:"role"`
	Symbols []string `json:"symbols"`
}

type packCaller struct {
	Symbol string `json:"symbol"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Note   string `json:"note"`
}

// contextPack is the map of the code a task touches, gathered once by a cheap model and handed to every
// later worker, so the expensive ones spend their effort on reasoning instead of on reading.
type contextPack struct {
	Summary     string       `json:"summary"`
	Files       []packFile   `json:"files"`
	Callers     []packCaller `json:"callers"`
	Invariants  []string     `json:"invariants"`
	Conventions []string     `json:"conventions"`
	Unknowns    []string     `json:"unknowns"`
	Route       config.Route `json:"scouted_by"`
}

var scoutSchema = object(map[string]any{"summary": text,
	"files":      items(map[string]any{"path": text, "role": text, "symbols": list(text)}),
	"callers":    items(map[string]any{"symbol": text, "path": text, "line": map[string]any{"type": "integer"}, "note": text}),
	"invariants": list(text), "conventions": list(text), "unknowns": list(text)})

const scoutTimeout = 600

// scout returns the path of the task's context pack, gathering it on first use. The pack is keyed by the
// contract, so the design pass and the writers of the same task share one. A mode without a scout route
// returns "". A scout that fails is reported and skipped: a cheap convenience never blocks the task.
func scout(resolved *Resolved, subs *substitutions) (string, string) {
	spec := resolved.ModeConfig().Scout
	if spec == "" {
		return "", ""
	}
	plan := resolved.Plan
	key := digest(map[string]any{"brief": plan.Brief, "files": plan.Files(), "context": plan.Context, "route": spec})
	dir := filepath.Join(checkoutDir(""), "scout", key[:32]) // shared by the sessions of a checkout, like designs
	path := filepath.Join(dir, "pack.json")
	if _, err := os.Stat(path); err == nil {
		return path, ""
	}
	packet := filepath.Join(dir, "packet.json")
	if err := writePacket(packet, map[string]any{"brief": plan.Brief, "files": plan.Files(), "streams": plan.Streams, "context": plan.Context}); err != nil {
		return "", err.Error()
	}
	tools, toolDirs, toolNote := workerTools(resolved.Config, "scout")
	prompt := assets.Prompt("scout") + "\nRead the packet at " + packet + ".\n" + toolNote
	result, used, err := subs.invoke(resolved.Config, provider.Call{Route: resolved.Config.MustRoute(spec), Prompt: prompt,
		Dir: filepath.Join(dir, "run"), Schema: scoutSchema, Timeout: seconds(scoutTimeout), ReadDirs: []string{dir}, MCP: tools, Writable: toolDirs}, nil)
	if err != nil {
		if ops.IsCancelled(err) {
			return "", "cancelled"
		}
		return "", "the scout did not finish: " + err.Error()
	}
	pack := contextPack{}
	if err := json.Unmarshal([]byte(result.Answer), &pack); err != nil || strings.TrimSpace(pack.Summary) == "" || len(pack.Files) == 0 {
		return "", "the scout returned no usable context pack"
	}
	pack.Route = used
	if err := ops.WriteJSON(path, pack); err != nil {
		return "", err.Error()
	}
	return path, ""
}

// packDirs is what a sandboxed worker must be able to read to open the pack.
func packDirs(path string) []string {
	if path == "" {
		return nil
	}
	return []string{filepath.Dir(path)}
}
