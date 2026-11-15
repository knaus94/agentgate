package dev

import (
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
	for index := 0; index < frozen; index++ {
		if digest(old.Stages[index]) != digest(next.Stages[index]) {
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
	did = append(did, changes...)
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
