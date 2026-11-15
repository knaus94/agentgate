package dev

import (
	"errors"
	"fmt"
	"os"
	"path"
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

// direct reports whether the entry names the path itself.
func (p pattern) direct(path string) bool {
	if p.glob == nil {
		return p.raw == path
	}
	return p.glob.MatchString(path)
}

// match reports whether the entry owns the path: it names the path, or a directory above it. A generator
// that names its output directory, `migrations/*_orders` for one, owns the files it writes inside.
func (p pattern) match(path string) bool {
	if p.direct(path) {
		return true
	}
	if !p.namesDirectory() {
		return false
	}
	for cut := strings.LastIndex(path, "/"); cut > 0; cut = strings.LastIndex(path[:cut], "/") {
		if p.direct(path[:cut]) {
			return true
		}
	}
	return false
}

// namesDirectory reports whether the last segment of an entry can name a directory. `*`, `**` and a name with
// an extension (`*.ts`, `schema.sql`) name files.
func (p pattern) namesDirectory() bool {
	last := p.raw[strings.LastIndex(p.raw, "/")+1:]
	if last == "*" || last == "**" {
		return false
	}
	dot := strings.LastIndex(last, ".")
	return dot <= 0 || dot == len(last)-1 || globMeta.MatchString(last[dot+1:])
}

func matchAny(patterns []pattern, path string) bool {
	return slices.ContainsFunc(patterns, func(p pattern) bool { return p.match(path) })
}

// overlap reports whether two scope entries could own the same path. It is exact: two entries overlap when one
// path exists that both own, directly or through a directory one of them names.
func overlap(a, b pattern) bool {
	for _, left := range a.forms() {
		for _, right := range b.forms() {
			if intersect(left, right, 0, 0, map[[2]int]bool{}) {
				return true
			}
		}
	}
	return false
}

// segment is one path segment of an entry: a literal name, a single-segment pattern, or `**`.
type segment struct {
	text    string
	literal bool
}

func (s segment) any() bool { return !s.literal && s.text == "**" }

// forms are the segment lists of the paths an entry owns: the entry itself, and what is below it when it
// can name a directory.
func (p pattern) forms() [][]segment {
	var parts []string
	if p.glob == nil {
		parts = strings.Split(p.raw, "/")
	} else {
		parts = globParts(p.raw)
	}
	base := make([]segment, len(parts))
	for index, part := range parts {
		base[index] = segment{text: part, literal: p.glob == nil}
	}
	out := [][]segment{base}
	if p.namesDirectory() {
		out = append(out, append(slices.Clone(base), segment{text: "*"}, segment{text: "**"}))
	}
	return out
}

// intersect reports whether one sequence of segments matches both lists from positions i and j.
func intersect(a, b []segment, i, j int, seen map[[2]int]bool) bool {
	if seen[[2]int{i, j}] {
		return false
	}
	seen[[2]int{i, j}] = true
	if i == len(a) && j == len(b) {
		return true
	}
	// `**` may stand for no segment.
	if i < len(a) && a[i].any() && intersect(a, b, i+1, j, seen) {
		return true
	}
	if j < len(b) && b[j].any() && intersect(a, b, i, j+1, seen) {
		return true
	}
	if i == len(a) || j == len(b) {
		return false
	}
	// Or both consume one segment: `**` stays where it is, any other segment moves on.
	nextI, nextJ := i+1, j+1
	if a[i].any() {
		nextI = i
	}
	if b[j].any() {
		nextJ = j
	}
	if !a[i].any() && !b[j].any() && !segmentsMeet(a[i], b[j]) {
		return false
	}
	return intersect(a, b, nextI, nextJ, seen)
}

// token is one element of a single-segment pattern: `*`, or a pattern of one character.
type token struct {
	star    bool
	text    string // for path.Match on one character
	literal rune
	exact   bool
}

func tokens(s segment) []token {
	if s.literal {
		var out []token
		for _, char := range s.text {
			out = append(out, token{literal: char, exact: true})
		}
		return out
	}
	var out []token
	runes := []rune(s.text)
	for index := 0; index < len(runes); index++ {
		switch runes[index] {
		case '*':
			out = append(out, token{star: true})
		case '\\':
			if index+1 < len(runes) {
				index++
			}
			out = append(out, token{literal: runes[index], exact: true})
		case '[':
			end := index + 1
			if end < len(runes) && runes[end] == '^' {
				end++
			}
			if end < len(runes) && runes[end] == ']' {
				end++
			}
			for end < len(runes) && runes[end] != ']' {
				if runes[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(runes) {
				end = len(runes) - 1
			}
			out = append(out, token{text: string(runes[index : end+1])})
			index = end
		case '?':
			out = append(out, token{text: "?"})
		default:
			out = append(out, token{literal: runes[index], exact: true})
		}
	}
	return out
}

func (t token) accepts(char rune) bool {
	switch {
	case char == '/':
		return false
	case t.star:
		return true
	case t.exact:
		return char == t.literal
	}
	ok, _ := path.Match(t.text, string(char))
	return ok
}

// candidates are characters that decide whether two one-character patterns share a character: every
// character they name, its neighbours (the edges of a negated class), and characters neither names.
func candidates(a, b token) []rune {
	out := []rune{'a', 'Z', '0', '_', '-', '.', '~', 'é', '中', '\U0010FFFD'}
	for _, t := range []token{a, b} {
		for _, char := range []rune(t.text + string(t.literal)) {
			out = append(out, char-1, char, char+1)
		}
	}
	return out
}

// segmentsMeet reports whether a non-empty name matches both single-segment patterns.
func segmentsMeet(a, b segment) bool {
	left, right := tokens(a), tokens(b)
	type state struct {
		i, j     int
		consumed bool
	}
	seen := map[state]bool{}
	var walk func(state) bool
	walk = func(s state) bool {
		if seen[s] {
			return false
		}
		seen[s] = true
		if s.i == len(left) && s.j == len(right) {
			return s.consumed
		}
		if s.i < len(left) && left[s.i].star && walk(state{s.i + 1, s.j, s.consumed}) {
			return true
		}
		if s.j < len(right) && right[s.j].star && walk(state{s.i, s.j + 1, s.consumed}) {
			return true
		}
		if s.i == len(left) || s.j == len(right) {
			return false
		}
		shared := slices.ContainsFunc(candidates(left[s.i], right[s.j]), func(char rune) bool {
			return left[s.i].accepts(char) && right[s.j].accepts(char)
		})
		if !shared {
			return false
		}
		next := state{s.i + 1, s.j + 1, true}
		if left[s.i].star {
			next.i = s.i
		}
		if right[s.j].star {
			next.j = s.j
		}
		return walk(next)
	}
	return walk(state{})
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
	if directory && !strings.HasSuffix(clean, "/**") {
		if !patterns {
			return "", fmt.Errorf("an exact file path is needed here, not a directory: %q", value)
		}
		return clean + "/**", nil // a directory, named exactly or by a pattern, owns everything below it
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
