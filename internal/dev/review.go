package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

// Change points a reviewer at readable before/after/diff files instead of embedding source in the prompt.
type Change struct {
	Path      string `json:"path"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Diff      string `json:"diff"`
	Generated bool   `json:"generated"`
	Bytes     int    `json:"diff_bytes"`
}

func evidence(dir string, from, to Snapshot, files []string) ([]Change, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var out []Change
	for index, path := range files {
		if same(from[path], to[path]) {
			continue
		}
		stem := filepath.Join(dir, fmt.Sprint(index))
		change := Change{Path: path, Before: stem + ".before.txt", After: stem + ".after.txt", Diff: stem + ".diff",
			Generated: strings.HasPrefix(path, "generated/") || strings.HasPrefix(path, "dist/")}
		if err := os.WriteFile(change.Before, []byte(from[path].text()), 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(change.After, []byte(to[path].text()), 0o600); err != nil {
			return nil, err
		}
		// diff exits 1 when the files differ; only its output matters here.
		diff, _ := exec.Command("diff", "-u", "--label", "before/"+path, "--label", "after/"+path, change.Before, change.After).Output()
		if err := os.WriteFile(change.Diff, diff, 0o600); err != nil {
			return nil, err
		}
		change.Bytes = len(diff)
		out = append(out, change)
	}
	return out, nil
}

func (g *Gate) contract() string {
	parts := map[string]any{"brief": g.Plan.Brief, "files": g.Files, "context": g.Context, "streams": g.Plan.Streams,
		"ui": g.Plan.UI, "checks": g.Plan.Checks, "mode": g.Mode}
	// A design that was pulled again is another design, also when it lies at the same path.
	if designs := g.designDigests(); designs != nil {
		parts["design"] = designs
	}
	if len(g.Plan.Requirements) > 0 {
		parts["requirements"] = g.Plan.Requirements
	}
	return digest(parts)
}

// Findings returns every finding ever reported for the task, latest version of each.
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
	out := make([]Finding, len(order))
	for index, id := range order {
		out[index] = seen[id]
	}
	return out
}

// isNote reports whether a finding is polish: recorded for the lead, never triaged, never blocking.
func (g *Gate) isNote(finding Finding) bool {
	return finding.Severity == "P3" && g.Dispositions[finding.ID].Decision != "accepted"
}

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
	if finding.Confidence == "low" || finding.Severity == "P3" {
		return false
	}
	return finding.BoundaryBreak || cfg.Modes[g.Mode].Blocks(finding.Severity)
}

func (g *Gate) accepted() []Finding {
	out := []Finding{} // packets must carry [] rather than null
	for _, finding := range g.Findings() {
		if _, resolved := g.Resolutions[finding.ID]; !resolved && g.Dispositions[finding.ID].Decision == "accepted" {
			out = append(out, finding)
		}
	}
	return out
}

// untriaged lists findings the lead has not judged yet. Notes need no judgement.
func (g *Gate) untriaged() []string {
	var out []string
	for _, finding := range g.Findings() {
		_, resolved := g.Resolutions[finding.ID]
		_, judged := g.Dispositions[finding.ID]
		if !resolved && !judged && !g.isNote(finding) {
			out = append(out, finding.ID)
		}
	}
	return out
}

type laneSpec struct {
	Axes  []string
	Scope string
	Files []string
	Route config.Route
}

// authors are the models whose code is under review; none of them may judge it.
func (g *Gate) authors() []string {
	var backend, all []string
	for name, writer := range g.latest() {
		all = append(all, writer.Route.Model)
		for _, stream := range g.Plan.Streams {
			if stream.Name == name && stream.Domain == "backend" {
				backend = append(backend, writer.Route.Model)
			}
		}
	}
	if len(backend) > 0 {
		return backend
	}
	return all
}

func (g *Gate) lanes(cfg *config.Config, subs *substitutions, quality bool) ([]laneSpec, func(config.Route) bool, error) {
	authors := g.authors()
	skip := func(route config.Route) bool {
		return slices.Contains(cfg.ReviewersExcluded, route.Alias) || slices.Contains(authors, route.Model)
	}
	type scope struct {
		name  string
		files []string
	}
	scopes := []scope{{"task", g.Files}}
	if g.Request.ReviewFirst() && len(g.Plan.Streams) > 1 && !quality {
		scopes = nil
		for _, stream := range g.Plan.Streams {
			scopes = append(scopes, scope{stream.Name, stream.Files})
		}
	}
	var out []laneSpec
	for _, lane := range cfg.Modes[g.Mode].Review {
		route := cfg.MustRoute(lane.Route)
		if skip(route) {
			replaced := false
			for _, alternate := range cfg.Alternates(route) {
				if !skip(alternate) {
					subs.list = append(subs.list, provider.Substitution{Wanted: route.String(), Used: alternate.String(),
						Reason: "the routed model wrote the code under review"})
					route, replaced = alternate, true
					break
				}
			}
			if !replaced {
				return nil, nil, fmt.Errorf("no reviewer independent of the writer is configured for %s", lane.Route)
			}
		}
		for _, scope := range scopes {
			out = append(out, laneSpec{Axes: lane.Axes, Scope: scope.name, Files: scope.files, Route: route})
		}
	}
	// The design is judged by eye by a model that did not write the code: the first lane takes it along.
	if len(g.Plan.Design) > 0 && len(out) > 0 {
		out[0].Axes = append(slices.Clone(out[0].Axes), "design")
	}
	return out, skip, nil
}

// LeadReview is the lead's own reading of the diff, used in light mode instead of a reviewer process.
type LeadReview struct {
	Summary  string         `json:"summary"`
	Findings []Finding      `json:"findings"`
	Quality  *QualityReport `json:"quality,omitempty"`
}

type ReviewInput struct {
	ReviewerTimeoutS int          `json:"reviewer_timeout_s,omitempty"`
	LeadReview       *LeadReview  `json:"lead_review,omitempty"`
	Visual           *VisualInput `json:"visual,omitempty"`
	SkipChecks       bool         `json:"skip_checks,omitempty"`
	Recheck          bool         `json:"recheck,omitempty"` // reconsider unchanged evidence; does not bypass any gate
	Finish           *FinishInput `json:"finish,omitempty"`  // close the task in the same call when nothing blocks
}

// ReviewTask checks the change before starting its reviewers. Browser observations used as acceptance
// evidence precede their reviewer; otherwise the browser pass can run alongside code review.
func ReviewTask(cfg *config.Config, gate *Gate, input ReviewInput) (any, error) {
	if !gate.active() || gate.Phase == "held" {
		return nil, fmt.Errorf("cannot review a gate that is %s", gate.Phase)
	}
	for _, writer := range gate.latest() {
		if writer.Status == "RUNNING" {
			return nil, errors.New("a writer is still running")
		}
	}
	if left := gate.unresolved(); len(left) > 0 {
		return nil, fmt.Errorf("conflict markers remain in %s; `fix` sends them to the writers", strings.Join(left, ", "))
	}
	gate.Conflicts = nil
	if err := gate.auditScope(); err != nil {
		return nil, err
	}
	if _, err := loadReviewRules(cfg, gate.Files); err != nil {
		return nil, err
	}
	if err := gate.prepareDesigns(false); err != nil {
		return nil, err
	}
	// Checks cost seconds and reviewers cost minutes and quota, so checks go first. Code that fails its own
	// checks is going to change: reviewing it would be paid for twice. A read-only review has no writer to send
	// the failure to, so there the checks only inform the reviewers.
	if !input.SkipChecks {
		if err := gate.runOutstanding(); err != nil {
			return nil, err
		}
		if gate.Request.Writes() && len(gate.failing()) > 0 {
			gate.Phase = "awaiting-review"
			return gate.Summary(cfg), gate.save()
		}
	}
	content, now, err := gate.contentDigest()
	if err != nil {
		return nil, err
	}
	contract := gate.contract()
	quality, err := gate.qualityInputs(cfg)
	if err != nil {
		return nil, err
	}
	// Acceptance claims may cite this browser pass. Collect it before its reviewer reads the evidence.
	if quality != nil && input.Visual != nil {
		stage, err := gate.prepareVisual(cfg, *input.Visual, now, content)
		if err != nil {
			return nil, err
		}
		if err := stage.run(); err != nil {
			return nil, err
		}
		if err := stage.apply(); err != nil {
			return nil, err
		}
		if err := gate.save(); err != nil {
			return nil, err
		}
		quality, err = gate.qualityInputs(cfg)
		if err != nil {
			return nil, err
		}
		input.Visual = nil
	}
	accepted := gate.accepted()
	if !input.Recheck && input.LeadReview == nil && gate.ReviewDigest == content && len(gate.Reviews) > 0 &&
		gate.Reviews[len(gate.Reviews)-1].Contract == contract && gate.Reviews[len(gate.Reviews)-1].QualityDigest == quality.fingerprint() {
		last := gate.Reviews[len(gate.Reviews)-1]
		pending := false
		for _, finding := range accepted {
			pending = pending || !slices.Contains(last.Rechecked, finding.ID)
		}
		if !pending && input.Visual == nil {
			gate.Phase = "reviewed"
			if err := gate.save(); err != nil {
				return nil, err
			}
			return gate.closeWith(cfg, input.Finish)
		}
	}
	changed := false
	for _, file := range gate.Files {
		changed = changed || !same(gate.Baseline[file], now[file])
	}
	if !changed && len(gate.Reviews) == 0 {
		return nil, errors.New("there is no change to review")
	}
	dir := filepath.Join(gate.dir(), fmt.Sprintf("review-%d-%s", gate.Iteration, newID()[:8]))
	// Reviewers are handed the check logs as evidence, so the directory must exist even when no check ran.
	if err := os.MkdirAll(filepath.Join(gate.dir(), "checks"), 0o700); err != nil {
		return nil, err
	}
	changes, err := evidence(filepath.Join(dir, "evidence"), gate.Baseline, now, gate.Files)
	if err != nil {
		return nil, err
	}
	// A free pass before any model reads the code: literal leftovers on the lines the task added.
	gate.Signals = scanSignals(gate.Baseline, now, gate.Files)
	packet := map[string]any{"task": gate.Request.Task, "brief": gate.Plan.Brief, "mode": gate.Mode, "context": gate.Context,
		"changes": changes, "slop_signals": gate.Signals, "accepted_findings": accepted, "prior_decisions": gate.decisions(), "checks": gate.Checks,
		"architecture": architectureFor(gate.Plan), "writer_notes": gate.writerNotes(), "report_p3": false,
		"context_pack": gate.ContextPack}
	packet["refresh"] = input.Recheck
	if ui, _ := loadUI(gate.Plan); ui != nil {
		packet["ui"] = ui
	}
	if designs, _ := gate.designReview(); designs != nil {
		packet["design"] = designs
	}
	if len(gate.Reviews) > 0 {
		previous := Snapshot{}
		if raw, err := os.ReadFile(filepath.Join(gate.Reviews[len(gate.Reviews)-1].Dir, "source.json")); err == nil {
			_ = json.Unmarshal(raw, &previous)
		}
		if packet["since_last_review"], err = evidence(filepath.Join(dir, "delta"), previous, now, gate.Files); err != nil {
			return nil, err
		}
	}
	if len(gate.Corrections) > 0 {
		packet["correction"] = gate.Corrections[len(gate.Corrections)-1]
	}
	if err := writePacket(filepath.Join(dir, "source.json"), now); err != nil {
		return nil, err
	}
	subs := &substitutions{}
	specs, skip, err := gate.lanes(cfg, subs, quality != nil)
	if err != nil {
		return nil, err
	}
	// After a correction only the lanes with something to verify run again, plus one correctness lane that reads
	// the delta for regressions. A lane that had no finding is not paid to re-read a fix it did not ask for.
	if len(gate.Reviews) > 0 && len(accepted) > 0 {
		var kept []laneSpec
		regression := false
		for index, spec := range specs {
			correctness := slices.Contains(spec.Axes, "correctness")
			if (index == 0 && quality != nil) || len(gate.relevant(spec, accepted)) > 0 || (correctness && !regression) {
				kept = append(kept, spec)
				regression = regression || correctness
			}
		}
		specs = kept
	}
	if len(specs) == 0 && input.LeadReview == nil {
		// Light mode: the lead is the reviewer. Hand over the evidence and wait for its reading.
		if err := gate.save(); err != nil {
			return nil, err
		}
		return map[string]any{"mode": gate.Mode, "evidence": changes, "slop_signals": gate.Signals, "quality": quality,
			"next": "Read these diffs yourself, then call review again with lead_review {summary, findings, quality}. Assess every supplied quality criterion. You are the only reviewer: " +
				"judge every slop_signal, and ask of each added line whether the task needs it (guide, section 5). An empty findings list is a valid result."}, nil
	}
	timeout, err := timeoutSeconds(input.ReviewerTimeoutS, gate.Plan.ReviewerTimeoutS, cfg.Modes[gate.Mode].ReviewerTimeoutS)
	if err != nil {
		return nil, err
	}
	resolved := &Resolved{Plan: gate.Plan, Request: gate.Request, Mode: gate.Mode, Config: cfg}
	limit, err := resolved.Parallelism()
	if err != nil {
		return nil, err
	}
	known := map[string]Finding{}
	for _, finding := range gate.Findings() {
		known[finding.ID] = finding
	}
	review := Review{Iteration: gate.Iteration, Digest: content, Contract: contract, Dir: dir, QualityDigest: quality.fingerprint()}
	for _, finding := range accepted {
		review.Rechecked = append(review.Rechecked, finding.ID)
	}
	gate.Phase, gate.ReviewError = "reviewing", ""
	if err := gate.save(); err != nil {
		return nil, err
	}

	// The three kinds of verification start together and are applied by this thread once all have ended.
	var visualStage *visualStage
	if input.Visual != nil {
		if visualStage, err = gate.prepareVisual(cfg, *input.Visual, now, content); err != nil {
			return nil, err
		}
	}
	lanes := make([]LaneRun, len(specs))
	jobs := []func() error{}
	for index, spec := range specs {
		jobs = append(jobs, func() (err error) {
			var assignedQuality *qualityInputs
			if index == 0 {
				assignedQuality = quality
			}
			lanes[index], err = gate.runLane(cfg, spec, index, dir, packet, content, contract, accepted, known, seconds(timeout), subs, skip, assignedQuality)
			return err
		})
	}
	var visualError error
	if visualStage != nil {
		jobs = append(jobs, func() error { visualError = visualStage.run(); return nil })
	}
	failure := firstError(parallel(len(jobs), limit+1, func(index int) error { return jobs[index]() }))

	if visualStage != nil {
		if visualError == nil && failure == nil {
			visualError = visualStage.apply()
		}
		if visualError != nil && !ops.IsCancelled(visualError) {
			gate.ReviewError = "browser pass: " + visualError.Error() // the code review still stands
		} else if visualError != nil {
			failure = visualError
		}
	}
	if failure == nil {
		if after, _, _ := gate.contentDigest(); after != content {
			failure = errors.New("files changed during the review; its evidence is stale")
		}
		currentQuality, err := gate.qualityInputs(cfg)
		if err != nil {
			failure = err
		} else if currentQuality.fingerprint() != review.QualityDigest || gate.contract() != contract {
			failure = errors.New("review criteria or evidence changed during the review; its evidence is stale")
		}
	}
	if failure != nil {
		gate.Phase = "awaiting-review"
		if !ops.IsCancelled(failure) {
			gate.ReviewError = failure.Error()
		}
		_ = gate.save()
		return nil, failure
	}
	if input.LeadReview != nil && len(specs) == 0 {
		lane, err := gate.leadLane(*input.LeadReview, known)
		if err != nil {
			gate.Phase = "awaiting-review"
			_ = gate.save()
			return nil, err
		}
		if err := gate.validateQuality(input.LeadReview.Quality, quality); err != nil {
			gate.Phase = "awaiting-review"
			_ = gate.save()
			return nil, err
		}
		lane.Quality = input.LeadReview.Quality
		lanes = []LaneRun{lane}
	}
	review.Lanes = lanes
	gate.applyReview(review, accepted)
	gate.Substitutions = append(gate.Substitutions, subs.list...)
	if err := gate.save(); err != nil {
		return nil, err
	}
	return gate.closeWith(cfg, input.Finish)
}

// closeWith ends a review call. A clean review can close the task in the same call, saving the lead a round trip.
func (g *Gate) closeWith(cfg *config.Config, finish *FinishInput) (any, error) {
	if finish != nil {
		value, err := Finish(cfg, g, *finish)
		if err == nil {
			return value, nil
		}
		summary := g.Summary(cfg)
		summary["finish_refused"] = err.Error()
		return summary, nil
	}
	return g.Summary(cfg), nil
}

func (g *Gate) writerNotes() []map[string]any {
	var out []map[string]any
	for name, writer := range g.latest() {
		out = append(out, map[string]any{"stream": name, "summary": writer.Summary, "deviations": writer.Deviations})
	}
	sort.Slice(out, func(a, b int) bool { return out[a]["stream"].(string) < out[b]["stream"].(string) })
	return out
}

func (g *Gate) decisions() []map[string]any {
	var out []map[string]any
	for _, finding := range g.Findings() {
		disposition, judged := g.Dispositions[finding.ID]
		resolution, resolved := g.Resolutions[finding.ID]
		if judged || resolved {
			out = append(out, map[string]any{"finding": finding, "disposition": disposition, "resolution": resolution})
		}
	}
	return out
}

func (g *Gate) relevant(spec laneSpec, accepted []Finding) []Finding {
	out := []Finding{}
	for _, finding := range accepted {
		axis := finding.Axis
		if axis == "visual" {
			axis = "correctness"
		}
		files := append([]string{finding.Path}, g.Dispositions[finding.ID].Files...)
		if slices.Contains(spec.Axes, axis) && slices.ContainsFunc(files, func(file string) bool { return slices.Contains(spec.Files, file) }) {
			out = append(out, finding)
		}
	}
	return out
}

func (g *Gate) runLane(cfg *config.Config, spec laneSpec, index int, dir string, packet map[string]any, content, contract string,
	accepted []Finding, known map[string]Finding, timeout time.Duration, subs *substitutions, skip func(config.Route) bool, quality *qualityInputs) (LaneRun, error) {
	relevant := g.relevant(spec, accepted)
	var recheck []string
	for _, finding := range relevant {
		recheck = append(recheck, finding.ID)
	}
	key := digest(map[string]any{"content": content, "contract": contract, "axes": spec.Axes, "scope": spec.Scope, "files": spec.Files,
		"route": spec.Route.String(), "recheck": recheck, "decisions": g.Dispositions, "quality": quality.fingerprint()})
	cache := filepath.Join(g.dir(), "review-cache", key+".json")
	lane := LaneRun{}
	if raw, err := os.ReadFile(cache); packet["refresh"] != true && err == nil && json.Unmarshal(raw, &lane) == nil {
		return lane, nil // a finished lane of a partly failed review is kept
	}
	scoped := map[string]any{"axes": spec.Axes, "scope": spec.Scope, "scope_files": spec.Files, "recheck": relevant}
	for name, value := range packet {
		scoped[name] = value
	}
	if quality != nil {
		scoped["quality"] = quality
	}
	for _, name := range []string{"changes", "since_last_review"} {
		if changes, ok := packet[name].([]Change); ok {
			var mine []Change
			for _, change := range changes {
				if slices.Contains(spec.Files, change.Path) {
					mine = append(mine, change)
				}
			}
			scoped[name] = mine
		}
	}
	scoped["accepted_findings"] = relevant
	path := filepath.Join(dir, fmt.Sprintf("lane-%d", index), "packet.json")
	if err := writePacket(path, scoped); err != nil {
		return lane, err
	}
	lane = LaneRun{Axes: spec.Axes, Scope: spec.Scope, Files: spec.Files, Route: spec.Route, Dir: filepath.Join(dir, fmt.Sprintf("lane-%d", index), "run")}
	prompt := assets.Prompt("review") + "\nMode: " + g.Mode + "; your axes: " + strings.Join(spec.Axes, ", ") + ".\nRead " + path + ".\n"
	// The reviewer that raised a finding verifies its fix in its own session: it only needs the delta.
	route := spec.Route
	if previous := g.previousLane(spec); previous != nil {
		lane.Dir, lane.Session = previous.Dir, previous.Session
		route.Account = previous.Route.Account
		route.Effort = config.Lower(route.Effort) // a recheck reads the delta, not the whole change
		prompt = "The task you reviewed was corrected. A fresh packet is at " + path + ": start from since_last_review and recheck, " +
			"resolve or keep every finding listed there, report new defects of the correction, and return the same JSON schema.\n"
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
			"each requirement needs id, status (verified/partial/missing), reason, evidence [{kind (code/check/browser), ref, detail}]. " +
			"Each rule needs id, status (pass/block/not_applicable/unavailable), evidence. Reassess all criteria even on a correction.\n"
	}
	result, used, err := subs.invoke(cfg, provider.Call{Route: route, Prompt: prompt + toolNote, Dir: lane.Dir, Schema: schema, MCP: tools, Writable: toolDirs,
		Timeout: timeout, Persist: true, Resume: lane.Session, ReadDirs: append(append([]string{dir, filepath.Join(g.dir(), "checks")}, packDirs(g.ContextPack)...), designDirs...)}, skip)
	if err != nil {
		return lane, err
	}
	var answer struct {
		Reports []Report       `json:"reports"`
		Quality *QualityReport `json:"quality"`
	}
	if err := decode(result.Answer, &answer); err != nil {
		return lane, err
	}
	if lane.Reports, err = g.validateReports(answer.Reports, spec.Axes, spec.Files, known); err != nil {
		return lane, err
	}
	if err := g.validateQuality(answer.Quality, quality); err != nil {
		return lane, err
	}
	lane.Quality = answer.Quality
	lane.Route, lane.Session = used, result.Session
	if err := ops.Checkpoint(); err != nil {
		return lane, err
	}
	_ = writePacket(cache, lane)
	return lane, nil
}

func (g *Gate) previousLane(spec laneSpec) *LaneRun {
	if len(g.Reviews) == 0 {
		return nil
	}
	lanes := g.Reviews[len(g.Reviews)-1].Lanes
	for index := range lanes {
		lane := &lanes[index]
		if slices.Equal(lane.Axes, spec.Axes) && lane.Scope == spec.Scope && lane.Route.Model == spec.Route.Model && lane.Session != "" {
			return lane
		}
	}
	return nil
}

// validateReports rejects a report that does not cover its axes or cannot be acted on, and gives findings stable ids.
func (g *Gate) validateReports(reports []Report, axes, files []string, known map[string]Finding) ([]Report, error) {
	var got []string
	for _, report := range reports {
		got = append(got, report.Axis)
	}
	want := slices.Clone(axes)
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		return nil, fmt.Errorf("the review must report each of its axes exactly once: want %v, got %v", want, got)
	}
	seen := map[string]bool{}
	for index := range reports {
		report := &reports[index]
		if report.Status == "BLOCKED" {
			return nil, fmt.Errorf("the %s reviewer could not review: %s", report.Axis, strings.Join(report.Checked, "; "))
		}
		if (report.Status != "PASS" && report.Status != "NEEDS_FIX") || len(report.Checked) == 0 {
			return nil, errors.New("a review report needs a status and what it checked")
		}
		if report.Status == "NEEDS_FIX" && len(report.Findings) == 0 {
			return nil, errors.New("NEEDS_FIX without findings")
		}
		report.ScopeFiles = files
		for position := range report.Findings {
			finding := &report.Findings[position]
			finding.Axis = report.Axis
			empty := slices.ContainsFunc([]string{finding.Evidence, finding.Impact, finding.Remedy}, func(value string) bool {
				return strings.TrimSpace(value) == ""
			})
			if !slices.Contains(config.Severities, finding.Severity) || !slices.Contains([]string{"high", "medium", "low"}, finding.Confidence) ||
				!slices.Contains(files, finding.Path) || finding.Line < 1 || empty {
				return nil, fmt.Errorf("finding %q is outside the scope or lacks evidence, impact or remedy", finding.ID)
			}
			if previous, ok := known[finding.ID]; !ok || previous.Axis != report.Axis {
				finding.ID = g.ID[:8] + "-" + digest([]string{report.Axis, finding.Path, finding.Category, finding.Evidence})[:16]
			}
			if seen[finding.ID] {
				return nil, errors.New("the same finding was reported twice")
			}
			seen[finding.ID] = true
		}
	}
	return reports, nil
}

func (g *Gate) leadLane(review LeadReview, known map[string]Finding) (LaneRun, error) {
	if strings.TrimSpace(review.Summary) == "" {
		return LaneRun{}, errors.New("lead_review needs a summary of what was read and verified")
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
	reports, err := g.validateReports([]Report{{Axis: "correctness", Status: status, Findings: review.Findings,
		Checked: []string{review.Summary}, Resolutions: resolutions}}, []string{"correctness"}, g.Files, known)
	return LaneRun{Axes: []string{"correctness"}, Scope: "task", Files: g.Files, Route: config.Route{Alias: "lead"}, Reports: reports}, err
}

// applyReview records the review and recomputes which accepted findings are now resolved.
func (g *Gate) applyReview(review Review, accepted []Finding) {
	for _, finding := range accepted {
		delete(g.Resolutions, finding.ID)
		axis := finding.Axis
		if axis == "visual" {
			axis = "correctness"
		}
		files := append([]string{finding.Path}, g.Dispositions[finding.ID].Files...)
		var votes []string
		unanimous, asked := true, false
		for _, lane := range review.Lanes {
			for _, report := range lane.Reports {
				if report.Axis != axis || !slices.ContainsFunc(files, func(file string) bool { return slices.Contains(report.ScopeFiles, file) }) {
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
		if asked && unanimous {
			prefix := ""
			if finding.Axis == "visual" {
				prefix = "Verified in code only; the browser was not used. "
			}
			g.Resolutions[finding.ID] = prefix + strings.Join(votes, "; ")
		}
	}
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
	g.Reviews = append(g.Reviews, review)
	g.ReviewDigest, g.Phase = review.Digest, "reviewed"
}

type TriageInput struct {
	Dispositions []Disposition `json:"dispositions"`
}

var decisionsAllowed = []string{"accepted", "rejected", "deferred", "needs-approval"}

// Triage records the lead's judgement of findings. Every judgement needs evidence from the code.
func Triage(cfg *config.Config, gate *Gate, input TriageInput) (any, error) {
	content, _, err := gate.contentDigest()
	if err != nil {
		return nil, err
	}
	if gate.Phase != "reviewed" || content != gate.ReviewDigest {
		return nil, errors.New("triage needs a review of the current code")
	}
	if gate.visualRequired() && gate.currentVisual(content) == nil {
		return nil, errors.New("collect the opted-in browser report before triage, or record why it is skipped")
	}
	known := map[string]Finding{}
	for _, finding := range gate.Findings() {
		known[finding.ID] = finding
	}
	for _, item := range input.Dispositions {
		finding, ok := known[item.ID]
		switch {
		case !ok || !slices.Contains(decisionsAllowed, item.Decision) || strings.TrimSpace(item.Evidence) == "":
			return nil, fmt.Errorf("disposition %q needs a known id, a decision (%s) and evidence", item.ID, strings.Join(decisionsAllowed, ", "))
		case finding.Severity == "P3" && item.Decision == "accepted" && strings.TrimSpace(item.Basis) == "":
			return nil, fmt.Errorf("%s is polish: accepting it needs basis naming the agreed criterion or the user's request", item.ID)
		case finding.Path == "" && item.Decision == "accepted" && len(item.Files) == 0:
			return nil, fmt.Errorf("%s has no source location: an accepted remedy needs files", item.ID)
		}
		if item.DuplicateOf != "" {
			if _, exists := known[item.DuplicateOf]; !exists || item.DuplicateOf == item.ID || item.Decision != "rejected" ||
				gate.Dispositions[item.DuplicateOf].DuplicateOf != "" {
				return nil, fmt.Errorf("%s: a duplicate is rejected and points directly at its canonical finding", item.ID)
			}
		}
		if item.Files, err = cleanPaths(item.Files); err != nil {
			return nil, err
		}
		if clash := owners(item.Files); len(clash) > 0 {
			return nil, fmt.Errorf("files are owned by another session's gate: %s", strings.Join(clash, "; "))
		}
		gate.Dispositions[item.ID] = item
	}
	return gate.Summary(cfg), gate.save()
}
