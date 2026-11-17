package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
)

// A task the user no longer wants is dropped: what it changed is taken back. One the user wants gone from the
// record is forgotten. Both happen only on the user's word.

// DropInput carries the user's word.
type DropInput struct {
	Reason string `json:"reason"`
}

// Drop ends a task and takes back what it changed. An isolated task loses its worktree and branch. A task that
// worked in the checkout gets its files back as they were when it started; a file someone changed after the
// task's writers left it is not overwritten, and then nothing is restored.
func Drop(cfg *config.Config, gate *Gate, input DropInput) (any, error) {
	if err := gate.can(ActDrop); err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return nil, errors.New("drop needs reason: the user's words asking for it")
	}
	did := []string{}
	if gate.Worktree != "" {
		if _, err := Land(cfg, gate, LandInput{How: "drop"}); err != nil {
			return nil, err
		}
		did = append(did, "the worktree and its branch are removed; the checkout was never touched")
	} else {
		restored, err := gate.restore()
		if err != nil {
			return nil, err
		}
		did = append(did, fmt.Sprintf("restored %d files to how the task found them", len(restored)))
	}
	if gate.Step != StepCancelled {
		if err := gate.setStep(StepCancelled); err != nil {
			return nil, err
		}
	}
	gate.Dropped = true
	gate.Pause = &Paused{Kind: PauseUser, Reason: "dropped: " + reason, At: time.Now().Unix()}
	if err := gate.save(); err != nil {
		return nil, err
	}
	return gate.respond(cfg, did, map[string]any{"reason": reason,
		"next": "The task is dropped. Tell the user what was taken back. `forget` removes it from the record, on their word."}), nil
}

// recordLeft remembers the working tree as the task's writers left it.
func (g *Gate) recordLeft() {
	if tree, err := g.snapshot(); err == nil {
		g.Left = tree
	}
}

// restore puts the files of the task back as they were when it started. It refuses, restoring nothing, when a
// file of the task changed after its writers last left it: that change is someone else's.
func (g *Gate) restore() ([]string, error) {
	now, err := g.snapshot()
	if err != nil {
		return nil, err
	}
	changes, err := diffTrees(g.objects(), g.BaseTree, now)
	if err != nil {
		return nil, err
	}
	scope := compilePatterns(g.Plan.Files())
	var mine []string
	for _, change := range changes {
		if matchAny(scope, change.Path) {
			mine = append(mine, change.Path)
		}
	}
	left := g.Left
	if left == "" {
		left = g.BaseTree // no writer ever wrote: every change is someone else's
	}
	since, err := diffTrees(g.objects(), left, now)
	if err != nil {
		return nil, err
	}
	var foreign []string
	for _, change := range since {
		if slices.Contains(mine, change.Path) {
			foreign = append(foreign, change.Path)
		}
	}
	if len(foreign) > 0 {
		return nil, fmt.Errorf("these files changed after the task's writers left them — by someone else, or by a writer that was interrupted: %s. "+
			"Nothing was restored. Bring them back to what the task left, or keep everything and end the task with `pause` {reason, cancelled: true}",
			strings.Join(foreign, ", "))
	}
	for _, path := range mine {
		if err := restoreFile(g.objects(), g.BaseTree, path); err != nil {
			return nil, fmt.Errorf("restore %s: %w", path, err)
		}
	}
	return mine, nil
}

// restoreFile makes a path of the working tree what it is in a tree: its content and mode, or absent.
func restoreFile(store, tree, path string) error {
	if info, err := os.Lstat(path); err == nil && (info.Mode()&os.ModeSymlink != 0 || info.Mode().IsRegular()) {
		if err := os.Remove(path); err != nil { // never write through a link
			return err
		}
	}
	entry, err := treeGit(store, nil, "ls-tree", "-z", tree, "--", path)
	if err != nil || len(entry) == 0 {
		return nil // the task created it
	}
	header, _, _ := strings.Cut(strings.TrimSuffix(string(entry), "\x00"), "\t")
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[1] != "blob" {
		return fmt.Errorf("unexpected tree entry %q", header)
	}
	content, err := treeGit(store, nil, "cat-file", "blob", fields[2])
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	switch fields[0] {
	case "120000":
		return os.Symlink(string(content), path)
	case "100755":
		return os.WriteFile(path, content, 0o755)
	}
	return os.WriteFile(path, content, 0o644)
}

// ForgetInput names the task to forget and carries the user's word. Without task it is this session's.
type ForgetInput struct {
	Task   string `json:"task,omitempty"` // the id `status --all` shows, or its first eight characters
	Reason string `json:"reason"`
}

// Forget removes a finished or dropped task of this checkout from the record: its state, its receipts and
// the operations that worked on it. The repository and its saved plans are not touched.
func Forget(input ForgetInput) (any, error) {
	if err := requireLead(nil); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Reason) == "" {
		return nil, errors.New("forget needs reason: the user's words asking for it")
	}
	path := filepath.Join(home(), "state.json")
	if input.Task != "" {
		var found []string
		paths, _ := filepath.Glob(filepath.Join(checkoutDir(""), "*", "state.json"))
		for _, candidate := range paths {
			if gate, err := loadGateFile(candidate); err == nil && gate != nil && len(input.Task) >= 8 && strings.HasPrefix(gate.ID, input.Task) {
				found = append(found, candidate)
			}
		}
		if len(found) != 1 {
			return nil, fmt.Errorf("task %q names %d tasks of this checkout; `status --all` lists them with their ids", input.Task, len(found))
		}
		path = found[0]
	}
	dir := filepath.Dir(path)
	unlock, err := flock(filepath.Join(dir, "lock"), syscall.LOCK_EX|syscall.LOCK_NB, errors.New("an operation is running for that task; wait for it, or `stop` it"))
	if err != nil {
		return nil, err
	}
	defer unlock()
	gate, err := loadGateFile(path)
	if err != nil {
		return nil, err
	}
	if gate == nil {
		return nil, errors.New("there is no task to forget in this session; `status --all` lists the tasks of this checkout")
	}
	if err := gate.can(ActForget); err != nil {
		return nil, err
	}
	removed := []string{filepath.Join(dir, gate.ID)}
	if err := os.RemoveAll(removed[0]); err != nil {
		return nil, err
	}
	if gate.Preparation != "" && gateIDPattern.MatchString(gate.Preparation) {
		preparation := filepath.Dir(preparationFile(gate.Preparation))
		if err := os.RemoveAll(preparation); err == nil {
			removed = append(removed, preparation)
		}
	}
	records, _ := filepath.Glob(filepath.Join(dir, "operations", "*.json"))
	for _, record := range append(records, filepath.Join(dir, "operation.json")) {
		raw, err := os.ReadFile(record)
		var operation ops.Record
		if err == nil && json.Unmarshal(raw, &operation) == nil && operation.GateID == gate.ID && os.Remove(record) == nil {
			removed = append(removed, record)
		}
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	removed = append(removed, path)
	return map[string]any{"forgot": gate.ID[:8], "task": firstLine(gate.Request.Task), "removed": removed,
		"next": "The task is gone from the record. The repository and its saved plans are unchanged."}, nil
}
