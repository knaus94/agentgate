package dev

import (
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/provider"
)

// stateVersion is the format of state.json. Older tasks are migrated when they are read.
const stateVersion = 2

// Round is the set of streams being written now: the first implementation of a stage or one correction.
type Round struct {
	Batch      string         `json:"batch"`
	Streams    []Stream       `json:"streams"`
	Correction bool           `json:"correction,omitempty"`
	Auto       bool           `json:"auto,omitempty"`           // opened by the tool from a verification, not by the lead
	Boost      int            `json:"boost,omitempty"`          // effort steps above the mode's base, earned by stalled corrections
	Failures   []CheckFailure `json:"check_failures,omitempty"` // declared checks this correction must make pass
	Findings   []Finding      `json:"findings,omitempty"`       // accepted findings this correction must resolve
	Remedy     string         `json:"remedy,omitempty"`
	Work       []WorkOrder    `json:"work,omitempty"`      // what the lead gave the writers of this round
	Conflicts  []string       `json:"conflicts,omitempty"` // files whose merge markers this correction must resolve
}

// WorkOrder is work the lead gives the writer of one stream: what to do, and the criterion it satisfies when
// there is one.
type WorkOrder struct {
	Stream      string `json:"stream"`
	Note        string `json:"note"`
	Requirement string `json:"requirement,omitempty"` // an unmet requirement or rule of the present assessment
	Escalate    bool   `json:"escalate,omitempty"`    // one more step of effort for this writer
}

// CheckFailure is a failing declared check handed back to the writers: the command and the end of its output.
type CheckFailure struct {
	Argv       []string `json:"argv"`
	ReturnCode int      `json:"returncode"`
	Log        string   `json:"log"`
	Tail       string   `json:"output_tail"`
	Setup      bool     `json:"setup,omitempty"` // the measuring tool could not observe the page, not a measured code defect
	// Environment: the check passed before on the same files of the task. Its failure lies outside the code: a
	// service, a stand, the machine.
	Environment bool `json:"environment,omitempty"`
}

// Dispute is a writer's evidence that a finding it was sent is wrong. The reviewer that raised the finding
// answers it; only a dispute the reviewer upholds reaches the lead.
type Dispute struct {
	ID       string `json:"id"`
	Evidence string `json:"evidence"`
	Stream   string `json:"stream,omitempty"`
	Upheld   bool   `json:"upheld,omitempty"` // the reviewer reported the finding again after reading the dispute
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
	Disputes   []Dispute    `json:"disputes,omitempty"`   // findings of this correction the writer shows to be wrong
	Error      string       `json:"error,omitempty"`
	Batch      string       `json:"batch"`
	Stale      bool         `json:"stale,omitempty"` // an amendment changed this stream's contract after it reported
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

// LaneRun is one reviewer process: which axes it judged, over which scope, on which model.
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

// Review is one independent reading of a change. It is bound to what it read (Key), to what was to be built
// (Contract) and to what it judged with which receipts (QualityDigest): each can go stale on its own.
type Review struct {
	Iteration     int       `json:"iteration"`
	Tree          string    `json:"tree"`     // the working tree it read
	Key           string    `json:"key"`      // the scoped change and the cited context
	Contract      string    `json:"contract"` // the code contract
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
	Stream      string   `json:"stream,omitempty"` // the stream an accepted finding goes to, when its path does not say
	Auto        bool     `json:"auto,omitempty"`   // sent to its author by the blocking rule, not judged by the lead
}

type CheckReceipt struct {
	Argv        []string `json:"argv"`
	ReturnCode  int      `json:"returncode"`
	Tree        string   `json:"tree,omitempty"`      // the working tree it ran against, when it declared no inputs
	InputKey    string   `json:"input_key,omitempty"` // the content of its declared inputs
	Error       string   `json:"error,omitempty"`
	Log         string   `json:"log"`
	Preexisting bool     `json:"preexisting,omitempty"`
	Reused      bool     `json:"reused,omitempty"`
	Iteration   int      `json:"iteration,omitempty"` // the correction it ran after: a failure from before the last one is stale
}

type Correction struct {
	Iteration  int         `json:"iteration"`
	FindingIDs []string    `json:"finding_ids"`
	StartKey   string      `json:"start_key"`           // the reviewed change this correction started from
	CheckIDs   []string    `json:"check_ids,omitempty"` // failing checks this correction was sent to repair
	Brief      string      `json:"brief"`
	Work       []WorkOrder `json:"work,omitempty"` // what the lead gave the writers
}

type VisualRun struct {
	Iteration int        `json:"iteration"`
	Key       string     `json:"key"`
	Contract  string     `json:"contract"`
	Status    string     `json:"status"`
	Summary   string     `json:"summary"`
	Coverage  []Coverage `json:"coverage"`
	Excluded  []Excluded `json:"excluded"`
	Findings  []Finding  `json:"findings"`
	Console   []string   `json:"console_errors"`
	Limits    []string   `json:"limits,omitempty"` // what the pass observed but could not establish
	Dir       string     `json:"dir"`
}

// Revision is one plan the task ran under: why it replaced the one before and what it changed.
type Revision struct {
	Number     int      `json:"number"`
	At         int64    `json:"at"`
	Reason     string   `json:"reason,omitempty"`
	Authorized string   `json:"authorized,omitempty"` // who allowed a changed requirement, in what words
	Rework     []string `json:"rework,omitempty"`     // the streams the amendment went to as new work
	Digest     string   `json:"digest"`
	Changes    []string `json:"changes,omitempty"`
}

// WriterRef is enough of a finished writer to continue its session later.
type WriterRef struct {
	Route   config.Route `json:"route"`
	Dir     string       `json:"dir"`
	Session string       `json:"session"`
}

// StageRecord is what is kept of an accepted stage.
type StageRecord struct {
	Name       string               `json:"name"`
	Mode       string               `json:"mode"`
	Summary    string               `json:"summary"`
	AcceptedAt int64                `json:"accepted_at"`
	Rounds     int                  `json:"correction_rounds"`
	Findings   int                  `json:"findings"`
	Commit     string               `json:"commit,omitempty"`
	Tree       string               `json:"tree,omitempty"`      // the working tree the stage was accepted at
	Handoffs   map[string]string    `json:"handoffs,omitempty"`  // stream -> the contract later stages build on
	Writers    map[string]WriterRef `json:"writers,omitempty"`   // stream -> the session a later correction continues
	Authors    []string             `json:"authors,omitempty"`   // a stage the session wrote: who wrote it, as the receipt names them
	Reviewers  []string             `json:"reviewers,omitempty"` // the routes that reviewed the stage
	Checks     []string             `json:"checks,omitempty"`    // the declared checks and their results
	Notes      []string             `json:"notes,omitempty"`     // findings left as notes, one line each
	Browser    string               `json:"browser,omitempty"`   // what its browser verification came to
	Archive    string               `json:"archive"`             // the stage's reviews, findings and receipts
}

// Final is the completion receipt.
type Final struct {
	Summary string `json:"summary"`
	Tree    string `json:"tree,omitempty"` // the working tree the task was completed at
	Key     string `json:"key,omitempty"`  // the task's own change at completion: what a later edit of its files alters
	At      int64  `json:"at"`
}

// Gate is the whole state of one task: what was agreed, where the work is, what was verified.
type Gate struct {
	Version      int                    `json:"version"`
	ID           string                 `json:"id"`
	Session      string                 `json:"session"`
	Cwd          string                 `json:"cwd"`
	Request      Request                `json:"request"`
	Plan         *Plan                  `json:"plan"`                   // in its one shape: stages only
	PlanName     string                 `json:"plan_name"`              // the saved plan it was started from, when there is one
	Revisions    []Revision             `json:"revisions"`              // every plan the task ran under
	Modes        []string               `json:"modes"`                  // the mode of every stage of the present plan
	Stage        int                    `json:"stage"`                  // index of the present stage; len(stages) once all are accepted
	Whole        bool                   `json:"whole,omitempty"`        // the pass over the whole change is on
	Step         string                 `json:"step"`                   // write, verify, decide, done or cancelled
	Pause        *Paused                `json:"pause,omitempty"`        // waiting for a person; the position is unchanged
	Mode         string                 `json:"mode"`                   // the mode of the present stage
	Executor     string                 `json:"executor,omitempty"`     // session: the lead's own session writes; workers, or empty in a task of 0.21 and earlier: sandboxed writers
	BaseTree     string                 `json:"base_tree"`              // the working tree as the task found it
	StageTree    string                 `json:"stage_tree"`             // the working tree as the present stage found it
	StartCommit  string                 `json:"start_commit,omitempty"` // an isolated task: the commit its worktree was made from
	Accepted     []StageRecord          `json:"accepted,omitempty"`
	Round        *Round                 `json:"round,omitempty"`
	Rounds       int                    `json:"rounds,omitempty"`      // writer rounds opened in the present stage
	AutoRounds   int                    `json:"auto_rounds,omitempty"` // corrections opened by the tool since the lead last acted
	Iteration    int                    `json:"iteration"`             // corrections of the present stage
	Writers      []*Writer              `json:"writers"`
	Reviews      []Review               `json:"reviews"`
	ReviewError  string                 `json:"review_error,omitempty"`
	VisualRuns   []VisualRun            `json:"visual_runs,omitempty"`
	VisualSkip   string                 `json:"visual_skip,omitempty"`
	Dispositions map[string]Disposition `json:"dispositions"`
	Resolutions  map[string]string      `json:"resolutions"`
	Disputes     map[string]Dispute     `json:"disputes,omitempty"`
	Demands      []Finding              `json:"demands,omitempty"`  // work sent to writers from an unmet criterion
	Prepared     map[string]int64       `json:"prepared,omitempty"` // prerequisite id -> when the lead said it exists
	// Continue are the sessions the writers of a stage added to a finished task continue: those of the stage
	// before it, under the names of its streams.
	Continue    map[string]WriterRef `json:"continue,omitempty"`
	Checks      []CheckReceipt       `json:"checks"`
	Baselines   map[string]int       `json:"baselines,omitempty"` // check key -> exit code before any writing
	Corrections []Correction         `json:"corrections,omitempty"`
	Ruled       bool                 `json:"ruled,omitempty"` // the lead ruled on a finding since the last correction: the next one is not a repetition
	// DesignFailure is why a design of the present stage could not be imported or measured. It is the lead's to
	// repair; the next run tries again.
	DesignFailure string                  `json:"design_failure,omitempty"`
	Reading       *Reading                `json:"reading,omitempty"` // a review the lead owes (a mode without reviewer processes)
	ContextPack   string                  `json:"context_pack,omitempty"`
	Preparation   string                  `json:"preparation,omitempty"` // the preparation whose worker can revise this plan
	Boost         int                     `json:"boost,omitempty"`
	Foreign       []string                `json:"foreign_changes,omitempty"`
	Disowned      []string                `json:"disowned,omitempty"` // a task the session writes: changes outside its plan the session says are somebody else's
	Substitutions []provider.Substitution `json:"substitutions,omitempty"`
	Final         *Final                  `json:"final,omitempty"`
	Worktree      string                  `json:"worktree,omitempty"`  // an isolated task works here, not in Cwd
	Branch        string                  `json:"branch,omitempty"`    // the branch of that worktree
	Landed        string                  `json:"landed,omitempty"`    // how an isolated task was closed: apply, commit, branch, drop
	Conflicts     []string                `json:"conflicts,omitempty"` // files left with merge markers by catching up with the checkout
	Signals       []Signal                `json:"slop_signals,omitempty"`
	Reported      bool                    `json:"completion_reported,omitempty"`
	// Left is the working tree as the task's writers last left it: a file of the task that differs from it was
	// changed by someone else, and `drop` does not overwrite it.
	Left        string   `json:"left_tree,omitempty"`
	Dropped     bool     `json:"dropped,omitempty"`     // the files the task changed in the checkout were restored
	Attachments []string `json:"attachments,omitempty"` // files the user gave with the request, copied into the task
	Opened      int64    `json:"opened,omitempty"`      // a task the session writes: when the request was handed over, before the plan
	CreatedAt   int64    `json:"created_at"`
	Tool        string   `json:"tool_version,omitempty"` // the agentgate that last wrote the task

	legacy *legacyState // a task of an earlier format whose origin trees are not built yet
	pin    *pinned      // the working tree of the present reading
	said   []string     // what an earlier part of this command found, for its answer
	// toolBefore is the agentgate that wrote the task before this one read it, when that was another version:
	// the protocol the lead learned may have changed.
	toolBefore string
}
