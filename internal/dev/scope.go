package dev

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// A stream owns exact files, directories and glob patterns. What it actually changed is read from the
// difference between two trees; the declared scope decides who may write a path and who a finding goes to.

// pattern is one entry of a scope.
type pattern struct {
	raw    string
	glob   *fileGlob // nil for an exact path
	prefix string    // the directories before the first wildcard, with a trailing slash; "" at the root
}

func compilePattern(raw string) pattern {
	out := pattern{raw: raw}
	location := globMeta.FindStringIndex(raw)
	if location == nil {
		return out
	}
	out.glob, _ = compileGlob(raw)
	if cut := strings.LastIndex(raw[:location[0]], "/"); cut >= 0 {
		out.prefix = raw[:cut+1]
	}
	return out
}

func compilePatterns(values []string) []pattern {
	out := make([]pattern, 0, len(values))
	for _, value := range values {
		out = append(out, compilePattern(value))
	}
	return out
}

func (p pattern) match(path string) bool {
	if p.glob == nil {
		return p.raw == path
	}
	return p.glob.MatchString(path)
}

func matchAny(patterns []pattern, path string) bool {
	return slices.ContainsFunc(patterns, func(p pattern) bool { return p.match(path) })
}

// extension is the fixed suffix of a pattern whose last segment is "*.ext", or "".
func (p pattern) extension() string {
	last := p.raw[strings.LastIndex(p.raw, "/")+1:]
	if rest, ok := strings.CutPrefix(last, "*."); ok && !globMeta.MatchString(rest) {
		return "." + rest
	}
	return ""
}

// overlap reports whether two scope entries could name the same path. It is conservative: two patterns under
// nested directories are taken to overlap unless their fixed extensions differ.
func overlap(a, b pattern) bool {
	switch {
	case a.glob == nil && b.glob == nil:
		return a.raw == b.raw
	case a.glob == nil:
		return b.match(a.raw)
	case b.glob == nil:
		return a.match(b.raw)
	}
	if !strings.HasPrefix(a.prefix, b.prefix) && !strings.HasPrefix(b.prefix, a.prefix) {
		return false
	}
	left, right := a.extension(), b.extension()
	return left == "" || right == "" || left == right
}

// firstOverlap names the first pair of entries from two scopes that could collide.
func firstOverlap(a, b []string) (string, string, bool) {
	left, right := compilePatterns(a), compilePatterns(b)
	for _, one := range left {
		for _, other := range right {
			if overlap(one, other) {
				return one.raw, other.raw, true
			}
		}
	}
	return "", "", false
}

// normalizeScope validates the entries of a scope and gives them one spelling: an exact repository-relative
// path, or a pattern. A directory, written with a trailing slash or found on disk, owns everything below it.
func normalizeScope(values []string) ([]string, error) {
	var out []string
	var problems []string
	for _, value := range values {
		entry, err := normalizeEntry(value, true)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		out = append(out, entry)
	}
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	sort.Strings(out)
	return slices.Compact(out), nil
}

// cleanPaths validates exact repository-relative file paths (existing or not yet created): context files,
// files a decision names, files of a consultation.
func cleanPaths(values []string) ([]string, error) {
	var out []string
	for _, value := range values {
		entry, err := normalizeEntry(value, false)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	sort.Strings(out)
	return slices.Compact(out), nil
}

func normalizeEntry(value string, patterns bool) (string, error) {
	directory := strings.HasSuffix(value, "/")
	clean := filepath.ToSlash(filepath.Clean(value))
	parts := strings.Split(clean, "/")
	if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\x00\n") || filepath.IsAbs(value) || slices.Contains(parts, "..") ||
		slices.Contains(parts, ".git") || clean == "." {
		return "", fmt.Errorf("scope takes repository-relative paths: %q", value)
	}
	if strings.Contains(value, ":") || strings.Contains(value, " — ") {
		return "", fmt.Errorf("scope takes plain repository-relative paths; a citation with a line number or a comment belongs in the brief: %q", value)
	}
	wild := globMeta.MatchString(clean)
	if wild && !patterns {
		return "", fmt.Errorf("an exact file path is needed here, not a pattern: %q", value)
	}
	if wild {
		if _, err := compileGlob(clean); err != nil {
			return "", fmt.Errorf("invalid scope pattern %q: %v", value, err)
		}
	}
	// No component that exists may be a symbolic link: a writer confined to a path must not reach through it.
	fixed := clean
	if wild {
		fixed = strings.TrimSuffix(compilePattern(clean).prefix, "/")
	}
	for current := fixed; current != "." && current != ""; current = filepath.ToSlash(filepath.Dir(current)) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect scope %q: %v", value, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("scope must not pass through a symbolic link: %q", value)
		}
		if current == clean && !wild {
			switch {
			case info.IsDir():
				directory = true
			case !info.Mode().IsRegular():
				return "", fmt.Errorf("scope names regular files, directories or patterns: %q", value)
			case directory:
				return "", fmt.Errorf("%q is a file, not a directory", value)
			}
		}
	}
	if directory && !wild {
		if !patterns {
			return "", fmt.Errorf("an exact file path is needed here, not a directory: %q", value)
		}
		return clean + "/**", nil
	}
	return clean, nil
}

// exactPaths are the entries of a scope that name one file.
func exactPaths(scope []string) []string {
	var out []string
	for _, value := range scope {
		if !globMeta.MatchString(value) {
			out = append(out, value)
		}
	}
	return out
}

func subtract(values, remove []string) []string {
	var out []string
	for _, value := range values {
		if !slices.Contains(remove, value) {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}
