package dev

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
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
//
// A staged plan is mapped stage by stage, in parallel, and the maps are joined: one scout does not get through
// the whole of a large specification in its time, and the first stage later finds its own map already made.
func scout(resolved *Resolved, subs *substitutions) (string, string) {
	plan := resolved.Plan
	if resolved.ModeConfig().Scout == "" || !plan.staged() {
		return scoutPlan(resolved, plan, subs)
	}
	packs, notes := make([]*contextPack, len(plan.Stages)), make([]string, len(plan.Stages))
	parallel(len(plan.Stages), resolved.Config.MaxParallel, func(index int) error {
		path, note := scoutPlan(resolved, plan.stageView(index, nil), subs)
		if raw, err := os.ReadFile(path); path != "" && (err != nil || json.Unmarshal(raw, &packs[index]) != nil) {
			packs[index], note = nil, "the context pack could not be read"
		}
		notes[index] = note
		return nil
	})
	if slices.Contains(notes, "cancelled") {
		return "", "cancelled"
	}
	joined, note := joinPacks(plan.Stages, packs, notes)
	if joined == nil {
		return "", note
	}
	// Named by what it holds, so a gate keeps reading the pack it was given.
	path := filepath.Join(checkoutDir(""), "scout", digest(joined)[:32], "pack.json")
	if err := writePacket(path, joined); err != nil {
		return "", err.Error()
	}
	return path, note
}

// joinPacks makes one pack of the stages' maps. A stage whose scout failed is named in the note and in the
// pack's unknowns, and the rest is still used.
func joinPacks(stages []Stage, packs []*contextPack, notes []string) (*contextPack, string) {
	out := &contextPack{}
	var summaries, failed []string
	at := map[string]int{}
	add := func(to *[]string, lines []string) {
		for _, line := range lines {
			if !slices.Contains(*to, line) {
				*to = append(*to, line)
			}
		}
	}
	for index, pack := range packs {
		if pack == nil {
			failed = append(failed, fmt.Sprintf("stage %s: %s", stages[index].Name, notes[index]))
			continue
		}
		if out.Route.Alias == "" {
			out.Route = pack.Route
		}
		summaries = append(summaries, stages[index].Name+": "+pack.Summary)
		for _, file := range pack.Files {
			if seen, ok := at[file.Path]; ok {
				add(&out.Files[seen].Symbols, file.Symbols)
				continue
			}
			at[file.Path] = len(out.Files)
			out.Files = append(out.Files, file)
		}
		for _, caller := range pack.Callers {
			if !slices.Contains(out.Callers, caller) {
				out.Callers = append(out.Callers, caller)
			}
		}
		add(&out.Invariants, pack.Invariants)
		add(&out.Conventions, pack.Conventions)
		add(&out.Unknowns, pack.Unknowns)
	}
	note := strings.Join(failed, "; ")
	if len(summaries) == 0 {
		return nil, note
	}
	out.Summary = strings.Join(summaries, "\n")
	for _, line := range failed {
		out.Unknowns = append(out.Unknowns, "not mapped — "+line)
	}
	return out, note
}

// scoutPlan maps one plan, or one stage seen as a plan.
func scoutPlan(resolved *Resolved, plan *Plan, subs *substitutions) (string, string) {
	spec := resolved.ModeConfig().Scout
	if spec == "" {
		return "", ""
	}
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
