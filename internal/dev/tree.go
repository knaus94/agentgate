package dev

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The before-image of a task is a git tree of the whole working tree: the user's uncommitted and untracked
// work exactly as it was, minus what git ignores. Every question about change — what a stage did, what a
// correction did, what a reviewer reads, what a file held before — is a comparison of two trees.
//
// Trees are written to a private object directory inside the task's state. The repository's own objects are
// read through git's alternates mechanism; its index, objects and refs are never written.

// FileChange is one path that differs between two trees.
type FileChange struct {
	Path    string `json:"path"`
	Status  string `json:"status"` // A, M or D
	OldMode uint32 `json:"old_mode,omitempty"`
	NewMode uint32 `json:"new_mode,omitempty"`
	OldBlob string `json:"-"`
	NewBlob string `json:"-"`
}

// treeGit runs git against the private object directory. Path arguments are literal, never patterns.
func treeGit(store string, extraEnv []string, args ...string) ([]byte, error) {
	objects, err := exec.Command("git", "rev-parse", "--git-path", "objects").Output()
	if err != nil {
		return nil, errors.New("run inside a git repository")
	}
	repository, err := filepath.Abs(strings.TrimSpace(string(objects)))
	if err != nil {
		return nil, err
	}
	alternates := repository
	if inherited := os.Getenv("GIT_ALTERNATE_OBJECT_DIRECTORIES"); inherited != "" {
		alternates += string(os.PathListSeparator) + inherited
	}
	cmd := exec.Command("git", args...)
	cmd.Env = append(append(os.Environ(), "GIT_OBJECT_DIRECTORY="+store, "GIT_ALTERNATE_OBJECT_DIRECTORIES="+alternates,
		"GIT_LITERAL_PATHSPECS=1"), extraEnv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("git %s: %s", args[0], lastLine(strings.TrimSpace(stderr.String())))
	}
	return out, nil
}

// snapshotTree records the working tree as it is now and returns its tree id. force names ignored files
// that a stream owns by their exact path: an owned file is evidence even when git would not track it.
func snapshotTree(store string, force []string) (string, error) {
	if err := os.MkdirAll(store, 0o700); err != nil {
		return "", err
	}
	index := filepath.Join(store, "index-"+newID()[:12])
	defer os.Remove(index)
	defer os.Remove(index + ".lock")
	// Starting from the repository's index keeps git's stat cache: unchanged files are not hashed again.
	if current, err := exec.Command("git", "rev-parse", "--git-path", "index").Output(); err == nil {
		if source, err := os.Open(strings.TrimSpace(string(current))); err == nil {
			info, statErr := source.Stat()
			target, createErr := os.OpenFile(index, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if createErr != nil {
				source.Close()
				return "", createErr
			}
			_, copyErr := io.Copy(target, source)
			source.Close()
			if err := errors.Join(copyErr, target.Close()); err != nil {
				return "", err
			}
			// git trusts the stat data of an entry only when the index file is newer than the entry; a file
			// edited in the same second as the index was written is compared by content. The copy must keep the
			// time of the original, or a same-size edit made in that second would be read from the cache as
			// unchanged. Without that time there is no cache to trust: every file is hashed.
			if statErr != nil || os.Chtimes(index, info.ModTime(), info.ModTime()) != nil {
				if err := os.Remove(index); err != nil {
					return "", err
				}
			}
		}
	}
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := treeGit(store, env, "add", "-A", "--", "."); err != nil {
		return "", fmt.Errorf("record the working tree: %w", err)
	}
	var owned []string
	for _, path := range force {
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			owned = append(owned, path)
		}
	}
	if len(owned) > 0 {
		if _, err := treeGit(store, env, append([]string{"add", "-f", "--"}, owned...)...); err != nil {
			return "", fmt.Errorf("record owned files: %w", err)
		}
	}
	// Saved plans and personal settings are notes about the work, not the work: saving a plan must not make
	// a check result stale.
	if _, err := treeGit(store, env, "rm", "-r", "-q", "--cached", "--ignore-unmatch", "--", ".agentgate", ".claude-dev"); err != nil {
		return "", err
	}
	out, err := treeGit(store, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("record the working tree: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// commitTree is the tree of a commit, or "" when the reference names none.
func commitTree(store, ref string) string {
	out, err := treeGit(store, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{tree}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func parseMode(text string) uint32 {
	value, _ := strconv.ParseUint(text, 8, 32)
	return uint32(value) & 0o777
}

// diffTrees lists every path that differs between two trees, sorted by path. Renames are reported as a
// deletion and an addition: ownership is decided per path.
func diffTrees(store, from, to string) ([]FileChange, error) {
	if from == to {
		return nil, nil
	}
	out, err := treeGit(store, nil, "diff-tree", "-r", "-z", "--raw", "--no-renames", "--no-abbrev", from, to)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(string(out), "\x00")
	var changes []FileChange
	for index := 0; index+1 < len(fields); index += 2 {
		meta := strings.Fields(strings.TrimPrefix(fields[index], ":"))
		if len(meta) != 5 {
			return nil, fmt.Errorf("unexpected tree comparison record %q", fields[index])
		}
		change := FileChange{Path: fields[index+1], Status: meta[4][:1], OldBlob: meta[2], NewBlob: meta[3]}
		if change.Status != "A" {
			change.OldMode = parseMode(meta[0])
		}
		if change.Status != "D" {
			change.NewMode = parseMode(meta[1])
		}
		if change.Status == "T" { // a file that became a link, or the reverse, is a modification of the path
			change.Status = "M"
		}
		changes = append(changes, change)
	}
	sort.Slice(changes, func(a, b int) bool { return changes[a].Path < changes[b].Path })
	return changes, nil
}

// treeFile is the content and mode of a path in a tree; ok is false when the tree has no such file.
func treeFile(store, tree, path string) (content []byte, mode uint32, ok bool) {
	if tree == "" {
		return nil, 0, false
	}
	entry, err := treeGit(store, nil, "ls-tree", "-z", tree, "--", path)
	if err != nil || len(entry) == 0 {
		return nil, 0, false
	}
	header, _, _ := strings.Cut(strings.TrimSuffix(string(entry), "\x00"), "\t")
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[1] != "blob" {
		return nil, 0, false
	}
	content, err = treeGit(store, nil, "cat-file", "blob", fields[2])
	if err != nil {
		return nil, 0, false
	}
	return content, parseMode(fields[0]), true
}

// treeEntries fingerprints the named paths of a tree: what a review or a contract that cites them is bound to.
func treeEntries(store, tree string, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", nil
	}
	out, err := treeGit(store, nil, append([]string{"ls-tree", "-r", "-z", tree, "--"}, paths...)...)
	if err != nil {
		return "", err
	}
	return digest(string(out)), nil
}

// treeFileEntry is one file of a recorded tree: its mode, its object and its path.
type treeFileEntry struct {
	Mode, Object, Path string
}

// listings keeps the files of recently listed trees. A tree never changes, so its listing never goes stale.
var listings = struct {
	sync.Mutex
	trees map[string][]treeFileEntry
}{trees: map[string][]treeFileEntry{}}

// treeListing is every file of a recorded tree, sorted by path.
func treeListing(store, tree string) ([]treeFileEntry, error) {
	listings.Lock()
	defer listings.Unlock()
	if entries, ok := listings.trees[tree]; ok {
		return entries, nil
	}
	out, err := treeGit(store, nil, "ls-tree", "-r", "-z", "--full-tree", tree)
	if err != nil {
		return nil, err
	}
	var entries []treeFileEntry
	for _, record := range strings.Split(string(out), "\x00") {
		header, path, ok := strings.Cut(record, "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 3 {
			continue
		}
		entries = append(entries, treeFileEntry{Mode: fields[0], Object: fields[2], Path: path})
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Path < entries[b].Path })
	if len(listings.trees) >= 8 {
		clear(listings.trees)
	}
	listings.trees[tree] = entries
	return entries, nil
}

// under is the part of a sorted listing at a path: the file itself, or everything below the directory.
func under(entries []treeFileEntry, root string) []treeFileEntry {
	if root == "." || root == "" {
		return entries
	}
	var out []treeFileEntry
	if index := sort.Search(len(entries), func(index int) bool { return entries[index].Path >= root }); index < len(entries) && entries[index].Path == root {
		out = append(out, entries[index])
	}
	prefix := root + "/"
	start := sort.Search(len(entries), func(index int) bool { return entries[index].Path >= prefix })
	end := start
	for end < len(entries) && strings.HasPrefix(entries[end].Path, prefix) {
		end++
	}
	return append(out, entries[start:end]...)
}

// treePatch is one unified diff of the named paths between two trees.
func treePatch(store, from, to string, paths []string) ([]byte, error) {
	if len(paths) == 0 || from == to {
		return nil, nil
	}
	return treeGit(store, nil, append([]string{"diff", "--no-color", "--no-ext-diff", "--no-renames", "--no-textconv", "-U8", from, to, "--"}, paths...)...)
}

// numstat counts the added and removed lines of every changed path; a binary file counts as -1.
func numstat(store, from, to string) (map[string][2]int, error) {
	out, err := treeGit(store, nil, "diff", "--numstat", "-z", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	counts := map[string][2]int{}
	for _, record := range strings.Split(string(out), "\x00") {
		fields := strings.SplitN(record, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		added, addErr := strconv.Atoi(fields[0])
		removed, removeErr := strconv.Atoi(fields[1])
		if addErr != nil || removeErr != nil {
			added, removed = -1, -1
		}
		counts[fields[2]] = [2]int{added, removed}
	}
	return counts, nil
}

// overlayTree is a tree with some paths replaced: a nil state removes the path. Migration uses it to turn
// the file snapshots of an older task into a tree.
func overlayTree(store, base string, files map[string]*legacyFile) (string, error) {
	if err := os.MkdirAll(store, 0o700); err != nil {
		return "", err
	}
	index := filepath.Join(store, "index-"+newID()[:12])
	defer os.Remove(index)
	defer os.Remove(index + ".lock")
	env := []string{"GIT_INDEX_FILE=" + index}
	if base != "" {
		if _, err := treeGit(store, env, "read-tree", base); err != nil {
			return "", err
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		state := files[name]
		if state == nil {
			if _, err := treeGit(store, env, "update-index", "--force-remove", "--", name); err != nil {
				return "", err
			}
			continue
		}
		cmd := exec.Command("git", "hash-object", "-w", "--stdin")
		cmd.Env = append(os.Environ(), "GIT_OBJECT_DIRECTORY="+store)
		cmd.Stdin = bytes.NewReader(state.content())
		blob, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("record %s: %w", name, err)
		}
		mode := "100644"
		if state.Mode&0o111 != 0 {
			mode = "100755"
		}
		if _, err := treeGit(store, env, "update-index", "--add", "--cacheinfo", mode+","+strings.TrimSpace(string(blob))+","+name); err != nil {
			return "", err
		}
	}
	out, err := treeGit(store, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// graftTree is a tree with some paths taken from another tree: the files a worktree was given outside git stay
// part of its base when the base moves to a newer commit.
func graftTree(store, base, donor string, paths []string) (string, error) {
	if base == "" {
		return "", errors.New("the commit to catch up with has no tree")
	}
	if len(paths) == 0 {
		return base, nil
	}
	index := filepath.Join(store, "index-"+newID()[:12])
	defer os.Remove(index)
	defer os.Remove(index + ".lock")
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := treeGit(store, env, "read-tree", base); err != nil {
		return "", err
	}
	for _, path := range paths {
		entry, err := treeGit(store, nil, "ls-tree", "-z", donor, "--", path)
		if err != nil || len(entry) == 0 {
			continue
		}
		header, _, _ := strings.Cut(strings.TrimSuffix(string(entry), "\x00"), "\t")
		fields := strings.Fields(header)
		if len(fields) != 3 {
			continue
		}
		if _, err := treeGit(store, env, "update-index", "--add", "--cacheinfo", fields[0]+","+fields[2]+","+path); err != nil {
			return "", err
		}
	}
	out, err := treeGit(store, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return string(out), err
}

// entry fingerprints a file outside the repository's trees, such as a design package: content and mode.
func entry(name string) string {
	value, err := fingerprint(name)
	if err != nil {
		return "unreadable:" + err.Error()
	}
	return value
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

// guidance names the AGENTS.md / CLAUDE.md files above the given paths, so a rule change invalidates a review.
func guidance(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, file := range paths {
		for dir := filepath.Dir(file); ; dir = filepath.Dir(dir) {
			for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
				path := filepath.ToSlash(filepath.Join(dir, name))
				if seen[path] {
					continue
				}
				seen[path] = true
				if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
					out = append(out, path)
				}
			}
			if dir == "." || dir == "/" {
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
