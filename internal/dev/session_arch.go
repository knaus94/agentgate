package dev

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/knaus94/agentgate/internal/config"
)

// When the session writes a task, the plan and the design are its own. The pass that used to produce a design
// challenges one instead: the advisor reads the plan with the conversation in hand, and in heavy and ultra
// critics that were not shown the conversation read the design — in ultra after designing the change themselves.
// The session answers every blocker; one it rejects goes to the user with both arguments.

var counselSchema = object(map[string]any{"summary": text, "agreements": text, "blockers": issuesSchema, "questions": questionsSchema})

// ArchitectureInputs runs the pass for an input that names its plan: the session's design to be challenged,
// its answers to the blockers, or — for a task of sandboxed writers — a revision for the architect.
func ArchitectureInputs(cfg *config.Config, input ArchitectureInput) (any, error) {
	plan, executor, err := inputPlan(cfg, input.Plan)
	if err != nil {
		return nil, err
	}
	if executor == "session" {
		return architecture(cfg, plan, input)
	}
	if input.Revise == "" {
		return nil, errors.New("architecture takes a plan, or {revise, answers, notes} for the architect of a design")
	}
	return ArchitectureRevise(cfg, input)
}

// inputPlan is the plan an input speaks of — the saved one it names, else the plan of this session's task —
// and who writes it.
func inputPlan(cfg *config.Config, name string) (*Plan, string, error) {
	if name != "" {
		plan := savedPlan(name)
		if plan == nil {
			return nil, "", fmt.Errorf("no saved plan %q; `agentgate plan list` shows the saved ones", name)
		}
		request, err := ParseRequest(plan.Request)
		if err != nil {
			return nil, "", err
		}
		return plan, cmp.Or(runningExecutor(plan), executorOf(cfg, request)), nil
	}
	gate, err := LoadCurrent()
	if err != nil {
		return nil, "", err
	}
	if gate == nil {
		return nil, "", errors.New("this session has no task: name the saved plan in `plan`")
	}
	return gate.Plan, gate.executor(), nil
}

// sessionPass has the session's plan read before the work starts, or records how the session closed a reading.
func sessionPass(cfg *config.Config, plan *Plan, input ArchitectureInput, ui *Receipt) (any, error) {
	p, err := newPass(cfg, plan, runningExecutor(plan))
	if err != nil {
		return nil, err
	}
	p.prompt, p.receipt.Executor = "counsel", "session"
	p.packet["mode"], p.packet["stages"], p.packet["grounding"] = p.resolved.Mode, p.resolved.Plan.Stages, plan.Grounding
	if ui != nil {
		p.receipt.UI = ui.ID
	}
	if input.Revise != "" {
		return p.close(input)
	}
	process := p.resolved.ModeConfig()
	if len(process.Architecture) > 0 && strings.TrimSpace(input.Design) == "" {
		return nil, fmt.Errorf("%s has the session's design read by independent critics: give {plan, design, alternatives, risks} — boundaries, data and "+
			"control flow, the contract between stages, the alternatives you weighed and why they lost, the real risks with their remedy", p.resolved.Mode)
	}
	own := archReport{Summary: firstLine(plan.Brief), Design: input.Design, Alternatives: input.Alternatives, Risks: input.Risks}
	session := ArchLane{Round: "design", Route: sessionRoute(), Report: own}
	limit, err := p.resolved.Parallelism()
	if err != nil {
		return nil, err
	}
	advisor := cfg.MustRoute(cfg.Advisor)
	shown := map[string]any{"session_design": own}
	// The advisor is the reader that knows what was said. The conversation goes only to a model of the lead's
	// own provider: it holds the user's words, and they do not cross to another vendor unasked.
	advice := map[string]any{"session_design": own}
	p.receipt.Conversation = unshown(advisor)
	if p.receipt.Conversation == "" {
		p.receipt.Conversation = "the client keeps no record of this session that could be read: the advisor read the plan without the conversation"
		if path := conversation(p.receipt.Dir, opened()); path != "" {
			advice["conversation"], p.reads, p.receipt.Conversation = path, []string{p.receipt.Dir}, "shown to the advisor"
		}
	}
	critics := make([]config.Route, len(process.Architecture))
	for index, spec := range process.Architecture {
		critics[index] = cfg.MustRoute(spec)
	}
	lanes := make([][]ArchLane, len(critics)+1)
	errs := parallel(len(lanes), limit, func(index int) error {
		if index == 0 {
			lane, err := p.run("advise", "advisor", advisor, advice, counselSchema, nil)
			lanes[0] = []ArchLane{lane}
			return err
		}
		critic, name := critics[index-1], fmt.Sprintf("critic-%d", index-1)
		var blind *ArchLane
		if process.Critique {
			// A design made without seeing the session's is what the session's is measured against.
			lane, err := p.run("design", name+"-design", critic, nil, designSchema, nil)
			if err != nil {
				return err
			}
			lanes[index], blind = append(lanes[index], lane), &lane
			critic.Effort = config.Lower(critic.Effort)
		}
		lane, err := p.run("critique", name, critic, shown, counselSchema, blind)
		lanes[index] = append(lanes[index], lane)
		return err
	})
	if err := firstError(errs); err != nil {
		return nil, fmt.Errorf("the reading of the plan is incomplete; run it again (the critics' finished rounds are kept): %w", err)
	}
	p.receipt.Lanes = []ArchLane{session}
	for _, group := range lanes {
		for _, lane := range group {
			for index := range lane.Report.Questions {
				lane.Report.Questions[index].ID = lane.Route.Alias + "/" + lane.Report.Questions[index].ID
			}
			p.receipt.Lanes = append(p.receipt.Lanes, lane)
		}
	}
	return p.finish()
}

// close records the session's answers to the blockers of a reading and binds the record to the plan as it is
// now. No model runs: the plan is the session's, and what it rejects is the user's to decide.
func (p *pass) close(input ArchitectureInput) (any, error) {
	previous, err := loadReceipt(input.Revise, "architecture")
	if err != nil {
		return nil, err
	}
	if previous.Executor != "session" {
		return nil, errors.New("that receipt is an architect's design: `architecture` with {revise, answers, notes} continues it for a task of sandboxed writers")
	}
	raised := previous.blockers()
	var answered []BlockerAnswer
	var missing []string
	for id := range raised {
		position := slices.IndexFunc(input.Answered, func(answer BlockerAnswer) bool {
			return answer.ID == id || strings.HasSuffix(id, "/"+answer.ID)
		})
		if position < 0 || strings.TrimSpace(input.Answered[position].How) == "" {
			missing = append(missing, id)
			continue
		}
		answered = append(answered, BlockerAnswer{ID: id, Resolved: input.Answered[position].Resolved, How: input.Answered[position].How})
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("every blocker gets an answer in answered [{id, resolved, how}] — how the plan now removes it, or the evidence that it is wrong: %s",
			strings.Join(missing, ", "))
	}
	sort.Slice(answered, func(a, b int) bool { return answered[a].ID < answered[b].ID })
	design := previous.accepted("")
	report := archReport{Summary: cmp.Or(strings.TrimSpace(input.Notes), "the session answered the reading of its plan"), Design: input.Design,
		Alternatives: input.Alternatives, Risks: input.Risks, Answered: answered}
	if report.Design == "" && design != nil {
		report.Design, report.Alternatives, report.Risks = design.Report.Design, design.Report.Alternatives, design.Report.Risks
	}
	p.receipt.Lanes = append(slices.Clone(previous.Lanes), ArchLane{Round: "final", Route: sessionRoute(), Answers: input.Answers, Report: report})
	p.receipt.Revised = true
	if p.receipt.UI == "" {
		p.receipt.UI = previous.UI
	}
	return p.finish()
}

// unshown says why a route is not given the lead's conversation: the user's words go only to a model of the
// lead's own provider. "" when it may be given.
func unshown(route config.Route) string {
	if route.Provider == config.Lead() {
		return ""
	}
	return fmt.Sprintf("not shown to %s: it is a %s model and this session is %s; the user's words do not cross to another provider",
		route.String(), route.Provider, config.Lead())
}

// asked are the questions a reading of the session's plan has for the user.
func (r *Receipt) asked() []Question {
	out := []Question{}
	for _, lane := range r.Lanes {
		if lane.Route.Alias != "session" && lane.Round != "design" {
			out = append(out, lane.Report.Questions...)
		}
	}
	return out
}

// settled are the blockers the session answered as resolved in the plan as it stands.
func (r *Receipt) settled() map[string]BlockerAnswer {
	out := map[string]BlockerAnswer{}
	for _, lane := range r.Lanes {
		if lane.Round == "final" {
			for _, answer := range lane.Report.Answered {
				out[answer.ID] = answer
			}
		}
	}
	return out
}

// requireCounsel refuses to start a task the session writes before its plan was read: every question of the
// readers has the user's answer, and every blocker is resolved by the session or decided by the user.
func (r *Resolved) requireCounsel() error {
	if !r.counselNeeded() {
		return nil
	}
	accepted := r.Plan.Architecture
	if accepted == nil || accepted.ID == "" {
		return fmt.Errorf("%s has the plan read before the work starts: `agentgate architecture` with the plan, then add architecture {id} to it", r.Mode)
	}
	receipt, err := loadReceipt(accepted.ID, "architecture")
	if err != nil {
		return err
	}
	if receipt.Executor != "session" {
		return errors.New("that receipt is an architect's design for sandboxed writers, not a reading of this plan: run `agentgate architecture` with the plan")
	}
	rules, err := loadReviewRules(r.Config, r.Plan.Files())
	if err != nil {
		return err
	}
	if receipt.Fingerprint != r.fingerprint(rules, false) {
		return fmt.Errorf("the brief, the interface, the mode or the project rules changed since the plan was read: `agentgate architecture` with "+
			"{revise: %q, plan, answered, notes} binds the record to the plan as it is now, and the receipt it returns goes into the plan", receipt.ID)
	}
	var unanswered []string
	for _, question := range receipt.asked() {
		if strings.TrimSpace(accepted.Answers[question.ID]) == "" {
			unanswered = append(unanswered, question.ID)
		}
	}
	if len(unanswered) > 0 {
		return fmt.Errorf("the readers' questions wait for the user: %s. Ask them as written (`agentgate show architecture`), then add architecture.answers {id: answer}",
			strings.Join(unanswered, ", "))
	}
	settled := receipt.settled()
	var open []string
	for id := range receipt.blockers() {
		if !settled[id].Resolved && strings.TrimSpace(accepted.Blockers[id]) == "" {
			open = append(open, id)
		}
	}
	if len(open) > 0 {
		sort.Strings(open)
		return fmt.Errorf("blockers of the reading are open: %s. Change the plan where one is right, then `agentgate architecture` with "+
			"{revise: %q, plan, answered: [{id, resolved, how}]}; one you reject is the user's to decide, and their decision goes in architecture.blockers",
			strings.Join(open, ", "), receipt.ID)
	}
	return nil
}

// sessionView is a reading of the session's plan as the session takes it to the user.
func (r *Receipt) sessionView() map[string]any {
	out := map[string]any{"id": r.ID, "mode": r.Mode, "substitutions": r.Substitutions, "receipt_file": filepath.Join(r.Dir, "receipt.json")}
	settled := r.settled()
	var readers []string
	blockers, designs, agreements := []map[string]any{}, []map[string]any{}, []string{}
	open, rejected := 0, map[string]string{}
	for _, lane := range r.Lanes {
		if lane.Route.Alias == "session" {
			continue
		}
		if name := lane.Route.String(); !slices.Contains(readers, name) {
			readers = append(readers, name)
		}
		if lane.Round == "design" {
			designs = append(designs, map[string]any{"by": lane.Route.String(), "design": lane.Report.Design, "alternatives": lane.Report.Alternatives})
			continue
		}
		if lane.Report.Agreements != "" {
			agreements = append(agreements, lane.Route.String()+": "+oneLine(lane.Report.Agreements))
		}
		for _, issue := range lane.Report.Blockers {
			item := map[string]any{"id": issue.ID, "evidence": issue.Evidence, "remedy": issue.Remedy}
			switch answer, answered := settled[issue.ID]; {
			case !answered:
				open++
			case answer.Resolved:
				item["resolved"] = answer.How
			default:
				item["rejected_by_the_session"] = answer.How
				rejected[issue.ID] = "<the user's decision>"
			}
			blockers = append(blockers, item)
		}
	}
	out["read_by"], out["blockers"], out["agreements"] = readers, blockers, agreements
	if r.Conversation != "" {
		out["conversation"] = r.Conversation
	}
	if len(designs) > 0 {
		out["independent_designs"] = designs
	}
	accept := map[string]any{"id": r.ID}
	questions := r.asked()
	if len(questions) > 0 {
		answers := map[string]string{}
		for _, question := range questions {
			answers[question.ID] = "<the user's answer: an option as written, or their own words>"
		}
		out["questions"], accept["answers"] = questions, answers
	}
	if len(rejected) > 0 {
		accept["blockers"] = rejected
	}
	input := map[string]any{"architecture": accept, "approved": "<the user's words approving the plan>"}
	if r.UI != "" {
		if ui, err := loadReceipt(r.UI, "ui"); err == nil {
			out["ui_design"], input["ui"] = interfaceView(ui), interfaceAccept(ui)
		}
	}
	var next string
	switch {
	case open > 0:
		next = fmt.Sprintf("Check every blocker against the code. Where one is right, change the plan (`plan save`); where one is wrong, keep the plan and say why. "+
			"Then `architecture` with {revise: %q, plan: <name>, answered: [{id, resolved, how}]}: no model runs, the record is bound to the plan as it is. ", r.ID)
		out["input"] = map[string]any{"revise": r.ID, "plan": "<the saved plan>", "answered": []map[string]any{{"id": "<blocker id>", "resolved": true,
			"how": "<how the plan now removes it, or the evidence that it is wrong>"}}}
	default:
		next = "Show the user the plan (`brief`) and what the readers said. "
		if len(rejected) > 0 {
			next += "You rejected blockers: give the user both arguments; their decision goes in `architecture.blockers`. "
		}
		if len(questions) > 0 {
			next += "Ask every question as it is written; the answers go in `architecture.answers`, and what they change goes into the plan. "
		}
		next += "On the user's approval put `input` into the plan (`plan save`) and `start`."
		out["input"] = input
	}
	if len(designs) > 0 {
		next += " `independent_designs` were made without seeing yours: take what is better and say in your answer what you took."
	}
	out["next"] = next
	return out
}
