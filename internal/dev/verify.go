package dev

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

// Change is one changed file as a reviewer is shown it. A reviewer reads one patch; the present files are in
// the checkout it already has, and the versions before the change are kept beside the patch.
type Change struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // added, modified or deleted
	Added     int    `json:"added"`
	Removed   int    `json:"removed"`
	Collapsed string `json:"collapsed,omitempty"` // why the file is listed but left out of the patch
	Before    string `json:"before,omitempty"`    // the file as it was, for a modified or deleted file in the patch
}

// largeChange is how many changed lines of one file a reviewer is still handed in the patch. Past it the file
// is listed, and opened only to investigate a concrete issue.
const largeChange = 1500

var generatedZones = []string{"generated/", "dist/", "build/", "vendor/", "node_modules/", "target/", ".next/"}

// generatedPath says why a path is generator output, or "".
func (g *Gate) generatedPath(path string, attributes map[string]bool) string {
	if matchAny(compilePatterns(g.Plan.Generated), path) {
		return "generated (declared in the plan)"
	}
	if attributes[path] {
		return "generated (.gitattributes)"
	}
	for _, zone := range generatedZones {
		if strings.HasPrefix(path, zone) || strings.Contains(path, "/"+zone) {
			return "generated (" + strings.TrimSuffix(zone, "/") + ")"
		}
	}
	base := filepath.Base(path)
	if strings.Contains(base, ".min.") || strings.Contains(base, ".gen.") || strings.HasSuffix(base, ".lock") || strings.HasSuffix(base, "-lock.json") ||
		strings.HasSuffix(base, ".snap") || strings.HasSuffix(base, ".sum") {
		return "generated (" + base + ")"
	}
	return ""
}

// markedGenerated reads the repository's own statement of which files are generated.
func markedGenerated(paths []string) map[string]bool {
	out := map[string]bool{}
	if len(paths) == 0 {
		return out
	}
	raw, err := exec.Command("git", append([]string{"check-attr", "-z", "linguist-generated", "--"}, paths...)...).Output()
	if err != nil {
		return out
	}
	fields := strings.Split(string(raw), "\x00")
	for index := 0; index+2 < len(fields); index += 3 {
		if value := fields[index+2]; value == "set" || value == "true" {
			out[fields[index]] = true
		}
	}
	return out
}

// evidence writes what a reviewer reads about the difference between two trees: one patch of the reviewable
// files, their earlier versions, and a list of everything that changed. It returns the list and the paths
// that are in the patch.
func (g *Gate) evidence(dir, from, to string) ([]Change, []string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	all, err := diffTrees(g.objects(), from, to)
	if err != nil {
		return nil, nil, err
	}
	scope := compilePatterns(g.scope())
	var owned []FileChange
	var paths []string
	for _, change := range all {
		if matchAny(scope, change.Path) {
			owned = append(owned, change)
			paths = append(paths, change.Path)
		}
	}
	counts, err := numstat(g.objects(), from, to)
	if err != nil {
		return nil, nil, err
	}
	attributes := markedGenerated(paths)
	changes := []Change{}
	var reviewable []string
	for _, change := range owned {
		item := Change{Path: change.Path, Status: map[string]string{"A": "added", "M": "modified", "D": "deleted"}[change.Status],
			Added: counts[change.Path][0], Removed: counts[change.Path][1]}
		switch reason := g.generatedPath(change.Path, attributes); {
		case reason != "":
			item.Collapsed = reason
		case item.Added < 0:
			item.Collapsed, item.Added, item.Removed = "binary", 0, 0
		case item.Added+item.Removed > largeChange:
			item.Collapsed = fmt.Sprintf("large: %d changed lines", item.Added+item.Removed)
		default:
			reviewable = append(reviewable, change.Path)
			if change.Status != "A" {
				if content, _, ok := treeFile(g.objects(), from, change.Path); ok {
					item.Before = filepath.Join(dir, "before", filepath.FromSlash(change.Path))
					if err := os.MkdirAll(filepath.Dir(item.Before), 0o700); err != nil {
						return nil, nil, err
					}
					if err := os.WriteFile(item.Before, content, 0o600); err != nil {
						return nil, nil, err
					}
				}
			}
		}
		changes = append(changes, item)
	}
	patch, err := treePatch(g.objects(), from, to, reviewable)
	if err != nil {
		return nil, nil, fmt.Errorf("produce the review patch: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "change.patch"), patch, 0o600); err != nil {
		return nil, nil, err
	}
	return changes, reviewable, nil
}

// signals scans the lines the present position added to its reviewable files.
func (g *Gate) signals(now string, paths []string) []Signal {
	out := []Signal{}
	for _, path := range paths {
		after, _, exists := treeFile(g.objects(), now, path)
		if !exists || !scannable(path) {
			continue
		}
		before, _, _ := treeFile(g.objects(), g.origin(), path)
		if out = append(out, scanText(path, string(before), string(after))...); len(out) >= signalLimit {
			return out[:signalLimit]
		}
	}
	return out
}

// Reading is a review the lead owes: the mode has no reviewer process, so the lead reads the patch itself.
type Reading struct {
	Key           string `json:"key"`
	Contract      string `json:"contract"`
	QualityDigest string `json:"quality_digest,omitempty"`
	Tree          string `json:"tree"`
	Dir           string `json:"dir"`
	Patch         string `json:"patch"`
	Packet        string `json:"packet"` // what to judge: the changed files, the criteria, the mechanical signals
}

// RunInput tunes one run of the task.
type RunInput struct {
	ReviewerTimeoutS int          `json:"reviewer_timeout_s,omitempty"`
	Visual           *VisualInput `json:"visual,omitempty"`  // observe the opted-in browser scenarios in this run
	Recheck          bool         `json:"recheck,omitempty"` // ask for a fresh judgement of unchanged evidence; bypasses no condition
}

// verify brings the evidence of the present position up to date: declared checks first, then independent
// review. It starts nothing when the evidence is current. What the evidence means is decided by classify.
func (g *Gate) verify(cfg *config.Config, input RunInput, did *[]string) error {
	for _, writer := range g.latest() {
		if writer.Status == "RUNNING" {
			return errors.New("a writer is still running")
		}
	}
	if len(g.unresolved()) > 0 {
		return nil // conflict markers go back to the writers before anything is judged
	}
	g.Conflicts = nil
	now, err := g.snapshot()
	if err != nil {
		return err
	}
	if err := g.audit(now); err != nil {
		return err
	}
	if g.Worktree != "" && len(g.Foreign) > 0 {
		return g.save() // what an isolated task changed outside its plan is settled before anything is judged
	}
	if _, err := loadReviewRules(cfg, g.scope()); err != nil {
		return err
	}
	if err := g.prepareDesigns(false); err != nil {
		return err
	}
	// Checks cost seconds and reviewers cost minutes and quota, so checks go first. Code that fails its own
	// checks is going to change: reviewing it would be paid for twice. A read-only review has no writer to send
	// the failure to, so there the checks only inform the reviewers.
	started := time.Now()
	ran, err := g.runOutstanding()
	if err != nil {
		return err
	}
	if failing := g.failing(); ran > 0 || len(failing) > 0 {
		line := fmt.Sprintf("checks: %d of %d pass", len(g.checks())-len(g.outstanding()), len(g.checks()))
		if ran > 0 {
			line += " in " + since(started)
		}
		*did = append(*did, line)
	}
	if g.Request.Writes() && len(g.failing()) > 0 {
		return g.save()
	}
	if now, err = g.snapshot(); err != nil {
		return err
	}
	key, err := g.reviewKey(now)
	if err != nil {
		return err
	}
	contract := g.codeContract()
	quality, err := g.qualityInputs(cfg)
	if err != nil {
		return err
	}
	// Acceptance claims may cite this browser pass. Collect it before its reviewer reads the evidence.
	if quality != nil && input.Visual != nil {
		started := time.Now()
		if err := g.observe(cfg, *input.Visual, now, key); err != nil {
			return err
		}
		*did = append(*did, g.observed(since(started)))
		if quality, err = g.qualityInputs(cfg); err != nil {
			return err
		}
		input.Visual = nil
	}
	owned, _, err := g.changes(now)
	if err != nil {
		return err
	}
	if len(owned) == 0 && len(g.Reviews) == 0 {
		if g.Request.ReviewFirst() {
			return errors.New("there is no change to review")
		}
		if quality == nil {
			return g.save() // nothing was changed and nothing is judged: there is nothing to read
		}
	}
	accepted := g.accepted()
	var last *Review
	if len(g.Reviews) > 0 {
		last = &g.Reviews[len(g.Reviews)-1]
	}
	if last != nil && !input.Recheck && last.Key == key && last.Contract == contract && g.independentReview(cfg, *last) {
		switch {
		case last.Iteration < g.Iteration:
			// A correction ran since: the reviewer verifies it, also when no byte changed.
		case last.QualityDigest == quality.fingerprint():
			if input.Visual == nil {
				return g.save() // the review is current: nothing to pay for
			}
		case input.Visual == nil:
			// The code was read and has not changed; only what is judged, or its receipts, did.
			done, err := g.reassess(cfg, quality, input)
			if err != nil {
				return err
			}
			if done {
				*did = append(*did, "criteria reassessed by the reviewer; the code review stands")
				return g.save()
			}
		}
	}
	if g.Reading != nil && g.Reading.Key == key && g.Reading.Contract == contract && g.Reading.QualityDigest == quality.fingerprint() && !input.Recheck {
		return nil // the lead already holds this evidence and owes its reading
	}
	dir := filepath.Join(g.dir(), fmt.Sprintf("review-%d-%s", g.Iteration, newID()[:8]))
	// Reviewers are handed the check logs as evidence, so the directory must exist even when no check ran.
	if err := os.MkdirAll(filepath.Join(g.dir(), "checks"), 0o700); err != nil {
		return err
	}
	changes, reviewable, err := g.evidence(dir, g.origin(), now)
	if err != nil {
		return err
	}
	// A free pass before any model reads the code: literal leftovers on the lines the task added.
	g.Signals = g.signals(now, reviewable)
	packet := map[string]any{"task": g.Request.Task, "brief": g.brief(), "mode": g.Mode, "context": g.context(),
		"changes": changes, "patch": filepath.Join(dir, "change.patch"), "before": filepath.Join(dir, "before"),
		"slop_signals": g.Signals, "prior_decisions": g.decisions(), "checks": g.checkEvidence(),
		"architecture": architectureFor(g.Plan), "writer_notes": g.writerNotes(), "report_p3": false,
		"context_pack": g.ContextPack, "refresh": input.Recheck}
	if ui, _ := loadUI(g.Plan); ui != nil {
		packet["ui"] = ui
	}
	if designs, _ := g.designReview(); designs != nil {
		packet["design"] = designs
	}
	if last != nil && last.Tree != "" {
		delta := filepath.Join(dir, "delta")
		since, _, err := g.evidence(delta, last.Tree, now)
		if err != nil {
			return err
		}
		packet["since_last_review"], packet["delta_patch"] = since, filepath.Join(delta, "change.patch")
		if last.Contract != contract {
			packet["contract_changed"] = "The plan was amended since your review: " + g.amendment()
		}
	}
	if len(g.Corrections) > 0 {
		packet["correction"] = g.Corrections[len(g.Corrections)-1]
	}
	if len(g.Reviews) > 0 {
		packet["open_notes"] = g.openNotes(cfg)
	}
	if unchecked := g.uncheckedTests(now); len(unchecked) > 0 {
		packet["unchecked_tests"] = map[string]any{"files": unchecked,
			"note": "Test files this task added or changed that no declared check runs: nothing here shows they pass."}
	}
	if len(g.Attachments) > 0 {
		packet["attachments"] = g.Attachments
	}
	subs := &substitutions{}
	specs, skip, err := g.lanes(cfg, subs)
	if err != nil {
		return err
	}
	// After a correction only the lanes with something to verify run again, plus one correctness lane that reads
	// the delta for regressions. A lane that had no finding is not paid to re-read a fix it did not ask for.
	if last != nil && len(accepted) > 0 {
		var kept []laneSpec
		regression := false
		for index, spec := range specs {
			correctness := slices.Contains(spec.Axes, "correctness")
			if (index == 0 && quality != nil) || len(g.relevant(spec, accepted)) > 0 || (correctness && !regression) {
				kept = append(kept, spec)
				regression = regression || correctness
			}
		}
		specs = kept
	}
	if len(specs) == 0 {
		// The lead is the reviewer. Hand over the evidence and wait for its reading.
		reading := &Reading{Key: key, Contract: contract, QualityDigest: quality.fingerprint(), Tree: now, Dir: dir,
			Patch: filepath.Join(dir, "change.patch"), Packet: filepath.Join(dir, "packet.json")}
		if quality != nil {
			packet["quality"] = quality
		}
		packet["accepted_findings"] = accepted
		if disputes := g.openDisputes(accepted); len(disputes) > 0 {
			packet["disputes"] = disputes // the lead raised these findings, so the lead answers their author
		}
		if err := writePacket(reading.Packet, packet); err != nil {
			return err
		}
		g.Reading = reading
		*did = append(*did, "evidence prepared for the lead's own review")
		return g.save()
	}
	timeout, err := timeoutSeconds(input.ReviewerTimeoutS, g.Plan.ReviewerTimeoutS, cfg.Modes[g.Mode].ReviewerTimeoutS)
	if err != nil {
		return err
	}
	limit, err := parallelism(cfg, g.Request)
	if err != nil {
		return err
	}
	known := map[string]Finding{}
	for _, finding := range g.Findings() {
		known[finding.ID] = finding
	}
	review := Review{Iteration: g.Iteration, Tree: now, Key: key, Contract: contract, Dir: dir, QualityDigest: quality.fingerprint(), Policy: g.reviewPolicy(cfg)}
	for _, finding := range accepted {
		review.Rechecked = append(review.Rechecked, finding.ID)
	}
	g.ReviewError = ""
	if err := g.save(); err != nil {
		return err
	}
	// Review lanes and the browser pass start together and are applied by this thread once all have ended.
	var browser *visualStage
	if input.Visual != nil {
		if browser, err = g.prepareVisual(cfg, *input.Visual, now, key); err != nil {
			return err
		}
	}
	var reviewers []string
	for _, spec := range specs {
		reviewers = append(reviewers, strings.Join(spec.Axes, "+")+" ("+spec.Route.String()+")")
	}
	if browser != nil {
		reviewers = append(reviewers, "browser pass ("+cfg.VisualReviewer+")")
	}
	ops.Doing("review: %s", strings.Join(reviewers, ", "))
	started = time.Now()
	lanes := make([]LaneRun, len(specs))
	jobs := []func() error{}
	for index, spec := range specs {
		jobs = append(jobs, func() (err error) {
			var assigned *qualityInputs
			if index == 0 {
				assigned = quality
			}
			lanes[index], err = g.runLane(cfg, spec, index, dir, packet, key, contract, accepted, known, seconds(timeout), subs, skip, assigned)
			return err
		})
	}
	var browserError error
	if browser != nil {
		jobs = append(jobs, func() error { browserError = browser.run(); return nil })
	}
	failure := firstError(parallel(len(jobs), limit, func(index int) error { return jobs[index]() }))
	if browser != nil {
		if browserError == nil && failure == nil {
			browserError = browser.apply()
		}
		if browserError != nil && !ops.IsCancelled(browserError) {
			g.ReviewError = "browser pass: " + browserError.Error() // the code review still stands
		} else if browserError != nil {
			failure = browserError
		}
	}
	if failure == nil {
		after, err := g.snapshot()
		if err == nil {
			var afterKey string
			if afterKey, err = g.reviewKey(after); err == nil && afterKey != key {
				err = errors.New("files changed during the review; its evidence is stale")
			}
		}
		if err == nil {
			var present *qualityInputs
			if present, err = g.qualityInputs(cfg); err == nil && (present.fingerprint() != review.QualityDigest || g.codeContract() != contract) {
				err = errors.New("review criteria or evidence changed during the review; its evidence is stale")
			}
		}
		failure = err
	}
	if failure != nil {
		if !ops.IsCancelled(failure) {
			g.ReviewError = failure.Error()
		}
		_ = g.save()
		return failure
	}
	review.Lanes = lanes
	g.applyReview(cfg, review, accepted)
	g.Reading = nil
	g.Substitutions = append(g.Substitutions, subs.list...)
	open, blocking := 0, 0
	for _, lane := range lanes {
		for _, report := range lane.Reports {
			for _, finding := range report.Findings {
				open++
				if g.blocking(cfg, finding) {
					blocking++
				}
			}
		}
	}
	*did = append(*did, fmt.Sprintf("review by %s: %d findings, %d blocking, in %s", laneRoutes(lanes), open, blocking, since(started)))
	if browser != nil && g.ReviewError == "" {
		*did = append(*did, g.observed(""))
	}
	return g.save()
}

func laneRoutes(lanes []LaneRun) string {
	var names []string
	for _, lane := range lanes {
		if name := lane.Route.String(); !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return strings.Join(names, " + ")
}

// checkEvidence is the receipts of the present checks as a reviewer cites them.
func (g *Gate) checkEvidence() []map[string]any {
	out := []map[string]any{}
	current := g.currentReceipts()
	for _, check := range g.checks() {
		kind := check.Kind
		if kind == "" {
			kind = "command"
		}
		item := map[string]any{"id": checkID(check.Argv), "argv": check.Argv, "kind": kind, "status": "not run"}
		if receipt := current[checkID(check.Argv)]; receipt != nil {
			item["status"], item["returncode"], item["log"] = "ran", receipt.ReturnCode, receipt.Log
			if receipt.Preexisting {
				item["note"] = "this check failed before the task too; that does not excuse the present failure"
			}
		}
		out = append(out, item)
	}
	return out
}

// reassess asks the reviewer that judged the criteria to judge them again, in its own session, without
// reading the code a second time. It reports false when that reviewer cannot be continued; the caller then
// runs an ordinary review.
func (g *Gate) reassess(cfg *config.Config, quality *qualityInputs, input RunInput) (bool, error) {
	last := g.Reviews[len(g.Reviews)-1]
	if quality == nil {
		// Nothing is judged any more: the code review stands as it is.
		renewed := last
		renewed.Lanes = slices.Clone(last.Lanes)
		for index := range renewed.Lanes {
			renewed.Lanes[index].Quality = nil
		}
		renewed.QualityDigest = ""
		g.Reviews = append(g.Reviews, renewed)
		return true, nil
	}
	position := slices.IndexFunc(last.Lanes, func(lane LaneRun) bool { return lane.Quality != nil })
	if position < 0 {
		position = 0
	}
	lane := last.Lanes[position]
	if lane.Route.Alias == "lead" || lane.Session == "" || slices.Contains(g.authors(), lane.Route.Model) ||
		slices.Contains(cfg.ReviewersExcluded, lane.Route.Alias) {
		return false, nil
	}
	dir := filepath.Join(g.dir(), fmt.Sprintf("review-%d-%s", g.Iteration, newID()[:8]))
	path := filepath.Join(dir, "criteria.json")
	if err := writePacket(path, map[string]any{"quality": quality, "checks": g.checkEvidence(), "brief": g.brief(),
		"why": "The criteria or their evidence changed since your review; the code did not. " + g.amendment()}); err != nil {
		return false, err
	}
	timeout, err := timeoutSeconds(input.ReviewerTimeoutS, g.Plan.ReviewerTimeoutS, cfg.Modes[g.Mode].ReviewerTimeoutS)
	if err != nil {
		return false, err
	}
	route := lane.Route
	route.Effort, route.SubstitutedFrom = config.Lower(route.Effort), ""
	prompt := "The criteria you judged, or the evidence for them, changed since your review. The code did not change: do not review it again. " +
		"Read " + path + " and assess every requirement and rule in quality against the code you already read and the current receipts. " +
		"Return JSON {quality: {requirements, rules}} with the same rules as before: every id exactly once, evidence for every verdict, " +
		"and a cause (code, proof or plan) for every requirement that is not verified.\n"
	subs := &substitutions{}
	designDirs := []string{}
	if quality.Browser != nil {
		designDirs = append(designDirs, quality.Browser.Dir)
	}
	result, used, err := subs.invoke(cfg, provider.Call{Route: route, Prompt: prompt, Dir: lane.Dir, Schema: object(map[string]any{"quality": qualitySchema}),
		Timeout: seconds(timeout), Persist: true, Resume: lane.Session, ReadDirs: append([]string{dir, filepath.Join(g.dir(), "checks")}, designDirs...)}, nil)
	if err != nil {
		if ops.IsCancelled(err) || errors.Is(err, ops.ErrTimeout) || errors.Is(err, provider.ErrLimit) {
			return false, err
		}
		return false, nil // the session could not be continued: read the change afresh instead
	}
	var answer struct {
		Quality *QualityReport `json:"quality"`
	}
	if err := decode(result.Answer, &answer); err != nil {
		return false, err
	}
	if err := g.validateQuality(answer.Quality, quality); err != nil {
		return false, err
	}
	renewed := last
	renewed.Lanes = slices.Clone(last.Lanes)
	for index := range renewed.Lanes {
		renewed.Lanes[index].Quality = nil
	}
	renewed.Lanes[position].Quality, renewed.Lanes[position].Session = answer.Quality, result.Session
	renewed.Lanes[position].Route.Account = used.Account
	renewed.QualityDigest, renewed.Dir = quality.fingerprint(), dir
	g.Reviews = append(g.Reviews, renewed)
	g.settleDemands()
	g.admitCriteria()
	return true, nil
}
