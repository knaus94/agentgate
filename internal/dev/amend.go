package dev

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/config"
)

// A large specification is not fully known when the work starts. Amending the plan is therefore an ordinary
// operation, legal at any step of an active task. It keeps what an amendment exists to keep — the task, its
// origin, the writers' sessions, the findings — and makes stale only what the change touches.

// Amend replaces the plan of an active task. The complete revised plan is given, with `update`: what changed
// and why.
func Amend(cfg *config.Config, gate *Gate, source *Plan) (any, error) {
	if err := gate.can(ActAmend); err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(source.Update)
	if reason == "" {
		return nil, errors.New("an amendment needs update: what changed and why, plus the complete revised plan")
	}
	for _, writer := range gate.latest() {
		if writer.Status == "RUNNING" {
			return nil, errors.New("cannot amend while a writer is running")
		}
	}
	resolved, err := Resolve(cfg, source)
	if err != nil {
		return nil, err
	}
	if resolved.Request.Action == "plan" {
		resolved.Request.Action = "implement" // the task was authorized when it started; the plan's wording does not withdraw that
	}
	if resolved.Request.Writes() != gate.Request.Writes() || resolved.Request.ReviewFirst() != gate.Request.ReviewFirst() {
		return nil, errors.New("an amendment keeps the task's action and write authority")
	}
	if gate.Request.Writes() && !gate.Request.ReviewFirst() {
		if err := resolved.requireArchitecture(); err != nil {
			return nil, err
		}
	}
	old, next := gate.Plan, resolved.Plan
	if _, err := loadReviewRules(cfg, next.Files()); err != nil {
		return nil, err
	}
	frozen := len(gate.Accepted)
	if len(next.Stages) < frozen {
		return nil, errors.New("the revised plan has fewer stages than are already accepted")
	}
	added := map[string]bool{}
	for _, requirement := range next.Requirements {
		if !slices.ContainsFunc(old.Requirements, func(other Requirement) bool { return other.ID == requirement.ID }) {
			added[requirement.ID] = true
		}
	}
	for index := 0; index < frozen; index++ {
		switch {
		case digest(old.Stages[index]) == digest(next.Stages[index]):
		case gate.Whole && digest(old.Stages[index]) == digest(withoutAdditions(old.Stages[index], next.Stages[index], added)):
			// The pass over the whole change found what no stage owned: a stream may take it, a new requirement
			// may be judged where it belongs.
		case gate.Whole:
			return nil, fmt.Errorf("stage %q is accepted: the pass over the whole change may only give its streams more files (scope) "+
				"and its covers a new requirement", old.Stages[index].Name)
		default:
			return nil, fmt.Errorf("stage %q is accepted and cannot change; revise the stages after it", old.Stages[index].Name)
		}
	}
	var changed []string
	for _, requirement := range old.Requirements {
		position := slices.IndexFunc(next.Requirements, func(other Requirement) bool { return other.ID == requirement.ID })
		if position >= 0 && next.Requirements[position] == requirement {
			continue
		}
		for index := 0; index < frozen; index++ {
			if slices.Contains(old.Stages[index].Covers, requirement.ID) {
				return nil, fmt.Errorf("requirement %q belongs to an accepted stage and cannot change", requirement.ID)
			}
		}
		changed = append(changed, requirement.ID)
	}
	if len(changed) > 0 && strings.TrimSpace(source.Authorized) == "" {
		return nil, fmt.Errorf("the amendment changes or removes requirements %s. A requirement is never weakened to pass a review: move its coverage to the "+
			"stage that completes it, or add an intermediate criterion beside it. When the user really changed what is asked, say who allowed it and in what "+
			"words in `authorized`", strings.Join(changed, ", "))
	}
	// The inputs of a check the amendment adds or changes are looked at now, not when the check first runs.
	var changedChecks []Check
	for _, check := range next.checksFor(-1) {
		if !slices.ContainsFunc(old.checksFor(-1), func(other Check) bool {
			return slices.Equal(other.Argv, check.Argv) && slices.Equal(other.Inputs, check.Inputs)
		}) {
			changedChecks = append(changedChecks, check)
		}
	}
	var inputNotes []string
	if len(changedChecks) > 0 {
		tree, err := gate.snapshot()
		if err != nil {
			return nil, err
		}
		if inputNotes, err = gate.inspectInputs(changedChecks, tree); err != nil {
			return nil, err
		}
	}
	var rework []string
	if gate.Whole {
		if len(next.Stages) != len(old.Stages) {
			return nil, errors.New("every stage is accepted; the pass over the whole change cannot add or remove stages")
		}
	} else {
		present := gate.Stage
		if present >= len(next.Stages) || next.Stages[present].Name != old.Stages[present].Name {
			return nil, fmt.Errorf("an amendment keeps the present stage and its name: %q", old.Stages[present].Name)
		}
		streams := next.Stages[present].Streams
		named := func(name string) bool {
			return slices.ContainsFunc(streams, func(stream Stream) bool { return stream.Name == name })
		}
		for name := range gate.latest() {
			if !named(name) {
				return nil, fmt.Errorf("stream %q already has a writer in this stage: an amendment keeps it and its name", name)
			}
		}
		// What the stage already changed must stay owned: a change that left the scope would never be reviewed.
		now, err := gate.snapshot()
		if err != nil {
			return nil, err
		}
		owned, _, err := gate.changes(now)
		if err != nil {
			return nil, err
		}
		scope := compilePatterns(next.scope(present))
		var orphaned []string
		for _, change := range owned {
			if !matchAny(scope, change.Path) {
				orphaned = append(orphaned, change.Path)
			}
		}
		if len(orphaned) > 0 {
			return nil, fmt.Errorf("these files were already changed by this stage and cannot leave its scope: %s", strings.Join(orphaned, ", "))
		}
		for _, name := range source.Rework {
			if !named(name) {
				return nil, fmt.Errorf("rework names no stream of the present stage: %q", name)
			}
			rework = append(rework, name)
		}
		for _, stream := range streams {
			if !stream.Done && !slices.ContainsFunc(old.Stages[present].Streams, func(other Stream) bool { return other.Name == stream.Name }) {
				rework = append(rework, stream.Name) // a stream the amendment adds is new work
			}
		}
	}
	if added := subtract(next.Files(), old.Files()); len(added) > 0 && gate.Worktree == "" {
		unlock, err := scopeLock()
		if err != nil {
			return nil, err
		}
		defer unlock()
		if err := checkOwnership(added); err != nil {
			return nil, err
		}
	}
	contract, quality := gate.codeContract(), ""
	if inputs, err := gate.qualityInputs(cfg); err == nil {
		quality = inputs.fingerprint()
	}
	changes := planChanges(old, next)
	// What the amendment says about itself belongs to its revision, not to the plan: a plan read back and
	// amended again must not carry the last amendment's rework with it.
	authorized := strings.TrimSpace(source.Authorized)
	next.Update, next.Authorized, next.Rework = "", "", nil
	gate.Plan, gate.Modes = next, resolved.Modes
	if gate.Whole {
		gate.Mode = gate.overallMode()
	} else {
		gate.Mode = gate.Modes[gate.Stage]
	}
	gate.Revisions = append(gate.Revisions, Revision{Number: len(gate.Revisions) + 1, At: time.Now().Unix(), Reason: reason, Authorized: authorized,
		Rework: rework, Digest: digest(next), Changes: changes})
	gate.Reading = nil
	did := []string{fmt.Sprintf("plan amended (revision %d): %s", len(gate.Revisions), reason)}
	did = append(append(did, changes...), inputNotes...)
	slices.Sort(rework)
	rework = slices.Compact(rework)
	switch {
	case gate.Step == StepWrite && gate.Round != nil:
		gate.restream(rework)
		if len(rework) > 0 {
			did = append(did, "the round now includes "+strings.Join(rework, ", "))
		}
	case gate.Step != StepWrite && len(rework) > 0:
		// The amendment is new work for these streams: they get it as an assignment, in their own sessions.
		var streams []Stream
		for _, stream := range gate.streams() {
			if slices.Contains(rework, stream.Name) {
				stream.Done = false
				streams = append(streams, stream)
			}
		}
		gate.Rounds++
		gate.Round = &Round{Batch: gate.batch(), Streams: streams, Correction: len(gate.Writers) > 0, Findings: gate.accepted(),
			Remedy: "The plan was amended: " + reason + "\nBring your stream in line with the amended assignment; preserve what already satisfies it."}
		if err := gate.setStep(StepWrite); err != nil {
			return nil, err
		}
		did = append(did, "the amendment goes to "+strings.Join(rework, ", ")+" as new work")
	case gate.Step == StepDecide:
		if err := gate.setStep(StepVerify); err != nil {
			return nil, err
		}
	}
	if gate.Step != StepWrite {
		inputs, err := gate.qualityInputs(cfg)
		switch {
		case gate.codeContract() != contract:
			did = append(did, "what was to be built changed: the code is reviewed again on the next run")
		case err == nil && inputs.fingerprint() != quality:
			did = append(did, "only the criteria changed: the reviewer reassesses them on the next run; the code review stands")
		default:
			did = append(did, "nothing that was verified is affected")
		}
	}
	gate.proceed()
	if err := gate.save(); err != nil {
		return nil, err
	}
	return gate.report(cfg, did), nil
}

// withoutAdditions is a revised accepted stage with the additions the pass over the whole change may make taken
// back out: files its streams gained and new requirements its covers gained. What is left differs from the
// accepted stage only by changes that pass may not make.
func withoutAdditions(accepted, revised Stage, added map[string]bool) Stage {
	out := revised
	out.Streams = slices.Clone(revised.Streams)
	if len(out.Streams) == len(accepted.Streams) {
		for index := range out.Streams {
			kept := accepted.Streams[index].Files
			if out.Streams[index].Name == accepted.Streams[index].Name &&
				!slices.ContainsFunc(kept, func(file string) bool { return !slices.Contains(out.Streams[index].Files, file) }) {
				out.Streams[index].Files = kept
			}
		}
	}
	covers := slices.DeleteFunc(slices.Clone(revised.Covers), func(id string) bool { return added[id] })
	if slices.Equal(covers, accepted.Covers) {
		out.Covers = accepted.Covers
	}
	return out
}

// Operations change named parts of the plan of an active task, instead of sending the complete plan. They are
// applied to a copy of the present plan, which is then amended like a complete one: the same validation, the
// same rules for accepted stages and changed requirements.
type Operations struct {
	Update       string              `json:"update"`
	Authorized   string              `json:"authorized,omitempty"`
	Rework       []string            `json:"rework,omitempty"`
	Mode         string              `json:"mode,omitempty"`         // light, medium, heavy or ultra, above the request's flag
	Scope        []ScopeChange       `json:"scope,omitempty"`        // files a stream gets
	Checks       []CheckChange       `json:"checks,omitempty"`       // checks added to or removed from the plan or a stage
	Requirements []RequirementChange `json:"requirements,omitempty"` // requirements added, changed, or moved to the stage they are due at
	Timeouts     *Timeouts           `json:"timeouts,omitempty"`
	Design       []DesignChange      `json:"design,omitempty"`
	Architecture *Accepted           `json:"architecture,omitempty"` // the receipt the task follows from now on, with the user's decisions
}

// ScopeChange gives a stream more files.
type ScopeChange struct {
	Stream string   `json:"stream"`
	Stage  string   `json:"stage,omitempty"` // default: the present stage
	Add    []string `json:"add"`
}

// CheckChange adds checks to, or removes them from, the plan's own list or a stage's.
type CheckChange struct {
	Stage  string   `json:"stage,omitempty"` // default: the plan's checks, which every stage runs
	Add    []Check  `json:"add,omitempty"`
	Remove []string `json:"remove,omitempty"` // a check's id, or a part of its command only it contains
}

// RequirementChange adds a requirement, changes one, or says at which stage it is due.
type RequirementChange struct {
	ID     string `json:"id"`
	Text   string `json:"text,omitempty"`   // a new requirement, or a changed one: that needs authorized
	Method string `json:"method,omitempty"` // code, check or browser
	Stage  string `json:"stage,omitempty"`  // the stage it is due at; a new one is due at the last stage by default
}

// Timeouts are the process ceilings of the task, in seconds.
type Timeouts struct {
	WriterS   int `json:"writer_s,omitempty"`
	ReviewerS int `json:"reviewer_s,omitempty"`
}

// DesignChange adds a design, or changes or removes the one it names.
type DesignChange struct {
	Add    *DesignRef `json:"add,omitempty"`
	Design string     `json:"design,omitempty"` // the entry to change or remove: its screen, name or figma link
	Set    *DesignRef `json:"set,omitempty"`    // the fields that change
	Remove bool       `json:"remove,omitempty"`
}

// AmendOperations applies named operations to the plan of an active task.
func AmendOperations(cfg *config.Config, gate *Gate, operations Operations) (any, error) {
	if err := gate.can(ActAmend); err != nil {
		return nil, err
	}
	next, err := operations.apply(gate.Plan, gate.stageIndex())
	if err != nil {
		return nil, err
	}
	return Amend(cfg, gate, next)
}

// apply makes the plan the operations describe; every problem is reported at once.
func (o Operations) apply(current *Plan, present int) (*Plan, error) {
	next := &Plan{}
	raw, _ := json.Marshal(current)
	_ = json.Unmarshal(raw, next)
	var problems PlanProblems
	fail := func(format string, values ...any) { problems = append(problems, fmt.Sprintf(format, values...)) }
	names := make([]string, len(next.Stages))
	for index, stage := range next.Stages {
		names[index] = stage.Name
	}
	stage := func(name string, fallback int) int {
		if name == "" {
			return fallback
		}
		if index := slices.Index(names, name); index >= 0 {
			return index
		}
		fail("no stage is called %q; stages: %s", name, strings.Join(names, ", "))
		return -1
	}
	last := len(next.Stages) - 1
	if present < 0 {
		present = last
	}
	for _, change := range o.Scope {
		// The pass over the whole change names its streams after their stage: `stage.stream`.
		if change.Stage == "" {
			for _, name := range names {
				if rest, ok := strings.CutPrefix(change.Stream, name+"."); ok {
					change.Stage, change.Stream = name, rest
					break
				}
			}
		}
		index := stage(change.Stage, present)
		if index < 0 {
			continue
		}
		streams := next.Stages[index].Streams
		position := slices.IndexFunc(streams, func(stream Stream) bool { return stream.Name == change.Stream })
		if position < 0 {
			var known []string
			for _, stream := range streams {
				known = append(known, stream.Name)
			}
			fail("stage %s has no stream %q; its streams: %s", names[index], change.Stream, strings.Join(known, ", "))
			continue
		}
		for _, file := range change.Add {
			if !slices.Contains(streams[position].Files, file) {
				streams[position].Files = append(streams[position].Files, file)
			}
		}
	}
	for _, change := range o.Checks {
		target := &next.Checks
		if change.Stage != "" {
			index := stage(change.Stage, -1)
			if index < 0 {
				continue
			}
			target = &next.Stages[index].Checks
		}
		for _, ref := range change.Remove {
			var matches, valid []string
			for _, check := range *target {
				label := strings.Join(check.Argv, " ")
				if checkID(check.Argv) == ref || strings.Contains(label, ref) {
					matches = append(matches, checkID(check.Argv))
				}
				valid = append(valid, checkID(check.Argv)+" ("+checkLabel(check.Argv)+")")
			}
			if len(matches) != 1 {
				fail("remove %q must name one check of that list, by its id or a part of its command only it contains: %s", ref, strings.Join(valid, ", "))
				continue
			}
			*target = slices.DeleteFunc(*target, func(check Check) bool { return checkID(check.Argv) == matches[0] })
		}
		*target = append(*target, change.Add...)
	}
	for _, change := range o.Requirements {
		position := slices.IndexFunc(next.Requirements, func(r Requirement) bool { return r.ID == change.ID })
		switch {
		case position < 0 && strings.TrimSpace(change.Text) == "":
			fail("requirement %q is new: give its text", change.ID)
			continue
		case position < 0:
			next.Requirements = append(next.Requirements, Requirement{ID: change.ID, Text: change.Text, Method: change.Method})
			if change.Stage == "" {
				change.Stage = names[last]
			}
		default:
			if change.Text != "" {
				next.Requirements[position].Text = change.Text
			}
			if change.Method != "" {
				next.Requirements[position].Method = change.Method
			}
		}
		if change.Stage == "" {
			continue
		}
		due := stage(change.Stage, -1)
		if due < 0 {
			continue
		}
		// Due at that stage: it covers the requirement, and no stage after it does.
		for index := range next.Stages {
			covers := &next.Stages[index].Covers
			switch {
			case index == due && !slices.Contains(*covers, change.ID):
				*covers = append(*covers, change.ID)
			case index > due:
				*covers = slices.DeleteFunc(*covers, func(id string) bool { return id == change.ID })
			}
		}
	}
	if o.Architecture != nil {
		next.Architecture = o.Architecture
	}
	if o.Timeouts != nil {
		if o.Timeouts.WriterS != 0 {
			next.WriterTimeoutS = o.Timeouts.WriterS
		}
		if o.Timeouts.ReviewerS != 0 {
			next.ReviewerTimeoutS = o.Timeouts.ReviewerS
		}
	}
	for _, change := range o.Design {
		if change.Add != nil {
			next.Design = append(next.Design, *change.Add)
			continue
		}
		position := slices.IndexFunc(next.Design, func(design DesignRef) bool {
			return change.Design != "" && (design.Screen == change.Design || design.Name == change.Design || design.Figma == change.Design)
		})
		if position < 0 {
			var known []string
			for _, design := range next.Design {
				known = append(known, cmp.Or(design.Name, design.Screen, design.Figma))
			}
			fail("design %q names no design of the plan; designs: %s", change.Design, strings.Join(known, ", "))
			continue
		}
		switch {
		case change.Remove:
			next.Design = slices.Delete(next.Design, position, position+1)
		case change.Set != nil:
			merged, _ := json.Marshal(next.Design[position])
			fields, _ := json.Marshal(change.Set)
			var base, set map[string]any
			_ = json.Unmarshal(merged, &base)
			_ = json.Unmarshal(fields, &set)
			for key, value := range set {
				base[key] = value
			}
			raw, _ := json.Marshal(base)
			next.Design[position] = DesignRef{}
			_ = json.Unmarshal(raw, &next.Design[position])
		default:
			fail("design %q: give set {fields} or remove: true", change.Design)
		}
	}
	if o.Mode != "" {
		next.Mode = o.Mode
	}
	if len(problems) > 0 {
		return nil, problems
	}
	next.Update, next.Authorized, next.Rework = o.Update, o.Authorized, o.Rework
	return next, nil
}

// restream brings an unfinished round in line with an amended plan: the streams it holds get their new
// definitions, streams the amendment adds join it, and streams named for rework report again.
func (g *Gate) restream(rework []string) {
	present := g.streams()
	var streams []Stream
	for _, stream := range g.Round.Streams {
		if position := slices.IndexFunc(present, func(other Stream) bool { return other.Name == stream.Name }); position >= 0 {
			updated := present[position]
			if g.Round.Correction {
				updated.After, updated.Done = nil, false
			}
			streams = append(streams, updated)
		}
	}
	for _, name := range rework {
		if slices.ContainsFunc(streams, func(stream Stream) bool { return stream.Name == name }) {
			continue
		}
		if position := slices.IndexFunc(present, func(other Stream) bool { return other.Name == name }); position >= 0 {
			added := present[position]
			if g.Round.Correction {
				added.After = nil
			}
			added.Done = false
			streams = append(streams, added)
		}
	}
	g.Round.Streams = streams
	latest := g.latest()
	for _, name := range rework {
		if writer := latest[name]; writer != nil && writer.Batch == g.Round.Batch && writer.Status == "COMPLETED" {
			writer.Stale = true
		}
	}
}

// planChanges says, for a person and for the record, what an amendment changed.
func planChanges(old, next *Plan) []string {
	var out []string
	add := func(format string, values ...any) { out = append(out, fmt.Sprintf(format, values...)) }
	requirement := func(plan *Plan, id string) *Requirement {
		if position := slices.IndexFunc(plan.Requirements, func(r Requirement) bool { return r.ID == id }); position >= 0 {
			return &plan.Requirements[position]
		}
		return nil
	}
	due := func(plan *Plan, id string) string {
		if last := plan.lastCover(id); last >= 0 {
			return plan.Stages[last].Name
		}
		return "no stage"
	}
	for _, item := range next.Requirements {
		previous := requirement(old, item.ID)
		switch {
		case previous == nil:
			add("requirement %s added, due at %s: %s", item.ID, due(next, item.ID), oneLine(item.Text))
		case *previous != item:
			add("requirement %s changed", item.ID)
		case due(old, item.ID) != due(next, item.ID):
			add("requirement %s is now due at %s (was %s)", item.ID, due(next, item.ID), due(old, item.ID))
		}
	}
	for _, item := range old.Requirements {
		if requirement(next, item.ID) == nil {
			add("requirement %s removed", item.ID)
		}
	}
	stage := func(plan *Plan, name string) *Stage {
		if position := slices.IndexFunc(plan.Stages, func(s Stage) bool { return s.Name == name }); position >= 0 {
			return &plan.Stages[position]
		}
		return nil
	}
	for _, item := range next.Stages {
		previous := stage(old, item.Name)
		if previous == nil {
			add("stage %s added", item.Name)
			continue
		}
		var before, after []string
		for _, stream := range previous.Streams {
			before = append(before, stream.Files...)
		}
		for _, stream := range item.Streams {
			after = append(after, stream.Files...)
			if !slices.ContainsFunc(previous.Streams, func(other Stream) bool { return other.Name == stream.Name }) {
				add("stage %s: stream %s added", item.Name, stream.Name)
			}
		}
		if widened := subtract(after, before); len(widened) > 0 {
			add("stage %s: scope gained %s", item.Name, strings.Join(widened, ", "))
		}
		if narrowed := subtract(before, after); len(narrowed) > 0 {
			add("stage %s: scope lost %s", item.Name, strings.Join(narrowed, ", "))
		}
		if digest(previous.Checks) != digest(item.Checks) {
			add("stage %s: checks changed", item.Name)
		}
		if previous.Goal != item.Goal || digest(previous.Acceptance) != digest(item.Acceptance) {
			add("stage %s: goal or acceptance changed", item.Name)
		}
	}
	for _, item := range old.Stages {
		if stage(next, item.Name) == nil {
			add("stage %s removed", item.Name)
		}
	}
	if old.Brief != next.Brief {
		add("the brief changed")
	}
	if digest(old.Checks) != digest(next.Checks) {
		add("the plan's checks changed")
	}
	if digest(old.UI) != digest(next.UI) || digest(old.Design) != digest(next.Design) || digest(old.Visual) != digest(next.Visual) {
		add("the interface contract changed")
	}
	if digest(old.Architecture) != digest(next.Architecture) {
		add("the accepted architecture changed")
	}
	return out
}
