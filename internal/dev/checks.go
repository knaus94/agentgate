package dev

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/knaus94/claude-dev/internal/ops"
	"github.com/knaus94/claude-dev/internal/provider"
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

// globRegexp translates a repository glob; "**" crosses directories.
func globRegexp(pattern string) *regexp.Regexp {
	var out strings.Builder
	out.WriteString("^")
	for index := 0; index < len(pattern); index++ {
		switch {
		case strings.HasPrefix(pattern[index:], "**/"):
			out.WriteString("(.*/)?")
			index += 2
		case strings.HasPrefix(pattern[index:], "**"):
			out.WriteString(".*")
			index++
		case pattern[index] == '*':
			out.WriteString("[^/]*")
		case pattern[index] == '?':
			out.WriteString("[^/]")
		default:
			out.WriteString(regexp.QuoteMeta(string(pattern[index])))
		}
	}
	out.WriteString("$")
	return regexp.MustCompile(out.String())
}

// inputFiles expands a check's input globs. A matched directory contributes everything below it.
func inputFiles(patterns []string) ([]string, error) {
	found := map[string]bool{}
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(filepath.Clean(pattern))
		root, matcher := pattern, (*regexp.Regexp)(nil)
		if location := globMeta.FindStringIndex(pattern); location != nil {
			root = filepath.ToSlash(filepath.Dir(pattern[:location[0]] + "x"))
			matcher = globRegexp(pattern)
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil // a missing root simply matches nothing; deleting an input changes the key
			}
			path = filepath.ToSlash(path)
			if entry.IsDir() {
				if entry.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				if matcher == nil || matcher.MatchString(path) {
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

func underMatched(matcher *regexp.Regexp, path string) bool {
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
		entries[file] = entry(file)
	}
	return digest(map[string]any{"argv": check.Argv, "inputs": check.Inputs, "files": entries}), nil
}

// currentReceipt returns the latest receipt of a check when it still describes the present code.
// A check with declared inputs survives unrelated edits; one without is bound to the whole task content.
func (g *Gate) currentReceipt(check *Check) *CheckReceipt {
	for index := len(g.Checks) - 1; index >= 0; index-- {
		receipt := &g.Checks[index]
		if !slices.Equal(receipt.Argv, check.Argv) {
			continue
		}
		if key, err := inputKey(check); err == nil && key != "" {
			if receipt.InputKey == key {
				return receipt
			}
			return nil
		}
		if content, _, err := g.contentDigest(); err == nil && receipt.Digest == content {
			return receipt
		}
		return nil
	}
	return nil
}

func passed(receipt *CheckReceipt) bool {
	return receipt != nil && (receipt.ReturnCode == 0 || receipt.Preexisting)
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
		fmt.Fprintf(output, "\n[claude-dev] %v\n", err)
		code = 124
	}
	return code, log, nil
}

// baselineChecks runs the checks marked baseline before any writing. A check that is already red
// for reasons unrelated to the task must not hold the task hostage later.
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
	if receipt := g.currentReceipt(check); receipt != nil && receipt.ReturnCode == 0 && len(check.Inputs) > 0 && !check.NoReuse {
		reused := *receipt
		reused.Reused = true
		return reused, nil
	}
	before, _, err := g.contentDigest()
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
	if after, _ := inputKey(check); after != key {
		key = "" // the command changed its own inputs: its result describes neither state
	}
	receipt := CheckReceipt{Argv: check.Argv, ReturnCode: code, Digest: before, InputKey: key, Log: log}
	if previous, ran := g.Baselines[checkID(check.Argv)]; ran && previous != 0 && code != 0 {
		receipt.Preexisting = true
	}
	return receipt, nil
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
			return nil, err
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
	var out []CheckFailure
	for index := range g.Plan.Checks {
		receipt := g.currentReceipt(&g.Plan.Checks[index])
		if receipt == nil || passed(receipt) {
			continue
		}
		raw, _ := os.ReadFile(receipt.Log)
		if len(raw) > 4000 {
			raw = raw[len(raw)-4000:]
		}
		out = append(out, CheckFailure{Argv: receipt.Argv, ReturnCode: receipt.ReturnCode, Log: receipt.Log, Tail: string(raw)})
	}
	return out
}

// runOutstanding runs every declared check that has no current passing result.
func (g *Gate) runOutstanding() error {
	for _, check := range g.outstanding() {
		receipt, err := g.runCheck(check)
		if err != nil {
			return err
		}
		if !receipt.Reused {
			g.Checks = append(g.Checks, receipt)
		}
	}
	return g.save()
}

// outstanding lists the declared checks without a current passing receipt.
func (g *Gate) outstanding() []*Check {
	var out []*Check
	for index := range g.Plan.Checks {
		if check := &g.Plan.Checks[index]; !passed(g.currentReceipt(check)) {
			out = append(out, check)
		}
	}
	return out
}
