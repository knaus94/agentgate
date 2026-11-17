package dev

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/knaus94/agentgate/internal/config"
)

// A task's environment file holds what its declared checks need and the repository must not: the URL of a
// disposable database, a token of a local service. The plan keeps the path; the values reach the checks and
// never an answer, a log line of the tool or a worker.

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// readEnvFile reads KEY=VALUE lines from a file outside the repository that only its owner can read. Blank
// lines and lines starting with # are skipped; `export ` in front and quotes around a value are allowed. An
// error names a line by its number, never by its content.
func readEnvFile(path string) ([]string, error) {
	path = config.ExpandHome(path)
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%s is not an absolute path", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read: %w", path, err)
	}
	if checkout, err := filepath.EvalSymlinks(base()); err == nil && (real == checkout || strings.HasPrefix(real, checkout+string(filepath.Separator))) {
		return nil, fmt.Errorf("%s lies inside the repository; keep it outside, where nothing commits it", path)
	}
	info, err := os.Stat(real)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s cannot be read: %w", path, err)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file", path)
	case info.Mode().Perm()&0o077 != 0:
		return nil, fmt.Errorf("%s can be read by others (mode %o); `chmod 600` it", path, info.Mode().Perm())
	}
	raw, err := os.ReadFile(real)
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read: %w", path, err)
	}
	var out []string
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for number := 1; scanner.Scan(); number++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, found := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		name = strings.TrimSpace(name)
		if !found || !envName.MatchString(name) {
			return nil, fmt.Errorf("%s, line %d is not NAME=value", path, number)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		out = append(out, name+"="+value)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s cannot be read: %w", path, err)
	}
	return out, nil
}

// envNames are the names of the variables a file sets, for a person to see which ones reach the checks.
func envNames(pairs []string) []string {
	names := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		name, _, _ := strings.Cut(pair, "=")
		names = append(names, name)
	}
	return names
}
