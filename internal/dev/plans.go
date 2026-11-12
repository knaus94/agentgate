package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/config"
)

// PlansDir is where saved plans live inside the repository: the plan the tool runs and a brief a person reads.
// Whether to commit the directory is the repository's choice; the gate never counts it as part of a change.
const PlansDir = ".agentgate/plans"

// plansDir is PlansDir, or the same directory under the tool's earlier name in a repository set up before the rename.
func plansDir() string { return filepath.Join(config.RepoDir(), "plans") }

var planName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// PlanFile is the path of a saved plan.
func PlanFile(name string) (string, error) {
	if !planName.MatchString(name) {
		return "", fmt.Errorf("a plan name is lowercase letters, digits and dashes: %q", name)
	}
	return filepath.Join(plansDir(), name, "plan.json"), nil
}

// slug derives a plan name from the request when the lead gave none.
func slug(plan *Plan) string {
	words := regexp.MustCompile(`[a-z0-9]+`).FindAllString(strings.ToLower(plan.Request+" "+plan.Brief), -1)
	var kept []string
	for _, word := range words {
		if len(word) > 2 && len(kept) < 5 {
			kept = append(kept, word)
		}
	}
	if name := strings.Join(kept, "-"); len(kept) >= 2 && planName.MatchString(name) {
		return name
	}
	return "plan-" + time.Now().Format("20060102-1504")
}

// PlanSave validates the plan and writes it with a readable brief next to it. Saving again under the same
// name replaces both, so the file always shows the plan that will run.
func PlanSave(cfg *config.Config, plan *Plan, name string) (any, error) {
	resolved, err := Resolve(cfg, plan)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = slug(plan)
	}
	path, err := PlanFile(name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	raw, _ := json.MarshalIndent(plan, "", "  ")
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return nil, err
	}
	brief := filepath.Join(filepath.Dir(path), "brief.md")
	if err := os.WriteFile(brief, []byte(renderBrief(name, resolved)), 0o644); err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "plan": path, "brief": brief, "mode": resolved.Mode, "decision": resolved.Decision,
		"next": "Show the user the brief path. Pass the plan to any command with `--plan " + name + "`; save again after every change to it."}, nil
}

// PlanList names the saved plans of this repository, newest first.
func PlanList() (any, error) {
	paths, _ := filepath.Glob(filepath.Join(plansDir(), "*", "plan.json"))
	type item struct {
		Name    string `json:"name"`
		Request string `json:"request"`
		Brief   string `json:"brief"`
		Updated string `json:"updated"`
		stamp   time.Time
	}
	items := []item{}
	for _, path := range paths {
		info, err := os.Stat(path)
		raw, _ := os.ReadFile(path)
		plan := &Plan{}
		if err != nil || json.Unmarshal(raw, plan) != nil {
			continue
		}
		items = append(items, item{Name: filepath.Base(filepath.Dir(path)), Request: firstLine(plan.Request), Brief: filepath.Join(filepath.Dir(path), "brief.md"),
			Updated: info.ModTime().Format("2006-01-02 15:04"), stamp: info.ModTime()})
	}
	sort.Slice(items, func(a, b int) bool { return items[a].stamp.After(items[b].stamp) })
	return map[string]any{"dir": plansDir(), "plans": items}, nil
}

// PlanShow prints the brief of a saved plan.
func PlanShow(name string) (any, error) {
	path, err := PlanFile(name)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "brief.md"))
	if err != nil {
		return nil, errors.New("no such plan; `agentgate plan list` shows the saved ones")
	}
	return string(raw), nil
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if len([]rune(line)) > 120 {
		line = string([]rune(line)[:120]) + "…"
	}
	return line
}

// renderBrief is the plan as a person reads it: what is built, by whom, how it is checked.
func renderBrief(name string, resolved *Resolved) string {
	plan := resolved.Plan
	var out strings.Builder
	line := func(format string, values ...any) { fmt.Fprintf(&out, format+"\n", values...) }
	line("# %s\n", name)
	line("> Written by `agentgate plan save`; edit `plan.json` and save again rather than this file.\n")
	if plan.Request != "" {
		line("**Request:** %s\n", strings.TrimSpace(plan.Request))
	}
	line("**Mode:** %s (%s: %s)\n", resolved.Mode, resolved.Decision.Source, strings.Join(resolved.Decision.Because, "; "))
	if assessment := plan.Assessment; assessment != nil {
		facts := []string{"size " + assessment.Size}
		if len(assessment.Risks) > 0 {
			facts = append(facts, "risks "+strings.Join(assessment.Risks, ", "))
		}
		if assessment.Irreversible {
			facts = append(facts, "irreversible")
		}
		if assessment.Uncertainty != "" {
			facts = append(facts, "uncertainty "+assessment.Uncertainty)
		}
		line("**Assessment:** %s. %s\n", strings.Join(facts, "; "), strings.TrimSpace(assessment.Evidence))
	}
	line("## Brief\n\n%s\n", strings.TrimSpace(plan.Brief))
	streams := func(resolved *Resolved, list []Stream, heading string) {
		for _, stream := range list {
			title := fmt.Sprintf("%s %s — %s, %s", heading, stream.Name, stream.Domain, resolved.WriterRoute(stream, 0))
			switch {
			case stream.Done:
				title = fmt.Sprintf("%s %s — %s, already done", heading, stream.Name, stream.Domain)
			case len(stream.After) > 0:
				title += ", after " + strings.Join(stream.After, ", ")
			}
			line("%s\n", title)
			if strings.TrimSpace(stream.Brief) != "" {
				line("%s\n", strings.TrimSpace(stream.Brief))
			}
			for _, file := range stream.Files {
				line("- `%s`", file)
			}
			line("")
		}
	}
	if plan.staged() {
		line("## Requirements\n")
		for _, requirement := range plan.Requirements {
			var where []string
			for _, stage := range plan.Stages {
				if slices.Contains(stage.Covers, requirement.ID) {
					where = append(where, stage.Name)
				}
			}
			line("- **%s** (%s): %s", requirement.ID, strings.Join(where, ", "), requirement.Text)
		}
		line("\n## Stages\n\nDone and reviewed one after another; a stage starts when the one before it is accepted.\n")
		for index, stage := range plan.Stages {
			line("### %d. %s — %s\n\n%s\n", index+1, stage.Name, resolved.Stages[index].Mode, strings.TrimSpace(stage.Goal))
			for _, item := range stage.Acceptance {
				line("- accepted when: %s", item)
			}
			if len(stage.Acceptance) > 0 {
				line("")
			}
			streams(resolved.Stages[index], stage.Streams, "####")
			for _, check := range stage.Checks {
				line("- check: `%s`", strings.Join(check.Argv, " "))
			}
			if len(stage.Checks) > 0 {
				line("")
			}
		}
		if integrationNeeded(resolved.Mode, len(plan.Stages)) {
			line("After the last stage the whole change gets one more review, for the seams between the stages.\n")
		}
	} else {
		line("## Streams\n")
		streams(resolved, plan.Streams, "###")
	}
	if len(plan.Checks) > 0 {
		line("## Checks\n")
		for _, check := range plan.Checks {
			note := ""
			if check.Baseline {
				note = " (baseline)"
			}
			line("- `%s`%s", strings.Join(check.Argv, " "), note)
		}
		line("")
	}
	if len(plan.Context) > 0 {
		line("## Context\n")
		for _, item := range plan.Context {
			line("- %s", item)
		}
		line("")
	}
	if ui := plan.UI; ui != nil {
		line("## Interface\n")
		switch {
		case ui.Skip != "":
			line("No interface contract: %s\n", ui.Skip)
		case ui.Design != "":
			line("Design receipt `%s`. %s\n", ui.Design, ui.Acceptance)
		default:
			line("%s\n", ui.Summary)
			for _, criterion := range ui.Criteria {
				line("- %s", criterion)
			}
			line("")
		}
	}
	if accepted := plan.Architecture; accepted != nil {
		line("## Accepted architecture\n")
		line("Receipt `%s`%s.\n\n%s\n", accepted.ID, map[bool]string{true: ", built on the design by " + accepted.Base, false: ""}[accepted.Base != ""],
			strings.TrimSpace(accepted.Acceptance))
		ids := make([]string, 0, len(accepted.Blockers))
		for id := range accepted.Blockers {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			line("- **%s:** %s", id, accepted.Blockers[id])
		}
		if lane := architectureLane(plan); lane != "" {
			line("\n### Design\n\n%s", lane)
		}
		line("")
	}
	line("---\n\nRun it: `agentgate launch --plan %s` (inside `/dev`, any session of this checkout).", name)
	return out.String()
}

// architectureLane is the accepted design text, when its receipt is still on this machine.
func architectureLane(plan *Plan) string {
	receipt, err := loadReceipt(plan.Architecture.ID, "architecture")
	if err != nil {
		return ""
	}
	if lane := receipt.accepted(plan.Architecture.Base); lane != nil {
		return strings.TrimSpace(lane.Report.Design)
	}
	return ""
}

// PlanLink keeps this repository's .agentgate directory (plans, personal settings, helper scripts) inside a
// synced folder and leaves a link in its place, so the same plans are there on every computer.
// home is the synced folder; the repository gets a subdirectory named after itself.
func PlanLink(cfg *config.Config, home string) (any, error) {
	if home == "" {
		home = cfg.PlansHome
	}
	if home == "" {
		return nil, errors.New("usage: agentgate plan link <synced directory>, e.g. ~/iCloud/agentgate (remembered for other repositories)")
	}
	top, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errors.New("run inside a git repository")
	}
	top = strings.TrimSpace(top)
	local := filepath.Join(top, config.RepoDir())
	target := filepath.Join(config.ExpandHome(home), filepath.Base(top))
	info, localErr := os.Lstat(local)
	_, targetErr := os.Stat(target)
	switch {
	case localErr == nil && info.Mode()&os.ModeSymlink != 0:
		current, _ := os.Readlink(local)
		if current != target {
			return nil, fmt.Errorf(".agentgate already links to %s; remove that link first to move it", current)
		}
	case localErr == nil && targetErr == nil:
		return nil, fmt.Errorf("both %s and %s hold data; merge them by hand, remove one, then link again", local, target)
	case localErr == nil:
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, err
		}
		if err := os.Rename(local, target); err != nil {
			// Another filesystem: copy, then remove the original only when the copy is complete.
			if out, err := exec.Command("cp", "-a", "--", local, target).CombinedOutput(); err != nil {
				return nil, fmt.Errorf("copy to %s: %s", target, strings.TrimSpace(string(out)))
			}
			if err := os.RemoveAll(local); err != nil {
				return nil, err
			}
		}
		if err := os.Symlink(target, local); err != nil {
			return nil, err
		}
	default:
		if err := os.MkdirAll(target, 0o700); err != nil {
			return nil, err
		}
		if err := os.Symlink(target, local); err != nil {
			return nil, err
		}
	}
	if cfg.PlansHome != home {
		if err := config.Set("plans_home", home); err != nil {
			return nil, err
		}
	}
	return map[string]any{"link": local, "target": target,
		"next": "Plans of this repository now live in the synced folder. On another computer run the same command in its checkout."}, nil
}
