package dev

import (
	"github.com/knaus94/agentgate/internal/config"
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
	Policy        string    `json:"policy_digest,omitempty"`
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
	Repository  string   `json:"repository_digest,omitempty"` // implicit inputs when no explicit globs were declared
	Error       string   `json:"error,omitempty"`
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
	AuthorModels  []string                `json:"author_models,omitempty"` // retained across stages and correction routes
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
	Worktree      string                  `json:"worktree,omitempty"`         // an isolated task works here, not in Cwd
	Branch        string                  `json:"branch,omitempty"`           // the branch of that worktree
	Landed        string                  `json:"landed,omitempty"`           // how an isolated task was closed: apply, commit, branch, drop
	Conflicts     []string                `json:"conflicts,omitempty"`        // files left with merge markers by catching up with the checkout
	Epic          *Plan                   `json:"epic,omitempty"`             // staged tasks: the whole plan; Plan is the present stage
	StageIndex    int                     `json:"stage_index,omitempty"`      // staged tasks: which stage is the present one
	StageRecords  []StageRecord           `json:"stage_records,omitempty"`    // staged tasks: the accepted stages
	EpicBaseline  Snapshot                `json:"epic_baseline,omitempty"`    // staged tasks: every file as the task found it
	EpicHead      string                  `json:"epic_head_before,omitempty"` // origin for files admitted after the first stage
	EpicManifest  map[string]string       `json:"epic_manifest,omitempty"`
	Integration   bool                    `json:"integration,omitempty"`  // staged tasks: the pass over the whole change is on
	Signals       []Signal                `json:"slop_signals,omitempty"` // mechanical hits on the lines the task added, as of the last review
	Reported      bool                    `json:"completion_reported,omitempty"`
	CreatedAt     int64                   `json:"created_at"`
}
