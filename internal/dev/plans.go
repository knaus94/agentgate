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

// planNameFrom makes a plan name of what the preparation worker proposed, or "".
func planNameFrom(proposed string) string {
	words := regexp.MustCompile(`[a-z0-9]+`).FindAllString(strings.ToLower(proposed), -1)
	name := strings.Join(words, "-")
	if len(name) > 40 {
		name = strings.TrimRight(name[:40], "-")
	}
	if planName.MatchString(name) && len(words) > 0 {
		return name
	}
	return ""
}

var link = regexp.MustCompile(`\bhttps?://\S+`)

// slug derives a plan name from the request when neither the lead nor the worker gave one.
func slug(plan *Plan) string {
	// The name comes from the task, not from the command, the flags that precede it or the links in it.
	task := plan.Request
	if request, err := ParseRequest(plan.Request); err == nil {
		task = request.Task
	}
	words := regexp.MustCompile(`[a-z0-9]+`).FindAllString(strings.ToLower(link.ReplaceAllString(task+" "+plan.Brief, " ")), -1)
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

// SavedPlan is where a plan was kept.
type SavedPlan struct {
	Name     string   `json:"name"`
	Plan     string   `json:"plan"`
	Brief    string   `json:"brief"`
	Mode     string   `json:"mode"`
	Decision Decision `json:"decision"`
	// A task the session writes: who writes it, and what the plan starts besides the session.
	Executor string   `json:"executor,omitempty"`
	Runs     int      `json:"model_runs"`
	Price    []string `json:"price,omitempty"`
	Next     string   `json:"next"`
}

// PlanSave validates the plan and writes it with a readable brief next to it. Saving again under the same
// name replaces both, so the file always shows the plan that will run.
func PlanSave(cfg *config.Config, plan *Plan, name string) (*SavedPlan, error) {
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
	var revisions []Revision
	if gate := Current(); gate != nil && gate.active() && gate.PlanName == name {
		revisions = gate.Revisions
	}
	brief := filepath.Join(filepath.Dir(path), "brief.md")
	if err := os.WriteFile(brief, []byte(renderBrief(name, resolved, revisions)), 0o644); err != nil {
		return nil, err
	}
	next := "Show the user the brief path. `start --plan " + name + "` runs it; an active task takes it with `amend --plan " + name + "`. Save again after every change."
	if resolved.Request.Action == "plan" {
		next = "Show the saved brief. This is a read-only proposal, not authorization to implement. Once the user authorizes it: `start --plan " + name + " --implement`."
	}
	saved := &SavedPlan{Name: name, Plan: path, Brief: brief, Mode: resolved.Mode, Decision: resolved.Decision, Next: next}
	if resolved.Executor != "session" {
		return saved, nil
	}
	saved.Executor = "session"
	saved.Runs, saved.Price = resolved.price()
	if err := resolved.keep(name, plan); err != nil {
		return nil, err
	}
	if gate := Current(); gate != nil && gate.active() {
		return saved, nil // the plan of a running task: `amend --plan` takes it
	}
	ui := plan.UI != nil && plan.UI.Usage != nil && plan.UI.Design == ""
	switch {
	case resolved.counselNeeded() && plan.Architecture == nil:
		saved.Next = "`architecture --plan " + name + "` has the plan read before the work: " + strings.Join(saved.Price, "; ") + "."
		if len(resolved.ModeConfig().Architecture) > 0 {
			saved.Next = "`architecture` with {plan: \"" + name + "\", design, alternatives, risks} has your design read before the work: " + strings.Join(saved.Price, "; ") + "."
		}
	case ui:
		saved.Next = "The plan adds an interface: `architecture --plan " + name + "` runs its designer first."
	case resolved.counselNeeded() && strings.TrimSpace(plan.Approved) == "":
		saved.Next = "Show the user the brief and the price. On their approval save the plan with approved: <their words>, then `start --plan " + name + "`."
	case resolved.Request.Action == "plan":
	default:
		saved.Next = "`start --plan " + name + "` opens the first stage. Tell the user the plan in a few lines as you start; they interrupt if it is wrong."
	}
	return saved, nil
}

// keep stores beside a saved plan what a task the session writes takes from its planning: the map of the area,
// where its readers find it, and the files the user gave.
func (r *Resolved) keep(name string, plan *Plan) error {
	proposal := &Proposal{Name: name, Digest: digest(plan)}
	if previous := loadProposal(name); previous != nil {
		proposal.ContextPack, proposal.Attachments = previous.ContextPack, previous.Attachments
	}
	if plan.Grounding.usable() {
		pack := *plan.Grounding
		pack.Route = sessionRoute()
		proposal.ContextPack = filepath.Join(checkoutDir(""), "packs", name+".json")
		if err := writePacket(proposal.ContextPack, pack); err != nil {
			return err
		}
	}
	if draft := loadDraft(); draft != nil && draft.Request == plan.Request {
		proposal.Attachments = draft.Attachments
	}
	return proposal.save()
}

// Proposal ties a saved plan to the preparation that drafted it: the worker session that can revise it and
// the map of the code it drew. It lives with the checkout's state, not in the repository.
type Proposal struct {
	Name        string   `json:"name"`
	Digest      string   `json:"digest"` // of the plan as it was saved; a plan edited by hand afterwards is no longer this proposal
	Preparation string   `json:"preparation"`
	ContextPack string   `json:"context_pack,omitempty"`
	Attachments []string `json:"attachments,omitempty"` // the files the user gave, as the preparation kept them
}

func proposalFile(name string) string {
	return filepath.Join(checkoutDir(""), "proposals", name+".json")
}

func loadProposal(name string) *Proposal {
	if !planName.MatchString(name) {
		return nil
	}
	raw, err := os.ReadFile(proposalFile(name))
	proposal := &Proposal{}
	if err != nil || json.Unmarshal(raw, proposal) != nil || proposal.Name != name {
		return nil
	}
	return proposal
}

func (p *Proposal) save() error { return writePacket(proposalFile(p.Name), p) }

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

// renderBrief is the plan as a person reads it: what is built, by whom, how it is checked, and how the plan
// changed while it ran.
func renderBrief(name string, resolved *Resolved, revisions []Revision) string {
	plan := resolved.Plan
	var out strings.Builder
	line := func(format string, values ...any) { fmt.Fprintf(&out, format+"\n", values...) }
	line("# %s\n", name)
	line("> Written by `agentgate`; edit `plan.json` and save again rather than this file.\n")
	if plan.Request != "" {
		// The request opens the brief; a long one is in plan.json, the brief says what was made of it.
		request := strings.TrimSpace(plan.Request)
		if lines := strings.Split(request, "\n"); len(lines) > 5 || len(request) > 600 {
			request = clipText(strings.Join(lines[:min(5, len(lines))], "\n"), 600) + "\n\n(the request continues in plan.json)"
		}
		line("**Request:** %s\n", request)
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
		if load := strings.TrimSpace(assessment.Load); load != "" {
			line("**Load:** %s\n", load)
		}
	}
	line("## Brief\n\n%s\n", strings.TrimSpace(plan.Brief))
	streams := func(mode string, list []Stream, heading string) {
		for _, stream := range list {
			title := fmt.Sprintf("%s %s — %s, %s", heading, stream.Name, stream.Domain, writerRoute(resolved.Config, mode, stream, 0))
			if resolved.Executor == "session" {
				title = fmt.Sprintf("%s %s — %s, written by the session", heading, stream.Name, stream.Domain)
			}
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
	staged := len(plan.Stages) > 1 || !plan.Single
	if len(plan.Requirements) > 0 {
		line("## Requirements\n")
		for _, requirement := range plan.Requirements {
			where := ""
			if last := plan.lastCover(requirement.ID); staged && last >= 0 {
				where = " (due at " + plan.Stages[last].Name + ")"
			}
			line("- **%s**%s: %s", requirement.ID, where, requirement.Text)
			if requirement.Method != "" {
				line("  - evidence method: %s", requirement.Method)
			}
		}
		line("")
	}
	follows := func(stage Stage) {
		if len(stage.Like) > 0 {
			line("Follows: `%s`\n", strings.Join(stage.Like, "`, `"))
		}
		if strings.TrimSpace(stage.LikeWaiver) != "" {
			line("**New pattern** — nothing existing to follow: %s\n", strings.TrimSpace(stage.LikeWaiver))
		}
	}
	if staged {
		line("## Stages\n\nDone and reviewed one after another; a stage starts when the one before it is accepted.\n")
		marks := resolved.checkpoints()
		for index, stage := range plan.Stages {
			title := resolved.Modes[index]
			if resolved.Executor == "session" {
				title = "checks only"
				if slices.Contains(marks, stage.Name) {
					title = "checkpoint: read here"
				}
			}
			line("### %d. %s — %s\n\n%s\n", index+1, stage.Name, title, strings.TrimSpace(stage.Goal))
			follows(stage)
			if len(stage.Covers) > 0 {
				line("Works on: %s\n", strings.Join(stage.Covers, ", "))
			}
			for _, item := range stage.Acceptance {
				line("- accepted when: %s", item)
			}
			if len(stage.Acceptance) > 0 {
				line("")
			}
			streams(resolved.Modes[index], stage.Streams, "####")
			for _, check := range stage.Checks {
				line("- check: `%s`", strings.Join(check.Argv, " "))
			}
			if len(stage.Checks) > 0 {
				line("")
			}
		}
		switch {
		case resolved.Executor == "session":
			line("After the last stage the whole change is read once: %s.\n", resolved.finalReader())
		case integrationNeeded(resolved.Mode, len(plan.Stages)):
			line("After the last stage the whole change gets one more review, for the seams between the stages.\n")
		}
	} else {
		line("## Streams\n")
		follows(plan.Stages[0])
		streams(resolved.Modes[0], plan.Stages[0].Streams, "###")
	}
	if resolved.Executor == "session" {
		runs, price := resolved.price()
		line("## Price\n\nThe session writes this plan. Besides it, the plan starts **%d model runs**:\n", runs)
		for _, item := range price {
			line("- %s", item)
		}
		if runs == 0 {
			line("- none: the tool runs the checks, and the session reads its own patch at the end")
		}
		line("\nA round of fixes adds a recheck of what it changed; a stall adds a question to the advisor.\n")
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
	if strings.TrimSpace(plan.ChecksWaiver) != "" {
		line("No command verifies this change: %s\n", strings.TrimSpace(plan.ChecksWaiver))
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
		for _, decided := range []map[string]string{accepted.Answers, accepted.Blockers} {
			ids := make([]string, 0, len(decided))
			for id := range decided {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				line("- **%s:** %s", id, decided[id])
			}
		}
		if lane := architectureLane(plan); lane != "" {
			line("\n### Design\n\n%s", lane)
		}
		line("")
	}
	if len(revisions) > 1 {
		line("## Revisions\n\nThe plan changed while the task ran. Each change is recorded with its reason.\n")
		for _, revision := range revisions[1:] {
			line("%d. %s — %s", revision.Number, time.Unix(revision.At, 0).Format("2006-01-02 15:04"), oneLine(revision.Reason))
			for _, change := range revision.Changes {
				line("   - %s", change)
			}
		}
		line("")
	}
	if strings.TrimSpace(plan.Approved) != "" {
		line("**Approved by the user:** %s\n", strings.TrimSpace(plan.Approved))
	}
	line("---\n\nRun it: `agentgate start --plan %s` (inside `/dev`, any session of this checkout).", name)
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
