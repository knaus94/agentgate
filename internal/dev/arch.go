package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

// Issue is a risk an architect names in its own design, or a blocker a critic finds in the other one.
type Issue struct {
	ID       string `json:"id"`
	Evidence string `json:"evidence"`
	Remedy   string `json:"remedy"`
}

type archReport struct {
	Summary       string   `json:"summary"`
	Design        string   `json:"design,omitempty"`
	Risks         []Issue  `json:"risks,omitempty"`
	OpenQuestions []string `json:"open_questions,omitempty"`
	Blockers      []Issue  `json:"blockers,omitempty"`
	Agreements    string   `json:"agreements,omitempty"`
}

type ArchLane struct {
	Round   string       `json:"round"` // design, critique or revision
	Route   config.Route `json:"route"`
	Session string       `json:"session"`
	Dir     string       `json:"dir,omitempty"` // where the session lives, so a later round can continue it
	Report  archReport   `json:"report"`
}

// Receipt is the stored result of an architecture pass or a UI design, bound to the contract it answered.
type Receipt struct {
	ID            string                  `json:"id"`
	Kind          string                  `json:"kind"` // architecture or ui
	Fingerprint   string                  `json:"fingerprint,omitempty"`
	Mode          string                  `json:"mode,omitempty"`
	Lanes         []ArchLane              `json:"lanes,omitempty"`
	Revised       bool                    `json:"revised,omitempty"`
	Substitutions []provider.Substitution `json:"substitutions,omitempty"`
	Design        *uiDesign               `json:"design,omitempty"`
	Route         *config.Route           `json:"route,omitempty"`
	Revision      int                     `json:"revision,omitempty"`
	Dir           string                  `json:"dir"`
}

var receiptID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// designsDir belongs to the checkout, not to one session: a plan designed in one conversation can be
// launched from another. A receipt stays bound to its contract by the fingerprint.
func designsDir() string { return filepath.Join(checkoutDir(""), "designs") }

// loadReceipt reads a receipt of this checkout.
func loadReceipt(id, kind string) (*Receipt, error) {
	raw, err := os.ReadFile(filepath.Join(designsDir(), id, "receipt.json"))
	receipt := &Receipt{}
	if !receiptID.MatchString(id) || err != nil || json.Unmarshal(raw, receipt) != nil || receipt.Kind != kind {
		return nil, fmt.Errorf("unknown %s receipt for this checkout: %q", kind, id)
	}
	return receipt, nil
}

func (r *Receipt) save() error {
	if err := os.MkdirAll(r.Dir, 0o700); err != nil {
		return err
	}
	return ops.WriteJSON(filepath.Join(r.Dir, "receipt.json"), r)
}

// fingerprint binds an architecture receipt to the contract, not to file contents:
// source drift after a long pass must not throw the design away.
func (r *Resolved) fingerprint() string {
	if r.Plan.staged() {
		// The design of a staged task is bound to what is asked and to the shape of the split, not to the
		// file lists of stages that are still ahead and may be revised.
		var names []string
		for _, stage := range r.Plan.Stages {
			names = append(names, stage.Name)
		}
		return digest(map[string]any{"brief": r.Plan.Brief, "requirements": r.Plan.Requirements, "stages": names, "ui": r.Plan.UI, "mode": r.Mode})
	}
	parts := map[string]any{"brief": r.Plan.Brief, "files": r.Plan.Files(), "ui": r.Plan.UI, "mode": r.Mode}
	if len(r.Plan.Requirements) > 0 {
		parts["requirements"] = r.Plan.Requirements
	}
	return digest(parts)
}

var designSchema = object(map[string]any{"summary": text, "design": text, "open_questions": list(text),
	"risks": list(object(map[string]any{"id": text, "evidence": text, "remedy": text}))})

var critiqueSchema = object(map[string]any{"summary": text, "agreements": text,
	"blockers": list(object(map[string]any{"id": text, "evidence": text, "remedy": text}))})

// Architecture runs the mode's design passes. Independent designs run in parallel on different pools;
// in ultra each architect then looks for blockers in the other's design. A critique and a revision continue
// the session of the architect that already read the code, instead of paying for that reading again.
func Architecture(cfg *config.Config, plan *Plan) (any, error) {
	resolved, err := Resolve(cfg, plan)
	if err != nil {
		return nil, err
	}
	mode := resolved.ModeConfig()
	if len(mode.Architecture) == 0 {
		return map[string]any{"mode": resolved.Mode, "decision": resolved.Decision,
			"next": "This mode has no architecture pass: the brief is the design. Launch directly."}, nil
	}
	ui, err := loadUI(plan)
	if err != nil {
		return nil, err
	}
	timeout := seconds(mode.ArchitectureTimeoutS)
	if timeout == 0 {
		timeout = seconds(timeoutDefault)
	}
	receipt := &Receipt{ID: newID(), Kind: "architecture", Fingerprint: resolved.fingerprint(), Mode: resolved.Mode}
	receipt.Dir = filepath.Join(designsDir(), receipt.ID)
	subs := &substitutions{}
	packet := map[string]any{"brief": plan.Brief, "requirements": plan.Requirements, "files": plan.Files(), "streams": plan.allStreams(), "context": plan.Context, "ui": ui}
	if plan.staged() {
		packet["requirements"], packet["stages"] = plan.Requirements, plan.Stages
	}
	run := func(round, name string, route config.Route, extra map[string]any, schema any, from *ArchLane) (ArchLane, error) {
		body := map[string]any{"round": round}
		for key, value := range packet {
			body[key] = value
		}
		for key, value := range extra {
			body[key] = value
		}
		key := digest(map[string]any{"fingerprint": receipt.Fingerprint, "route": route.String(), "body": body})
		cache := filepath.Join(designsDir(), "cache", key+".json")
		lane := ArchLane{}
		if raw, err := os.ReadFile(cache); err == nil && json.Unmarshal(raw, &lane) == nil {
			return lane, nil // a completed pass of an interrupted run is never paid for twice
		}
		dir := filepath.Join(receipt.Dir, name)
		if err := writePacket(filepath.Join(dir, "packet.json"), body); err != nil {
			return lane, err
		}
		tools, toolDirs, toolNote := workerTools(cfg, "architect")
		prompt := assets.Prompt("architecture") + "\nYour round: " + round + ". Read the packet at " + filepath.Join(dir, "packet.json") + ".\n" + toolNote
		call := provider.Call{Route: route, Prompt: prompt, Dir: dir, Schema: schema, Timeout: timeout, MCP: tools, Writable: toolDirs, Persist: true}
		var result provider.Result
		var used config.Route
		err := errors.New("no session to continue")
		if from != nil && from.Session != "" && dirExists(from.Dir) {
			again := call
			again.Dir, again.Resume, again.ReadDirs = from.Dir, from.Session, []string{dir}
			again.Route = from.Route // the model and the account the session belongs to
			again.Route.Effort, again.Route.SubstitutedFrom = route.Effort, ""
			again.Prompt = "Your next round: " + round + ". Its rules are in your first instructions. Read the packet at " +
				filepath.Join(dir, "packet.json") + " and return JSON matching the schema of this turn.\n" + toolNote
			if result, used, err = subs.invoke(cfg, again, nil); err == nil {
				dir = from.Dir
			} else if ops.IsCancelled(err) || errors.Is(err, ops.ErrTimeout) {
				return lane, err
			}
		}
		if err != nil { // nothing to continue, or the session could not be continued: a fresh architect
			if result, used, err = subs.invoke(cfg, call, nil); err != nil {
				return lane, err
			}
		}
		lane = ArchLane{Round: round, Route: used, Session: result.Session, Dir: dir}
		if err := decode(result.Answer, &lane.Report); err != nil {
			return lane, err
		}
		if strings.TrimSpace(lane.Report.Summary) == "" {
			return lane, errors.New("an architecture report came back empty")
		}
		for index := range lane.Report.Blockers {
			lane.Report.Blockers[index].ID = used.Alias + "/" + lane.Report.Blockers[index].ID
		}
		_ = writePacket(cache, lane)
		return lane, nil
	}

	if plan.Revise != "" {
		return revise(resolved, receipt, run)
	}
	routes := make([]config.Route, len(mode.Architecture))
	for index, spec := range mode.Architecture {
		routes[index] = cfg.MustRoute(spec)
	}
	designs := make([]ArchLane, len(routes))
	errs := parallel(len(routes), len(routes), func(index int) (err error) {
		designs[index], err = run("design", fmt.Sprintf("design-%d", index), routes[index], nil, designSchema, nil)
		return err
	})
	if err := firstError(errs); err != nil {
		return nil, fmt.Errorf("architecture incomplete (finished passes are kept; run it again): %w", err)
	}
	receipt.Lanes = designs
	if mode.Critique {
		critiques := make([]ArchLane, len(routes))
		errs := parallel(len(routes), len(routes), func(index int) (err error) {
			other := designs[len(routes)-1-index].Report
			critic := routes[index]
			critic.Effort = config.Lower(critic.Effort) // judging one finished design takes less than producing one
			critiques[index], err = run("critique", fmt.Sprintf("critique-%d", index), critic,
				map[string]any{"design_under_review": other}, critiqueSchema, &designs[index])
			return err
		})
		if err := firstError(errs); err != nil {
			return nil, fmt.Errorf("architecture incomplete (finished passes are kept; run it again): %w", err)
		}
		receipt.Lanes = append(receipt.Lanes, critiques...)
	}
	receipt.Substitutions = subs.list
	return receipt, receipt.save()
}

// revise lets the first architect address the blockers the lead verified. One round only:
// what is still open after it is a decision for the lead or the user, not another model pass.
func revise(resolved *Resolved, receipt *Receipt, run func(string, string, config.Route, map[string]any, any, *ArchLane) (ArchLane, error)) (any, error) {
	plan := resolved.Plan
	previous, err := loadReceipt(plan.Revise, "architecture")
	if err != nil {
		return nil, err
	}
	if previous.Fingerprint != receipt.Fingerprint {
		return nil, errors.New("the contract changed since that receipt; run a fresh architecture pass")
	}
	if previous.Revised {
		return nil, errors.New("this receipt was already revised once; resolve what remains in architecture.blockers")
	}
	known := previous.blockers()
	var chosen []Issue
	for _, id := range plan.ReviseBlockers {
		issue, ok := known[id]
		if !ok {
			return nil, fmt.Errorf("unknown blocker %q", id)
		}
		chosen = append(chosen, issue)
	}
	if len(chosen) == 0 {
		return nil, errors.New("revise_blockers lists the blockers the lead verified against the code")
	}
	primary := resolved.Config.MustRoute(resolved.ModeConfig().Architecture[0])
	var designed *ArchLane
	if len(previous.Lanes) > 0 && previous.Lanes[0].Round == "design" {
		designed = &previous.Lanes[0]
	}
	lane, err := run("revision", "revision", primary, map[string]any{"prior": previous.Lanes, "verified_blockers": chosen}, designSchema, designed)
	if err != nil {
		return nil, err
	}
	receipt.Lanes, receipt.Revised = append(previous.Lanes, lane), true
	return receipt, receipt.save()
}

func (r *Receipt) blockers() map[string]Issue {
	out := map[string]Issue{}
	for _, lane := range r.Lanes {
		for _, issue := range lane.Report.Blockers {
			out[issue.ID] = issue
		}
	}
	return out
}

// requireArchitecture refuses to start writers before the mode's design is accepted and every blocker has an answer.
func (r *Resolved) requireArchitecture() error {
	if len(r.ModeConfig().Architecture) == 0 || r.Request.ReviewFirst() {
		return nil
	}
	accepted := r.Plan.Architecture
	if accepted == nil || strings.TrimSpace(accepted.Acceptance) == "" {
		return fmt.Errorf("%s needs an architecture pass: run `agentgate architecture` and add architecture {id, acceptance}", r.Mode)
	}
	receipt, err := loadReceipt(accepted.ID, "architecture")
	if err != nil {
		return err
	}
	if receipt.Fingerprint != r.fingerprint() {
		return errors.New("the brief, files, ui or mode changed since the architecture pass; run it again")
	}
	if receipt.accepted(accepted.Base) == nil {
		return fmt.Errorf("architecture.base %q names no design of that receipt", accepted.Base)
	}
	var open []string
	for id := range receipt.blockers() {
		if strings.TrimSpace(accepted.Blockers[id]) == "" {
			open = append(open, id)
		}
	}
	if len(open) > 0 {
		return fmt.Errorf("say how each blocker is resolved or why it is rejected in architecture.blockers: %s", strings.Join(open, ", "))
	}
	return nil
}

// accepted returns the one design the task follows: the revision when there was one, otherwise the design
// the lead chose as its base.
func (r *Receipt) accepted(base string) *ArchLane {
	var chosen *ArchLane
	for index := range r.Lanes {
		lane := &r.Lanes[index]
		switch {
		case lane.Round == "revision":
			return lane
		case lane.Round == "design" && ((chosen == nil && base == "") || lane.Route.Alias == base):
			chosen = lane
		}
	}
	return chosen
}

// architectureFor is what writers and reviewers receive: one accepted design and the lead's amendments to it.
// The other design stays in the receipt; handing both to a writer would make it choose between them again.
func architectureFor(plan *Plan) any {
	if plan.Architecture == nil {
		return nil
	}
	receipt, err := loadReceipt(plan.Architecture.ID, "architecture")
	if err != nil {
		return nil
	}
	lane := receipt.accepted(plan.Architecture.Base)
	if lane == nil {
		return nil
	}
	return map[string]any{"design": lane.Report.Design, "risks": lane.Report.Risks, "designed_by": lane.Route.String(),
		"lead_acceptance": plan.Architecture.Acceptance, "blockers": plan.Architecture.Blockers,
		"rule":        "Follow the design as amended by lead_acceptance. Where they differ, lead_acceptance wins.",
		"all_designs": filepath.Join(receipt.Dir, "receipt.json")}
}
