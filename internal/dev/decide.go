package dev

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/knaus94/agentgate/internal/config"
)

// CriterionDecision sends an unmet requirement or rule to the writers as work.
type CriterionDecision struct {
	ID     string `json:"id"`
	Action string `json:"action"` // fix
	Note   string `json:"note,omitempty"`
}

// DecideInput is everything the lead settles in one call. Whatever is given is applied, then the task runs on.
type DecideInput struct {
	Findings     []Disposition       `json:"findings,omitempty"`     // rulings on findings
	Review       *LeadReview         `json:"review,omitempty"`       // the lead's own reading, in a mode without reviewer processes
	Requirements []CriterionDecision `json:"requirements,omitempty"` // unmet criteria that are missing code, not missing proof
	Reassessment *Reassessment       `json:"reassessment,omitempty"` // a new diagnosis after a correction that went nowhere
	Escalate     bool                `json:"escalate,omitempty"`     // one more step of effort for the next correction
	Brief        string              `json:"brief,omitempty"`        // a decision or constraint for the next correction
	Streams      []string            `json:"streams,omitempty"`      // which streams repair a failing check; default: all
}

var decisionsAllowed = []string{"accepted", "rejected", "deferred", "needs-approval"}

// Decide records the lead's decisions and lets the task run on with them.
func Decide(cfg *config.Config, gate *Gate, input DecideInput) (any, error) {
	if err := gate.can(ActDecide); err != nil {
		return nil, err
	}
	if len(input.Findings) == 0 && input.Review == nil && len(input.Requirements) == 0 && input.Reassessment == nil && !input.Escalate &&
		strings.TrimSpace(input.Brief) == "" && len(input.Streams) == 0 {
		return nil, errors.New("decide needs at least one of findings, review, requirements, reassessment, escalate, brief or streams")
	}
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
		if item.DuplicateOf != "" && item.DuplicateOf != item.ID {
			// A duplicate is closed in favour of its canonical finding, whatever decision it was given; a chain of
			// duplicates points at the finding at its end.
			for seen := map[string]bool{item.ID: true}; gate.Dispositions[item.DuplicateOf].DuplicateOf != "" && !seen[item.DuplicateOf]; {
				seen[item.DuplicateOf] = true
				item.DuplicateOf = gate.Dispositions[item.DuplicateOf].DuplicateOf
			}
			item.Decision = "rejected"
			if strings.TrimSpace(item.Evidence) == "" {
				item.Evidence = "duplicate of " + item.DuplicateOf
			}
		}
		finding, ok := known[item.ID]
		switch {
		case !ok || !slices.Contains(decisionsAllowed, item.Decision) || strings.TrimSpace(item.Evidence) == "":
			return nil, fmt.Errorf("finding %q needs a known id, a decision (%s) and evidence", item.ID, strings.Join(decisionsAllowed, ", "))
		case finding.Severity == "P3" && item.Decision == "accepted" && strings.TrimSpace(item.Basis) == "":
			return nil, fmt.Errorf("%s is polish: accepting it needs basis naming the agreed criterion or the user's request", item.ID)
		case finding.Path == "" && item.Decision == "accepted" && len(item.Files) == 0 && len(gate.streams()) > 1:
			return nil, fmt.Errorf("%s has no source location: an accepted remedy needs files, to say which stream it goes to", item.ID)
		}
		if item.DuplicateOf != "" {
			if _, exists := known[item.DuplicateOf]; !exists || item.DuplicateOf == item.ID {
				return nil, fmt.Errorf("%s: duplicate_of names no other finding of this task: %q", item.ID, item.DuplicateOf)
			}
		}
		files, err := cleanPaths(item.Files)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if gate.owner(file) == nil {
				return nil, fmt.Errorf("%s is outside the present scope: `amend` the plan to add it to a stream before sending a finding there", file)
			}
		}
		item.Files, item.Auto = files, false
		gate.Dispositions[item.ID] = item
		switch item.Decision {
		case "rejected":
			delete(gate.Disputes, item.ID)
		case "accepted":
			gate.Ruled = true // the writers now act on the lead's ruling, not on the attempt before it
		}
	}
	if len(input.Requirements) > 0 {
		if err := gate.demand(cfg, input.Requirements); err != nil {
			return nil, err
		}
	}
	if input.Reassessment != nil {
		if strings.TrimSpace(input.Reassessment.Evidence) == "" || strings.TrimSpace(input.Reassessment.ChangedApproach) == "" {
			return nil, errors.New("reassessment needs evidence — what the new diagnosis found — and changed_approach")
		}
		gate.Reassessed = input.Reassessment
	}
	for _, name := range input.Streams {
		if !slices.ContainsFunc(gate.streams(), func(stream Stream) bool { return stream.Name == name }) {
			return nil, fmt.Errorf("streams names no stream of the present stage: %q", name)
		}
	}
	if len(input.Streams) > 0 {
		gate.FixStreams = input.Streams
	}
	if strings.TrimSpace(input.Brief) != "" {
		gate.Remedy = input.Brief
	}
	gate.Escalate = gate.Escalate || input.Escalate
	if err := gate.save(); err != nil {
		return nil, err
	}
	return Run(cfg, gate, RunInput{})
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

// demand turns unmet criteria into work for the writers. A criterion that is missing proof rather than code
// is not for this: collect the proof and run again.
func (g *Gate) demand(cfg *config.Config, decisions []CriterionDecision) error {
	quality, err := g.qualityInputs(cfg)
	if err != nil {
		return err
	}
	report := g.qualityReport()
	if quality == nil || report == nil {
		return errors.New("there is no assessment of the criteria to act on yet; `run` verifies first")
	}
	for _, decision := range decisions {
		if decision.Action != "fix" {
			return fmt.Errorf("requirements[].action is \"fix\": send the unmet criterion %q to the writers. Other answers are `amend` (the plan is wrong) or new evidence and `run`", decision.ID)
		}
		demand := Finding{ID: g.ID[:8] + "-criterion-" + decision.ID, Axis: "criterion", Severity: "P1", Confidence: "high", Category: "unmet criterion",
			Remedy: decision.Note, Preserve: "everything the accepted contract already delivers"}
		found := false
		for _, result := range report.Requirements {
			index := slices.IndexFunc(quality.Requirements, func(r criterion) bool { return r.ID == result.ID })
			if result.ID == decision.ID && index >= 0 && result.Status != "verified" {
				demand.Evidence = fmt.Sprintf("requirement %s is %s: %s", result.ID, result.Status, result.Reason)
				demand.Impact, found = quality.Requirements[index].Text, true
			}
		}
		for _, result := range report.Rules {
			if result.ID == decision.ID && result.Status != "pass" && result.Status != "not_applicable" {
				demand.Evidence = fmt.Sprintf("project rule %s is %s: %s", result.ID, result.Status, result.Evidence)
				demand.Impact, found = "a mandatory project rule is not satisfied", true
			}
		}
		if !found {
			return fmt.Errorf("%q is not an unmet requirement or rule of the present assessment", decision.ID)
		}
		if strings.TrimSpace(demand.Remedy) == "" {
			demand.Remedy = "Implement what the criterion asks, or make its proof available, without weakening the criterion."
		}
		g.Demands = slices.DeleteFunc(g.Demands, func(other Finding) bool { return other.ID == demand.ID })
		g.Demands = append(g.Demands, demand)
		delete(g.Resolutions, demand.ID)
		g.Dispositions[demand.ID] = Disposition{ID: demand.ID, Decision: "accepted", Evidence: "sent to the writers by the lead"}
		g.Ruled = true // the writers act on the lead's demand, not on the attempt before it
	}
	return nil
}

// settleDemands closes the work the lead demanded from a criterion once the criterion itself is satisfied.
func (g *Gate) settleDemands() {
	report := g.qualityReport()
	if report == nil {
		return
	}
	prefix := g.ID[:8] + "-criterion-"
	for _, demand := range g.Demands {
		id := strings.TrimPrefix(demand.ID, prefix)
		for _, result := range report.Requirements {
			if result.ID == id && result.Status == "verified" {
				g.Resolutions[demand.ID] = "the requirement is verified: " + result.Reason
			}
		}
		for _, result := range report.Rules {
			if result.ID == id && (result.Status == "pass" || result.Status == "not_applicable") {
				g.Resolutions[demand.ID] = "the rule is satisfied: " + result.Evidence
			}
		}
	}
}
