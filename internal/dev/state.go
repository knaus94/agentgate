package dev

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
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

// Root is where gates, receipts and designs live; tests replace it.
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

// session identifies the lead conversation that owns a gate.
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

// sessionDir holds one lead session's gate in one checkout, so parallel sessions never block each other.
func sessionDir(cwd, id string) string {
	// Session identifiers come from CLI environments and hooks. Preserve normal
	// UUIDs and names, but never let an identifier become a path expression.
	if id != "" && !safeName.MatchString(id) {
		id = "session-" + digest(id)[:24]
	}
	return filepath.Join(checkoutDir(cwd), id)
}

func home() string { return sessionDir("", session()) }

func (g *Gate) dir() string { return filepath.Join(sessionDir(g.Cwd, g.Session), g.ID) }

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

func loadGateFile(path string) (*Gate, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read task state: %w", err)
	}
	gate := &Gate{}
	if err := json.Unmarshal(raw, gate); err != nil {
		return nil, fmt.Errorf("task state is corrupt; preserve state.json and restore its last good copy: %w", err)
	}
	if err := validateGateState(gate); err != nil {
		return nil, fmt.Errorf("task state is invalid; preserve state.json and restore its last good copy: %w", err)
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
	return gate, nil
}

// Current is this session's gate in this checkout, or nil.
func Current() *Gate { return readGate("", session()) }

// LoadCurrent distinguishes a missing task from an unreadable or damaged one.
// Mutations must use it so corruption can never silently start another task.
func LoadCurrent() (*Gate, error) { return loadGate("", session()) }

func (g *Gate) save() error {
	dir := sessionDir(g.Cwd, g.Session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return ops.WriteJSON(filepath.Join(dir, "state.json"), g)
}

func (g *Gate) active() bool { return g.Phase != "complete" && g.Phase != "cancelled" }

// requireLead refuses to let a spawned worker, or a different session, drive the gate.
func requireLead(gate *Gate) error {
	if os.Getenv(provider.WorkerEnv) != "" {
		return errors.New("a worker cannot approve, triage or complete the gate")
	}
	if session() == "" {
		return errors.New("no lead session: run inside Claude Code or Codex, or set AGENTGATE_SESSION")
	}
	if gate != nil && gate.Session != session() {
		return errors.New("the gate belongs to another lead session")
	}
	return nil
}

// lock serializes operations of one session in one checkout. A second operation is rejected, not queued.
func lock() (func(), error) {
	if err := os.MkdirAll(home(), 0o700); err != nil {
		return nil, err
	}
	return flock(filepath.Join(home(), "lock"), syscall.LOCK_EX|syscall.LOCK_NB, "an operation is already running; inspect status")
}

// scopeLock makes claiming files atomic across the sessions of one checkout.
func scopeLock() (func(), error) {
	if err := os.MkdirAll(checkoutDir(""), 0o700); err != nil {
		return nil, err
	}
	return flock(filepath.Join(checkoutDir(""), "scope.lock"), syscall.LOCK_EX, "")
}

func flock(path string, how int, busy string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), how); err != nil {
		file.Close()
		if busy != "" {
			return nil, errors.New(busy)
		}
		return nil, err
	}
	return func() { file.Close() }, nil
}

// checkOwnership refuses both known conflicts and unreadable claims. Corrupt peer state does not release
// its files; the caller holds scopeLock while checking and recording a claim.
func checkOwnership(files []string) error {
	if len(files) == 0 {
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
		if other.Session == session() || !other.active() || other.Worktree != "" {
			continue
		}
		for _, file := range files {
			if slices.Contains(other.Files, file) {
				out = append(out, fmt.Sprintf("%s (session %s, %s)", file, other.Session, other.Phase))
			}
		}
	}
	if len(out) > 0 {
		return fmt.Errorf("files are owned by another session's gate: %s", strings.Join(out, "; "))
	}
	return nil
}

func subtract(values, remove []string) []string {
	var out []string
	for _, value := range values {
		if !slices.Contains(remove, value) {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// begin creates the gate and freezes the pre-task state of every owned file, including the user's uncommitted edits.
func begin(resolved *Resolved) (gate *Gate, err error) {
	if previous := Current(); previous != nil && previous.active() {
		return nil, errors.New("an unfinished gate already exists; resume it or hold it explicitly")
	}
	if previous := Current(); previous != nil && previous.Worktree != "" && previous.Landed == "" {
		return nil, errors.New("this session's finished isolated task is not landed yet: `land` it (or drop it) before starting another")
	}
	// A staged task begins with its first stage; the gate keeps the whole plan beside it.
	whole := resolved
	if len(resolved.Stages) > 0 {
		resolved = resolved.Stages[0]
	}
	plan := resolved.Plan
	files := plan.Files()
	gate = &Gate{ID: newID(), Session: session(), Request: resolved.Request, Cwd: base()}
	if resolved.Request.Worktree || (resolved.Config.Worktree.Default && !resolved.Request.ReviewFirst()) ||
		(len(whole.Stages) > 0 && isolateStaged(resolved.Config)) {
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
		if err := checkOwnership(files); err != nil {
			return nil, err
		}
	}
	context := subtract(append(slices.Clone(plan.Context), guidance(files)...), files)
	baseline, err := capture(append(slices.Clone(files), context...))
	if err != nil {
		return nil, err
	}
	phase := "implementing"
	if resolved.Request.ReviewFirst() {
		phase = "awaiting-review"
		ref := plan.BaseRef
		if ref == "" {
			ref = "HEAD"
		}
		commit, err := git("rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
		if err != nil {
			return nil, fmt.Errorf("base_ref %q is not a commit", ref)
		}
		commit = strings.TrimSpace(commit)
		for _, file := range files {
			baseline[file] = nil
			if raw, err := exec.Command("git", "show", commit+":"+file).Output(); err == nil {
				mode := uint32(0o644)
				if tree, _ := git("ls-tree", commit, "--", file); strings.HasPrefix(tree, "100755") {
					mode = 0o755
				}
				baseline[file] = &FileState{Bytes: base64.StdEncoding.EncodeToString(raw), Mode: mode}
			}
		}
	}
	head, _ := git("rev-parse", "HEAD")
	files0, err := manifest()
	if err != nil {
		return nil, err
	}
	gate.Mode, gate.Plan, gate.PlanVersion = resolved.Mode, plan, 1
	gate.Files, gate.Context, gate.Baseline, gate.HeadBefore, gate.Manifest = files, context, baseline, strings.TrimSpace(head), files0
	gate.WriteFiles, gate.Phase, gate.CreatedAt = files, phase, time.Now().Unix()
	gate.Dispositions, gate.Resolutions, gate.Baselines = map[string]Disposition{}, map[string]string{}, map[string]int{}
	if len(whole.Stages) > 0 {
		gate.Epic = whole.Plan
		gate.EpicHead, gate.EpicManifest = gate.HeadBefore, maps.Clone(gate.Manifest)
		if gate.EpicBaseline, err = capture(whole.Plan.Files()); err != nil {
			return nil, err
		}
	}
	ops.BindGate(gate.ID)
	prune(resolved.Config.RetentionDays)
	return gate, gate.save()
}

// extend replaces the contract of an unfinished task. Accumulated files and original baselines are kept.
func (g *Gate) extend(resolved *Resolved) error {
	plan := resolved.Plan
	if strings.TrimSpace(plan.Update) == "" {
		return errors.New("a continuation needs update: what changed and why, plus the complete replacement plan")
	}
	files := plan.Files()
	if missing := subtract(g.Files, files); len(missing) > 0 {
		return fmt.Errorf("a continuation keeps every owned file; missing: %s", strings.Join(missing, ", "))
	}
	if resolved.Request.Mode == "" {
		resolved.Mode = g.Mode // a continuation keeps its mode unless the request names another
	}
	unlock, err := scopeLock()
	if err != nil {
		return err
	}
	defer unlock()
	added := subtract(files, g.Files)
	if g.Worktree == "" {
		if err := checkOwnership(added); err != nil {
			return err
		}
	}
	context := subtract(append(append(slices.Clone(g.Context), plan.Context...), guidance(files)...), files)
	fresh, err := capture(append(added, subtract(context, g.Context)...))
	if err != nil {
		return err
	}
	for name, state := range fresh {
		if _, known := g.Baseline[name]; !known {
			g.Baseline[name] = state
		}
	}
	g.Files, g.Context, g.WriteFiles = files, context, files
	g.Foreign = subtract(g.Foreign, files)
	g.Plan, g.Mode, g.PlanVersion = plan, resolved.Mode, g.PlanVersion+1
	// Keep the original checkout manifest: changing a plan must not turn
	// unreviewed writes outside its scope into an accepted baseline.
	return g.save()
}

// prune removes finished gates older than the retention period.
func prune(days int) int {
	if days <= 0 {
		return 0
	}
	removed := 0
	limit := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	paths, _ := filepath.Glob(filepath.Join(Root(), "*", "*", "state.json"))
	for _, path := range paths {
		info, err := os.Stat(path)
		raw, _ := os.ReadFile(path)
		gate := &Gate{}
		if err != nil || info.ModTime().After(limit) || json.Unmarshal(raw, gate) != nil || gate.active() ||
			(gate.Worktree != "" && gate.Landed == "") { // finished work nobody landed yet is not garbage
			continue
		}
		if os.RemoveAll(filepath.Dir(path)) == nil {
			removed++
		}
	}
	// Designs and context packs belong to a checkout; an old one is not counted, only dropped.
	shared, _ := filepath.Glob(filepath.Join(Root(), "*", "designs", "*"))
	packs, _ := filepath.Glob(filepath.Join(Root(), "*", "scout", "*"))
	for _, path := range append(shared, packs...) {
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
