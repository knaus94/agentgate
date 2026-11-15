package dev

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// What a task is working on now is read from its plan and its position; nothing here is stored.

// stageIndex is the present stage, or -1 for the pass over the whole change.
func (g *Gate) stageIndex() int {
	if g.Whole || g.Stage >= len(g.Plan.Stages) {
		return -1
	}
	return g.Stage
}

func (g *Gate) stage() *Stage {
	if index := g.stageIndex(); index >= 0 {
		return &g.Plan.Stages[index]
	}
	return nil
}

// scope is what the present position owns: the stage's entries, or the whole plan's in the final pass.
func (g *Gate) scope() []string { return g.Plan.scope(g.stageIndex()) }

// origin is the tree the present position is measured from.
func (g *Gate) origin() string {
	if g.Whole {
		return g.BaseTree
	}
	return g.StageTree
}

// checks are the commands that must pass at the present position.
func (g *Gate) checks() []Check { return g.Plan.checksFor(g.stageIndex()) }

// designs are the designs built at the present position.
func (g *Gate) designs() []DesignRef { return g.Plan.designsFor(g.stageIndex()) }

// context are the files workers are pointed at but do not own: the plan's context and the instruction files
// above the scope. A change to one of them makes a review stale.
func (g *Gate) context() []string {
	scope := compilePatterns(g.scope())
	var out []string
	for _, file := range append(slices.Clone(g.Plan.Context), guidance(g.scope())...) {
		if !matchAny(scope, file) {
			out = append(out, file)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// snapshot records the working tree as it is now.
func (g *Gate) snapshot() (string, error) {
	return snapshotTree(g.objects(), exactPaths(g.Plan.Files()))
}

// changes splits everything that differs between the origin and a tree into what the present position owns
// and what it does not.
func (g *Gate) changes(now string) (owned []FileChange, foreign []string, err error) {
	return g.changesBetween(g.origin(), now)
}

func (g *Gate) changesBetween(from, to string) (owned []FileChange, foreign []string, err error) {
	all, err := diffTrees(g.objects(), from, to)
	if err != nil {
		return nil, nil, err
	}
	scope := compilePatterns(g.scope())
	for _, change := range all {
		switch {
		case matchAny(scope, change.Path):
			owned = append(owned, change)
		case !g.designFile(change.Path):
			foreign = append(foreign, change.Path)
		}
	}
	return owned, foreign, nil
}

// designFile reports whether a path belongs to a design screen the tool made for this task: its package and
// its measurements are the tool's, not a change of the task.
func (g *Gate) designFile(path string) bool {
	return slices.ContainsFunc(g.Plan.Design, func(design DesignRef) bool {
		return design.Screen != "" && strings.HasPrefix(path, filepath.ToSlash(filepath.Clean(design.Screen))+"/")
	})
}

// reviewKey is what a review of the present position is bound to: every owned change, byte for byte, and
// the content of the context it was pointed at.
func (g *Gate) reviewKey(now string) (string, error) {
	owned, _, err := g.changes(now)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(owned))
	for _, change := range owned {
		lines = append(lines, fmt.Sprintf("%s %o %o %s %s %s", change.Status, change.OldMode, change.NewMode, change.OldBlob, change.NewBlob, change.Path))
	}
	context, err := treeEntries(g.objects(), now, g.context())
	if err != nil {
		return "", err
	}
	return digest(map[string]any{"origin": g.origin(), "changes": lines, "context": context}), nil
}

// taskKey is the change of the whole task since it began, whatever stage it is at: what a finished task is
// bound to. Edits to files the plan does not own leave it unchanged.
func (g *Gate) taskKey(now string) (string, error) {
	all, err := diffTrees(g.objects(), g.BaseTree, now)
	if err != nil {
		return "", err
	}
	scope := compilePatterns(g.Plan.Files())
	var lines []string
	for _, change := range all {
		if matchAny(scope, change.Path) {
			lines = append(lines, fmt.Sprintf("%s %o %o %s %s %s", change.Status, change.OldMode, change.NewMode, change.OldBlob, change.NewBlob, change.Path))
		}
	}
	context, err := treeEntries(g.objects(), now, g.Plan.Context)
	if err != nil {
		return "", err
	}
	return digest(map[string]any{"base": g.BaseTree, "changes": lines, "context": context}), nil
}

// audit records changes outside the task's scope. They are reported, not fatal: parallel sessions share the
// checkout and the lead inspects the diff.
func (g *Gate) audit(now string) error {
	_, foreign, err := g.changes(now)
	if err != nil {
		return err
	}
	g.Foreign = foreign
	return nil
}

// brief is what a worker of the present position is told: the plan's brief and where this piece of work
// sits in it. It is rendered when it is needed and takes part in no digest.
func (g *Gate) brief() string {
	plan := g.Plan
	var out strings.Builder
	out.WriteString(strings.TrimSpace(plan.Brief))
	if g.Whole {
		out.WriteString("\n\n## Integration pass\n\nThe work was written and reviewed stage by stage. This pass reads the whole change for what a " +
			"review of one stage cannot see: seams between stages, a contract that drifted from one stage to the next, leftovers of an earlier " +
			"stage, a requirement that no stage met end to end.\n\nStages:\n")
		for _, record := range g.Accepted {
			fmt.Fprintf(&out, "- %s: %s\n", record.Name, oneLine(record.Summary))
		}
		if len(plan.Requirements) > 0 {
			out.WriteString("\nRequirements of the whole task:\n")
			for _, requirement := range plan.Requirements {
				fmt.Fprintf(&out, "- %s: %s\n", requirement.ID, requirement.Text)
			}
		}
		return out.String()
	}
	stage := g.stage()
	if stage == nil || (plan.Single && len(plan.Stages) == 1) {
		return out.String()
	}
	fmt.Fprintf(&out, "\n\n## This stage: %s (%d of %d)\n\n%s\n", stage.Name, g.Stage+1, len(plan.Stages), strings.TrimSpace(stage.Goal))
	if len(stage.Acceptance) > 0 {
		out.WriteString("\nAccepted when:\n")
		for _, item := range stage.Acceptance {
			out.WriteString("- " + item + "\n")
		}
	}
	if covered := plan.covered(g.Stage); len(covered) > 0 {
		out.WriteString("\nRequirements this stage works on:\n")
		for _, requirement := range covered {
			due := "due at the end of this stage"
			if last := plan.lastCover(requirement.ID); last != g.Stage {
				due = "this stage contributes; due at stage " + plan.Stages[last].Name
			}
			fmt.Fprintf(&out, "- %s: %s (%s)\n", requirement.ID, requirement.Text, due)
		}
	}
	if len(g.Accepted) > 0 {
		out.WriteString("\n## Earlier stages, accepted — build on them, do not redo them\n")
		for _, record := range g.Accepted {
			fmt.Fprintf(&out, "- %s: %s\n", record.Name, oneLine(record.Summary))
			streams := make([]string, 0, len(record.Handoffs))
			for stream := range record.Handoffs {
				streams = append(streams, stream)
			}
			sort.Strings(streams)
			for _, stream := range streams {
				fmt.Fprintf(&out, "  - contract of %s: %s\n", stream, oneLine(record.Handoffs[stream]))
			}
		}
	}
	if g.Stage+1 < len(plan.Stages) {
		out.WriteString("\n## Later stages — not part of this one\n")
		for _, later := range plan.Stages[g.Stage+1:] {
			fmt.Fprintf(&out, "- %s: %s\n", later.Name, oneLine(later.Goal))
		}
	}
	return out.String()
}

// criterion is a requirement as the workers of the present position see it.
type criterion struct {
	Requirement
	Due   bool   `json:"due"`              // must be verified before this position is accepted
	DueAt string `json:"due_at,omitempty"` // the later stage it is due at, when it is not due here
}

// requirementsFor are the requirements the present position works on. One is due here when this is the last
// stage that covers it; the pass over the whole change judges every requirement.
func (g *Gate) requirementsFor() []criterion {
	plan, index := g.Plan, g.stageIndex()
	out := []criterion{}
	for _, requirement := range plan.covered(index) {
		item := criterion{Requirement: requirement, Due: true}
		if last := plan.lastCover(requirement.ID); index >= 0 && last != index {
			item.Due, item.DueAt = false, plan.Stages[last].Name
		}
		out = append(out, item)
	}
	return out
}

// codeContract is what was to be built at the present position. A review of the code is bound to it; the
// criteria the review judged are bound separately, so moving a requirement between stages does not ask for
// the code to be read again.
func (g *Gate) codeContract() string {
	type streamContract struct {
		Name, Domain, Brief string
		Files               []string
	}
	var streams []streamContract
	for _, stream := range g.streams() {
		streams = append(streams, streamContract{stream.Name, stream.Domain, stream.Brief, stream.Files})
	}
	parts := map[string]any{"brief": g.Plan.Brief, "streams": streams, "context": g.context(), "ui": g.Plan.UI, "mode": g.Mode,
		"architecture": architectureFor(g.Plan), "whole": g.Whole}
	if stage := g.stage(); stage != nil {
		parts["goal"], parts["acceptance"] = stage.Goal, stage.Acceptance
	}
	ui, uiError := loadUI(g.Plan)
	parts["ui_contract"] = ui
	if uiError != nil {
		parts["ui_error"] = uiError.Error()
	}
	// A design that was pulled again is another design, also when it lies at the same path.
	if designs := g.designDigests(); designs != nil {
		parts["design"] = designs
	}
	return digest(parts)
}

// amendment is what the last amendment of the plan changed and why, or "".
func (g *Gate) amendment() string {
	if len(g.Revisions) == 0 {
		return ""
	}
	return g.Revisions[len(g.Revisions)-1].Reason
}
