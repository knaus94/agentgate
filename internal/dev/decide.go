package dev

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/knaus94/agentgate/internal/config"
)

// DecideInput is everything the lead settles in one call: it rules on findings, it judges a patch where no
// reviewer process runs, and it gives work. Whatever is given is applied, then the task runs on.
type DecideInput struct {
	Findings []Disposition `json:"findings,omitempty"` // rulings on findings
	Review   *LeadReview   `json:"review,omitempty"`   // the lead's own reading, in a mode without reviewer processes
	Work     []WorkOrder   `json:"work,omitempty"`     // work for the writers of named streams
}

var decisionsAllowed = []string{"accepted", "rejected", "deferred", "needs-approval"}

// Decide records the lead's decisions and lets the task run on with them.
func Decide(cfg *config.Config, gate *Gate, input DecideInput) (any, error) {
	if err := gate.can(ActDecide); err != nil {
		return nil, err
	}
	if len(input.Findings) == 0 && input.Review == nil && len(input.Work) == 0 {
		return nil, errors.New("decide takes findings [{id, decision, evidence}], review {summary, findings, quality} or work [{stream, note, requirement?, escalate?}]")
	}
	switch {
	case len(input.Work) > 0 && !gate.Request.Writes():
		return nil, errors.New("a review without --fix has no authority to change code: there is no writer to give work to")
	case len(input.Work) > 0 && gate.Step == StepWrite:
		return nil, &Refusal{Action: ActDecide, Instead: gate.actions(),
			Because: "work is given once the writers of the round have reported: `answer` a blocked writer, `run` retries a failed one"}
	case gate.Step == StepDone && input.Review != nil:
		return nil, errors.New("a finished task owes no review; accept a note with findings, or give work")
	}
	// The notes of a finished task are fixed in the same task: it opens again where it ended.
	if gate.Step == StepDone {
		if err := gate.resumeFinished(); err != nil {
			return nil, err
		}
	}
	// A diagnosis given after a correction that went nowhere earns its writers one more step of effort.
	stalled := len(input.Work) > 0 && slices.ContainsFunc(gate.needs(cfg), func(need Need) bool { return need.Kind == "stall" })
	known := map[string]Finding{}
	for _, finding := range gate.Findings() {
		known[finding.ID] = finding
	}
	if input.Review != nil {
		if err := gate.read(cfg, *input.Review, known); err != nil {
			return nil, err
		}
		for _, finding := range gate.Findings() {
			known[finding.ID] = finding
		}
	}
	for _, item := range input.Findings {
		if err := gate.rule(item, known); err != nil {
			return nil, err
		}
	}
	if len(input.Work) == 0 {
		if err := gate.save(); err != nil {
			return nil, err
		}
		return Run(cfg, gate, RunInput{})
	}
	if err := gate.validateWork(cfg, input.Work); err != nil {
		return nil, err
	}
	if stalled {
		gate.Boost++
	}
	gate.proceed()
	note, err := gate.openCorrection(cfg, false, input.Work)
	if err != nil {
		return nil, err
	}
	answer, err := Run(cfg, gate, RunInput{})
	if summary, ok := answer.(map[string]any); ok && err == nil {
		summary["did"] = append([]string{note}, stringList(summary["did"])...)
	}
	return answer, err
}

func stringList(value any) []string {
	list, _ := value.([]string)
	return list
}

// resumeFinished opens a finished task again to fix what it left. Its content is what it was completed with,
// so it continues where it ended; content changed since is verified as a whole first.
func (g *Gate) resumeFinished() error {
	now, err := g.snapshot()
	if err != nil {
		return err
	}
	key, err := g.taskKey(now)
	if err != nil {
		return err
	}
	if key != g.Final.Key {
		return g.reopen()
	}
	if err := g.setStep(StepVerify); err != nil {
		return err
	}
	g.Final, g.Reported = nil, false
	return nil
}

// rule records one ruling of the lead on a finding.
func (g *Gate) rule(item Disposition, known map[string]Finding) error {
	if item.DuplicateOf != "" && item.DuplicateOf != item.ID {
		// A duplicate is closed in favour of its canonical finding, whatever decision it was given; a chain of
		// duplicates points at the finding at its end.
		for seen := map[string]bool{item.ID: true}; g.Dispositions[item.DuplicateOf].DuplicateOf != "" && !seen[item.DuplicateOf]; {
			seen[item.DuplicateOf] = true
			item.DuplicateOf = g.Dispositions[item.DuplicateOf].DuplicateOf
		}
		item.Decision = "rejected"
		if strings.TrimSpace(item.Evidence) == "" {
			item.Evidence = "duplicate of " + item.DuplicateOf
		}
	}
	finding, ok := known[item.ID]
	switch {
	case !ok:
		return fmt.Errorf("finding %q is not a finding of this task; open ones: %s", item.ID, strings.Join(g.openIDs(), ", "))
	case !slices.Contains(decisionsAllowed, item.Decision) || strings.TrimSpace(item.Evidence) == "":
		return fmt.Errorf("finding %q needs a decision (%s) and evidence", item.ID, strings.Join(decisionsAllowed, ", "))
	case finding.Severity == "P3" && item.Decision == "accepted" && strings.TrimSpace(item.Basis) == "":
		return fmt.Errorf("%s is polish: accepting it needs basis naming the agreed criterion or the user's request", item.ID)
	}
	if item.DuplicateOf != "" {
		if _, exists := known[item.DuplicateOf]; !exists || item.DuplicateOf == item.ID {
			return fmt.Errorf("%s: duplicate_of names no other finding of this task: %q", item.ID, item.DuplicateOf)
		}
	}
	files, err := cleanPaths(item.Files)
	if err != nil {
		return err
	}
	for _, file := range files {
		if g.owner(file) == nil {
			return fmt.Errorf("%s is outside the present scope: `amend` {scope} gives it to a stream before a finding is sent there", file)
		}
	}
	if item.Stream != "" && !slices.Contains(g.streamNames(), item.Stream) {
		return fmt.Errorf("%s: stream %q is not a stream of the present position; streams: %s", item.ID, item.Stream, strings.Join(g.streamNames(), ", "))
	}
	if finding.Path == "" && item.Decision == "accepted" && len(files) == 0 && item.Stream == "" && len(g.streams()) > 1 {
		return fmt.Errorf("%s has no source location: an accepted remedy needs stream (one of %s) or files", item.ID, strings.Join(g.streamNames(), ", "))
	}
	item.Files, item.Auto = files, false
	g.Dispositions[item.ID] = item
	switch item.Decision {
	case "rejected":
		delete(g.Disputes, item.ID)
	case "accepted":
		g.Ruled = true // the writers now act on the lead's ruling, not on the attempt before it
	}
	return nil
}

// openIDs are the findings of the present position nobody closed yet, for a refusal that names a wrong one.
func (g *Gate) openIDs() []string {
	var out []string
	for _, finding := range g.Findings() {
		if _, resolved := g.Resolutions[finding.ID]; !resolved && g.Dispositions[finding.ID].Decision != "rejected" {
			out = append(out, finding.ID)
		}
	}
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}

// streamNames are the streams of the present position.
func (g *Gate) streamNames() []string {
	var out []string
	for _, stream := range g.streams() {
		out = append(out, stream.Name)
	}
	return out
}

// validateWork checks the lead's work orders and turns those that name a criterion into tracked work: the
// criterion itself says when it is done.
func (g *Gate) validateWork(cfg *config.Config, work []WorkOrder) error {
	var problems []string
	for _, item := range work {
		switch {
		case !slices.Contains(g.streamNames(), item.Stream):
			problems = append(problems, fmt.Sprintf("work names %q, which is not a stream of the present position; streams: %s", item.Stream, strings.Join(g.streamNames(), ", ")))
		case strings.TrimSpace(item.Note) == "":
			problems = append(problems, fmt.Sprintf("work for %s needs note: what its writer is to do", item.Stream))
		case item.Requirement != "":
			if err := g.demand(cfg, item); err != nil {
				problems = append(problems, err.Error())
			}
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// read applies the lead's own review of the evidence it was handed.
func (g *Gate) read(cfg *config.Config, review LeadReview, known map[string]Finding) error {
	reading := g.Reading
	if reading == nil {
		return errors.New("no review is owed by the lead: this mode has reviewer processes, or `run` has not prepared the evidence yet")
	}
	now, err := g.snapshot()
	if err != nil {
		return err
	}
	key, err := g.reviewKey(now)
	if err != nil {
		return err
	}
	quality, err := g.qualityInputs(cfg)
	if err != nil {
		return err
	}
	if key != reading.Key || g.codeContract() != reading.Contract || quality.fingerprint() != reading.QualityDigest {
		g.Reading = nil
		_ = g.save()
		return errors.New("the files or the criteria changed since the evidence was prepared; `run` prepares it again")
	}
	accepted := g.accepted()
	// Every problem of the review goes back in one answer, findings and criteria alike.
	lane, laneErr := g.leadLane(review, known)
	if err := errors.Join(laneErr, g.validateQuality(review.Quality, quality)); err != nil {
		return err
	}
	lane.Quality = review.Quality
	record := Review{Iteration: g.Iteration, Tree: reading.Tree, Key: key, Contract: reading.Contract, Dir: reading.Dir,
		QualityDigest: reading.QualityDigest, Policy: g.reviewPolicy(cfg), Lanes: []LaneRun{lane}}
	for _, finding := range accepted {
		record.Rechecked = append(record.Rechecked, finding.ID)
	}
	g.applyReview(cfg, record, accepted)
	g.Reading = nil
	return nil
}

// criterionID is the finding that carries the work for an unmet criterion.
func (g *Gate) criterionID(id string) string { return g.ID[:8] + "-criterion-" + id }

// demand turns an unmet criterion into work for the writer the lead named. A criterion that is missing proof
// rather than code is not for this: collect the proof and run again.
func (g *Gate) demand(cfg *config.Config, order WorkOrder) error {
	quality, err := g.qualityInputs(cfg)
	if err != nil {
		return err
	}
	report := g.qualityReport()
	if quality == nil || report == nil {
		return errors.New("there is no assessment of the criteria to act on yet; `run` verifies first")
	}
	demand := Finding{ID: g.criterionID(order.Requirement), Axis: "criterion", Severity: "P1", Confidence: "high", Category: "unmet criterion",
		Remedy: order.Note, Preserve: "everything the accepted contract already delivers"}
	found := false
	var unmet []string
	for _, result := range report.Requirements {
		index := slices.IndexFunc(quality.Requirements, func(r criterion) bool { return r.ID == result.ID })
		if index < 0 || result.Status == "verified" {
			continue
		}
		unmet = append(unmet, result.ID)
		if result.ID == order.Requirement {
			demand.Evidence = fmt.Sprintf("requirement %s is %s: %s", result.ID, result.Status, result.Reason)
			demand.Impact, found = quality.Requirements[index].Text, true
		}
	}
	for _, result := range report.Rules {
		if result.Status == "pass" || result.Status == "not_applicable" {
			continue
		}
		unmet = append(unmet, result.ID)
		if result.ID == order.Requirement {
			demand.Evidence = fmt.Sprintf("project rule %s is %s: %s", result.ID, result.Status, result.Evidence)
			demand.Impact, found = "a mandatory project rule is not satisfied", true
		}
	}
	if !found {
		if len(unmet) == 0 {
			unmet = []string{"none"}
		}
		return fmt.Errorf("%q is not an unmet requirement or rule of the present assessment; unmet: %s", order.Requirement, strings.Join(unmet, ", "))
	}
	g.Demands = slices.DeleteFunc(g.Demands, func(other Finding) bool { return other.ID == demand.ID })
	g.Demands = append(g.Demands, demand)
	delete(g.Resolutions, demand.ID)
	delete(g.Disputes, demand.ID)
	g.Dispositions[demand.ID] = Disposition{ID: demand.ID, Decision: "accepted", Stream: order.Stream, Evidence: "sent to the writer by the lead"}
	g.Ruled = true // the writers act on the lead's work, not on the attempt before it
	return nil
}

// admitCriteria sends a due requirement the reviewer found missing in the code to the writer that owns it, as a
// blocking finding goes to its author. The lead hears of it when no single writer owns it, when the writer
// disputes it and the reviewer keeps it, or when the corrections go nowhere.
func (g *Gate) admitCriteria() {
	report := g.qualityReport()
	if report == nil || !g.Request.Writes() {
		return
	}
	criteria := g.requirementsFor()
	for _, result := range report.Requirements {
		index := slices.IndexFunc(criteria, func(c criterion) bool { return c.ID == result.ID })
		if index < 0 || !criteria[index].Due || result.Status == "verified" || result.Cause != "code" {
			continue
		}
		id := g.criterionID(result.ID)
		if disposition, judged := g.Dispositions[id]; judged && disposition.Decision != "accepted" || g.Disputes[id].Upheld {
			continue
		}
		stream := g.criterionStream(result)
		if stream == "" {
			continue
		}
		demand := Finding{ID: id, Axis: "criterion", Severity: "P1", Confidence: "high", Category: "unmet criterion",
			Evidence: fmt.Sprintf("requirement %s is %s: %s", result.ID, result.Status, result.Reason), Impact: criteria[index].Text,
			Remedy: "Implement what the requirement asks; the reviewer found the code missing.", Preserve: "everything the accepted contract already delivers"}
		g.Demands = slices.DeleteFunc(g.Demands, func(other Finding) bool { return other.ID == id })
		g.Demands = append(g.Demands, demand)
		delete(g.Resolutions, id)
		if _, judged := g.Dispositions[id]; !judged {
			g.Dispositions[id] = Disposition{ID: id, Decision: "accepted", Auto: true, Stream: stream,
				Evidence: "the reviewer found code missing for a due requirement"}
		}
	}
}

// criterionStream is the one stream an unmet requirement belongs to: the owner of the code its evidence cites,
// or the only stream of the position. "" when the reviewer's evidence does not decide it.
func (g *Gate) criterionStream(result RequirementResult) string {
	var owners []string
	for _, proof := range result.Evidence {
		path, _, _ := strings.Cut(proof.Ref, ":")
		if stream := g.owner(path); proof.Kind == "code" && stream != nil && !slices.Contains(owners, stream.Name) {
			owners = append(owners, stream.Name)
		}
	}
	if len(owners) == 0 {
		for _, stream := range g.streams() {
			if !stream.Done {
				owners = append(owners, stream.Name)
			}
		}
	}
	if len(owners) == 1 {
		return owners[0]
	}
	return ""
}

// settleDemands closes the work sent from a criterion once the criterion itself is satisfied, or once it is no
// longer due here. A writer that disputed such work is answered by the criterion's new verdict: still unmet,
// the dispute is upheld.
func (g *Gate) settleDemands() {
	report := g.qualityReport()
	if report == nil {
		return
	}
	criteria := g.requirementsFor()
	for _, demand := range g.Demands {
		id := strings.TrimPrefix(demand.ID, g.ID[:8]+"-criterion-")
		judged, satisfied, reason := false, false, ""
		for _, result := range report.Requirements {
			if result.ID == id {
				judged, satisfied, reason = true, result.Status == "verified", "the requirement is verified: "+result.Reason
				if index := slices.IndexFunc(criteria, func(c criterion) bool { return c.ID == id }); index < 0 || !criteria[index].Due {
					satisfied, reason = true, "the requirement is no longer due at this position"
				}
			}
		}
		for _, result := range report.Rules {
			if result.ID == id {
				judged, satisfied, reason = true, result.Status == "pass" || result.Status == "not_applicable", "the rule is satisfied: "+result.Evidence
			}
		}
		switch dispute, disputed := g.Disputes[demand.ID]; {
		case satisfied:
			g.Resolutions[demand.ID] = reason
			delete(g.Disputes, demand.ID)
		case judged && disputed && !dispute.Upheld:
			dispute.Upheld = true
			g.Disputes[demand.ID] = dispute
			if g.Dispositions[demand.ID].Auto {
				delete(g.Dispositions, demand.ID)
			} else {
				g.Ruled = true
			}
		}
	}
}
