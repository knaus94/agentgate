package dev

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

type PrepareInput struct {
	Request        string   `json:"request"`
	Name           string   `json:"name,omitempty"`
	Context        []string `json:"context,omitempty"`
	Notes          string   `json:"notes,omitempty"`
	RefreshDesigns bool     `json:"refresh_designs,omitempty"` // re-read Figma when the selected design itself changed
}

var prepareSchema = object(map[string]any{
	"status": enum("READY", "BLOCKED"), "summary": text, "questions": list(text),
	"plan_json": text, "setup": list(text), "context_pack": scoutSchema,
})

var figmaURL = regexp.MustCompile(`https://(?:www\.)?figma\.com/[^\s<>"\x60]+`)

// Prepare delegates repository discovery and plan drafting, not product decisions or execution authority.
// Figma access remains in the host tool; a worker receives only its bounded catalogue, never the profile.
func Prepare(cfg *config.Config, input PrepareInput) (any, error) {
	gate := Current()
	if input.Request == "" && gate != nil && gate.active() {
		input.Request = gate.Plan.Request
	}
	request, err := ParseRequest(input.Request)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Task) == "" || request.Preview || (!request.Writes() && !request.ReviewFirst() && request.Action != "plan") {
		return nil, errors.New("prepare needs a development or planning request")
	}
	context, err := cleanPaths(input.Context)
	if err != nil {
		return nil, err
	}
	mode := request.Mode
	if mode == "" {
		mode = "medium"
	}
	route := cfg.Modes[mode].Scout
	if route == "" {
		route = cfg.Modes["medium"].Scout
	}
	if route == "" {
		return nil, errors.New("prepare needs a scout route; configure modes.medium.scout")
	}
	dir := filepath.Join(checkoutDir(""), "preparations", newID())
	var stand struct {
		Command    []string `json:"command"`
		Ready      string   `json:"ready"`
		EnvFile    string   `json:"env_file"`
		GraphQLEnv []string `json:"graphql_env"`
	}
	if len(cfg.Design.Stand) > 0 {
		if err := json.Unmarshal(cfg.Design.Stand, &stand); err != nil {
			return nil, err
		}
	}
	packet := map[string]any{"request": input.Request, "context": context, "notes": input.Notes,
		"design_settings": map[string]any{"screens": cfg.Design.Screens, "stand": stand}}
	if gate != nil && gate.active() {
		packet["current_plan"], packet["phase"], packet["check_failures"] = gate.Plan, gate.Phase, gate.failing()
		packet["hold_reason"] = gate.HoldReason
		packet["design_context"] = gate.designContext()
	}
	var designs []json.RawMessage
	seen := map[string]bool{}
	links := figmaURL.FindAllString(input.Request, -1)
	if gate != nil && gate.active() && len(gate.Plan.Design) > 0 && input.Request == gate.Plan.Request && !input.RefreshDesigns {
		links = nil
		packet["design_source"] = "Reuse the selected packages and design_context for this task. No Figma browser was started; refresh_designs is needed only when the design selection itself must be rediscovered."
	}
	for _, link := range links {
		link = strings.TrimRight(link, ").,;")
		if seen[link] {
			continue
		}
		seen[link] = true
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(layoutCommand(), "figma", "frames", link, "--json", "--depth", "3")
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := ops.Run(cmd, seconds(layoutTimeout)); err != nil {
			return nil, fmt.Errorf("design catalogue: %w: %s", err, lastLine(stderr.String()))
		}
		if !json.Valid(stdout.Bytes()) {
			return nil, errors.New("figma frames returned no JSON catalogue")
		}
		designs = append(designs, json.RawMessage(stdout.Bytes()))
	}
	packet["design_catalogues"] = designs
	path := filepath.Join(dir, "packet.json")
	tools, toolDirs, toolNote := workerTools(cfg, "scout")
	subs := &substitutions{}
	session := ""
	used := cfg.MustRoute(route)
	for attempt := 0; attempt < 2; attempt++ {
		if err := writePacket(path, packet); err != nil {
			return nil, err
		}
		result, actual, err := subs.invoke(cfg, provider.Call{Route: used,
			Prompt: assets.Prompt("prepare") + "\nRead " + path + ".\n" + toolNote,
			Dir:    filepath.Join(dir, "worker"), Schema: prepareSchema, Timeout: seconds(scoutTimeout),
			Persist: true, Resume: session, MCP: tools, Writable: toolDirs, ReadDirs: []string{dir}}, nil)
		if err != nil {
			return nil, err
		}
		session, used = result.Session, actual
		var answer struct {
			Status    string      `json:"status"`
			Summary   string      `json:"summary"`
			PlanJSON  string      `json:"plan_json"`
			Questions []string    `json:"questions"`
			Setup     []string    `json:"setup"`
			Pack      contextPack `json:"context_pack"`
		}
		if err := decode(result.Answer, &answer); err != nil {
			return nil, err
		}
		if answer.Status == "BLOCKED" && strings.TrimSpace(answer.Summary) != "" {
			return map[string]any{"status": "BLOCKED", "summary": answer.Summary, "questions": answer.Questions,
				"setup": answer.Setup, "route": used, "dir": dir, "next": "Resolve only the reported decisions or technical prerequisites, then prepare again with notes. " +
					"The blocker is recorded here; do not parse private worker files to discover it. No implementation has started."}, nil
		}
		plan := &Plan{}
		decoder := json.NewDecoder(strings.NewReader(answer.PlanJSON))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(plan)
		if err == nil && (answer.Status != "READY" || len(answer.Questions) > 0 || strings.TrimSpace(answer.Summary) == "") {
			err = errors.New("READY needs a summary and no unresolved decisions")
		}
		plan.Request = input.Request // the worker cannot relax the user's mode or request flags
		var resolved *Resolved
		if err == nil {
			resolved, err = Resolve(cfg, plan)
		}
		if err != nil {
			packet["invalid_plan"], packet["validation_error"] = answer.PlanJSON, err.Error()
			continue
		}
		saved, err := PlanSave(cfg, plan, input.Name)
		if err != nil {
			return nil, err
		}
		if !plan.staged() && strings.TrimSpace(answer.Pack.Summary) != "" && len(answer.Pack.Files) > 0 && resolved.ModeConfig().Scout != "" {
			answer.Pack.Route = used
			if err := writePacket(scoutPath(resolved), answer.Pack); err != nil {
				return nil, err
			}
		}
		return map[string]any{"status": "READY", "summary": answer.Summary, "saved": saved, "setup": answer.Setup,
			"checks": plan.Checks, "route": used, "dir": dir, "substitutions": subs.list,
			"next": "Review the brief for product scope and authorization. Approve the listed checks/stand as required; launch the saved plan, " +
				"or replan an active task. Do not repeat repository discovery, parse design data or inspect layout yourself."}, nil
	}
	return nil, fmt.Errorf("preparation could not produce a valid plan: %v; inspect %s", packet["validation_error"], path)
}
