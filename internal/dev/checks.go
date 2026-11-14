package dev

import (
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
	"strings"

	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

func checkID(argv []string) string { return digest(argv)[:16] }

func (g *Gate) findCheck(argv []string) *Check {
	for index := range g.Plan.Checks {
		if slices.Equal(g.Plan.Checks[index].Argv, argv) {
			return &g.Plan.Checks[index]
		}
	}
	return nil
}

var globMeta = regexp.MustCompile(`[*?\[]`)

// fileGlob uses standard Go glob syntax plus a ** path segment. Dynamic
// programming bounds matching work and supports character classes correctly.
type fileGlob struct{ parts []string }

func compileGlob(pattern string) (*fileGlob, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	return &fileGlob{parts: strings.Split(pattern, "/")}, nil
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
	for index := range g.Plan.Checks {
		check := &g.Plan.Checks[index]
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

// runCheck executes one declared check, or reuses its receipt when its inputs did not change.
func (g *Gate) runCheck(check *Check) (CheckReceipt, error) {
	if len(check.Inputs) > 0 && !check.NoReuse {
		if receipt := g.currentReceipt(check); passed(receipt) {
			reused := *receipt
			reused.Reused = true
			return reused, nil
		}
	}
	before, _, err := g.contentDigest()
	if err != nil {
		return CheckReceipt{}, err
	}
	key, err := inputKey(check)
	if err != nil {
		return CheckReceipt{}, err
	}
	repository := ""
	if len(check.Inputs) == 0 {
		repository, err = repositoryDigest()
		if err != nil {
			return CheckReceipt{}, err
		}
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
	if after, _, err := g.contentDigest(); err != nil {
		return CheckReceipt{}, err
	} else if after != before {
		return CheckReceipt{}, errors.New("task files changed during the check; its result is stale, run verification again")
	}
	if len(check.Inputs) == 0 {
		afterRepository, err := repositoryDigest()
		if err != nil {
			return CheckReceipt{}, err
		}
		if afterRepository != repository {
			return CheckReceipt{}, errors.New("repository changed during the check; its result is stale, declare stable inputs or run verification again")
		}
	}
	receipt := CheckReceipt{Argv: check.Argv, ReturnCode: code, Digest: before, InputKey: key, Repository: repository, Log: log}
	if previous, ran := g.Baselines[checkID(check.Argv)]; ran && previous != 0 && code != 0 {
		receipt.Preexisting = true
	}
	return receipt, nil
}

// A failed attempt invalidates older successful receipts even when no child
// process could start. A repaired tool must be run again before finish.
func (g *Gate) invalidateCheck(check *Check, failure error) error {
	content, _, _ := g.contentDigest()
	key, _ := inputKey(check)
	repository, _ := repositoryDigest()
	g.Checks = append(g.Checks, CheckReceipt{Argv: slices.Clone(check.Argv), ReturnCode: 125, Digest: content,
		InputKey: key, Repository: repository, Error: failure.Error()})
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
	if !gate.active() || gate.Phase == "held" {
		return nil, errors.New("the gate is not active")
	}
	var selected []*Check
	if input.All {
		for index := range gate.Plan.Checks {
			selected = append(selected, &gate.Plan.Checks[index])
		}
	} else if check := gate.findCheck(input.Argv); check != nil {
		selected = append(selected, check)
	} else {
		return nil, errors.New("that command is not in the approved checks of the plan")
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
	for index := range g.Plan.Checks {
		receipt := receipts[checkID(g.Plan.Checks[index].Argv)]
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
		out = append(out, CheckFailure{Argv: receipt.Argv, ReturnCode: receipt.ReturnCode, Log: receipt.Log, Tail: string(raw),
			Setup: generatedDesignCheck(g.Plan.Checks[index]) && receipt.ReturnCode != 1})
	}
	return out
}

// runOutstanding runs every declared check that has no current passing result.
func (g *Gate) runOutstanding() error {
	current := g.currentReceipts()
	for index := range g.Plan.Checks {
		check := &g.Plan.Checks[index]
		if !check.NoReuse && passed(current[checkID(check.Argv)]) {
			continue
		}
		receipt, err := g.runCheck(check)
		if err != nil {
			return errors.Join(err, g.invalidateCheck(check, err))
		}
		if !receipt.Reused {
			g.Checks = append(g.Checks, receipt)
		}
	}
	return g.save()
}

// outstanding lists the declared checks without a current passing receipt.
func (g *Gate) outstanding() []*Check {
	return g.outstandingReceipts(g.currentReceipts())
}

func (g *Gate) outstandingReceipts(receipts map[string]*CheckReceipt) []*Check {
	var out []*Check
	for index := range g.Plan.Checks {
		if check := &g.Plan.Checks[index]; !passed(receipts[checkID(check.Argv)]) {
			out = append(out, check)
		}
	}
	return out
}
