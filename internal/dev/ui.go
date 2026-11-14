package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/provider"
)

// uiDesign is a designer's specification stored in a receipt.
type uiDesign struct {
	Status       string          `json:"status"` // READY or BLOCKED
	UsageContext string          `json:"usage_context"`
	Spec         json.RawMessage `json:"spec"`
}

// uiContract is what writers, architects and reviewers receive: the specification and the lead's acceptance.
type uiContract struct {
	Spec         any    `json:"spec"`
	Acceptance   string `json:"acceptance"`
	UsageContext string `json:"usage_context,omitempty"`
}

// loadUI normalizes the two UI routes so every role sees the same effective contract.
func loadUI(plan *Plan) (*uiContract, error) {
	ui := plan.UI
	switch {
	case ui == nil || ui.Skip != "":
		return nil, nil
	case ui.Summary != "":
		return &uiContract{Spec: ui, Acceptance: "Lead-authored local contract; preserve existing behaviour outside the stated change."}, nil
	}
	receipt, err := loadReceipt(ui.Design, "ui")
	if err != nil {
		return nil, err
	}
	if receipt.Design.Status != "READY" {
		return nil, errors.New("a blocked UI design cannot be accepted; resolve its open questions first")
	}
	return &uiContract{Spec: receipt.Design.Spec, Acceptance: ui.Acceptance, UsageContext: receipt.Design.UsageContext}, nil
}

type DesignInput struct {
	Task         string   `json:"task"`
	Brief        string   `json:"brief"`
	UsageContext string   `json:"usage_context"` // roles, frequency, the decisions users make and the data they need
	Files        []string `json:"files,omitempty"`
	Context      []string `json:"context,omitempty"`
	Revise       string   `json:"revise,omitempty"`
	Notes        string   `json:"notes,omitempty"`
	Effort       string   `json:"effort,omitempty"` // medium or high
	TimeoutS     int      `json:"timeout_s,omitempty"`
}

const designRevisions = 2

func items(properties map[string]any) map[string]any { return list(object(properties)) }

var uiSchema = object(map[string]any{
	"status": enum("READY", "BLOCKED"), "summary": text, "users_and_goals": list(text),
	"existing_patterns": items(map[string]any{"path": text, "reuse": text}),
	"flows":             items(map[string]any{"name": text, "steps": list(text)}),
	"screens": items(map[string]any{"name": text, "location": text, "purpose": text, "layout": text, "wireframe": text,
		"components":   items(map[string]any{"name": text, "source": text, "justification": text, "behavior": text}),
		"states":       items(map[string]any{"state": text, "presentation": text}),
		"interactions": list(text), "copy": items(map[string]any{"where": text, "text": text}),
		"validation": list(text), "responsive": text, "accessibility": text}),
	"data_requirements":     items(map[string]any{"field": text, "source": text, "status": enum("available", "missing")}),
	"rejected_alternatives": items(map[string]any{"option": text, "reason": text}),
	"open_questions":        list(text), "assumptions": list(text), "revision_notes": list(text)})

// Design asks a read-only designer for the specification of a new screen or flow.
// Exact local edits do not need it: the lead writes a short contract instead.
func Design(cfg *config.Config, input DesignInput) (any, error) {
	if strings.TrimSpace(input.Task) == "" || strings.TrimSpace(input.Brief) == "" || strings.TrimSpace(input.UsageContext) == "" {
		return nil, errors.New("provide task, brief and usage_context (roles, frequency, decisions and the data they need)")
	}
	files, err := cleanPaths(input.Files)
	if err != nil {
		return nil, err
	}
	context, err := cleanPaths(input.Context)
	if err != nil {
		return nil, err
	}
	route := cfg.MustRoute(cfg.UIDesigner)
	if input.Effort != "" {
		if input.Effort != "medium" && input.Effort != "high" {
			return nil, errors.New("effort is medium or high")
		}
		route.Effort = input.Effort
	}
	timeout, err := timeoutSeconds(input.TimeoutS)
	if err != nil {
		return nil, err
	}
	receipt := &Receipt{ID: newID(), Kind: "ui"}
	receipt.Dir = filepath.Join(designsDir(), receipt.ID)
	rules, err := loadReviewRules(cfg, files)
	if err != nil {
		return nil, err
	}
	packet := map[string]any{"task": input.Task, "brief": input.Brief, "usage_context": input.UsageContext, "files": files, "context": context,
		"review_rules": rules}
	if len(files) == 0 {
		packet["review_rule_catalogue"] = reviewRuleCatalogue(cfg)
	}
	if input.Revise != "" {
		previous, err := loadReceipt(input.Revise, "ui")
		if err != nil {
			return nil, err
		}
		if receipt.Revision = previous.Revision + 1; receipt.Revision > designRevisions {
			return nil, errors.New("the design was revised twice already; take the open points to the user")
		}
		if strings.TrimSpace(input.Notes) == "" {
			return nil, errors.New("a revision needs the lead's notes")
		}
		packet["previous_design"], packet["lead_notes"] = previous.Design.Spec, input.Notes
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
	receipt.Design = &uiDesign{Status: parsed.Status, UsageContext: input.UsageContext, Spec: json.RawMessage(result.Answer)}
	receipt.Route, receipt.Substitutions = &used, subs.list
	return map[string]any{"design": receipt.ID, "status": parsed.Status, "revision": receipt.Revision, "route": used,
		"spec": receipt.Design.Spec}, receipt.save()
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
	for index, lane := range input.Lanes {
		if strings.TrimSpace(lane.Name) == "" || strings.TrimSpace(lane.Question) == "" {
			return nil, errors.New("each lane needs a name and a bounded question")
		}
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
		answers[index] = map[string]any{"name": lane.Name, "route": used, "answer": result.Answer}
		return nil
	})
	if err := firstError(errs); err != nil {
		return nil, err
	}
	return map[string]any{"answers": answers, "substitutions": subs.list, "artifacts": dir}, nil
}
