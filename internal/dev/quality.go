package dev

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/knaus94/agentgate/internal/config"
)

// Proof names evidence the gate can locate; the reviewer judges whether it proves the claim.
type Proof struct {
	Kind   string `json:"kind"` // code, check or browser
	Ref    string `json:"ref"`  // file:line, check id or scenario id
	Detail string `json:"detail"`
}

type RequirementResult struct {
	ID       string  `json:"id"`
	Status   string  `json:"status"` // verified, partial or missing
	Reason   string  `json:"reason"`
	Evidence []Proof `json:"evidence"`
}

type RuleResult struct {
	ID       string `json:"id"`
	Status   string `json:"status"` // pass, block, not_applicable or unavailable
	Evidence string `json:"evidence"`
}

type QualityReport struct {
	Requirements []RequirementResult `json:"requirements"`
	Rules        []RuleResult        `json:"rules"`
}

type reviewRule struct {
	ID           string            `json:"id"`
	Mode         string            `json:"mode"`
	Files        []string          `json:"files"`
	Instructions map[string]string `json:"instructions"` // snapshot of each declared instruction file
	Unavailable  string            `json:"unavailable,omitempty"`
	Required     bool              `json:"required,omitempty"` // applicable by construction; cannot be waived as not_applicable
	Sources      []string          `json:"sources,omitempty"`
}

type qualityCheck struct {
	Kind    string        `json:"kind"`
	Receipt *CheckReceipt `json:"receipt"`
}

type qualityInputs struct {
	Requirements []Requirement           `json:"requirements"`
	Rules        []reviewRule            `json:"rules"`
	Checks       map[string]qualityCheck `json:"checks"`
	Browser      *VisualRun              `json:"browser"`
}

var qualitySchema = object(map[string]any{
	"requirements": items(map[string]any{"id": text, "status": enum("verified", "partial", "missing"), "reason": text,
		"evidence": items(map[string]any{"kind": enum("code", "check", "browser"), "ref": text, "detail": text})}),
	"rules": items(map[string]any{"id": text, "status": enum("pass", "block", "not_applicable", "unavailable"), "evidence": text}),
})

func validateRequirements(requirements []Requirement) error {
	seen := map[string]bool{}
	for _, r := range requirements {
		if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.Text) == "" || seen[r.ID] {
			return fmt.Errorf("requirements need a unique id and a text: %q", r.ID)
		}
		if r.Method != "" && !slices.Contains([]string{"code", "check", "browser"}, r.Method) {
			return fmt.Errorf("requirement %q: method is code, check or browser", r.ID)
		}
		seen[r.ID] = true
	}
	return nil
}

func loadReviewRules(cfg *config.Config, files []string) ([]reviewRule, error) {
	names := make([]string, 0, len(cfg.ReviewRules))
	for name := range cfg.ReviewRules {
		names = append(names, name)
	}
	sort.Strings(names)
	var rules []reviewRule
	var failures []error
	for _, name := range names {
		spec := cfg.ReviewRules[name]
		rule := reviewRule{ID: name, Mode: spec.Mode, Instructions: map[string]string{}, Sources: cfg.ReviewRuleSources[name]}
		if rule.Mode == "" {
			rule.Mode = "blocking"
		}
		for _, file := range files {
			if len(spec.Files) == 0 || slices.ContainsFunc(spec.Files, func(pattern string) bool {
				return globRegexp(filepath.ToSlash(filepath.Clean(pattern))).MatchString(filepath.ToSlash(file))
			}) {
				rule.Files = append(rule.Files, file)
			}
		}
		if len(rule.Files) == 0 {
			continue
		}
		for _, path := range spec.Instructions {
			path = config.ExpandHome(path)
			info, err := os.Stat(path)
			if err == nil && !info.Mode().IsRegular() {
				err = errors.New("not a regular file")
			}
			var raw []byte
			if err == nil {
				raw, err = os.ReadFile(path)
			}
			if err == nil && strings.TrimSpace(string(raw)) == "" {
				err = errors.New("empty instructions")
			}
			if err != nil {
				rule.Unavailable = fmt.Sprintf("review rule %q: %s: %v", name, path, err)
				if rule.Mode == "blocking" {
					failures = append(failures, errors.New(rule.Unavailable))
				}
				break
			}
			rule.Instructions[path] = string(raw)
		}
		rules = append(rules, rule)
	}
	return rules, errors.Join(failures...)
}

// Before discovery the scope is unknown: advertise instruction paths and globs, without loading unrelated
// skills. After discovery the same catalogue describes only the rules actually applied to the plan.
func reviewRuleCatalogue(cfg *config.Config) []map[string]any {
	var names []string
	for name := range cfg.ReviewRules {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []map[string]any{}
	for _, name := range names {
		rule := cfg.ReviewRules[name]
		mode := rule.Mode
		if mode == "" {
			mode = "blocking"
		}
		var paths []string
		for _, path := range rule.Instructions {
			paths = append(paths, config.ExpandHome(path))
		}
		out = append(out, map[string]any{"id": name, "mode": mode, "scope": rule.Files,
			"instructions": paths, "sources": cfg.ReviewRuleSources[name]})
	}
	return out
}

func reviewRuleSummary(cfg *config.Config, rules []reviewRule) []map[string]any {
	out := []map[string]any{}
	for _, info := range reviewRuleCatalogue(cfg) {
		for _, rule := range rules {
			if info["id"] == rule.ID {
				info["files"], info["unavailable"] = rule.Files, rule.Unavailable
				out = append(out, info)
				break
			}
		}
	}
	return out
}

func (g *Gate) qualityInputs(cfg *config.Config) (*qualityInputs, error) {
	rules, err := loadReviewRules(cfg, g.Files)
	if err != nil {
		return nil, err
	}
	rules = append(rules, g.designRules()...)
	if len(g.Plan.Requirements) == 0 && len(rules) == 0 {
		return nil, nil
	}
	input := &qualityInputs{Requirements: g.Plan.Requirements, Rules: rules, Checks: map[string]qualityCheck{}}
	for _, check := range g.Plan.Checks {
		kind := check.Kind
		if kind == "" {
			kind = "command"
		}
		if receipt := g.currentReceipt(&check); receipt != nil {
			copy := *receipt
			copy.Reused = false // reusing the same evidence does not change its meaning
			input.Checks[checkID(check.Argv)] = qualityCheck{Kind: kind, Receipt: &copy}
		}
	}
	if content, _, err := g.contentDigest(); err != nil {
		return nil, err
	} else if g.visualRequired() {
		input.Browser = g.currentVisual(content)
	}
	return input, nil
}

func (q *qualityInputs) fingerprint() string {
	if q == nil {
		return ""
	}
	return digest(q)
}

func (g *Gate) validateQuality(report *QualityReport, input *qualityInputs) error {
	if input == nil {
		if report != nil && (len(report.Requirements) > 0 || len(report.Rules) > 0) {
			return errors.New("quality report supplied for a lane without quality criteria")
		}
		return nil
	}
	if report == nil || len(report.Requirements) != len(input.Requirements) || len(report.Rules) != len(input.Rules) {
		return errors.New("quality report must assess every requirement and review rule exactly once")
	}
	seen := map[string]bool{}
	for _, result := range report.Requirements {
		index := slices.IndexFunc(input.Requirements, func(r Requirement) bool { return r.ID == result.ID })
		if index < 0 || seen[result.ID] || !slices.Contains([]string{"verified", "partial", "missing"}, result.Status) || strings.TrimSpace(result.Reason) == "" {
			return fmt.Errorf("invalid requirement result %q: needs a known unique id, status and reason", result.ID)
		}
		seen[result.ID] = true
		requirement := input.Requirements[index]
		method := requirement.Method == ""
		for _, proof := range result.Evidence {
			if err := g.validateProof(proof, input, result.Status == "verified"); err != nil {
				return fmt.Errorf("requirement %q: %w", result.ID, err)
			}
			method = method || proof.Kind == requirement.Method
		}
		if result.Status == "verified" && (len(result.Evidence) == 0 || !method) {
			return fmt.Errorf("requirement %q needs evidence using its declared method %q", result.ID, requirement.Method)
		}
	}
	seen = map[string]bool{}
	for _, result := range report.Rules {
		index := slices.IndexFunc(input.Rules, func(r reviewRule) bool { return r.ID == result.ID })
		if index < 0 || seen[result.ID] || strings.TrimSpace(result.Evidence) == "" ||
			!slices.Contains([]string{"pass", "block", "not_applicable", "unavailable"}, result.Status) {
			return fmt.Errorf("invalid review rule result %q", result.ID)
		}
		seen[result.ID] = true
		if input.Rules[index].Required && result.Status == "not_applicable" {
			return fmt.Errorf("review rule %q must be assessed; missing evidence is unavailable", result.ID)
		}
		if input.Rules[index].Unavailable != "" && result.Status != "unavailable" {
			return fmt.Errorf("review rule %q is unavailable and cannot be approved", result.ID)
		}
	}
	return nil
}

func (g *Gate) validateProof(proof Proof, input *qualityInputs, verified bool) error {
	if strings.TrimSpace(proof.Detail) == "" {
		return errors.New("evidence needs a detail explaining what it proves")
	}
	switch proof.Kind {
	case "code":
		path, number, ok := strings.Cut(proof.Ref, ":")
		line, err := strconv.Atoi(number)
		if !ok || err != nil || line < 1 || (!slices.Contains(g.Files, path) && !slices.Contains(g.Context, path)) {
			return fmt.Errorf("code evidence %q must name a scoped file:line", proof.Ref)
		}
		raw, err := os.ReadFile(path)
		if err != nil || line > len(strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")) || len(raw) == 0 {
			return fmt.Errorf("code evidence %q cannot be read", proof.Ref)
		}
	case "check":
		check, ok := input.Checks[proof.Ref]
		if !ok || (verified && check.Receipt.ReturnCode != 0) {
			return fmt.Errorf("check evidence %q needs a current successful receipt to verify a requirement", proof.Ref)
		}
		if info, err := os.Stat(check.Receipt.Log); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("check evidence %q has no readable log", proof.Ref)
		}
	case "browser":
		run := input.Browser
		if run == nil || !slices.ContainsFunc(run.Coverage, func(c Coverage) bool {
			info, err := os.Stat(c.Screenshot)
			return c.ScenarioID == proof.Ref && c.Observed && err == nil && info.Mode().IsRegular()
		}) {
			return fmt.Errorf("browser evidence %q needs a current observed scenario", proof.Ref)
		}
	default:
		return fmt.Errorf("unknown evidence kind %q", proof.Kind)
	}
	return nil
}

func qualityProblems(report *QualityReport, input *qualityInputs) []string {
	var problems []string
	for _, result := range report.Requirements {
		if result.Status != "verified" {
			problems = append(problems, result.ID+": "+result.Status+" — "+result.Reason)
		}
	}
	for _, result := range report.Rules {
		index := slices.IndexFunc(input.Rules, func(r reviewRule) bool { return r.ID == result.ID })
		if input.Rules[index].Mode == "blocking" && result.Status != "pass" && result.Status != "not_applicable" {
			problems = append(problems, result.ID+": "+result.Status+" — "+result.Evidence)
		}
	}
	return problems
}

func (g *Gate) qualityStatus(cfg *config.Config) map[string]any {
	input, err := g.qualityInputs(cfg)
	if err != nil {
		return map[string]any{"current": false, "problems": []string{err.Error()}}
	}
	if input == nil {
		if len(g.Reviews) > 0 && g.Reviews[len(g.Reviews)-1].QualityDigest != "" {
			return map[string]any{"current": false, "problems": []string{"quality criteria changed; need a current review"}}
		}
		return nil
	}
	status := map[string]any{"current": false, "requirements": input.Requirements, "checks": input.Checks,
		"problems": []string{"quality criteria need a current review"}}
	if len(g.Reviews) == 0 {
		return status
	}
	last := g.Reviews[len(g.Reviews)-1]
	for _, lane := range last.Lanes {
		if lane.Quality == nil {
			continue
		}
		status["report"] = lane.Quality
		content, _, err := g.contentDigest()
		if err != nil || content != last.Digest || last.Contract != g.contract() || last.QualityDigest != input.fingerprint() {
			return status
		}
		if err := g.validateQuality(lane.Quality, input); err != nil {
			status["problems"] = []string{err.Error()}
			return status
		}
		status["current"], status["problems"] = true, qualityProblems(lane.Quality, input)
		return status
	}
	return status
}
