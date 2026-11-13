package dev

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

// FileState is the content and mode of one file; a nil entry in a Snapshot means the file is absent.
type FileState struct {
	Bytes string `json:"bytes"`
	Mode  uint32 `json:"mode"`
}

type Snapshot map[string]*FileState

// Round is the set of streams being written now: the first implementation or one correction.
type Round struct {
	Batch      string         `json:"batch"`
	Streams    []Stream       `json:"streams"`
	Correction bool           `json:"correction,omitempty"`
	Boost      int            `json:"boost,omitempty"`          // effort steps above the mode's base, earned by stalled corrections
	Failures   []CheckFailure `json:"check_failures,omitempty"` // declared checks this correction must make pass
	Findings   []Finding      `json:"findings,omitempty"`       // accepted findings this correction must resolve
	Remedy     string         `json:"remedy,omitempty"`
	Conflicts  []string       `json:"conflicts,omitempty"` // files whose merge markers this correction must resolve
}

// CheckFailure is a failing declared check handed back to the writers: the command and the end of its output.
type CheckFailure struct {
	Argv       []string `json:"argv"`
	ReturnCode int      `json:"returncode"`
	Log        string   `json:"log"`
	Tail       string   `json:"output_tail"`
	Setup      bool     `json:"setup,omitempty"` // the measuring tool could not observe the page, not a measured code defect
}

type Writer struct {
	Stream     string       `json:"stream"`
	Route      config.Route `json:"route"`
	Dir        string       `json:"dir"`
	Session    string       `json:"session,omitempty"`
	Status     string       `json:"status"` // RUNNING, COMPLETED, BLOCKED, FAILED, CANCELLED
	Summary    string       `json:"summary"`
	Blockers   []string     `json:"blockers"`
	Handoff    string       `json:"handoff,omitempty"`    // the contract dependent streams build on
	Deviations []string     `json:"deviations,omitempty"` // where the work departs from the brief, for reviewers
	Error      string       `json:"error,omitempty"`
	Batch      string       `json:"batch"`
}

type Finding struct {
	ID            string `json:"id"`
	Axis          string `json:"axis"`
	Severity      string `json:"severity"`
	Confidence    string `json:"confidence"`
	Category      string `json:"category"`
	Path          string `json:"path"`
	Line          int    `json:"line"`
	Evidence      string `json:"evidence"`
	Impact        string `json:"impact"`
	Remedy        string `json:"remedy"`
	Preserve      string `json:"preserve"`
	BoundaryBreak bool   `json:"boundary_break"`
	Screenshot    string `json:"screenshot,omitempty"`
}

type Resolution struct {
	ID       string `json:"id"`
	Resolved bool   `json:"resolved"`
	Evidence string `json:"evidence"`
}

type Report struct {
	Axis        string       `json:"axis"`
	Status      string       `json:"status"` // PASS or NEEDS_FIX; a BLOCKED report fails its lane
	Findings    []Finding    `json:"findings"`
	Checked     []string     `json:"checked"`
	Resolutions []Resolution `json:"resolutions"`
	ScopeFiles  []string     `json:"scope_files,omitempty"`
}

// LaneRun is one reviewer process: which axes it judged, over which files, on which model.
type LaneRun struct {
	Axes    []string       `json:"axes"`
	Scope   string         `json:"scope"`
	Files   []string       `json:"files"`
	Route   config.Route   `json:"route"`
	Session string         `json:"session"`
	Dir     string         `json:"dir"`
	Reports []Report       `json:"reports"`
	Quality *QualityReport `json:"quality,omitempty"`
}

type Review struct {
	Iteration     int       `json:"iteration"`
	Digest        string    `json:"digest"`
	Contract      string    `json:"contract"`
	Lanes         []LaneRun `json:"lanes"`
	Dir           string    `json:"dir"`
	Rechecked     []string  `json:"rechecked"`
	QualityDigest string    `json:"quality_digest,omitempty"`
}

type Disposition struct {
	ID          string   `json:"id"`
	Decision    string   `json:"decision"` // accepted, rejected, deferred, needs-approval
	Evidence    string   `json:"evidence"`
	Files       []string `json:"files,omitempty"`
	Basis       string   `json:"basis,omitempty"`
	DuplicateOf string   `json:"duplicate_of,omitempty"`
}

type CheckReceipt struct {
	Argv        []string `json:"argv"`
	ReturnCode  int      `json:"returncode"`
	Digest      string   `json:"digest"`
	InputKey    string   `json:"input_key,omitempty"`
	Log         string   `json:"log"`
	Preexisting bool     `json:"preexisting,omitempty"`
	Reused      bool     `json:"reused,omitempty"`
}

type Reassessment struct {
	Evidence        string `json:"evidence"`
	ChangedApproach string `json:"changed_approach"`
}

type Correction struct {
	Iteration    int           `json:"iteration"`
	FindingIDs   []string      `json:"finding_ids"`
	StartDigest  string        `json:"start_digest"`
	CheckIDs     []string      `json:"check_ids,omitempty"` // failing checks this correction was sent to repair
	Brief        string        `json:"brief"`
	Reassessment *Reassessment `json:"reassessment,omitempty"`
}

type VisualRun struct {
	Iteration int        `json:"iteration"`
	Digest    string     `json:"digest"`
	Contract  string     `json:"contract"`
	Status    string     `json:"status"`
	Summary   string     `json:"summary"`
	Coverage  []Coverage `json:"coverage"`
	Excluded  []Excluded `json:"excluded"`
	Findings  []Finding  `json:"findings"`
	Console   []string   `json:"console_errors"`
	Dir       string     `json:"dir"`
}

// Gate is the whole state of one task: what was agreed, what changed, what was verified.
type Gate struct {
	ID            string                  `json:"id"`
	Session       string                  `json:"session"`
	Cwd           string                  `json:"cwd"`
	Request       Request                 `json:"request"`
	Mode          string                  `json:"mode"`
	Plan          *Plan                   `json:"plan"`
	PlanVersion   int                     `json:"plan_version"`
	Files         []string                `json:"files"`
	Context       []string                `json:"context"`
	Baseline      Snapshot                `json:"baseline"`
	HeadBefore    string                  `json:"head_before"`
	Manifest      map[string]string       `json:"manifest"`
	WriteFiles    []string                `json:"write_files"`
	Phase         string                  `json:"phase"`
	ResumePhase   string                  `json:"resume_phase,omitempty"`
	HoldReason    string                  `json:"hold_reason,omitempty"`
	Iteration     int                     `json:"iteration"`
	Writers       []*Writer               `json:"writers"`
	Reviews       []Review                `json:"reviews"`
	ReviewDigest  string                  `json:"review_digest,omitempty"`
	ReviewError   string                  `json:"review_error,omitempty"`
	VisualRuns    []VisualRun             `json:"visual_runs,omitempty"`
	VisualSkip    string                  `json:"visual_skip,omitempty"`
	Dispositions  map[string]Disposition  `json:"dispositions"`
	Resolutions   map[string]string       `json:"resolutions"`
	Checks        []CheckReceipt          `json:"checks"`
	Baselines     map[string]int          `json:"baselines,omitempty"` // check key -> exit code before any writing
	Corrections   []Correction            `json:"corrections,omitempty"`
	Round         *Round                  `json:"round,omitempty"`
	ContextPack   string                  `json:"context_pack,omitempty"`
	ScoutNote     string                  `json:"scout_note,omitempty"`
	Boost         int                     `json:"boost,omitempty"`
	Foreign       []string                `json:"foreign_changes,omitempty"`
	Substitutions []provider.Substitution `json:"substitutions,omitempty"`
	Final         *Final                  `json:"final,omitempty"`
	Worktree      string                  `json:"worktree,omitempty"`      // an isolated task works here, not in Cwd
	Branch        string                  `json:"branch,omitempty"`        // the branch of that worktree
	Landed        string                  `json:"landed,omitempty"`        // how an isolated task was closed: apply, commit, branch, drop
	Conflicts     []string                `json:"conflicts,omitempty"`     // files left with merge markers by catching up with the checkout
	Epic          *Plan                   `json:"epic,omitempty"`          // staged tasks: the whole plan; Plan is the present stage
	StageIndex    int                     `json:"stage_index,omitempty"`   // staged tasks: which stage is the present one
	StageRecords  []StageRecord           `json:"stage_records,omitempty"` // staged tasks: the accepted stages
	EpicBaseline  Snapshot                `json:"epic_baseline,omitempty"` // staged tasks: every file as the task found it
	Integration   bool                    `json:"integration,omitempty"`   // staged tasks: the pass over the whole change is on
	Signals       []Signal                `json:"slop_signals,omitempty"`  // mechanical hits on the lines the task added, as of the last review
	Reported      bool                    `json:"completion_reported,omitempty"`
	CreatedAt     int64                   `json:"created_at"`
}

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
func sessionDir(cwd, id string) string { return filepath.Join(checkoutDir(cwd), id) }

func home() string { return sessionDir("", session()) }

func (g *Gate) dir() string { return filepath.Join(sessionDir(g.Cwd, g.Session), g.ID) }

func readGate(cwd, id string) *Gate {
	raw, err := os.ReadFile(filepath.Join(sessionDir(cwd, id), "state.json"))
	if err != nil {
		return nil
	}
	gate := &Gate{}
	if json.Unmarshal(raw, gate) != nil {
		return nil
	}
	return gate
}

// Current is this session's gate in this checkout, or nil.
func Current() *Gate { return readGate("", session()) }

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

func capture(files []string) (Snapshot, error) {
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
	return capture(append(slices.Clone(g.Files), g.Context...))
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
	info, err := os.Lstat(name)
	switch {
	case err != nil:
		return "deleted"
	case info.Mode()&os.ModeSymlink != 0:
		target, _ := os.Readlink(name)
		return "link:" + target
	case !info.Mode().IsRegular():
		return "special"
	}
	raw, _ := os.ReadFile(name)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]) + ":" + strconv.FormatUint(uint64(info.Mode().Perm()), 8)
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
			out[name] = entry(name)
		}
	}
	return out, nil
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
	for name := range mergeKeys(g.Manifest, now) {
		if g.Manifest[name] != now[name] && !slices.Contains(g.WriteFiles, name) && !slices.Contains(g.Foreign, name) {
			g.Foreign = append(g.Foreign, name)
		}
	}
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

// owners lists unfinished gates of other sessions in this checkout that own any of the files.
func owners(files []string) []string {
	var out []string
	paths, _ := filepath.Glob(filepath.Join(checkoutDir(""), "*", "state.json"))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		other := &Gate{}
		if err != nil || json.Unmarshal(raw, other) != nil || other.Session == session() || !other.active() || other.Worktree != "" {
			continue
		}
		for _, file := range files {
			if slices.Contains(other.Files, file) {
				out = append(out, fmt.Sprintf("%s (session %s, %s)", file, other.Session, other.Phase))
			}
		}
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
	if clash := owners(files); gate.Worktree == "" && len(clash) > 0 {
		return nil, fmt.Errorf("files are owned by another session's gate: %s", strings.Join(clash, "; "))
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
	if clash := owners(added); g.Worktree == "" && len(clash) > 0 {
		return fmt.Errorf("files are owned by another session's gate: %s", strings.Join(clash, "; "))
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
	g.Plan, g.Mode, g.PlanVersion = plan, resolved.Mode, g.PlanVersion+1
	if g.Manifest, err = manifest(); err != nil {
		return err
	}
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

// resourceCapacity is how many workers fit in the free CPU and memory, honouring cgroup limits.
func resourceCapacity(cfg *config.Config) int {
	cpus := runtime.NumCPU()
	memory := int64(1) << 62
	if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "MemAvailable:" {
				kib, _ := strconv.ParseInt(fields[1], 10, 64)
				memory = kib * 1024
			}
		}
	}
	group := "/"
	if raw, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if rest, ok := strings.CutPrefix(line, "0::"); ok {
				group = rest
			}
		}
	}
	for dir := filepath.Join("/sys/fs/cgroup", group); strings.HasPrefix(dir, "/sys/fs/cgroup"); dir = filepath.Dir(dir) {
		limit, _ := os.ReadFile(filepath.Join(dir, "memory.max"))
		used, _ := os.ReadFile(filepath.Join(dir, "memory.current"))
		if value, err := strconv.ParseInt(strings.TrimSpace(string(limit)), 10, 64); err == nil {
			current, _ := strconv.ParseInt(strings.TrimSpace(string(used)), 10, 64)
			memory = min(memory, max(0, value-current))
		}
		if quota := strings.Fields(string(readOr(filepath.Join(dir, "cpu.max")))); len(quota) == 2 && quota[0] != "max" {
			q, _ := strconv.Atoi(quota[0])
			p, _ := strconv.Atoi(quota[1])
			if p > 0 {
				cpus = min(cpus, max(1, q/p))
			}
		}
	}
	return int(min(int64(cpus*2), memory/(int64(cfg.WorkerMemoryMiB)<<20)))
}

func readOr(path string) []byte { raw, _ := os.ReadFile(path); return raw }

// Prune removes finished tasks older than the retention period and reports how many.
func Prune(days int) int { return prune(days) }

// HasSession reports whether the caller is a lead session.
func HasSession() bool { return session() != "" }
