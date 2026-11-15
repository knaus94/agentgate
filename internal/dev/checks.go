package dev

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

func checkID(argv []string) string { return digest(argv)[:16] }

func (g *Gate) findCheck(argv []string) *Check {
	checks := g.checks()
	for index := range checks {
		if slices.Equal(checks[index].Argv, argv) {
			return &checks[index]
		}
	}
	return nil
}

// fingerprint is the content and mode of one file on disk.
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

var globMeta = regexp.MustCompile(`[*?\[]`)

// fileGlob uses standard Go glob syntax plus a ** path segment. Dynamic
// programming bounds matching work and supports character classes correctly.
type fileGlob struct{ parts []string }

func compileGlob(pattern string) (*fileGlob, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	return &fileGlob{parts: globParts(pattern)}, nil
}

// globParts splits a pattern into path segments. A trailing `**` after a directory stands for what is inside
// it, so it needs at least one segment: `dir/**` names the files below `dir`, not `dir` itself.
func globParts(pattern string) []string {
	parts := strings.Split(pattern, "/")
	if last := len(parts) - 1; last > 0 && parts[last] == "**" {
		parts = append(parts[:last:last], "*", "**")
	}
	return parts
}

func (glob *fileGlob) MatchString(name string) bool {
	parts := strings.Split(name, "/")
	previous := make([]bool, len(parts)+1)
	previous[0] = true
	for _, pattern := range glob.parts {
		next := make([]bool, len(parts)+1)
		if pattern == "**" {
			next[0] = previous[0]
			for index := range parts {
				next[index+1] = previous[index+1] || next[index]
			}
		} else {
			for index, part := range parts {
				match, _ := path.Match(pattern, part)
				next[index+1] = previous[index] && match
			}
		}
		previous = next
	}
	return previous[len(parts)]
}

// inputFiles expands a check's input globs. A matched directory contributes everything below it.
func inputFiles(patterns []string) ([]string, error) {
	found := map[string]bool{}
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(filepath.Clean(pattern))
		if filepath.IsAbs(pattern) || pattern == ".." || strings.HasPrefix(pattern, "../") || strings.ContainsRune(pattern, '\x00') {
			return nil, fmt.Errorf("check input is outside the repository: %q", pattern)
		}
		root, matcher := pattern, (*fileGlob)(nil)
		if location := globMeta.FindStringIndex(pattern); location != nil {
			root = filepath.ToSlash(filepath.Dir(pattern[:location[0]] + "x"))
			var err error
			matcher, err = compileGlob(pattern)
			if err != nil {
				return nil, err
			}
		}
		// WalkDir does not follow a symlink root, and cannot reveal a symlink in
		// its ancestors. Check those explicitly instead of accepting an empty key.
		for current := root; current != "." && current != "/"; current = filepath.Dir(current) {
			info, err := os.Lstat(current)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("inspect check input %s: %w", current, err)
			}
			if err == nil && info.Mode()&fs.ModeSymlink != 0 {
				return nil, fmt.Errorf("check input %s is a symlink; declare the real dependency instead", current)
			}
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, os.ErrNotExist) && path == root {
					return nil // deleting an input changes the key
				}
				return fmt.Errorf("read check input %s: %w", path, err)
			}
			path = filepath.ToSlash(path)
			if entry.IsDir() {
				if entry.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				if matcher == nil || matcher.MatchString(path) || underMatched(matcher, path) {
					return fmt.Errorf("check input %s is a symlink; declare the real dependency instead", path)
				}
				return nil
			}
			if matcher == nil || matcher.MatchString(path) || underMatched(matcher, path) {
				found[path] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(found))
	for path := range found {
		out = append(out, path)
	}
	sort.Strings(out)
	return out, nil
}

func underMatched(matcher *fileGlob, path string) bool {
	for dir := filepath.ToSlash(filepath.Dir(path)); dir != "." && dir != "/"; dir = filepath.ToSlash(filepath.Dir(dir)) {
		if matcher.MatchString(dir) {
			return true
		}
	}
	return false
}

// inputKey fingerprints the declared inputs of a check, or returns "" when it declared none.
func inputKey(check *Check) (string, error) {
	if len(check.Inputs) == 0 {
		return "", nil
	}
	files, err := inputFiles(check.Inputs)
	if err != nil {
		return "", err
	}
	entries := map[string]string{}
	for _, file := range files {
		value, err := fingerprint(file)
		if err != nil {
			return "", err
		}
		entries[file] = value
	}
	return digest(map[string]any{"argv": check.Argv, "inputs": check.Inputs, "files": entries}), nil
}

func passed(receipt *CheckReceipt) bool {
	return receipt != nil && receipt.ReturnCode == 0
}

func (g *Gate) execCheck(check *Check, label string) (int, string, error) {
	if err := os.MkdirAll(filepath.Join(g.dir(), "checks"), 0o700); err != nil {
		return 0, "", err
	}
	log := filepath.Join(g.dir(), "checks", fmt.Sprintf("%s-%s-%s.txt", checkID(check.Argv), label, newID()[:8]))
	output, err := os.Create(log)
	if err != nil {
		return 0, "", err
	}
	defer output.Close()
	argv := check.Argv
	if !g.Request.Writes() {
		// A review-only task has no authority to change anything, including through its checks.
		scratch := filepath.Join(g.dir(), "checks", "scratch-"+newID()[:8])
		if err := os.MkdirAll(scratch, 0o700); err != nil {
			return 0, "", err
		}
		argv = provider.Sandbox(scratch, false, nil, nil, argv)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = output, output
	err = ops.Run(cmd, seconds(check.TimeoutS))
	if err != nil && cmd.ProcessState == nil {
		return 0, log, err
	}
	if ops.IsCancelled(err) {
		return 0, log, err
	}
	code := cmd.ProcessState.ExitCode()
	if errors.Is(err, ops.ErrTimeout) || code < 0 {
		fmt.Fprintf(output, "\n[agentgate] %v\n", err)
		code = 124
	}
	return code, log, nil
}

// baselineChecks records failures observed before writing. An exit code alone
// cannot prove that a later failure has the same cause, so it never waives it.
func (g *Gate) baselineChecks() error {
	checks := g.checks()
	for index := range checks {
		check := &checks[index]
		if !check.Baseline {
			continue
		}
		code, _, err := g.execCheck(check, "baseline")
		if err != nil {
			return err
		}
		g.Baselines[checkID(check.Argv)] = code
	}
	return g.save()
}

// runCheck executes one declared check, or reuses its receipt when its inputs did not change. A check that
// declared no inputs is bound to the whole working tree it ran against.
func (g *Gate) runCheck(check *Check) (CheckReceipt, error) {
	if len(check.Inputs) > 0 && !check.NoReuse {
		if receipt := g.currentReceipt(check); passed(receipt) {
			reused := *receipt
			reused.Reused = true
			return reused, nil
		}
	}
	before, err := g.snapshot()
	if err != nil {
		return CheckReceipt{}, err
	}
	key, err := inputKey(check)
	if err != nil {
		return CheckReceipt{}, err
	}
	code, log, err := g.execCheck(check, "run")
	if err != nil {
		return CheckReceipt{}, err
	}
	afterKey, err := inputKey(check)
	if err != nil {
		return CheckReceipt{}, err
	}
	if afterKey != key {
		return CheckReceipt{}, errors.New("check changed its declared inputs; its result is stale, run a non-mutating verification command")
	}
	after, err := g.snapshot()
	if err != nil {
		return CheckReceipt{}, err
	}
	if after != before {
		owned, _, err := g.changesBetween(before, after)
		if err != nil {
			return CheckReceipt{}, err
		}
		if len(owned) > 0 {
			return CheckReceipt{}, errors.New("task files changed during the check; its result is stale, run verification again")
		}
		if len(check.Inputs) == 0 {
			return CheckReceipt{}, errors.New("repository changed during the check; its result is stale, declare stable inputs or run verification again")
		}
	}
	receipt := CheckReceipt{Argv: check.Argv, ReturnCode: code, InputKey: key, Log: log, Iteration: g.Iteration}
	if len(check.Inputs) == 0 {
		receipt.Tree = before
	}
	if previous, ran := g.Baselines[checkID(check.Argv)]; ran && previous != 0 && code != 0 {
		receipt.Preexisting = true
	}
	return receipt, nil
}

// A failed attempt invalidates older successful receipts even when no child process could start. A repaired
// tool must be run again before the task can go on.
func (g *Gate) invalidateCheck(check *Check, failure error) error {
	key, _ := inputKey(check)
	tree, _ := g.snapshot()
	g.Checks = append(g.Checks, CheckReceipt{Argv: slices.Clone(check.Argv), ReturnCode: 125, Tree: tree, InputKey: key, Error: failure.Error(), Iteration: g.Iteration})
	return g.save()
}

// CheckInput selects one declared check by its exact argv, or all of them.
type CheckInput struct {
	Argv []string `json:"argv,omitempty"`
	All  bool     `json:"all,omitempty"`
}

// CheckResult reports the receipts; Failed is set when any check failed for a reason the task owns.
type CheckResult struct {
	Receipts []CheckReceipt `json:"receipts"`
	Failed   bool           `json:"failed"`
}

// RunChecks executes declared checks. Commands outside the approved list are refused.
func RunChecks(gate *Gate, input CheckInput) (*CheckResult, error) {
	if err := gate.can(ActCheck); err != nil {
		return nil, err
	}
	checks := gate.checks()
	var selected []*Check
	if input.All {
		for index := range checks {
			selected = append(selected, &checks[index])
		}
	} else if check := gate.findCheck(input.Argv); check != nil {
		selected = append(selected, check)
	} else {
		return nil, errors.New("that command is not in the approved checks of the plan; `amend` the plan to add a check")
	}
	result := &CheckResult{}
	for _, check := range selected {
		receipt, err := gate.runCheck(check)
		if err != nil {
			return nil, errors.Join(err, gate.invalidateCheck(check, err))
		}
		if !receipt.Reused {
			gate.Checks = append(gate.Checks, receipt)
		}
		result.Receipts = append(result.Receipts, receipt)
		result.Failed = result.Failed || !passed(&receipt)
	}
	return result, gate.save()
}

// failing lists the declared checks whose current result is a failure the task owns.
func (g *Gate) failing() []CheckFailure {
	return g.failingReceipts(g.currentReceipts())
}

func (g *Gate) failingReceipts(receipts map[string]*CheckReceipt) []CheckFailure {
	var out []CheckFailure
	checks := g.checks()
	for index := range checks {
		receipt := receipts[checkID(checks[index].Argv)]
		if receipt == nil || passed(receipt) {
			continue
		}
		raw, _ := os.ReadFile(receipt.Log)
		if receipt.Error != "" {
			raw = []byte(receipt.Error)
		}
		if len(raw) > 4000 {
			raw = raw[len(raw)-4000:]
		}
		setup := generatedDesignCheck(checks[index]) && receipt.ReturnCode != 1 && receipt.ReturnCode != layoutRootMissing
		out = append(out, CheckFailure{Argv: receipt.Argv, ReturnCode: receipt.ReturnCode, Log: receipt.Log, Tail: string(raw),
			Setup: setup, Environment: !setup && g.passedBefore(receipt)})
	}
	return out
}

// layoutRootMissing is the exit code of a layout check whose page opened where it was asked to and does not
// show the element of the design: a defect of the code, for its writer.
const layoutRootMissing = 3

// passedBefore reports whether the check of a failing receipt passed earlier on the same files of the task: the
// same declared inputs, or a working tree that differs from today's only outside the task's scope.
func (g *Gate) passedBefore(failure *CheckReceipt) bool {
	id := checkID(failure.Argv)
	for index := len(g.Checks) - 1; index >= 0; index-- {
		earlier := &g.Checks[index]
		if earlier == failure || checkID(earlier.Argv) != id || !passed(earlier) {
			continue
		}
		if earlier.InputKey != "" && failure.InputKey != "" {
			return earlier.InputKey == failure.InputKey
		}
		if earlier.Tree == "" || failure.Tree == "" {
			return false
		}
		owned, _, err := g.changesBetween(earlier.Tree, failure.Tree)
		return err == nil && len(owned) == 0
	}
	return false
}

// runOutstanding runs every declared check that has no current passing result, and reports how many it ran.
func (g *Gate) runOutstanding() (int, error) {
	current := g.currentReceipts()
	checks := g.checks()
	ran := 0
	for index := range checks {
		check := &checks[index]
		if !check.NoReuse && passed(current[checkID(check.Argv)]) {
			continue
		}
		receipt, err := g.runCheck(check)
		if err != nil {
			return ran, errors.Join(err, g.invalidateCheck(check, err))
		}
		if !receipt.Reused {
			g.Checks = append(g.Checks, receipt)
			ran++
		}
	}
	return ran, g.save()
}

// outstanding lists the declared checks without a current passing receipt.
func (g *Gate) outstanding() []*Check {
	return g.outstandingReceipts(g.currentReceipts())
}

func (g *Gate) outstandingReceipts(receipts map[string]*CheckReceipt) []*Check {
	var out []*Check
	checks := g.checks()
	for index := range checks {
		if check := &checks[index]; !passed(receipts[checkID(check.Argv)]) {
			out = append(out, check)
		}
	}
	return out
}

// checkLabel names a check in one short line.
func checkLabel(argv []string) string {
	label := strings.Join(argv, " ")
	if runes := []rune(label); len(runes) > 100 {
		return string(runes[:100]) + "…"
	}
	return label
}
