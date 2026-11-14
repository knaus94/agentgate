package dev

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

func capture(files []string) (Snapshot, error) {
	if _, err := cleanPaths(files); err != nil {
		return nil, err
	}
	out := Snapshot{}
	for _, name := range files {
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			out[name] = nil
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file", name)
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out[name] = &FileState{Bytes: base64.StdEncoding.EncodeToString(raw), Mode: uint32(info.Mode().Perm())}
	}
	return out, nil
}

func (g *Gate) current() (Snapshot, error) {
	files := append(slices.Clone(g.Files), g.Context...)
	return capture(append(files, guidance(g.Files)...))
}

func (g *Gate) contentDigest() (string, Snapshot, error) {
	now, err := g.current()
	return digest(now), now, err
}

func (state *FileState) text() string {
	if state == nil {
		return ""
	}
	raw, _ := base64.StdEncoding.DecodeString(state.Bytes)
	return string(raw)
}

func same(a, b *FileState) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return string(out), err
}

func entry(name string) string {
	value, err := fingerprint(name)
	if err != nil {
		return "unreadable:" + err.Error()
	}
	return value
}

func fingerprint(name string) (string, error) {
	info, err := os.Lstat(name)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "deleted", nil
	case err != nil:
		return "", fmt.Errorf("inspect %s: %w", name, err)
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(name)
		return "link:" + target, err
	case !info.Mode().IsRegular():
		return "", fmt.Errorf("%s is not a regular file", name)
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]) + ":" + strconv.FormatUint(uint64(info.Mode().Perm()), 8), nil
}

// manifest fingerprints every file that differs from HEAD, tracked or not.
func manifest() (map[string]string, error) {
	changed, err := git("diff", "--name-only", "-z", "HEAD")
	if err != nil {
		// A repository without commits has no HEAD; every tracked and untracked file counts as changed.
		changed, _ = git("ls-files", "-z")
	}
	untracked, err := git("ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, errors.New("run inside a git repository")
	}
	out := map[string]string{}
	for _, name := range strings.Split(changed+untracked, "\x00") {
		if name != "" && !ownNotes(name) { // saved plans are notes about the work, not the work
			value, err := fingerprint(name)
			if err != nil {
				return nil, err
			}
			out[name] = value
		}
	}
	return out, nil
}

// repositoryDigest binds a check with unspecified inputs to the whole visible
// repository: committed content, tracked edits and untracked files. This avoids
// reusing a passing task check after a sibling dependency changed.
func repositoryDigest() (string, error) {
	head, err := git("rev-parse", "--verify", "HEAD")
	if err != nil {
		head = "unborn"
	}
	files, err := manifest()
	if err != nil {
		return "", err
	}
	return digest(map[string]any{"head": strings.TrimSpace(head), "files": files}), nil
}

// ownNotes says whether a path is the tool's own directory in the repository, under its present or earlier name.
func ownNotes(name string) bool {
	for _, dir := range []string{".agentgate", ".claude-dev"} {
		if name == dir || strings.HasPrefix(name, dir+"/") {
			return true
		}
	}
	return false
}

// auditScope records changes outside the task's files. They are reported, not fatal:
// parallel sessions share the checkout and the lead inspects the diff.
func (g *Gate) auditScope() error {
	head, _ := git("rev-parse", "HEAD")
	head = strings.TrimSpace(head)
	now, err := manifest()
	if err != nil {
		return err
	}
	if g.HeadBefore != "" && head != g.HeadBefore {
		if exec.Command("git", "merge-base", "--is-ancestor", g.HeadBefore, head).Run() != nil {
			return errors.New("HEAD was rewritten during the task; the gate cannot vouch for its baseline")
		}
		names, _ := git("diff", "--name-only", "-z", g.HeadBefore, head)
		for _, name := range strings.Split(names, "\x00") {
			if name != "" {
				now[name] = entry(name) // committed files leave the HEAD-relative manifest; compare their content
			}
		}
	}
	var foreign []string
	for name := range mergeKeys(g.Manifest, now) {
		if g.Manifest[name] != now[name] && !slices.Contains(g.WriteFiles, name) {
			foreign = append(foreign, name)
		}
	}
	g.Foreign = foreign
	sort.Strings(g.Foreign)
	return nil
}

func mergeKeys(a, b map[string]string) map[string]bool {
	out := map[string]bool{}
	for key := range a {
		out[key] = true
	}
	for key := range b {
		out[key] = true
	}
	return out
}

// guidance adds the AGENTS.md / CLAUDE.md files above the owned files, so a rule change invalidates the review.
func guidance(files []string) []string {
	var out []string
	for _, file := range files {
		for dir := filepath.Dir(file); ; dir = filepath.Dir(dir) {
			for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
				path := filepath.ToSlash(filepath.Join(dir, name))
				if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
					out = append(out, path)
				}
			}
			if dir == "." || dir == "/" {
				break
			}
		}
	}
	return out
}
