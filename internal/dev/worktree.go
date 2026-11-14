package dev

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
)

// An isolated task works in its own git worktree on its own branch. The user's checkout does not change until
// the user lands the finished task, so several sessions can work in one repository without seeing each other.
//
// The process moves into the worktree for the length of one operation: every relative path, every git call and
// the sandbox of every worker then mean the worktree. State stays keyed by the user's checkout.

// origin is the user's checkout while the process is inside a task's worktree.
var origin string

// base is the user's checkout: where the command was run, whatever directory the process is in now.
func base() string {
	if origin != "" {
		return origin
	}
	cwd, _ := os.Getwd()
	return cwd
}

// enter moves the process into the task's worktree. It is a no-op for an ordinary task.
func (g *Gate) enter() error {
	if g == nil || g.Worktree == "" || g.Landed != "" || origin != "" {
		return nil
	}
	here, _ := os.Getwd()
	if err := os.Chdir(g.Worktree); err != nil {
		return fmt.Errorf("the task's worktree is gone (%s); `land` {how: drop} clears the task", g.Worktree)
	}
	origin = here
	return nil
}

// leave returns the process to the user's checkout.
func leave() {
	if origin != "" {
		_ = os.Chdir(origin)
		origin = ""
	}
}

// Inside runs a read-only view of the task from where its files are.
func (g *Gate) Inside(view func()) {
	if origin == "" && g.enter() == nil {
		defer leave()
	}
	view()
}

func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// nulList splits git's NUL-separated output; a file name may hold spaces.
func nulList(raw string) []string {
	out := []string{}
	for _, name := range strings.Split(raw, "\x00") {
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// committed is a file as a commit has it, or nil when the commit has no such file.
func committed(commit, file string) *FileState {
	raw, err := exec.Command("git", "show", commit+":"+file).Output()
	if err != nil {
		return nil
	}
	mode := uint32(0o644)
	if tree, _ := git("ls-tree", commit, "--", file); strings.HasPrefix(tree, "100755") {
		mode = 0o755
	}
	return &FileState{Bytes: base64.StdEncoding.EncodeToString(raw), Mode: mode}
}

// isolate creates the worktree of a new task from the checkout's HEAD, prepares it and moves the process in.
func (g *Gate) isolate(cfg *config.Config) error {
	main := base()
	head, err := gitIn(main, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return errors.New("an isolated task starts from a commit; this repository has none")
	}
	words := regexp.MustCompile(`[a-z0-9]+`).FindAllString(strings.ToLower(g.Request.Task), 4)
	g.Branch = "dev/" + strings.Join(append(words, g.ID[:6]), "-")
	g.Worktree = filepath.Join(checkoutDir(""), "worktrees", g.ID[:12])
	if err := os.MkdirAll(filepath.Dir(g.Worktree), 0o700); err != nil {
		return err
	}
	if _, err := gitIn(main, "worktree", "add", "-q", "-b", g.Branch, g.Worktree, strings.TrimSpace(head)); err != nil {
		return err
	}
	fail := func(err error) error {
		leave()
		g.discard()
		return err
	}
	for _, path := range cfg.Worktree.Copy {
		source, target := filepath.Join(main, path), filepath.Join(g.Worktree, path)
		if _, err := os.Stat(source); err != nil {
			continue // nothing to bring: a fresh clone may not have its local files yet
		}
		_ = os.MkdirAll(filepath.Dir(target), 0o755)
		if out, err := exec.Command("cp", "-a", "--", source, target).CombinedOutput(); err != nil {
			return fail(fmt.Errorf("worktree.copy %s: %s", path, strings.TrimSpace(string(out))))
		}
	}
	// Plans, the personal settings and helper scripts kept next to them are not tracked: the worktree sees the
	// checkout's own copy.
	for _, notes := range []string{".agentgate", ".claude-dev"} {
		if _, err := os.Stat(filepath.Join(main, notes)); err == nil {
			_ = os.Symlink(filepath.Join(main, notes), filepath.Join(g.Worktree, notes))
		}
	}
	if err := g.enter(); err != nil {
		return fail(err)
	}
	for index, argv := range cfg.Worktree.Setup {
		log := filepath.Join(g.dir(), fmt.Sprintf("setup-%d.txt", index))
		_ = os.MkdirAll(g.dir(), 0o700)
		output, err := os.Create(log)
		if err != nil {
			return fail(err)
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdout, cmd.Stderr = output, output
		cmd.Env = append(os.Environ(), "AGENTGATE_CHECKOUT="+main, "AGENTGATE_WORKTREE="+g.Worktree)
		err = ops.Run(cmd, seconds(cfg.Worktree.SetupTimeoutS))
		output.Close()
		if err != nil {
			if ops.IsCancelled(err) {
				return fail(err)
			}
			return fail(fmt.Errorf("worktree.setup `%s` failed (%v); its output is in %s", strings.Join(argv, " "), err, log))
		}
	}
	return nil
}

// discard removes the worktree and its branch.
func (g *Gate) discard() error {
	if err := g.validateWorktreePath(); err != nil {
		return err
	}
	main := g.Cwd
	if main == "" {
		main = base()
	}
	if _, err := gitIn(main, "worktree", "remove", "--force", g.Worktree); err != nil {
		if _, statErr := os.Lstat(g.Worktree); !errors.Is(statErr, os.ErrNotExist) {
			return err
		}
	}
	_, _ = gitIn(main, "worktree", "prune")
	if _, err := gitIn(main, "branch", "-D", g.Branch); err != nil {
		if _, verifyErr := gitIn(main, "show-ref", "--verify", "--quiet", "refs/heads/"+g.Branch); verifyErr == nil {
			return err
		}
	}
	return nil
}

// validateWorktreePath confines destructive cleanup to this task's generated
// directory and branch. Stored state is evidence, never authority to delete an
// arbitrary path. Existing legacy directory aliases remain supported.
func (g *Gate) validateWorktreePath() error {
	if !gateIDPattern.MatchString(g.ID) || !filepath.IsAbs(g.Worktree) || !strings.HasPrefix(g.Branch, "dev/") ||
		!strings.HasSuffix(g.Branch, g.ID[:6]) || strings.ContainsAny(strings.TrimPrefix(g.Branch, "dev/"), "/\\\x00") {
		return errors.New("invalid managed worktree identity; refusing cleanup")
	}
	expected := filepath.Join(checkoutDir(g.Cwd), "worktrees", g.ID[:12])
	if info, err := os.Lstat(g.Worktree); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed worktree is a symlink; refusing cleanup")
	}
	if filepath.Clean(g.Worktree) == expected {
		return nil
	}
	parent, parentErr := filepath.EvalSymlinks(filepath.Dir(g.Worktree))
	expectedParent, expectedErr := filepath.EvalSymlinks(filepath.Dir(expected))
	if parentErr == nil && expectedErr == nil && parent == expectedParent && filepath.Base(g.Worktree) == filepath.Base(expected) {
		return nil
	}
	return errors.New("worktree path is outside this task's managed directory; refusing cleanup")
}

// commitTask records the task's files on its branch. The branch is private to the tool, so no hook runs.
func (g *Gate) commitTask(message string) error {
	if err := g.auditScope(); err != nil {
		return err
	}
	if len(g.Foreign) > 0 {
		return fmt.Errorf("the isolated task changed files outside its reviewed scope: %s; add them through replan and review before finishing", strings.Join(g.Foreign, ", "))
	}
	beforeRepository, err := repositoryDigest()
	if err != nil {
		return err
	}
	staged, err := git("diff", "--cached", "--name-only", "-z")
	if err != nil {
		return err
	}
	for _, file := range nulList(staged) {
		if !slices.Contains(g.Files, file) {
			return fmt.Errorf("the private worktree has an unreviewed staged file: %s", file)
		}
	}
	for _, file := range g.Files {
		if _, err := os.Lstat(file); err == nil {
			if _, err := gitIn(".", "add", "-f", "--", file); err != nil {
				return err
			}
		} else if _, err := gitIn(".", "ls-files", "--error-unmatch", "--", file); err == nil {
			if _, err := gitIn(".", "rm", "-q", "--cached", "--", file); err != nil {
				return err
			}
		}
	}
	if exec.Command("git", "diff", "--cached", "--quiet").Run() == nil {
		return nil
	}
	if _, err := gitIn(".", "-c", "core.hooksPath=/dev/null", "commit", "-q", "--no-verify", "-m", message); err != nil {
		return err
	}
	if err := g.remeasure(); err != nil {
		return err
	}
	afterRepository, err := repositoryDigest()
	if err != nil {
		return err
	}
	// Recording the already verified bytes as a private commit changes HEAD,
	// but not the checked content. Preserve only receipts bound to that state.
	for index := range g.Checks {
		if g.Checks[index].Repository == beforeRepository {
			g.Checks[index].Repository = afterRepository
		}
	}
	return nil
}

// remeasure makes the worktree's present HEAD the point later changes are measured from.
func (g *Gate) remeasure() (err error) {
	head, _ := git("rev-parse", "HEAD")
	g.HeadBefore = strings.TrimSpace(head)
	g.Manifest, err = manifest()
	return err
}

var conflictMarker = regexp.MustCompile(`(?m)^(<<<<<<< |>>>>>>> |=======$)`)

// unresolved lists the files that still carry the conflict markers of a merge.
func (g *Gate) unresolved() []string {
	var out []string
	for _, file := range g.Conflicts {
		if raw, err := os.ReadFile(file); err == nil && conflictMarker.Match(raw) {
			out = append(out, file)
		}
	}
	return out
}

type LandInput struct {
	How string `json:"how,omitempty"` // apply (default), commit, branch or drop
}

// Land brings a finished isolated task into the user's checkout, on the user's word.
//
//	apply   the change appears in the checkout uncommitted; the user reviews and commits it
//	commit  the same, committed on the current branch with the task's summary
//	branch  the checkout is not touched; the task's branch is kept for a pull request
//	drop    the work is thrown away
//
// When the checkout moved on since the task started, it is merged into the task first, inside the worktree.
// The user's checkout never sees a conflict: one that appears goes back to the writers.
func Land(cfg *config.Config, gate *Gate, input LandInput) (any, error) {
	if gate.Worktree == "" {
		return nil, errors.New("this task was not isolated: its changes are already in the checkout")
	}
	if gate.Landed != "" {
		return nil, fmt.Errorf("this task is already closed (%s)", gate.Landed)
	}
	how := input.How
	if how == "" {
		how = "apply"
	}
	if !slices.Contains([]string{"apply", "commit", "branch", "drop"}, how) {
		return nil, errors.New("how is apply, commit, branch or drop")
	}
	main := gate.Cwd
	unlockLanding, err := flock(filepath.Join(checkoutDir(main), "land.lock"), syscall.LOCK_EX|syscall.LOCK_NB, "another task is landing in this checkout; retry after it finishes")
	if err != nil {
		return nil, err
	}
	defer unlockLanding()
	close := func(result map[string]any) (any, error) {
		leave()
		if how == "branch" {
			if _, err := gitIn(main, "worktree", "remove", "--force", gate.Worktree); err != nil {
				return nil, err
			}
			_, _ = gitIn(main, "worktree", "prune")
		} else {
			if err := gate.discard(); err != nil {
				return nil, err
			}
		}
		gate.Landed = how
		if gate.active() {
			gate.Phase, gate.HoldReason = "cancelled", "the isolated task was dropped"
		}
		result["landed"], result["phase"] = how, gate.Phase
		return result, gate.save()
	}
	if how == "drop" {
		return close(map[string]any{"next": "The worktree and its branch are removed. Nothing changed in the checkout."})
	}
	if gate.Phase != "complete" {
		return nil, fmt.Errorf("only a finished task lands; this one is %s", gate.Phase)
	}
	if origin == "" {
		return nil, fmt.Errorf("the task's worktree is gone (%s); only `land` {how: drop} is left", gate.Worktree)
	}
	if how == "commit" {
		staged, err := gitIn(main, "diff", "--cached", "--name-only", "-z")
		if err != nil {
			return nil, err
		}
		if staged != "" {
			return nil, errors.New("the checkout has staged changes; commit or unstage them before land {how: commit}, or use how: apply to preserve the index")
		}
	}
	// Only committed, reviewed contents may be landed. In particular, never
	// discard work someone added to the private worktree after completion.
	if changed, err := manifest(); err != nil {
		return nil, err
	} else if digest(changed) != digest(gate.Manifest) {
		return nil, errors.New("the completed worktree has new uncommitted changes; review and finish them before landing, or explicitly drop the task")
	}
	if err := gate.auditScope(); err != nil {
		return nil, err
	}
	if len(gate.Foreign) > 0 {
		return nil, fmt.Errorf("the completed worktree has unreviewed changes outside its scope: %s", strings.Join(gate.Foreign, ", "))
	}
	if current, _, err := gate.contentDigest(); err != nil {
		return nil, err
	} else if current != gate.ReviewDigest || len(gate.Reviews) == 0 || gate.Reviews[len(gate.Reviews)-1].Contract != gate.contract() {
		gate.Phase, gate.Final, gate.Reported = "awaiting-review", nil, false
		if err := gate.save(); err != nil {
			return nil, err
		}
		return nil, errors.New("the finished task changed since review; review and finish it again before landing")
	}
	target, err := gitIn(main, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	target = strings.TrimSpace(target)
	if exec.Command("git", "merge-base", "--is-ancestor", target, "HEAD").Run() != nil {
		if reopened, err := gate.catchUp(cfg, target); reopened != nil || err != nil {
			return reopened, err
		}
	}
	if how == "branch" {
		return close(map[string]any{"branch": gate.Branch,
			"next": "The branch holds the finished work on top of the checkout's HEAD. Push it or open a pull request when the user asks."})
	}
	patch := filepath.Join(gate.dir(), "land.patch")
	diff, err := exec.Command("git", "diff", "--binary", target, "HEAD").Output()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(patch, diff, 0o600); err != nil {
		return nil, err
	}
	changed, _ := git("diff", "--name-only", "-z", target, "HEAD")
	files := nulList(changed)
	unlockScope, err := scopeLock()
	if err != nil {
		return nil, err
	}
	defer unlockScope()
	if err := checkOwnership(files); err != nil {
		return nil, fmt.Errorf("cannot land task: %w", err)
	}
	if how == "commit" && len(files) > 0 {
		args := append([]string{"status", "--porcelain", "-z", "--untracked-files=all", "--"}, files...)
		dirty, err := gitIn(main, args...)
		if err != nil {
			return nil, err
		}
		if dirty != "" {
			return nil, errors.New("the checkout has uncommitted changes in files this task would commit; nothing was applied, commit or stash them first, or use how: apply")
		}
	}
	if current, err := gitIn(main, "rev-parse", "HEAD"); err != nil {
		return nil, err
	} else if strings.TrimSpace(current) != target {
		return nil, errors.New("the checkout advanced while preparing the landing; nothing was applied, retry to catch up")
	}
	if len(diff) > 0 {
		if _, err := gitIn(main, "apply", "--check", "--whitespace=nowarn", patch); err != nil {
			return nil, fmt.Errorf("the checkout has its own uncommitted changes where the task lands, so nothing was applied: %v. "+
				"Commit or stash them and land again, or land with how: branch", err)
		}
		if _, err := gitIn(main, "apply", "--whitespace=nowarn", patch); err != nil {
			return nil, err
		}
	}
	result := map[string]any{"files": files, "next": "The change is in the checkout, uncommitted. Tell the user; committing is theirs."}
	if how == "commit" && len(files) > 0 {
		message := "dev: " + firstLine(gate.Final.Summary)
		args := append([]string{"-C", main, "add", "--"}, files...)
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			result["commit_error"] = strings.TrimSpace(string(out))
		} else if out, err := exec.Command("git", append([]string{"-C", main, "commit", "-q", "--only", "-m", message, "--"}, files...)...).CombinedOutput(); err != nil {
			result["commit_error"] = strings.TrimSpace(string(out))
		} else {
			commit, _ := gitIn(main, "rev-parse", "--short", "HEAD")
			result["commit"], result["next"] = strings.TrimSpace(commit), "The change is committed on the current branch. Pushing is the user's."
		}
		if result["commit_error"] != nil {
			result["next"] = "The change is applied but the commit was refused (see commit_error). Report it; do not bypass the hook."
			// The actual outcome is an applied change, not a successful commit.
			how = "apply"
			result["requested"] = "commit"
			value, saveErr := close(result)
			return value, errors.Join(fmt.Errorf("change applied, but commit failed: %v", result["commit_error"]), saveErr)
		}
	}
	return close(result)
}

// catchUp merges the checkout's present HEAD into the task. A clean merge that leaves the reviewed content
// and the checks intact lets the landing go on; anything else reopens the task and returns its summary.
func (g *Gate) catchUp(cfg *config.Config, target string) (any, error) {
	reviewed := g.ReviewDigest
	reopen := func(reason string) (any, error) {
		g.Phase, g.Final, g.Reported = "awaiting-review", nil, false
		if err := g.save(); err != nil {
			return nil, err
		}
		summary := g.Summary(cfg)
		summary["landed"], summary["reopened"] = false, reason
		return summary, nil
	}
	_, mergeErr := gitIn(".", "merge", "--no-edit", "--no-verify", "-q", target)
	if mergeErr != nil {
		names, _ := git("diff", "--name-only", "-z", "--diff-filter=U")
		g.Conflicts = nulList(names)
		if len(g.Conflicts) == 0 {
			_, _ = gitIn(".", "merge", "--abort")
			return nil, mergeErr
		}
		// The markers are committed on the private branch, so the task's own history stays simple: resolving
		// them is an ordinary edit by the writers that own those files.
		if _, err := gitIn(".", "add", "-A"); err != nil {
			return nil, err
		}
		if _, err := gitIn(".", "commit", "-q", "--no-verify", "-m", "merge the checkout (conflicts to resolve)"); err != nil {
			return nil, err
		}
	}
	// The task is now measured against the new base: what the checkout brought in is not the task's change.
	for _, file := range append(slices.Clone(g.Files), g.Context...) {
		g.Baseline[file] = committed(target, file)
	}
	for _, file := range g.Conflicts {
		if !slices.Contains(g.Files, file) {
			g.Files = append(g.Files, file)
			g.Plan.Streams[0].Files = append(g.Plan.Streams[0].Files, file)
			g.Baseline[file] = committed(target, file)
		}
	}
	g.Context = subtract(g.Context, g.Files)
	g.WriteFiles = g.Files
	if err := g.remeasure(); err != nil {
		return nil, err
	}
	if len(g.Conflicts) > 0 {
		return reopen("the checkout changed the same lines: conflict markers are in " + strings.Join(g.Conflicts, ", "))
	}
	// Other code changed under the task: every declared check runs again, whatever its receipt says.
	failed := false
	content, _, err := g.contentDigest()
	if err != nil {
		return nil, err
	}
	for index := range g.Plan.Checks {
		check := &g.Plan.Checks[index]
		force := *check
		force.NoReuse = true
		receipt, err := g.runCheck(&force)
		if err != nil {
			return nil, err
		}
		g.Checks = append(g.Checks, receipt)
		failed = failed || receipt.ReturnCode != 0
	}
	switch {
	case failed:
		return reopen("a declared check fails after merging the checkout's new commits")
	case content != reviewed:
		return reopen("the checkout's new commits changed files this task also changes; the merged result needs a review")
	}
	quality, err := g.qualityInputs(cfg)
	if err != nil {
		return nil, err
	}
	if len(g.Reviews) == 0 || g.Reviews[len(g.Reviews)-1].QualityDigest != quality.fingerprint() {
		return reopen("fresh verification receipts after merging need acceptance review")
	}
	return nil, g.save()
}

// landing describes an isolated task for the summary.
func (g *Gate) landing() map[string]any {
	out := map[string]any{"worktree": g.Worktree, "branch": g.Branch}
	switch {
	case g.Landed != "":
		out["landed"] = g.Landed
	case g.Phase == "complete":
		out["ready_since"] = time.Unix(g.Final.At, 0).Format("2006-01-02 15:04")
	}
	if len(g.Conflicts) > 0 {
		out["conflicts"] = g.Conflicts
	}
	return out
}
