package dev

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
	"github.com/knaus94/agentgate/internal/update"
)

// AdoptLegacy brings over what an installation under the tool's earlier name left: its state and its settings
// move to the new directories, and a link stays at the old place so paths recorded in unfinished tasks, and a
// binary of the old name that is still running, keep working.
func AdoptLegacy() {
	home, _ := os.UserHomeDir()
	bases := map[string]string{"XDG_STATE_HOME": filepath.Join(home, ".local", "state"), "XDG_CONFIG_HOME": filepath.Join(home, ".config")}
	for variable, fallback := range bases {
		base := os.Getenv(variable)
		if base == "" {
			base = fallback
		}
		old, current := filepath.Join(base, "claude-dev"), filepath.Join(base, "agentgate")
		info, err := os.Lstat(old)
		if err != nil || !info.IsDir() {
			continue // nothing to adopt, or already a link
		}
		if _, err := os.Lstat(current); err == nil {
			continue
		}
		if os.Rename(old, current) == nil {
			_ = os.Symlink(current, old)
		}
	}
}

// Confined reports that the process cannot write its own state: it was started inside the caller's sandbox.
// Codex marks every command of a session with its sandbox variables, also the ones a rule lets out, so the
// only reliable sign is the thing itself.
func Confined() bool {
	if err := os.MkdirAll(Root(), 0o700); err != nil {
		return true
	}
	probe, err := os.CreateTemp(Root(), ".writable-*")
	if err != nil {
		return true
	}
	probe.Close()
	_ = os.Remove(probe.Name())
	return false
}

// Root is where tasks, receipts and designs live; tests replace it.
var Root = func() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "agentgate")
}

func digest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func newID() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())))
	return hex.EncodeToString(sum[:16])
}

// session identifies the lead conversation that owns a task.
func session() string {
	if value := os.Getenv("AGENTGATE_SESSION"); value != "" {
		return value
	}
	if config.Lead() == "codex" {
		return os.Getenv("CODEX_THREAD_ID")
	}
	return os.Getenv("CLAUDE_CODE_SESSION_ID")
}

func checkoutDir(cwd string) string {
	if cwd == "" {
		cwd = base()
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	return filepath.Join(Root(), digest(cwd)[:24])
}

// sessionDir holds one lead session's task in one checkout, so parallel sessions never block each other.
func sessionDir(cwd, id string) string {
	// Session identifiers come from CLI environments and hooks. Preserve normal UUIDs and names, but never
	// let an identifier become a path expression.
	if id != "" && !safeName.MatchString(id) {
		id = "session-" + digest(id)[:24]
	}
	return filepath.Join(checkoutDir(cwd), id)
}

func home() string { return sessionDir("", session()) }

func (g *Gate) dir() string { return filepath.Join(sessionDir(g.Cwd, g.Session), g.ID) }

// objects is the task's private object directory: its trees, and the blobs the repository does not have.
func (g *Gate) objects() string { return filepath.Join(g.dir(), "objects") }

func readGate(cwd, id string) *Gate {
	gate, _ := loadGate(cwd, id)
	return gate
}

func loadGate(cwd, id string) (*Gate, error) {
	gate, err := loadGateFile(filepath.Join(sessionDir(cwd, id), "state.json"))
	if err != nil || gate == nil {
		return gate, err
	}
	if gate.Session != id {
		return nil, errors.New("task state belongs to another session")
	}
	if checkoutDir(gate.Cwd) != checkoutDir(cwd) {
		return nil, errors.New("task state belongs to another checkout")
	}
	return gate, nil
}

// loadGateFile reads one task. A task saved by an earlier format is translated in memory; its origin trees
// are built later, under the task's own lock and in its own checkout (see upgrade).
func loadGateFile(path string) (*Gate, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read task state: %w", err)
	}
	return parseGate(raw)
}

// parseGate reads one task from the content of its state file.
func parseGate(raw []byte) (*Gate, error) {
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("task state is corrupt; preserve state.json and restore its last good copy: %w", err)
	}
	gate, err := &Gate{}, error(nil)
	switch {
	case probe.Version > stateVersion:
		return nil, fmt.Errorf("task state was written by a newer agentgate (format %d); update the tool", probe.Version)
	case probe.Version < stateVersion:
		if gate, err = migrateState(raw); err != nil {
			return nil, fmt.Errorf("task state of an earlier version cannot be read; preserve state.json: %w", err)
		}
	default:
		if err := json.Unmarshal(raw, gate); err != nil {
			return nil, fmt.Errorf("task state is corrupt; preserve state.json and restore its last good copy: %w", err)
		}
	}
	if err := validateGateState(gate); err != nil {
		return nil, fmt.Errorf("task state is invalid; preserve state.json and restore its last good copy: %w", err)
	}
	if gate.Plan != nil {
		// An earlier version kept the last amendment's own fields in the plan; they live in its revision.
		gate.Plan.Update, gate.Plan.Authorized, gate.Plan.Rework = "", "", nil
	}
	if gate.Dispositions == nil {
		gate.Dispositions = map[string]Disposition{}
	}
	if gate.Resolutions == nil {
		gate.Resolutions = map[string]string{}
	}
	if gate.Baselines == nil {
		gate.Baselines = map[string]int{}
	}
	if gate.Disputes == nil {
		gate.Disputes = map[string]Dispute{}
	}
	if gate.Tool != update.Version {
		gate.toolBefore = cmp.Or(gate.Tool, "an earlier version")
	}
	return gate, nil
}

// Current is this session's task in this checkout, or nil.
func Current() *Gate { return readGate("", session()) }

// LoadCurrent distinguishes a missing task from an unreadable or damaged one. Mutations must use it so
// corruption can never silently start another task.
func LoadCurrent() (*Gate, error) { return loadGate("", session()) }

func (g *Gate) save() error {
	g.Tool = update.Version
	dir := sessionDir(g.Cwd, g.Session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return ops.WriteJSON(filepath.Join(dir, "state.json"), g)
}

// requireLead refuses to let a spawned worker, or a different session, drive the task.
func requireLead(gate *Gate) error {
	if os.Getenv(provider.WorkerEnv) != "" {
		return errors.New("a worker cannot approve, decide or complete the task")
	}
	if session() == "" {
		return errors.New("no lead session: run inside Claude Code or Codex, or set AGENTGATE_SESSION")
	}
	if gate != nil && gate.Session != session() {
		return errors.New("the task belongs to another lead session")
	}
	return nil
}

// ErrBusy is the refusal of a command started while another operation of the same session runs.
var ErrBusy = errors.New("an operation is already running; inspect status")

// lock serializes operations of one session in one checkout. A second operation is rejected, not queued.
func lock() (func(), error) {
	if err := os.MkdirAll(home(), 0o700); err != nil {
		return nil, err
	}
	return flock(filepath.Join(home(), "lock"), syscall.LOCK_EX|syscall.LOCK_NB, ErrBusy)
}

// scopeLock makes claiming files atomic across the sessions of one checkout.
func scopeLock() (func(), error) {
	if err := os.MkdirAll(checkoutDir(""), 0o700); err != nil {
		return nil, err
	}
	return flock(filepath.Join(checkoutDir(""), "scope.lock"), syscall.LOCK_EX, nil)
}

// flock takes a lock on a file. busy, when given, is the error of a lock somebody else holds.
func flock(path string, how int, busy error) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), how); err != nil {
		file.Close()
		if busy != nil {
			return nil, busy
		}
		return nil, err
	}
	return func() { file.Close() }, nil
}

// claims is what the task owns in a shared checkout: the scope of its whole plan, for as long as it is active.
func (g *Gate) claims() []string {
	if !g.active() || g.Worktree != "" {
		return nil
	}
	return g.Plan.Files()
}

// checkOwnership refuses both known conflicts and unreadable claims. Corrupt peer state does not release
// its files; the caller holds scopeLock while checking and recording a claim.
func checkOwnership(scope []string) error {
	if len(scope) == 0 {
		return nil
	}
	var out []string
	root := checkoutDir("")
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("cannot verify file ownership in %s: %w", root, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		path := filepath.Join(root, entry.Name(), "state.json")
		if path == filepath.Join(home(), "state.json") {
			continue
		}
		other, err := loadGateFile(path)
		if err == nil && other == nil {
			continue
		}
		if err == nil && (other.Session == "" || path != filepath.Join(sessionDir(other.Cwd, other.Session), "state.json")) {
			err = errors.New("task state belongs to another checkout or session")
		}
		if err != nil {
			return fmt.Errorf("cannot verify file ownership from %s: %w; preserve the file and restore its last good copy", path, err)
		}
		if other.Session == session() {
			continue
		}
		if mine, theirs, collide := firstOverlap(scope, other.claims()); collide {
			what := mine
			if mine != theirs {
				what = mine + " (their " + theirs + ")"
			}
			out = append(out, fmt.Sprintf("%s (session %s, %s)", what, other.Session, other.Step))
		}
	}
	if len(out) > 0 {
		return fmt.Errorf("files are owned by another session's task: %s", strings.Join(out, "; "))
	}
	return nil
}

// atRoot refuses to open a task anywhere but the root of the working tree: every recorded path is relative to it.
func atRoot() error {
	top, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return errors.New("run inside a git repository")
	}
	here, _ := os.Getwd()
	root, _ := filepath.EvalSymlinks(strings.TrimSpace(top))
	if real, err := filepath.EvalSymlinks(here); err == nil {
		here = real
	}
	if root != here {
		return fmt.Errorf("run agentgate from the root of the working tree: %s", strings.TrimSpace(top))
	}
	return nil
}

// begin opens the task and records the working tree as the task finds it, including the user's uncommitted edits.
func begin(resolved *Resolved, name string) (gate *Gate, err error) {
	previous, err := LoadCurrent()
	if err != nil {
		return nil, err
	}
	if previous != nil && previous.active() {
		return nil, errors.New("an unfinished task already exists; `run` continues it, `amend` changes its plan, `pause` {cancelled: true} drops it")
	}
	if previous != nil && previous.Worktree != "" && previous.Landed == "" {
		return nil, errors.New("this session's finished isolated task is not landed yet: `land` it (or drop it) before starting another")
	}
	if err := atRoot(); err != nil {
		return nil, err
	}
	plan := resolved.Plan
	gate = &Gate{Version: stateVersion, ID: newID(), Session: session(), Request: resolved.Request, Cwd: base(), Plan: plan, PlanName: name,
		Modes: resolved.Modes, Mode: resolved.Modes[0], Step: StepWrite, CreatedAt: time.Now().Unix(), Executor: resolved.Executor,
		Dispositions: map[string]Disposition{}, Resolutions: map[string]string{}, Baselines: map[string]int{}, Disputes: map[string]Dispute{}}
	gate.Revisions = []Revision{{Number: 1, At: gate.CreatedAt, Digest: digest(plan)}}
	// A task the session planned keeps the id its trees were recorded under when the request was handed over.
	var draft *Draft
	if candidate := loadDraft(); gate.sessionWrites() && candidate != nil && candidate.Request == plan.Request {
		draft, gate.ID, gate.Opened = candidate, candidate.ID, candidate.At
	}
	staged := len(plan.Stages) > 1
	// A task the session writes works in the checkout the user is looking at: it is never isolated.
	if !gate.sessionWrites() && (resolved.Request.Worktree || (resolved.Config.Worktree.Default && !resolved.Request.ReviewFirst()) || (staged && isolateStaged(resolved.Config))) {
		// An isolated task shares no file with anyone, so it claims none. Preparing the worktree can take
		// minutes and must not hold the checkout's lock.
		if err := gate.isolate(resolved.Config); err != nil {
			return nil, err
		}
		defer func() {
			if err != nil {
				leave()
				gate.discard()
			}
		}()
	}
	unlock, err := scopeLock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if gate.Worktree == "" {
		if err := checkOwnership(plan.Files()); err != nil {
			return nil, err
		}
	}
	if gate.BaseTree, err = gate.snapshot(); err != nil {
		return nil, err
	}
	if draft != nil {
		// What changed between the request and now was written before the plan started: it is measured as the
		// task's own change, so a file no stage owns shows up instead of becoming part of what the task found.
		// The comparison is like for like: the earlier tree was recorded before any stream owned a file.
		if plain, err := snapshotTree(gate.objects(), nil); err == nil {
			if early, _ := diffTrees(gate.objects(), draft.Tree, plain); len(early) > 0 {
				gate.BaseTree = draft.Tree
			}
		}
		_ = os.Remove(draftFile())
	}
	// The declared inputs of every check are looked at before any work, not after a writer's round.
	if gate.said, err = gate.inspectInputs(plan.checksFor(-1), gate.BaseTree); err != nil {
		_ = os.RemoveAll(gate.dir())
		return nil, err
	}
	if resolved.Request.ReviewFirst() {
		// A review of existing work is measured from the commit it started at, not from the present tree.
		ref := plan.BaseRef
		if ref == "" {
			ref = "HEAD"
		}
		if gate.BaseTree = commitTree(gate.objects(), ref); gate.BaseTree == "" {
			return nil, fmt.Errorf("base_ref %q is not a commit", ref)
		}
		gate.Step = StepVerify
	}
	gate.StageTree = gate.BaseTree
	ops.BindGate(gate.ID)
	prune(resolved.Config.RetentionDays)
	return gate, gate.save()
}

// prune removes finished tasks older than the retention period.
func prune(days int) int {
	if days <= 0 {
		return 0
	}
	removed := 0
	limit := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	paths, _ := filepath.Glob(filepath.Join(Root(), "*", "*", "state.json"))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.ModTime().After(limit) {
			continue
		}
		gate, err := loadGateFile(path)
		if err != nil || gate == nil || gate.active() || (gate.Worktree != "" && gate.Landed == "") { // finished work nobody landed yet is not garbage
			continue
		}
		if os.RemoveAll(filepath.Dir(path)) == nil {
			removed++
		}
	}
	// Designs, preparations and context packs belong to a checkout; an old one is not counted, only dropped.
	var shared []string
	for _, kind := range []string{"designs", "preparations", "packs", "scout"} {
		found, _ := filepath.Glob(filepath.Join(Root(), "*", kind, "*"))
		shared = append(shared, found...)
	}
	for _, path := range shared {
		if info, err := os.Stat(path); err == nil && info.ModTime().Before(limit) {
			_ = os.RemoveAll(path)
		}
	}
	return removed
}

// Prune removes finished tasks older than the retention period and reports how many.
func Prune(days int) int { return prune(days) }

// HasSession reports whether the caller is a lead session.
func HasSession() bool { return session() != "" }
