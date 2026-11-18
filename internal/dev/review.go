package dev

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

var findingSchema = object(map[string]any{"id": text, "severity": enum(config.Severities...), "confidence": enum("high", "medium", "low"),
	"category": text, "path": text, "line": map[string]any{"type": "integer"}, "evidence": text, "impact": text, "remedy": text,
	"preserve": text, "boundary_break": boolean})

var reviewSchema = object(map[string]any{"reports": list(object(map[string]any{"axis": enum(config.Axes...),
	"status": enum("PASS", "NEEDS_FIX", "BLOCKED"), "findings": list(findingSchema), "checked": list(text),
	"resolutions": list(object(map[string]any{"id": text, "resolved": boolean, "evidence": text}))}))})

// Findings returns every finding ever reported for the present position, latest version of each.
func (g *Gate) Findings() []Finding {
	seen, order := map[string]Finding{}, []string{}
	add := func(finding Finding) {
		if _, known := seen[finding.ID]; !known {
			order = append(order, finding.ID)
		}
		seen[finding.ID] = finding
	}
	for _, review := range g.Reviews {
		for _, lane := range review.Lanes {
			for _, report := range lane.Reports {
				for _, finding := range report.Findings {
					add(finding)
				}
			}
		}
	}
	for _, run := range g.VisualRuns {
		for _, finding := range run.Findings {
			add(finding)
		}
	}
	for _, demand := range g.Demands {
		add(demand)
	}
	out := make([]Finding, len(order))
	for index, id := range order {
		out[index] = seen[id]
	}
	return out
}

// blocking reports whether a finding keeps the present position from being accepted.
func (g *Gate) blocking(cfg *config.Config, finding Finding) bool {
	if _, resolved := g.Resolutions[finding.ID]; resolved {
		return false
	}
	switch g.Dispositions[finding.ID].Decision {
	case "rejected":
		return false
	case "accepted", "needs-approval":
		return true
	}
	return g.blocks(cfg, finding)
}

// blocks is the rule that sends a finding to its author without waiting for the lead: a severity the mode
// blocks on, held with at least medium confidence, or a broken boundary.
func (g *Gate) blocks(cfg *config.Config, finding Finding) bool {
	if finding.Confidence == "low" || finding.Severity == "P3" {
		return false
	}
	return finding.BoundaryBreak || g.process(cfg).Blocks(finding.Severity)
}

// isNote reports whether an open finding is a note: listed for the lead, never sent to a writer by itself.
// whyBlocks says in one line why a finding went to its author without a ruling.
func (g *Gate) whyBlocks(cfg *config.Config, finding Finding) string {
	if finding.BoundaryBreak && !g.process(cfg).Blocks(finding.Severity) {
		return fmt.Sprintf("%s with %s confidence breaks a boundary, which blocks at any severity", finding.Severity, finding.Confidence)
	}
	return fmt.Sprintf("%s with %s confidence blocks in %s mode", finding.Severity, finding.Confidence, g.Mode)
}

func (g *Gate) isNote(cfg *config.Config, finding Finding) bool {
	_, judged := g.Dispositions[finding.ID]
	return !judged && !g.blocks(cfg, finding)
}

// accepted are the findings writers have to resolve: accepted by the blocking rule or by the lead, and not
// yet confirmed fixed.
func (g *Gate) accepted() []Finding {
	out := []Finding{} // packets must carry [] rather than null
	for _, finding := range g.Findings() {
		if _, resolved := g.Resolutions[finding.ID]; !resolved && g.Dispositions[finding.ID].Decision == "accepted" {
			out = append(out, finding)
		}
	}
	return out
}

// undecided are the blocking findings only the lead can settle: the author disputed one and the reviewer
// kept it, it waits for an approval, or the lead deferred a finding that still blocks.
func (g *Gate) undecided(cfg *config.Config) []Finding {
	var out []Finding
	for _, finding := range g.Findings() {
		if g.blocking(cfg, finding) && g.Dispositions[finding.ID].Decision != "accepted" {
			out = append(out, finding)
		}
	}
	return out
}

type laneSpec struct {
	Axes  []string
	Scope string
	Files []string
	Route config.Route
	// Conversation says the lane is shown the digest of the lead's conversation; Unshown says why a lane the
	// configuration gives it to is not.
	Conversation bool
	Unshown      string
}

func (g *Gate) reviewPolicy(cfg *config.Config) string {
	return digest(map[string]any{"lanes": g.process(cfg).Review, "excluded": cfg.ReviewersExcluded, "models": cfg.Models})
}

func (g *Gate) independentReview(cfg *config.Config, review Review) bool {
	if review.Policy != g.reviewPolicy(cfg) {
		return false
	}
	for _, lane := range review.Lanes {
		if lane.Route.Alias == "lead" && len(g.process(cfg).Review) == 0 {
			continue
		}
		if lane.Route.Model == "" || slices.Contains(cfg.ReviewersExcluded, lane.Route.Alias) {
			return false
		}
	}
	return len(review.Lanes) > 0
}

func (g *Gate) reviewDiversity() map[string]any {
	if len(g.Reviews) == 0 {
		return nil
	}
	var models, providers []string
	for _, lane := range g.Reviews[len(g.Reviews)-1].Lanes {
		if lane.Route.Model != "" {
			models = append(models, lane.Route.Model)
			providers = append(providers, lane.Route.Provider)
		}
	}
	sort.Strings(models)
	sort.Strings(providers)
	models, providers = slices.Compact(models), slices.Compact(providers)
	result := map[string]any{"models": models, "providers": providers}
	if len(models) > 0 && len(providers) < 2 {
		result["note"] = "The completed review used one provider; it did not provide cross-provider validation."
	}
	return result
}

// reviewerSkip says why a route may not judge the code: the configuration keeps it from review. "" when it may.
// A reviewer runs on the model configured for it, also when that model wrote the code: which model reviews is
// the user's choice.
func reviewerSkip(cfg *config.Config) func(config.Route) string {
	return func(route config.Route) string {
		if slices.Contains(cfg.ReviewersExcluded, route.Alias) {
			return route.Alias + " is kept from review by reviewers_excluded"
		}
		return ""
	}
}

// lanes are the reviewer processes of the mode.

func (g *Gate) lanes(cfg *config.Config, subs *substitutions) ([]laneSpec, func(config.Route) string, error) {
	skip := reviewerSkip(cfg)
	var out []laneSpec
	for _, lane := range g.process(cfg).Review {
		route := cfg.MustRoute(lane.Route)
		if reason := skip(route); reason != "" {
			replaced := false
			for _, alternate := range cfg.Alternates(route) {
				if skip(alternate) == "" {
					subs.list = append(subs.list, provider.Substitution{Wanted: route.String(), Used: alternate.String(), Reason: reason})
					route, replaced = alternate, true
					break
				}
			}
			if !replaced {
				return nil, nil, fmt.Errorf("%s is kept from review by reviewers_excluded and no fallback of it may review", lane.Route)
			}
		}
		// The user's words go to a reader the configuration names for them, and only to a model of the lead's own
		// provider: they do not cross to another vendor unasked.
		spec := laneSpec{Axes: lane.Axes, Scope: "task", Files: g.scope(), Route: route}
		if lane.Conversation {
			spec.Unshown = unshown(route)
			spec.Conversation = spec.Unshown == ""
		}
		out = append(out, spec)
	}
	// A path whose cost grows with data is also judged for that cost, by the lane that judges correctness.
	if g.load() != "" && !slices.ContainsFunc(out, func(lane laneSpec) bool { return slices.Contains(lane.Axes, "performance") }) {
		for index := range out {
			if slices.Contains(out[index].Axes, "correctness") {
				out[index].Axes = append(slices.Clone(out[index].Axes), "performance")
				break
			}
		}
	}
	// The design is judged by eye by a model that did not write the code: the first lane takes it along.
	if len(g.designs()) > 0 && len(out) > 0 {
		out[0].Axes = append(slices.Clone(out[0].Axes), "design")
	}
	// An interface the change adds is judged for the jobs it is for, wherever its code is written.
	if g.usage() != nil && g.writesInterface() && len(out) > 0 && !slices.ContainsFunc(out, func(lane laneSpec) bool { return slices.Contains(lane.Axes, "usability") }) {
		out[0].Axes = append(slices.Clone(out[0].Axes), "usability")
	}
	return out, skip, nil
}

// usage is the jobs of an interface the change adds, or nil.
func (g *Gate) usage() *Usage {
	if g.Plan.UI == nil || g.Plan.UI.Skip != "" {
		return nil
	}
	return g.Plan.UI.Usage
}

// writesInterface says whether the present position writes interface code: a frontend stream, or a file of
// an interface kind in its scope.
func (g *Gate) writesInterface() bool {
	for _, stream := range g.streams() {
		if stream.Domain == "frontend" || slices.ContainsFunc(stream.Files, func(file string) bool { return slices.Contains(uiSuffixes, filepath.Ext(file)) }) {
			return true
		}
	}
	return false
}

// LeadReview is the lead's own reading of the patch, used in light mode instead of a reviewer process.
type LeadReview struct {
	Summary  string         `json:"summary"`
	Findings []Finding      `json:"findings"`
	Quality  *QualityReport `json:"quality,omitempty"`
}

func (g *Gate) writerNotes() []map[string]any {
	var out []map[string]any
	for name, writer := range g.latest() {
		summary := writer.Summary
		if runes := []rune(summary); len(runes) > 700 && writer.Session != "" {
			summary = string(runes[:700]) + "… (the full report is in " + writer.Dir + ")"
		}
		note := map[string]any{"stream": name, "summary": summary, "deviations": writer.Deviations}
		out = append(out, note)
	}
	sort.Slice(out, func(a, b int) bool { return out[a]["stream"].(string) < out[b]["stream"].(string) })
	return out
}

func (g *Gate) decisions() []map[string]any {
	var out []map[string]any
	for _, finding := range g.Findings() {
		disposition, judged := g.Dispositions[finding.ID]
		resolution, resolved := g.Resolutions[finding.ID]
		if (judged && !disposition.Auto) || resolved {
			out = append(out, map[string]any{"finding": finding, "disposition": disposition, "resolution": resolution})
		}
	}
	return out
}

// relevant are the accepted findings a lane has to verify: those of its own axes.
func (g *Gate) relevant(spec laneSpec, accepted []Finding) []Finding {
	out := []Finding{}
	for _, finding := range accepted {
		if slices.Contains(spec.Axes, reviewAxis(finding)) {
			out = append(out, finding)
		}
	}
	return out
}

// reviewAxis is the axis whose reviewers verify a finding. A browser finding excluded from the browser, and
// work the lead demanded from an unmet criterion, are verified by correctness.
func reviewAxis(finding Finding) string {
	if finding.Axis == "visual" || finding.Axis == "criterion" {
		return "correctness"
	}
	return finding.Axis
}

func (g *Gate) runLane(cfg *config.Config, spec laneSpec, index int, dir string, packet map[string]any, key, contract string,
	accepted []Finding, known map[string]Finding, timeout time.Duration, subs *substitutions, skip func(config.Route) string, quality *qualityInputs) (LaneRun, error) {
	relevant := g.relevant(spec, accepted)
	var recheck []string
	for _, finding := range relevant {
		recheck = append(recheck, finding.ID)
	}
	cacheKey := digest(map[string]any{"content": key, "contract": contract, "axes": spec.Axes, "scope": spec.Scope, "files": spec.Files,
		"route": spec.Route.String(), "recheck": recheck, "decisions": g.Dispositions, "disputes": g.Disputes, "quality": quality.fingerprint()})
	cache := filepath.Join(g.dir(), "review-cache", cacheKey+".json")
	lane := LaneRun{}
	if raw, err := os.ReadFile(cache); packet["refresh"] != true && err == nil && json.Unmarshal(raw, &lane) == nil && skip(lane.Route) == "" {
		return lane, nil // a finished lane of a partly failed review is kept
	}
	scoped := map[string]any{"axes": spec.Axes, "scope": spec.Scope, "scope_files": spec.Files}
	for name, value := range packet {
		scoped[name] = value
	}
	if quality != nil {
		scoped["quality"] = quality
	}
	scoped["accepted_findings"] = relevant
	if spec.Conversation {
		if digest := conversation(dir, cmp.Or(g.Opened, g.CreatedAt)); digest != "" {
			scoped["conversation"] = digest
		}
	}
	if disputes := g.openDisputes(relevant); len(disputes) > 0 {
		scoped["disputes"] = disputes
	}
	path := filepath.Join(dir, fmt.Sprintf("lane-%d", index), "packet.json")
	if err := writePacket(path, scoped); err != nil {
		return lane, err
	}
	lane = LaneRun{Axes: spec.Axes, Scope: spec.Scope, Files: spec.Files, Route: spec.Route, Dir: filepath.Join(dir, fmt.Sprintf("lane-%d", index), "run")}
	prompt := assets.Prompt("review") + "\nMode: " + g.Mode + "; your axes: " + strings.Join(spec.Axes, ", ") + ".\nRead " + path + ".\n"
	// The reviewer that raised a finding verifies its fix in its own session: it only needs the delta.
	route := spec.Route
	if previous := g.previousLane(cfg, spec); previous != nil {
		lane.Dir, lane.Session = previous.Dir, previous.Session
		route = previous.Route
		route.Effort, route.SubstitutedFrom = config.Lower(spec.Route.Effort), "" // a recheck reads the delta, not the whole change
		prompt = "The task you reviewed was corrected. A fresh packet is at " + path + ": start from delta_patch, " +
			"resolve or keep every finding in accepted_findings, answer every entry of disputes, report new defects of the correction, " +
			"and return the same JSON schema.\n"
	}
	tools, toolDirs, toolNote := workerTools(cfg, "reviewer")
	_, designDirs := g.designReview()
	if quality != nil && quality.Browser != nil {
		designDirs = append(designDirs, quality.Browser.Dir)
	}
	schema := reviewSchema
	if quality != nil {
		schema = object(map[string]any{"reports": reviewSchema["properties"].(map[string]any)["reports"], "quality": qualitySchema})
		prompt += "\nAlso assess every requirement and rule in quality, using the current evidence. Return quality {requirements, rules}; " +
			"each requirement needs id, status (verified/partial/missing), cause when it is not verified (code/proof/plan), reason, evidence [{kind (code/check/browser), ref, detail}]. " +
			"Each rule needs id, status (pass/block/not_applicable/unavailable), evidence. Reassess all criteria even on a correction.\n"
	}
	call := provider.Call{Route: route, Prompt: prompt + toolNote, Dir: lane.Dir, Schema: schema, MCP: tools, Writable: toolDirs,
		Timeout: timeout, Persist: true, Resume: lane.Session,
		ReadDirs: append(append(append([]string{dir, filepath.Join(g.dir(), "checks")}, packDirs(g.ContextPack)...), designDirs...), g.attachmentDirs()...)}
	for attempt := 0; ; attempt++ {
		result, used, err := subs.invoke(cfg, call, skip)
		if err != nil {
			return lane, err
		}
		lane.Route, lane.Session = used, result.Session
		if result.Dir != "" {
			lane.Dir = result.Dir
		}
		var answer struct {
			Reports []Report       `json:"reports"`
			Quality *QualityReport `json:"quality"`
		}
		var problems []string
		if err := decode(result.Answer, &answer); err != nil {
			problems = append(problems, "the answer is not the JSON the schema asks for: "+err.Error())
		} else {
			for _, report := range answer.Reports {
				if report.Status == "BLOCKED" {
					return lane, fmt.Errorf("the %s reviewer could not review: %s", report.Axis, strings.Join(report.Checked, "; "))
				}
			}
			if lane.Reports, err = g.validateReports(answer.Reports, spec.Axes, spec.Files, known, answer.Quality.unmet()); err != nil {
				problems = append(problems, err.Error())
			}
			if err := g.validateQuality(answer.Quality, quality); err != nil {
				problems = append(problems, err.Error())
			}
		}
		if len(problems) == 0 {
			lane.Quality = answer.Quality
			break
		}
		if attempt >= reportCorrections || result.Session == "" {
			return lane, errors.New(strings.Join(problems, "; "))
		}
		// A report the host cannot use goes back to the reviewer that wrote it. It has read the change:
		// correcting a report costs a fraction of reviewing again, and a discarded review costs all of it.
		call.Route, call.Resume, call.Dir = used, result.Session, lane.Dir
		call.Route.SubstitutedFrom = ""
		call.Prompt = "Your report on the packet at " + path + " cannot be used:\n- " + strings.Join(problems, "\n- ") +
			"\nCorrect exactly that and return the complete JSON in the same schema. Do not review the change again.\n"
	}
	if err := ops.Checkpoint(); err != nil {
		return lane, err
	}
	_ = writePacket(cache, lane)
	return lane, nil
}

func (g *Gate) previousLane(cfg *config.Config, spec laneSpec) *LaneRun {
	if len(g.Reviews) == 0 {
		return nil
	}
	lanes := g.Reviews[len(g.Reviews)-1].Lanes
	for index := range lanes {
		lane := &lanes[index]
		if slices.Equal(lane.Axes, spec.Axes) && lane.Scope == spec.Scope && lane.Session != "" &&
			permittedContinuation(cfg, spec.Route, lane.Route) && !slices.Contains(cfg.ReviewersExcluded, lane.Route.Alias) {
			return lane
		}
	}
	return nil
}

// openDisputes are the author's objections to the given findings that their reviewer has not answered yet.
func (g *Gate) openDisputes(findings []Finding) []map[string]any {
	var out []map[string]any
	for _, finding := range findings {
		if dispute, disputed := g.Disputes[finding.ID]; disputed && !dispute.Upheld {
			out = append(out, map[string]any{"id": finding.ID, "author_evidence": dispute.Evidence, "stream": dispute.Stream})
		}
	}
	return out
}

// reportCorrections is how many times a reviewer is asked, in its own session, to correct a report the host
// cannot use before its lane fails.
const reportCorrections = 2

// validateReports rejects a report that does not cover its axes or cannot be acted on, and gives findings
// stable ids. unmet says that the same reviewer judged a criterion not satisfied: that explains an axis that
// needs a fix and names no defect in the code.
func (g *Gate) validateReports(reports []Report, axes, scope []string, known map[string]Finding, unmet bool) ([]Report, error) {
	var problems []string
	add := func(format string, values ...any) { problems = append(problems, fmt.Sprintf(format, values...)) }
	var got []string
	for _, report := range reports {
		got = append(got, report.Axis)
	}
	want := slices.Clone(axes)
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		add("the review must report each of its axes exactly once: want %v, got %v", want, got)
	}
	owned := compilePatterns(scope)
	seen := map[string]bool{}
	for index := range reports {
		report := &reports[index]
		if report.Status == "BLOCKED" {
			return nil, fmt.Errorf("the %s reviewer could not review: %s", report.Axis, strings.Join(report.Checked, "; "))
		}
		if (report.Status != "PASS" && report.Status != "NEEDS_FIX") || len(report.Checked) == 0 {
			add("the %s report needs a status (PASS or NEEDS_FIX) and what it checked", report.Axis)
		}
		if report.Status == "NEEDS_FIX" && len(report.Findings) == 0 && !unmet {
			add("the %s report says NEEDS_FIX and lists no finding: name the defect as a finding with its path, line, "+
				"evidence, impact and remedy, or return PASS", report.Axis)
		}
		report.ScopeFiles = scope
		for position := range report.Findings {
			finding := &report.Findings[position]
			finding.Axis = report.Axis
			where := fmt.Sprintf("the %s finding at %s:%d", report.Axis, finding.Path, finding.Line)
			var wrong []string
			if !slices.Contains(config.Severities, finding.Severity) {
				wrong = append(wrong, "severity is one of "+strings.Join(config.Severities, ", "))
			}
			if !slices.Contains([]string{"high", "medium", "low"}, finding.Confidence) {
				wrong = append(wrong, "confidence is high, medium or low")
			}
			if !matchAny(owned, finding.Path) {
				wrong = append(wrong, "path must be a file of the change's scope")
			}
			if finding.Line < 1 {
				wrong = append(wrong, "line is one line number, 1 or more")
			}
			for name, value := range map[string]string{"evidence": finding.Evidence, "impact": finding.Impact, "remedy": finding.Remedy} {
				if strings.TrimSpace(value) == "" {
					wrong = append(wrong, name+" is empty")
				}
			}
			if len(wrong) > 0 {
				sort.Strings(wrong)
				add("%s: %s", where, strings.Join(wrong, "; "))
				continue
			}
			if previous, ok := known[finding.ID]; !ok || previous.Axis != report.Axis {
				finding.ID = g.ID[:8] + "-" + digest([]string{report.Axis, finding.Path, finding.Category, finding.Evidence})[:16]
				// A later pass that raises an earlier note again under a new id joins it: the same axis, path and line.
				if earlier := sameSpot(known, report.Axis, finding.Path, finding.Line); earlier != "" && !seen[earlier] {
					finding.ID = earlier
				}
			}
			if seen[finding.ID] {
				add("%s is reported twice", where)
			}
			seen[finding.ID] = true
		}
	}
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	return reports, nil
}

// sameSpot is the earliest known finding of an axis at one path and line, or "".
func sameSpot(known map[string]Finding, axis, path string, line int) string {
	match := ""
	for id, finding := range known {
		if finding.Axis == axis && finding.Path == path && finding.Line == line && (match == "" || id < match) {
			match = id
		}
	}
	return match
}

// openNotes are the notes of the present position nobody acted on: a later pass is shown them so it does not
// raise them again under new ids.
func (g *Gate) openNotes(cfg *config.Config) []map[string]any {
	_, _, notes := g.tally(cfg)
	out := []map[string]any{}
	for _, finding := range notes {
		out = append(out, map[string]any{"id": finding.ID, "axis": finding.Axis, "severity": finding.Severity, "path": finding.Path,
			"line": finding.Line, "evidence": clipText(oneLine(finding.Evidence), 300)})
	}
	return out
}

func (g *Gate) leadLane(review LeadReview, known map[string]Finding) (LaneRun, error) {
	if strings.TrimSpace(review.Summary) == "" {
		return LaneRun{}, errors.New("review needs a summary of what was read and verified")
	}
	status := "PASS"
	if len(review.Findings) > 0 {
		status = "NEEDS_FIX"
	}
	var resolutions []Resolution
	present := map[string]bool{}
	for _, finding := range review.Findings {
		present[finding.ID] = true
	}
	for _, finding := range g.accepted() {
		if !present[finding.ID] {
			resolutions = append(resolutions, Resolution{ID: finding.ID, Resolved: true, Evidence: review.Summary})
		}
	}
	scope := g.scope()
	reports, err := g.validateReports([]Report{{Axis: "correctness", Status: status, Findings: review.Findings,
		Checked: []string{review.Summary}, Resolutions: resolutions}}, []string{"correctness"}, scope, known, false)
	return LaneRun{Axes: []string{"correctness"}, Scope: "task", Files: scope, Route: config.Route{Alias: "lead"}, Reports: reports}, err
}

// applyReview records a review and settles what it says about the findings it was asked to verify.
//
// A finding the review reports again is open, whatever anyone voted. A finding every asked lane confirms as
// fixed is resolved. A finding its author disputed is either withdrawn by the reviewer (resolved) or upheld:
// an upheld dispute leaves the blocking rule and waits for the lead. Every other finding that blocks is
// accepted and goes to its author.
func (g *Gate) applyReview(cfg *config.Config, review Review, accepted []Finding) {
	reported := map[string]bool{}
	for _, lane := range review.Lanes {
		for _, report := range lane.Reports {
			for _, finding := range report.Findings {
				reported[finding.ID] = true
			}
		}
	}
	for _, finding := range accepted {
		if finding.Axis == "criterion" {
			continue // work sent from a criterion is done when the criterion is met: settleDemands
		}
		delete(g.Resolutions, finding.ID)
		axis := reviewAxis(finding)
		var votes []string
		unanimous, asked := true, false
		for _, lane := range review.Lanes {
			for _, report := range lane.Reports {
				if report.Axis != axis {
					continue
				}
				asked = true
				position := slices.IndexFunc(report.Resolutions, func(vote Resolution) bool { return vote.ID == finding.ID })
				if position < 0 || !report.Resolutions[position].Resolved || strings.TrimSpace(report.Resolutions[position].Evidence) == "" {
					unanimous = false
					continue
				}
				votes = append(votes, report.Resolutions[position].Evidence)
			}
		}
		if asked && unanimous && !reported[finding.ID] {
			prefix := ""
			if finding.Axis == "visual" {
				prefix = "Verified in code only; the browser was not used. "
			}
			g.Resolutions[finding.ID] = prefix + strings.Join(votes, "; ")
			delete(g.Disputes, finding.ID)
			continue
		}
		if dispute, disputed := g.Disputes[finding.ID]; disputed && asked && !dispute.Upheld {
			// The reviewer read the author's evidence and did not withdraw the finding.
			dispute.Upheld = true
			g.Disputes[finding.ID] = dispute
			if g.Dispositions[finding.ID].Auto {
				delete(g.Dispositions, finding.ID)
			} else {
				// The lead had accepted the finding and its reviewer has now answered the author: the writers act
				// on that answer, and code the author left unchanged while it objected is not a stalled correction.
				g.Ruled = true
			}
		}
	}
	g.Reviews = append(g.Reviews, review)
	for _, lane := range review.Lanes {
		for _, report := range lane.Reports {
			for _, finding := range report.Findings {
				delete(g.Resolutions, finding.ID) // reported again: it is not resolved, whatever anyone voted
				if decision := g.Dispositions[finding.ID].Decision; decision == "rejected" || decision == "deferred" {
					delete(g.Dispositions, finding.ID) // new evidence reopens an earlier judgement
				}
			}
		}
	}
	g.admitFindings(cfg, review.Lanes)
	g.settleDemands()
	g.admitCriteria()
}

// admitFindings applies the blocking rule: a blocking finding nobody judged yet goes to its author. A finding
// the lead wrote in its own review is accepted by the act of writing it. An upheld dispute is the exception;
// it is the lead's to settle.
func (g *Gate) admitFindings(cfg *config.Config, lanes []LaneRun) {
	for _, lane := range lanes {
		for _, report := range lane.Reports {
			for _, finding := range report.Findings {
				if _, judged := g.Dispositions[finding.ID]; judged || g.Disputes[finding.ID].Upheld {
					continue
				}
				switch {
				case lane.Route.Alias == "lead":
					g.Dispositions[finding.ID] = Disposition{ID: finding.ID, Decision: "accepted", Evidence: "found by the lead's own review"}
				case g.blocks(cfg, finding):
					g.Dispositions[finding.ID] = Disposition{ID: finding.ID, Decision: "accepted", Auto: true, Evidence: g.whyBlocks(cfg, finding)}
				}
			}
		}
	}
}
