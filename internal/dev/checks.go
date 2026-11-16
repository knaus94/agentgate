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
	"strconv"
	"strings"
	"time"

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

const symlinkMode = "120000"

// inputFiles fingerprints the files a check's input patterns name: path -> mode and object of the recorded
// tree, or the content of a file read from the disk. A matched directory contributes everything below it.
func inputFiles(entries []treeFileEntry, patterns []string) (map[string]string, error) {
	files := map[string]string{}
	for _, pattern := range patterns {
		if _, err := expandInput(entries, pattern, files); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// expandInput adds what one input pattern names to files and reports whether it named anything. The tree holds
// every file git does not ignore, with the object id git computed for it, so these cost no read. What git
// ignores counts only where the pattern's literal prefix points into it (`dist/**`): that part is read from
// the disk. A link counts as the link and as the files it leads to.
func expandInput(entries []treeFileEntry, pattern string, files map[string]string) (bool, error) {
	pattern = filepath.ToSlash(filepath.Clean(pattern))
	if filepath.IsAbs(pattern) || pattern == ".." || strings.HasPrefix(pattern, "../") || strings.ContainsRune(pattern, '\x00') {
		return false, fmt.Errorf("check input is outside the repository: %q", pattern)
	}
	root, matcher := pattern, (*fileGlob)(nil)
	if location := globMeta.FindStringIndex(pattern); location != nil {
		root = filepath.ToSlash(filepath.Dir(pattern[:location[0]] + "x"))
		var err error
		if matcher, err = compileGlob(pattern); err != nil {
			return false, err
		}
	}
	// A link at the root or on the way to it stands for what it leads to.
	for current := root; current != "." && current != "/"; current = path.Dir(current) {
		if link := under(entries, current); len(link) == 1 && link[0].Path == current && link[0].Mode == symlinkMode {
			return true, follow(entries, link[0], files)
		}
	}
	recorded := under(entries, root)
	found := false
	for _, entry := range recorded {
		if matcher != nil && !matcher.MatchString(entry.Path) && !underMatched(matcher, entry.Path) {
			continue
		}
		found = true
		files[entry.Path] = entry.Mode + ":" + entry.Object
		if entry.Mode == symlinkMode {
			if err := follow(entries, entry, files); err != nil {
				return true, err
			}
		}
	}
	if len(recorded) > 0 {
		return found, nil
	}
	// Git records nothing there: the pattern points into what git ignores, or at nothing yet.
	return diskInputs(root, matcher, files)
}

// follow adds a link and the files it leads to inside the repository; a link leading outside is refused.
func follow(entries []treeFileEntry, link treeFileEntry, files map[string]string) error {
	files[link.Path] = link.Mode + ":" + link.Object
	target, err := os.Readlink(link.Path)
	if err != nil {
		return fmt.Errorf("read the link %s: %w", link.Path, err)
	}
	resolved := path.Clean(path.Join(path.Dir(link.Path), filepath.ToSlash(target)))
	if filepath.IsAbs(target) {
		here, _ := os.Getwd()
		relative, err := filepath.Rel(here, target)
		if err != nil {
			relative = ".."
		}
		resolved = filepath.ToSlash(relative)
	}
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("check input %s is a link leading outside the repository (%s); declare what it leads to instead", link.Path, target)
	}
	if targets := under(entries, resolved); len(targets) > 0 {
		for _, entry := range targets {
			files[entry.Path] = entry.Mode + ":" + entry.Object
		}
		return nil
	}
	_, err = diskInputs(resolved, nil, files)
	return err
}

// diskInputs fingerprints the files below a root git does not record, by their content.
func diskInputs(root string, matcher *fileGlob, files map[string]string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && name == root {
				return nil // deleting an input changes the key
			}
			return fmt.Errorf("read check input %s: %w", name, err)
		}
		name = filepath.ToSlash(name)
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if matcher != nil && !matcher.MatchString(name) && !underMatched(matcher, name) {
			return nil
		}
		value, err := fingerprint(name)
		if err != nil {
			return err
		}
		files[name], found = "disk:"+value, true
		return nil
	})
	return found, err
}

func underMatched(matcher *fileGlob, path string) bool {
	for dir := filepath.ToSlash(filepath.Dir(path)); dir != "." && dir != "/"; dir = filepath.ToSlash(filepath.Dir(dir)) {
		if matcher.MatchString(dir) {
			return true
		}
	}
	return false
}

// inputKey fingerprints the declared inputs of a check as a recorded tree holds them, or returns "" when it
// declared none.
func (g *Gate) inputKey(check *Check, tree string) (string, error) {
	if len(check.Inputs) == 0 {
		return "", nil
	}
	entries, err := treeListing(g.objects(), tree)
	if err != nil {
		return "", err
	}
	files, err := inputFiles(entries, check.Inputs)
	if err != nil {
		return "", err
	}
	return digest(map[string]any{"argv": check.Argv, "inputs": check.Inputs, "files": files}), nil
}

// inspectInputs looks at the declared inputs of every check of the plan when it is accepted: a link leading
// outside the repository is refused now rather than after a writer's round, and a pattern that matches
// nothing yet is said, since the task may create the file.
func (g *Gate) inspectInputs(checks []Check, tree string) ([]string, error) {
	entries, err := treeListing(g.objects(), tree)
	if err != nil {
		return nil, err
	}
	var notes []string
	seen := map[string]bool{}
	for _, check := range checks {
		for _, pattern := range check.Inputs {
			if seen[pattern] {
				continue
			}
			seen[pattern] = true
			found, err := expandInput(entries, pattern, map[string]string{})
			if err != nil {
				return nil, fmt.Errorf("check `%s`: %w", checkLabel(check.Argv), err)
			}
			if !found {
				notes = append(notes, fmt.Sprintf("check input %s matches no file yet (check `%s`)", pattern, checkLabel(check.Argv)))
			}
		}
	}
	return notes, nil
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
		ops.Doing("check before writing: %s", checkLabel(commandArgv(check.Argv)))
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
	key, err := g.inputKey(check, before)
	if err != nil {
		return CheckReceipt{}, err
	}
	code, log, err := g.execCheck(check, "run")
	if err != nil {
		return CheckReceipt{}, err
	}
	after, err := g.snapshot()
	if err != nil {
		return CheckReceipt{}, err
	}
	afterKey, err := g.inputKey(check, after)
	if err != nil {
		return CheckReceipt{}, err
	}
	if afterKey != key {
		return CheckReceipt{}, errors.New("check changed its declared inputs; its result is stale, run a non-mutating verification command")
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
	tree, _ := g.snapshot()
	key, _ := g.inputKey(check, tree)
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
		ops.Doing("check %d of %d: %s", index+1, len(checks), checkLabel(commandArgv(check.Argv)))
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

// commandArgv is a check's command without the environment assignments in front of it.
func commandArgv(argv []string) []string {
	if len(argv) > 1 && argv[0] == "env" {
		argv = argv[1:]
		for len(argv) > 1 && strings.Contains(argv[0], "=") && !strings.HasPrefix(argv[0], "-") {
			argv = argv[1:]
		}
	}
	return argv
}

// since is the time from a moment until now, as the panel writes it.
func since(start time.Time) string { return clock(time.Since(start).Seconds()) }

// checkLabel names a check in one short line.
func checkLabel(argv []string) string {
	label := strings.Join(argv, " ")
	if runes := []rune(label); len(runes) > 100 {
		return string(runes[:100]) + "…"
	}
	return label
}

// testFile recognises the test files of the common ecosystems by their paths.
var testFile = regexp.MustCompile(`(^|/)(__tests__|tests?|specs?)/|(^|/)(test_[^/]+\.py|[^/]+_test\.(go|py|rb|exs)|[^/]+\.(test|spec)\.[A-Za-z]+|[^/]+(Test|Tests|Spec)\.(java|kt|cs|swift|scala))$`)

// runnerWords are the words of a test command that name its runner, not what it runs.
var runnerWords = []string{"test", "tests", "run", "exec", "npx", "yarn", "npm", "pnpm", "bun", "deno", "node", "go", "pytest", "python", "python3",
	"jest", "vitest", "mocha", "cargo", "make", "mvn", "gradle", "gradlew", "dotnet", "nx", "turbo", "rspec", "bundle", "mix"}

// runsTests says whether a check may run tests at all: a test, or a command of unknown kind.
func (c Check) runsTests() bool { return c.Kind == "" || c.Kind == "test" || c.Kind == "command" }

// covers says whether a check runs a test file, as far as its declaration shows: its inputs match the file, or
// its command names the file, a directory above it or a project it lies in, or names nothing but its runner.
func (c Check) covers(path string) bool {
	for _, input := range c.Inputs {
		if glob, err := compileGlob(input); err == nil && glob.MatchString(path) {
			return true
		}
	}
	dirs := strings.Split(path, "/")
	dirs = dirs[:len(dirs)-1]
	named := !slices.Contains(runnerWords, filepath.Base(c.Argv[0]))
	for _, arg := range c.Argv[1:] {
		token := strings.TrimSuffix(strings.TrimPrefix(arg, "./"), "/")
		switch {
		case strings.HasPrefix(arg, "-"):
		case token == "...":
			if strings.HasSuffix(path, ".go") {
				return true
			}
			named = true
		case strings.HasSuffix(token, "/..."):
			if strings.HasPrefix(path, strings.TrimSuffix(token, "...")) {
				return true
			}
			named = true
		case token == path, strings.HasPrefix(path, token+"/"), slices.Contains(dirs, token):
			return true
		case !slices.Contains(runnerWords, token):
			named = true
		}
	}
	return !named
}

// uncheckedTests are the test files the task added or changed that no declared check runs: tests that run
// nowhere here fail later, in somebody's pipeline.
func (g *Gate) uncheckedTests(tree string) []string {
	changes, err := diffTrees(g.objects(), g.BaseTree, tree)
	if err != nil {
		return nil
	}
	scope := compilePatterns(g.Plan.Files())
	checks := g.Plan.checksFor(-1)
	var out []string
	for _, change := range changes {
		if change.Status == "D" || !testFile.MatchString(change.Path) || !matchAny(scope, change.Path) {
			continue
		}
		if !slices.ContainsFunc(checks, func(check Check) bool { return check.runsTests() && check.covers(change.Path) }) {
			out = append(out, change.Path)
		}
	}
	return out
}
