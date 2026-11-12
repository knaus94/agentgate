package dev

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Signal is a mechanical hit on a line the task added: something that is often left behind by a model and
// costs a maintainer later. A signal is a lead for the reviewer, not a verdict: the same text can be right.
type Signal struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// The kinds are deliberately few and literal. Anything that needs judgment — an abstraction with one caller,
// a check for a state that cannot happen — is the reviewer's, on the simplicity axis.
var signalRules = []struct {
	kind    string
	pattern *regexp.Regexp
}{
	{"conflict-marker", regexp.MustCompile(`^(<<<<<<<|>>>>>>>) `)},
	{"placeholder", regexp.MustCompile(`(?i)your code here|implementation goes here|in a real (implementation|app|application|system|world)|not (yet )?implemented|\b(stub|mock|hard-?coded?)\b.{0,20}\bfor now\b|\bfor now\b.{0,20}\b(stub|mock|hard-?coded?)\b`)},
	{"todo", regexp.MustCompile(`(//|#|/\*|\*|--|<!--)\s*(TODO|FIXME|XXX|HACK)\b`)},
	{"debug-output", regexp.MustCompile(`\bconsole\.(log|debug|trace)\(|\bdebugger\s*;|\bvar_dump\(|\bdd\(|\bbinding\.pry\b|\bpdb\.set_trace\(|\bbreakpoint\(\)|\bdbg!\(`)},
	{"disabled-test", regexp.MustCompile(`\b(it|test|describe|context)\.(only|skip|todo)\(|\b(xit|xdescribe|xtest|fit|fdescribe)\(|\bt\.Skip(f|Now)?\(|@pytest\.mark\.(skip|xfail)|@unittest\.skip|@Disabled\b|@Ignore\b`)},
	{"suppression", regexp.MustCompile(`\bas any\b|@ts-ignore|@ts-nocheck|@ts-expect-error|eslint-disable|#\s*type:\s*ignore|#\s*noqa|//\s*nolint|@SuppressWarnings|#\s*pragma: no cover`)},
	{"swallowed-error", regexp.MustCompile(`\bcatch\s*(\([^)]*\))?\s*\{\s*\}|\bexcept\b[^:]*:\s*pass\b`)},
	{"change-narration", regexp.MustCompile(`(?i)(//|#|/\*|\*)\s*(added|removed|changed|updated|modified|fixed|refactored|renamed|moved|new)\b\s*[:(-]|(//|#|/\*|\*).*\b(as requested|per (the )?(review|request|brief|feedback)|was previously|used to be|no longer needed)\b`)},
	{"brace-label", regexp.MustCompile(`\}\s*(//|#)\s*end\b`)},
}

var (
	catchOpens  = regexp.MustCompile(`\bcatch\s*(\([^)]*\))?\s*\{\s*$`)
	exceptOpens = regexp.MustCompile(`^\s*except\b[^:]*:\s*$`)
)

// scannable says whether a file holds code worth scanning: prose, data and generated output are not.
func scannable(path string) bool {
	for _, zone := range []string{"generated/", "dist/", "build/", "vendor/", "node_modules/", "target/", ".next/"} {
		if strings.HasPrefix(path, zone) || strings.Contains(path, "/"+zone) {
			return false
		}
	}
	base := filepath.Base(path)
	if strings.Contains(base, ".min.") || strings.Contains(base, ".gen.") || strings.HasSuffix(base, ".lock") || strings.HasSuffix(base, "-lock.json") {
		return false
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".md", ".mdx", ".txt", ".rst", ".json", ".yaml", ".yml", ".toml", ".svg", ".snap", ".csv", ".lock", ".sum", "":
		return false
	}
	return true
}

// scanSignals looks at the lines the task added to its files. A line that already existed anywhere in the
// file before the task is not the task's: moving code does not raise a signal.
func scanSignals(from, to Snapshot, files []string) []Signal {
	out := []Signal{}
	for _, path := range files {
		if !scannable(path) || to[path] == nil || same(from[path], to[path]) {
			continue
		}
		existing := map[string]int{}
		for _, line := range strings.Split(from[path].text(), "\n") {
			existing[strings.TrimSpace(line)]++
		}
		lines := strings.Split(to[path].text(), "\n")
		for index, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if existing[trimmed] > 0 {
				existing[trimmed]--
				continue
			}
			kind := ""
			for _, rule := range signalRules {
				if rule.pattern.MatchString(line) {
					kind = rule.kind
					break
				}
			}
			if kind == "" && index+1 < len(lines) {
				next := strings.TrimSpace(lines[index+1])
				if (catchOpens.MatchString(line) && next == "}") || (exceptOpens.MatchString(line) && next == "pass") {
					kind = "swallowed-error"
				}
			}
			if kind == "" {
				continue
			}
			if len([]rune(trimmed)) > 160 {
				trimmed = string([]rune(trimmed)[:160]) + "…"
			}
			if out = append(out, Signal{Path: path, Line: index + 1, Kind: kind, Text: trimmed}); len(out) >= 60 {
				return out
			}
		}
	}
	return out
}
