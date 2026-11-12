package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/knaus94/claude-dev/internal/config"
)

// Stream is one disjoint piece of work owned by one writer.
type Stream struct {
	Name   string   `json:"name"`
	Domain string   `json:"domain"` // backend or frontend
	Files  []string `json:"files"`
	After  []string `json:"after,omitempty"`  // streams that must finish first
	Brief  string   `json:"brief,omitempty"`  // what this stream does, beyond the shared brief
	Effort string   `json:"effort,omitempty"` // raise the writer's effort for a hard stream, up to the mode's ceiling
	Done   bool     `json:"done,omitempty"`   // keeps ownership but starts no writer
}

// Check is an approved verification command. It may be written as a plain argv array.
type Check struct {
	Argv     []string `json:"argv"`
	Inputs   []string `json:"inputs,omitempty"` // globs whose content decides whether a result is still current
	TimeoutS int      `json:"timeout_s,omitempty"`
	Baseline bool     `json:"baseline,omitempty"` // run before writing; a failure that was already there does not block
	NoReuse  bool     `json:"no_reuse,omitempty"` // depends on external state: always rerun
}

func (c *Check) UnmarshalJSON(raw []byte) error {
	if json.Unmarshal(raw, &c.Argv) == nil {
		return nil
	}
	type plain Check
	return json.Unmarshal(raw, (*plain)(c))
}

// UI is the accepted interface contract: exactly one of a local contract, a design or a skip.
type UI struct {
	Summary    string   `json:"summary,omitempty"`
	Criteria   []string `json:"criteria,omitempty"`
	Preserve   []string `json:"preserve,omitempty"`
	Design     string   `json:"design,omitempty"`
	Acceptance string   `json:"acceptance,omitempty"`
	Skip       string   `json:"skip,omitempty"`
}

type Scenario struct {
	ID       string `json:"id"`
	Screen   string `json:"screen"`
	State    string `json:"state"`
	Viewport string `json:"viewport"`
	Role     string `json:"role"`
	Expected string `json:"expected"`
}

// Visual opts in to browser verification on a local stand.
type Visual struct {
	Enabled   bool       `json:"enabled"`
	Reason    string     `json:"reason,omitempty"`
	Scenarios []Scenario `json:"scenarios,omitempty"`
}

// Accepted is the lead's acceptance of an architecture receipt.
type Accepted struct {
	ID         string            `json:"id"`
	Acceptance string            `json:"acceptance"`
	Base       string            `json:"base,omitempty"`     // model alias of the design the lead builds on; default: the first
	Blockers   map[string]string `json:"blockers,omitempty"` // blocker id -> how it is resolved or why it is rejected
}

type Plan struct {
	Request          string      `json:"request"`
	Assessment       *Assessment `json:"assessment,omitempty"`
	Brief            string      `json:"brief"`
	Streams          []Stream    `json:"streams"`
	Context          []string    `json:"context,omitempty"`
	Checks           []Check     `json:"checks,omitempty"`
	UI               *UI         `json:"ui,omitempty"`
	Visual           *Visual     `json:"visual,omitempty"`
	Architecture     *Accepted   `json:"architecture,omitempty"`
	Update           string      `json:"update,omitempty"`   // what changed, for a continuation of an unfinished task
	BaseRef          string      `json:"base_ref,omitempty"` // review and clean: the commit the diff starts from
	WriterTimeoutS   int         `json:"writer_timeout_s,omitempty"`
	ReviewerTimeoutS int         `json:"reviewer_timeout_s,omitempty"`
	Revise           string      `json:"revise,omitempty"`          // architecture only: receipt to revise
	ReviseBlockers   []string    `json:"revise_blockers,omitempty"` // architecture only: blockers the lead verified
}

// Assessment is what the lead found in the code. The mode is computed from these facts, not chosen by taste.
type Assessment struct {
	Size         string   `json:"size"`                   // trivial, ordinary or complex
	Risks        []string `json:"risks,omitempty"`        // named risks the change really touches
	Irreversible bool     `json:"irreversible,omitempty"` // hard to undo once shipped: data migration, destructive change, published contract
	Uncertainty  string   `json:"uncertainty,omitempty"`  // low (default) or material
	Evidence     string   `json:"evidence"`               // the inspected code that supports the facts above
}

// Risks are the named reasons that raise the mode.
var Risks = []string{"authorization", "security", "money", "data-integrity", "concurrency", "migration", "public-contract"}

// severeRisks make a complex change ultra even when it can be undone.
var severeRisks = []string{"authorization", "security", "money"}

// Decision is the mode and the reasons for it.
type Decision struct {
	Mode    string   `json:"mode"`
	Source  string   `json:"source"` // flag or assessment
	Because []string `json:"because"`
}

// decide applies the rule table, most thorough rule first.
func decide(a *Assessment, files, streams int) Decision {
	severe := false
	for _, risk := range a.Risks {
		severe = severe || slices.Contains(severeRisks, risk)
	}
	named := strings.Join(a.Risks, ", ")
	switch {
	case len(a.Risks) > 0 && a.Irreversible:
		return Decision{"ultra", "assessment", []string{"touches " + named, "cannot be undone once shipped"}}
	case severe && a.Size == "complex":
		return Decision{"ultra", "assessment", []string{"touches " + named, "the change is complex"}}
	case len(a.Risks) > 0:
		return Decision{"heavy", "assessment", []string{"touches " + named}}
	case a.Size == "complex" && a.Uncertainty == "material":
		return Decision{"heavy", "assessment", []string{"complex change with material uncertainty"}}
	case a.Size == "trivial" && a.Uncertainty != "material" && files <= 3 && streams == 1:
		return Decision{"light", "assessment", []string{"exact change of at most three files", "no named risk"}}
	}
	return Decision{"medium", "assessment", []string{"ordinary change", "no named risk"}}
}

var uiSuffixes = []string{".tsx", ".jsx", ".vue", ".svelte", ".html", ".css", ".scss", ".sass", ".less"}

var viewport = regexp.MustCompile(`^\d{3,4}x\d{3,4}$`)

const (
	timeoutDefault = 1200
	timeoutMin     = 120
	timeoutMax     = 7200
)

// Files is every path owned by the plan.
func (p *Plan) Files() []string {
	var files []string
	for _, stream := range p.Streams {
		files = append(files, stream.Files...)
	}
	sort.Strings(files)
	return slices.Compact(files)
}

// cleanPaths validates repository-relative regular-file paths (existing or not yet created).
func cleanPaths(values []string) ([]string, error) {
	var out []string
	for _, value := range values {
		clean := filepath.ToSlash(filepath.Clean(value))
		parts := strings.Split(clean, "/")
		if value == "" || filepath.IsAbs(value) || slices.Contains(parts, "..") || slices.Contains(parts, ".git") || clean == "." {
			return nil, fmt.Errorf("scope must contain repository-relative file paths: %q", value)
		}
		for current := clean; current != "."; current = filepath.ToSlash(filepath.Dir(current)) {
			info, err := os.Lstat(current)
			if err == nil && (info.Mode()&os.ModeSymlink != 0 || (current == clean && info.IsDir())) {
				return nil, fmt.Errorf("scope must name regular files, not directories or symlinks: %q", value)
			}
		}
		out = append(out, clean)
	}
	sort.Strings(out)
	return slices.Compact(out), nil
}

// timeoutSeconds picks the first set value: the task's, then the mode's, then the default.
func timeoutSeconds(values ...int) (int, error) {
	value := 0
	for _, candidate := range values {
		if value == 0 {
			value = candidate
		}
	}
	if value == 0 {
		return timeoutDefault, nil
	}
	if value < timeoutMin || value > timeoutMax {
		return 0, fmt.Errorf("timeouts are %d..%d seconds", timeoutMin, timeoutMax)
	}
	return value, nil
}

// Resolved is a validated plan with its request and mode.
type Resolved struct {
	Plan     *Plan
	Request  Request
	Mode     string
	Decision Decision
	Config   *config.Config
}

func (r *Resolved) ModeConfig() config.Mode { return r.Config.Modes[r.Mode] }

// Resolve validates the plan and decides its mode. A flag in the request always wins.
func Resolve(cfg *config.Config, plan *Plan) (*Resolved, error) {
	request, err := ParseRequest(plan.Request)
	if err != nil {
		return nil, err
	}
	if slices.Contains([]string{"status", "stop", "help"}, request.Action) || request.Preview {
		return nil, errors.New("this request starts no work")
	}
	resolved := &Resolved{Plan: plan, Request: request, Mode: request.Mode, Config: cfg}
	if strings.TrimSpace(plan.Brief) == "" {
		return nil, errors.New("brief is required: the accepted requirements, invariants and acceptance criteria")
	}
	if len(plan.Streams) == 0 {
		return nil, errors.New("streams are required: at least one named stream with its files")
	}
	names, owner := map[string]bool{}, map[string]string{}
	for index := range plan.Streams {
		stream := &plan.Streams[index]
		if stream.Name == "" || names[stream.Name] {
			return nil, fmt.Errorf("stream names must be unique and nonempty: %q", stream.Name)
		}
		names[stream.Name] = true
		if stream.Domain != "backend" && stream.Domain != "frontend" {
			return nil, fmt.Errorf("stream %q: domain must be backend or frontend", stream.Name)
		}
		if stream.Files, err = cleanPaths(stream.Files); err != nil {
			return nil, err
		}
		if len(stream.Files) == 0 {
			return nil, fmt.Errorf("stream %q owns no files", stream.Name)
		}
		for _, file := range stream.Files {
			if other, taken := owner[file]; taken {
				return nil, fmt.Errorf("%s is owned by both %q and %q; streams must be disjoint", file, other, stream.Name)
			}
			owner[file] = stream.Name
		}
	}
	if _, err := waves(plan.Streams); err != nil {
		return nil, err
	}
	if plan.Context, err = cleanPaths(plan.Context); err != nil {
		return nil, err
	}
	for _, timeout := range []int{plan.WriterTimeoutS, plan.ReviewerTimeoutS} {
		if _, err := timeoutSeconds(timeout); err != nil {
			return nil, err
		}
	}
	if err := validateChecks(plan.Checks); err != nil {
		return nil, err
	}
	if err := resolved.decideMode(); err != nil {
		return nil, err
	}
	ceiling := resolved.ModeConfig().EscalateTo
	for _, stream := range plan.Streams {
		if stream.Effort != "" && (!slices.Contains(config.Efforts, stream.Effort) ||
			slices.Index(config.Efforts, stream.Effort) > slices.Index(config.Efforts, ceiling)) {
			return nil, fmt.Errorf("stream %q: effort %q is above what %s allows (%s)", stream.Name, stream.Effort, resolved.Mode, ceiling)
		}
	}
	return resolved, resolved.validateUI()
}

func (r *Resolved) decideMode() error {
	plan := r.Plan
	if r.Request.Action == "research" || r.Request.Action == "verify" {
		r.Mode, r.Decision = "medium", Decision{"medium", "read-only", []string{"read-only consultation"}}
		return nil
	}
	if r.Mode != "" {
		r.Decision = Decision{r.Mode, "flag", []string{"selected explicitly in the request"}}
		return nil
	}
	a := plan.Assessment
	if a == nil || strings.TrimSpace(a.Evidence) == "" || !slices.Contains([]string{"trivial", "ordinary", "complex"}, a.Size) {
		return errors.New("without a mode flag the plan needs assessment {size: trivial|ordinary|complex, risks, irreversible, uncertainty, evidence}")
	}
	if a.Uncertainty != "" && a.Uncertainty != "low" && a.Uncertainty != "material" {
		return errors.New("assessment.uncertainty is low or material")
	}
	for _, risk := range a.Risks {
		if !slices.Contains(Risks, risk) {
			return fmt.Errorf("unknown risk %q; named risks are %s", risk, strings.Join(Risks, ", "))
		}
	}
	r.Decision = decide(a, len(plan.Files()), len(plan.Streams))
	r.Mode = r.Decision.Mode
	return nil
}

func (r *Resolved) validateUI() error {
	plan := r.Plan
	visible := false
	for _, file := range plan.Files() {
		visible = visible || slices.Contains(uiSuffixes, filepath.Ext(file))
	}
	writes := r.Request.Action == "implement" || r.Request.Action == "fix"
	if plan.UI == nil {
		if visible && writes {
			return errors.New("UI files are in scope: provide ui {summary, criteria, preserve}, {design, acceptance} or {skip}")
		}
	} else {
		ui := plan.UI
		kinds := 0
		for _, set := range []bool{ui.Summary != "", ui.Design != "", ui.Skip != ""} {
			if set {
				kinds++
			}
		}
		switch {
		case kinds != 1:
			return errors.New("ui takes exactly one of a local contract (summary), a design or a skip")
		case ui.Summary != "" && (len(ui.Criteria) == 0 || len(ui.Preserve) == 0):
			return errors.New("a local ui contract needs criteria and preserve")
		case ui.Design != "" && strings.TrimSpace(ui.Acceptance) == "":
			return errors.New("record why the lead accepted the UI design")
		}
	}
	if plan.Visual != nil && plan.Visual.Enabled {
		if plan.UI == nil || plan.UI.Skip != "" {
			return errors.New("browser verification needs an accepted ui contract")
		}
		return validateScenarios(plan.Visual.Scenarios)
	}
	return nil
}

func validateScenarios(scenarios []Scenario) error {
	if len(scenarios) == 0 {
		return errors.New("browser verification needs a bounded scenario matrix")
	}
	ids := map[string]bool{}
	for _, row := range scenarios {
		for _, field := range []string{row.ID, row.Screen, row.State, row.Role, row.Expected} {
			if strings.TrimSpace(field) == "" {
				return errors.New("each scenario needs id, screen, state, viewport, role and expected")
			}
		}
		if ids[row.ID] || !viewport.MatchString(row.Viewport) {
			return errors.New("scenario ids must be unique and viewports WIDTHxHEIGHT")
		}
		ids[row.ID] = true
	}
	return nil
}

func validateChecks(checks []Check) error {
	seen := map[string]bool{}
	for _, check := range checks {
		key := strings.Join(check.Argv, "\x00")
		if len(check.Argv) == 0 || seen[key] {
			return errors.New("checks need a nonempty, unique argv")
		}
		seen[key] = true
		for _, pattern := range check.Inputs {
			parts := strings.Split(filepath.ToSlash(pattern), "/")
			if pattern == "" || filepath.IsAbs(pattern) || slices.Contains(parts, "..") || slices.Contains(parts, ".git") {
				return fmt.Errorf("check inputs are repository-relative globs outside .git: %q", pattern)
			}
		}
		if check.TimeoutS < 0 {
			return errors.New("check timeout_s must be positive")
		}
	}
	return nil
}

// waves orders streams so that each wave only depends on earlier waves.
func waves(streams []Stream) ([][]Stream, error) {
	placed, out := map[string]bool{}, [][]Stream{}
	known := map[string]bool{}
	for _, stream := range streams {
		known[stream.Name] = true
	}
	for len(placed) < len(streams) {
		var wave []Stream
		for _, stream := range streams {
			ready := !placed[stream.Name]
			for _, dependency := range stream.After {
				if !known[dependency] {
					return nil, fmt.Errorf("stream %q waits for unknown stream %q", stream.Name, dependency)
				}
				ready = ready && placed[dependency]
			}
			if ready {
				wave = append(wave, stream)
			}
		}
		if len(wave) == 0 {
			return nil, errors.New("streams wait for each other in a cycle")
		}
		for _, stream := range wave {
			placed[stream.Name] = true
		}
		out = append(out, wave)
	}
	return out, nil
}

// WriterRoute is the model that writes a stream in the mode. A stream may ask for more effort than the base;
// boost adds the steps a stalled correction earned. Neither goes above the mode's ceiling.
func (r *Resolved) WriterRoute(stream Stream, boost int) config.Route {
	mode := r.ModeConfig()
	route := r.Config.MustRoute(mode.Backend)
	if stream.Domain == "frontend" {
		route = r.Config.MustRoute(mode.Frontend)
	}
	if stream.Effort != "" && slices.Index(config.Efforts, stream.Effort) > slices.Index(config.Efforts, route.Effort) {
		route.Effort = stream.Effort
	}
	route.Effort = config.Raise(route.Effort, boost, mode.EscalateTo)
	return route
}

// Parallelism is how many model processes may run at once.
func (r *Resolved) Parallelism() (int, error) {
	capacity := resourceCapacity(r.Config)
	if capacity < 1 {
		return 0, errors.New("not enough free CPU or memory for one worker; nothing started")
	}
	if r.Request.Parallel > 0 {
		if r.Request.Parallel > capacity {
			return 0, fmt.Errorf("-%d exceeds the free CPU/memory (%d workers); lower it explicitly", r.Request.Parallel, capacity)
		}
		return r.Request.Parallel, nil
	}
	return min(r.Config.MaxParallel, capacity), nil
}
