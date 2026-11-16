package dev

import (
	"path/filepath"

	"github.com/knaus94/agentgate/internal/config"
)

// The preparation worker returns the plan as a structure, not as a string of JSON: both providers enforce
// the schema of an answer, so a plan that names an unknown check kind or leaves out its streams cannot be
// produced in the first place. Strict schemas require every field; an optional one is nullable.

func nullable(schema map[string]any) map[string]any {
	return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
}

var integer = map[string]any{"type": "integer"}

var assessmentSchema = object(map[string]any{"size": enum("trivial", "ordinary", "complex"), "risks": list(enum(Risks...)),
	"irreversible": boolean, "uncertainty": enum("low", "material"), "evidence": text})

var streamSchema = object(map[string]any{"name": text, "domain": enum("backend", "frontend"), "files": list(text), "after": list(text),
	"brief": text, "effort": nullable(enum(config.Efforts...)), "done": boolean})

var checkSchema = object(map[string]any{"argv": list(text), "kind": enum(checkKinds...), "inputs": list(text), "timeout_s": integer,
	"baseline": boolean, "no_reuse": boolean})

var contextPackSchema = object(map[string]any{"summary": text,
	"files":      items(map[string]any{"path": text, "role": text, "symbols": list(text)}),
	"callers":    items(map[string]any{"symbol": text, "path": text, "line": integer, "note": text}),
	"invariants": list(text), "conventions": list(text), "unknowns": list(text)})

var planSchema = object(map[string]any{
	"brief":        text,
	"assessment":   nullable(assessmentSchema),
	"requirements": items(map[string]any{"id": text, "text": text, "method": nullable(enum("code", "check", "browser"))}),
	"stages": items(map[string]any{"name": text, "goal": text, "acceptance": list(text), "covers": list(text),
		"streams": list(streamSchema), "checks": list(checkSchema), "assessment": nullable(assessmentSchema)}),
	"streams":       list(streamSchema),
	"context":       list(text),
	"checks":        list(checkSchema),
	"checks_waiver": nullable(text),
	"generated":     list(text),
	"ui":            nullable(object(map[string]any{"summary": nullable(text), "criteria": list(text), "preserve": list(text), "skip": nullable(text)})),
	"visual": nullable(object(map[string]any{"enabled": boolean, "reason": text,
		"scenarios": items(map[string]any{"id": text, "screen": text, "state": text, "viewport": text, "role": text, "expected": text})})),
	"design": items(map[string]any{"figma": nullable(text), "name": nullable(text), "screen": nullable(text), "url": nullable(text), "root": nullable(text),
		"stream": text, "viewport": nullable(text), "device": nullable(enum("desktop", "mobile")), "locale": nullable(text),
		"steps":   items(map[string]any{"click": nullable(text), "fill": nullable(text), "value": nullable(text), "wait": nullable(text)}),
		"compare": nullable(enum("measure", "review"))}),
	"update": nullable(text),
	"rework": list(text),
})

var prepareSchema = object(map[string]any{
	"status":    enum("READY", "BLOCKED"),
	"name":      nullable(text),
	"summary":   text,
	"questions": items(map[string]any{"id": text, "question": text, "options": list(text), "recommended": text}),
	"setup":     list(text),
	// A preparation that needs more of a Figma file than the catalogue it was given asks for it; the tool reads it.
	"design_requests": items(map[string]any{"node": text, "depth": integer}),
	"plan":            nullable(planSchema),
	"context_pack":    nullable(contextPackSchema),
})

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

// contextPack is the map of the code a task touches: files and their roles, callers, invariants, conventions.
// The preparation worker draws it while it plans, and writers and reviewers of every stage start from it.
type contextPack struct {
	Summary     string       `json:"summary"`
	Files       []packFile   `json:"files"`
	Callers     []packCaller `json:"callers"`
	Invariants  []string     `json:"invariants"`
	Conventions []string     `json:"conventions"`
	Unknowns    []string     `json:"unknowns"`
	Route       config.Route `json:"mapped_by"`
}

func (pack *contextPack) usable() bool {
	return pack != nil && pack.Summary != "" && len(pack.Files) > 0
}

// packDirs is what a sandboxed worker must be able to read to open the pack.
func packDirs(path string) []string {
	if path == "" {
		return nil
	}
	return []string{filepath.Dir(path)}
}
