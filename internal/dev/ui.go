package dev

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/provider"
)

// uiDesign is a designer's specification stored in a receipt.
type uiDesign struct {
	Status       string          `json:"status"` // READY or BLOCKED
	UsageContext string          `json:"usage_context"`
	Usage        *Usage          `json:"usage,omitempty"` // the jobs it was designed for
	Spec         json.RawMessage `json:"spec"`
}

// uiContract is what writers, architects and reviewers receive: the specification, the lead's acceptance and
// the jobs the interface is for.
type uiContract struct {
	Spec         any    `json:"spec"`
	Acceptance   string `json:"acceptance"`
	UsageContext string `json:"usage_context,omitempty"`
	Usage        *Usage `json:"usage,omitempty"`
}

// loadUI normalizes the two UI routes so every role sees the same effective contract.
func loadUI(plan *Plan) (*uiContract, error) {
	ui := plan.UI
	switch {
	case ui == nil || ui.Skip != "":
		return nil, nil
	case ui.Summary != "":
		local := *ui
		local.Usage = nil
		return &uiContract{Spec: local, Usage: ui.Usage, Acceptance: "Lead-authored local contract; preserve existing behaviour outside the stated change."}, nil
	}
	receipt, err := loadReceipt(ui.Design, "ui")
	if err != nil {
		return nil, err
	}
	if receipt.Design.Status != "READY" {
		return nil, errors.New("a blocked UI design cannot be accepted; resolve its open questions first")
	}
	return &uiContract{Spec: receipt.Design.Spec, Acceptance: ui.Acceptance, UsageContext: receipt.Design.UsageContext,
		Usage: cmp.Or(ui.Usage, receipt.Design.Usage)}, nil
}

// requireInterfaceDesign refuses to let writers build an interface nobody designed for its jobs.
func (r *Resolved) requireInterfaceDesign() error {
	ui := r.Plan.UI
	if ui == nil || ui.Usage == nil || ui.Design != "" || r.Request.ReviewFirst() {
		return nil
	}
	return errors.New("the plan adds an interface (ui.usage): it is designed for its jobs before anyone writes it. `agentgate architecture` " +
		"with the plan runs the mode's designer, and its architects, and answers with the ui entry that accepts the design")
}

type DesignInput struct {
	Task         string   `json:"task"`
	Brief        string   `json:"brief"`
	UsageContext string   `json:"usage_context"` // roles, frequency, the decisions users make and the data they need
	Usage        *Usage   `json:"usage,omitempty"`
	Criteria     []string `json:"criteria,omitempty"` // what the interface must achieve: the plan's ui.criteria
	Preserve     []string `json:"preserve,omitempty"` // what it must not change: the plan's ui.preserve
	Files        []string `json:"files,omitempty"`
	Context      []string `json:"context,omitempty"`
	Revise       string   `json:"revise,omitempty"`
	Notes        string   `json:"notes,omitempty"`
	Mode         string   `json:"mode,omitempty"`   // the task's mode: its ui_designer runs; default: the global one
	Effort       string   `json:"effort,omitempty"` // an effort for this design instead of the route's
	TimeoutS     int      `json:"timeout_s,omitempty"`
}

// jobWalk is one job of the usage as the designer walked it through the proposed interface.
type jobWalk struct {
	ID    string   `json:"id"`
	Steps []string `json:"steps"`
}

const designRevisions = 2

func items(properties map[string]any) map[string]any { return list(object(properties)) }

var uiSchema = object(map[string]any{
	"status": enum("READY", "BLOCKED"), "summary": text, "users_and_goals": list(text),
	"existing_patterns": items(map[string]any{"path": text, "reuse": text}),
	"flows":             items(map[string]any{"name": text, "steps": list(text)}),
	// jobs walks every job of the usage through the proposed interface, from what the person has in hand.
	"jobs": items(map[string]any{"id": text, "steps": list(text), "must_know": list(text)}),
	"screens": items(map[string]any{"name": text, "location": text, "purpose": text, "layout": text, "wireframe": text,
		"components":   items(map[string]any{"name": text, "source": text, "justification": text, "behavior": text}),
		"states":       items(map[string]any{"state": text, "presentation": text}),
		"interactions": list(text), "copy": items(map[string]any{"where": text, "text": text}),
		"validation": list(text), "responsive": text, "accessibility": text}),
	"data_requirements":     items(map[string]any{"field": text, "source": text, "status": enum("available", "missing")}),
	"rejected_alternatives": items(map[string]any{"option": text, "reason": text}),
	"open_questions":        list(text), "assumptions": list(text), "revision_notes": list(text)})

// Design asks a read-only designer for the specification of a new screen or flow, or for a revision of one.
// Exact local edits do not need it: the lead writes a short contract instead. A plan that names its jobs
// (ui.usage) gets its design from `architecture`, which calls the same designer.
func Design(cfg *config.Config, input DesignInput) (any, error) {
	receipt, err := designInterface(cfg, input)
	if err != nil {
		return nil, err
	}
	return map[string]any{"design": receipt.ID, "status": receipt.Design.Status, "revision": receipt.Revision, "route": receipt.Route,
		"spec": receipt.Design.Spec}, nil
}

// designInterface runs the designer and stores its receipt. A revision continues from the packet of the design
// it revises: what the lead does not give again is what the designer had before.
func designInterface(cfg *config.Config, input DesignInput) (*Receipt, error) {
	receipt := &Receipt{ID: newID(), Kind: "ui"}
	var previous *Receipt
	if input.Revise != "" {
		var err error
		if previous, err = loadReceipt(input.Revise, "ui"); err != nil {
			return nil, err
		}
		if receipt.Revision = previous.Revision + 1; receipt.Revision > designRevisions {
			return nil, errors.New("the design was revised twice already; take the open points to the user")
		}
		if strings.TrimSpace(input.Notes) == "" {
			return nil, errors.New("a revision needs notes: what the user decided or said about the design")
		}
		var before DesignInput
		raw, _ := os.ReadFile(filepath.Join(previous.Dir, "packet.json"))
		_ = json.Unmarshal(raw, &before)
		input.Task, input.Brief = cmp.Or(input.Task, before.Task), cmp.Or(input.Brief, before.Brief)
		input.UsageContext, input.Usage = cmp.Or(input.UsageContext, before.UsageContext), cmp.Or(input.Usage, before.Usage, previous.Design.Usage)
		if len(input.Files) == 0 {
			input.Files = before.Files
		}
		if len(input.Criteria) == 0 {
			input.Criteria = before.Criteria
		}
		if len(input.Preserve) == 0 {
			input.Preserve = before.Preserve
		}
		if len(input.Context) == 0 {
			input.Context = before.Context
		}
	}
	if strings.TrimSpace(input.Task) == "" || strings.TrimSpace(input.Brief) == "" || strings.TrimSpace(input.UsageContext) == "" && input.Usage == nil {
		return nil, errors.New("provide task, brief and usage_context (roles, frequency, decisions and the data they need) or usage {who, jobs}")
	}
	files, err := normalizeScope(input.Files)
	if err != nil {
		return nil, err
	}
	context, err := cleanPaths(input.Context)
	if err != nil {
		return nil, err
	}
	if input.Mode != "" && !slices.Contains(config.ModeNames, input.Mode) {
		return nil, fmt.Errorf("mode is one of %s", strings.Join(config.ModeNames, ", "))
	}
	route := cfg.MustRoute(cfg.UIDesignerFor(input.Mode))
	if input.Effort != "" {
		if !slices.Contains(config.Efforts, input.Effort) {
			return nil, fmt.Errorf("effort is one of %s", strings.Join(config.Efforts, ", "))
		}
		route.Effort = input.Effort
	}
	timeout, err := timeoutSeconds(input.TimeoutS)
	if err != nil {
		return nil, err
	}
	receipt.Dir = filepath.Join(designsDir(), receipt.ID)
	rules, err := loadReviewRules(cfg, files)
	if err != nil {
		return nil, err
	}
	packet := map[string]any{"task": input.Task, "brief": input.Brief, "usage_context": input.UsageContext, "files": files, "context": context,
		"review_rules": rules}
	if input.Usage != nil {
		packet["usage"] = input.Usage
	}
	if len(input.Criteria) > 0 {
		packet["criteria"] = input.Criteria
	}
	if len(input.Preserve) > 0 {
		packet["preserve"] = input.Preserve
	}
	if len(files) == 0 {
		packet["review_rule_catalogue"] = reviewRuleCatalogue(cfg)
	}
	if previous != nil {
		packet["previous_design"], packet["lead_notes"] = previous.Design.Spec, input.Notes
	}
	// The same question to the same designer is not paid for twice: an architecture pass that failed after its
	// interface was designed runs again on that design.
	cache := filepath.Join(designsDir(), "cache", "ui-"+digest(map[string]any{"route": route.String(), "packet": packet})+".json")
	var known struct {
		ID string `json:"id"`
	}
	if raw, err := os.ReadFile(cache); err == nil && json.Unmarshal(raw, &known) == nil {
		if earlier, err := loadReceipt(known.ID, "ui"); err == nil {
			return earlier, nil
		}
	}
	path := filepath.Join(receipt.Dir, "packet.json")
	if err := writePacket(path, packet); err != nil {
		return nil, err
	}
	subs := &substitutions{}
	prompt := assets.Prompt("ui-design") + "\nRead the design packet at " + path + ". Its paths are relative to the current directory.\n"
	result, used, err := subs.invoke(cfg, provider.Call{Route: route, Prompt: prompt, Dir: filepath.Join(receipt.Dir, "designer"),
		Schema: uiSchema, Timeout: seconds(timeout), ReadDirs: []string{receipt.Dir}}, nil)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Status        string            `json:"status"`
		Screens       []json.RawMessage `json:"screens"`
		OpenQuestions []string          `json:"open_questions"`
		Jobs          []jobWalk         `json:"jobs"`
	}
	if err := decode(result.Answer, &parsed); err != nil {
		return nil, err
	}
	if parsed.Status == "READY" && (len(parsed.Screens) == 0 || len(parsed.OpenQuestions) > 0) {
		return nil, errors.New("the designer reported READY without screens or with open questions")
	}
	if parsed.Status != "READY" && parsed.Status != "BLOCKED" {
		return nil, fmt.Errorf("the designer reported an unknown status %q", parsed.Status)
	}
	if parsed.Status == "READY" && input.Usage != nil {
		var unwalked []string
		for _, job := range input.Usage.Jobs {
			if !slices.ContainsFunc(parsed.Jobs, func(walk jobWalk) bool { return walk.ID == job.ID && len(walk.Steps) > 0 }) {
				unwalked = append(unwalked, job.ID)
			}
		}
		if len(unwalked) > 0 {
			return nil, fmt.Errorf("the designer reported READY without walking jobs %s through the interface; run it again", strings.Join(unwalked, ", "))
		}
	}
	receipt.Design = &uiDesign{Status: parsed.Status, UsageContext: input.UsageContext, Usage: input.Usage, Spec: json.RawMessage(result.Answer)}
	receipt.Route, receipt.Substitutions = &used, subs.list
	if err := receipt.save(); err != nil {
		return nil, err
	}
	return receipt, writePacket(cache, map[string]string{"id": receipt.ID})
}

type ConsultLane struct {
	Name     string   `json:"name"`
	Question string   `json:"question"`
	Files    []string `json:"files,omitempty"`
	Route    string   `json:"route,omitempty"` // alias:effort; defaults to the configured consultant
}

type ConsultInput struct {
	Lanes    []ConsultLane `json:"lanes"`
	TimeoutS int           `json:"timeout_s,omitempty"`
}

// Consult runs read-only questions in parallel for plan, research and verify. It opens no gate and edits nothing.
func Consult(cfg *config.Config, input ConsultInput) (any, error) {
	if len(input.Lanes) == 0 {
		return nil, errors.New("provide lanes: [{name, question, files?, route?}]")
	}
	timeout, err := timeoutSeconds(input.TimeoutS)
	if err != nil {
		return nil, err
	}
	routes := make([]config.Route, len(input.Lanes))
	names := map[string]bool{}
	for index, lane := range input.Lanes {
		if !safeName.MatchString(lane.Name) || strings.TrimSpace(lane.Question) == "" {
			return nil, errors.New("each lane needs a safe name (letters, numbers, dot, dash or underscore) and a bounded question")
		}
		if names[lane.Name] {
			return nil, fmt.Errorf("duplicate consultation lane %q", lane.Name)
		}
		names[lane.Name] = true
		spec := lane.Route
		if spec == "" {
			spec = cfg.Consultant
		}
		if routes[index], err = cfg.Route(spec); err != nil {
			return nil, err
		}
		if input.Lanes[index].Files, err = cleanPaths(lane.Files); err != nil {
			return nil, err
		}
	}
	dir := filepath.Join(home(), "consults", newID())
	subs := &substitutions{}
	answers := make([]map[string]any, len(input.Lanes))
	errs := parallel(len(input.Lanes), cfg.MaxParallel, func(index int) error {
		lane := input.Lanes[index]
		path := filepath.Join(dir, lane.Name, "packet.json")
		if err := writePacket(path, lane); err != nil {
			return err
		}
		tools, toolDirs, toolNote := workerTools(cfg, "consultant")
		prompt := assets.Prompt("consult") + "\nRead your question at " + path + ".\n" + toolNote
		result, used, err := subs.invoke(cfg, provider.Call{Route: routes[index], Prompt: prompt, Dir: filepath.Join(dir, lane.Name, "run"),
			Timeout: seconds(timeout), ReadDirs: []string{filepath.Join(dir, lane.Name)}, MCP: tools, Writable: toolDirs}, nil)
		if err != nil {
			return err
		}
		answers[index] = map[string]any{"name": lane.Name, "route": used, "answer": result.Answer, "artifacts": result.Dir}
		return nil
	})
	if err := firstError(errs); err != nil {
		return nil, err
	}
	return map[string]any{"answers": answers, "substitutions": subs.list, "artifacts": dir}, nil
}
