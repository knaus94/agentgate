package dev

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

// Issue is a risk an architect names in its own design, or a blocker a critic finds in the other one.
type Issue struct {
	ID       string `json:"id"`
	Evidence string `json:"evidence"`
	Remedy   string `json:"remedy"`
}

// BlockerAnswer is what the closing design does with a blocker raised against it.
type BlockerAnswer struct {
	ID       string `json:"id"`
	Resolved bool   `json:"resolved"`
	How      string `json:"how"` // how the design resolves it, or the evidence that it is wrong
}

type archReport struct {
	Summary       string          `json:"summary"`
	Design        string          `json:"design,omitempty"`
	Alternatives  []string        `json:"alternatives,omitempty"`
	Risks         []Issue         `json:"risks,omitempty"`
	Questions     []Question      `json:"questions,omitempty"`
	OpenQuestions []string        `json:"open_questions,omitempty"` // a receipt of 0.19 or earlier
	Answered      []BlockerAnswer `json:"answered,omitempty"`
	PlanChanges   []string        `json:"plan_changes,omitempty"`
	Blockers      []Issue         `json:"blockers,omitempty"`
	Agreements    string          `json:"agreements,omitempty"`
}

type ArchLane struct {
	Round   string            `json:"round"` // design, critique, final or revision
	Route   config.Route      `json:"route"`
	Session string            `json:"session"`
	Dir     string            `json:"dir,omitempty"`     // where the session lives, so a later round can continue it
	Answers map[string]string `json:"answers,omitempty"` // a revision: the user's answers it was given
	Report  archReport        `json:"report"`
}

// Receipt is the stored result of an architecture pass or a UI design, bound to the contract it answered.
type Receipt struct {
	ID            string                  `json:"id"`
	Kind          string                  `json:"kind"` // architecture or ui
	Fingerprint   string                  `json:"fingerprint,omitempty"`
	Mode          string                  `json:"mode,omitempty"`
	Lanes         []ArchLane              `json:"lanes,omitempty"`
	Revised       bool                    `json:"revised,omitempty"`
	Substitutions []provider.Substitution `json:"substitutions,omitempty"`
	Design        *uiDesign               `json:"design,omitempty"`
	Route         *config.Route           `json:"route,omitempty"`
	Revision      int                     `json:"revision,omitempty"`
	UI            string                  `json:"ui,omitempty"` // an architecture pass: the interface design it ran first and designed on
	// Executor is "session" when the design is the session's own and the other rounds challenge it.
	Executor string `json:"executor,omitempty"`
	// Conversation says whether the advisor of a session's plan was shown the conversation, and why not.
	Conversation string `json:"conversation,omitempty"`
	Dir          string `json:"dir"`
}

var receiptID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// designsDir belongs to the checkout, not to one session: a plan designed in one conversation can be
// launched from another. A receipt stays bound to its contract by the fingerprint.
func designsDir() string { return filepath.Join(checkoutDir(""), "designs") }

// loadReceipt reads a receipt of this checkout.
func loadReceipt(id, kind string) (*Receipt, error) {
	if !receiptID.MatchString(id) {
		return nil, fmt.Errorf("unknown %s receipt for this checkout: %q", kind, id)
	}
	raw, err := os.ReadFile(filepath.Join(designsDir(), id, "receipt.json"))
	receipt := &Receipt{}
	if err != nil || json.Unmarshal(raw, receipt) != nil || receipt.Kind != kind || receipt.ID != id || kind == "ui" && receipt.Design == nil {
		return nil, fmt.Errorf("unknown %s receipt for this checkout: %q", kind, id)
	}
	return receipt, nil
}

func (r *Receipt) save() error {
	if err := os.MkdirAll(r.Dir, 0o700); err != nil {
		return err
	}
	return ops.WriteJSON(filepath.Join(r.Dir, "receipt.json"), r)
}

// fingerprint binds an architecture receipt to what was asked, not to file contents or to how the work is
// split: source drift after a long pass, a stage added later, an intermediate criterion or a file a stream
// turned out to need must not throw the design away. withFiles is the binding of 0.19 and earlier, which also
// held a one-stage plan to its files.
func (r *Resolved) fingerprint(rules []reviewRule, withFiles bool) string {
	parts := map[string]any{"brief": r.Plan.Brief, "ui": r.Plan.UI, "mode": r.Mode}
	if ui := r.Plan.UI; ui != nil && ui.Usage != nil {
		// An interface with jobs is designed in the same pass: the architecture answers the jobs, not the lead's
		// acceptance of the design that came out of the pass.
		parts["ui"] = map[string]any{"usage": ui.Usage}
	}
	if withFiles && len(r.Plan.Stages) == 1 {
		parts["files"] = r.Plan.Files()
	}
	if len(rules) > 0 {
		policies := map[string]any{}
		for _, rule := range rules {
			policies[rule.ID] = map[string]any{"mode": rule.Mode, "scope": r.Config.ReviewRules[rule.ID].Files,
				"instructions": rule.Instructions, "unavailable": rule.Unavailable}
		}
		parts["review_rules"] = policies
	}
	return digest(parts)
}

func (r *Resolved) checkArchitectureContract(fingerprint string) error {
	rules, err := loadReviewRules(r.Config, r.Plan.Files())
	if err != nil {
		return err
	}
	if fingerprint != r.fingerprint(rules, false) && fingerprint != r.fingerprint(rules, true) {
		return errors.New("the brief, ui, mode or project rules changed since the architecture pass: `agentgate architecture` with {revise, notes} " +
			"lets its architect bring the design to the present contract")
	}
	return nil
}

var issuesSchema = items(map[string]any{"id": text, "evidence": text, "remedy": text})

// designSchema is the answer of every round that returns a design: the first one, the closing one and a revision.
var designSchema = object(map[string]any{"summary": text, "design": text, "alternatives": list(text), "risks": issuesSchema,
	"questions": questionsSchema, "answered": items(map[string]any{"id": text, "resolved": boolean, "how": text}), "plan_changes": list(text)})

var critiqueSchema = object(map[string]any{"summary": text, "agreements": text, "blockers": issuesSchema})

// pass is one call of `architecture`: the plan it designs for, the receipt it writes and how a round is run.
type pass struct {
	cfg      *config.Config
	resolved *Resolved
	receipt  *Receipt
	packet   map[string]any
	timeout  time.Duration
	subs     *substitutions
	prompt   string   // the brief of its rounds; default: architecture
	reads    []string // directories a round must be able to read besides its own
}

// newPass binds a pass to the plan as it is now. The architect is told what the preparation found — the named
// risks and the load — and reads the code itself: a prepared map saves an expensive model no reading.
func newPass(cfg *config.Config, source *Plan, executor string) (*pass, error) {
	resolved, err := resolveAs(cfg, source, executor)
	if err != nil {
		return nil, err
	}
	plan := resolved.Plan
	ui, err := loadUI(plan)
	if err != nil {
		return nil, err
	}
	rules, err := loadReviewRules(cfg, plan.Files())
	if err != nil {
		return nil, err
	}
	timeout := seconds(resolved.ModeConfig().ArchitectureTimeoutS)
	if timeout == 0 {
		timeout = seconds(timeoutDefault)
	}
	receipt := &Receipt{ID: newID(), Kind: "architecture", Fingerprint: resolved.fingerprint(rules, false), Mode: resolved.Mode}
	receipt.Dir = filepath.Join(designsDir(), receipt.ID)
	packet := map[string]any{"brief": plan.Brief, "assessment": plan.Assessment, "requirements": plan.Requirements, "files": plan.Files(),
		"streams": plan.allStreams(), "context": plan.Context, "ui": ui, "review_rules": rules}
	if len(plan.Stages) > 1 {
		packet["stages"] = plan.Stages
	}
	return &pass{cfg: cfg, resolved: resolved, receipt: receipt, packet: packet, timeout: timeout, subs: &substitutions{}}, nil
}

// run is one round of one architect. A round after the first continues the session of the architect that
// already read the code, instead of paying for that reading again.
func (p *pass) run(round, name string, route config.Route, extra map[string]any, schema any, from *ArchLane) (ArchLane, error) {
	body := map[string]any{"round": round}
	for key, value := range p.packet {
		body[key] = value
	}
	for key, value := range extra {
		body[key] = value
	}
	key := digest(map[string]any{"fingerprint": p.receipt.Fingerprint, "route": route.String(), "body": body})
	cache := filepath.Join(designsDir(), "cache", key+".json")
	lane := ArchLane{}
	if raw, err := os.ReadFile(cache); err == nil && json.Unmarshal(raw, &lane) == nil {
		return lane, nil // a completed pass of an interrupted run is never paid for twice
	}
	dir := filepath.Join(p.receipt.Dir, name)
	if err := writePacket(filepath.Join(dir, "packet.json"), body); err != nil {
		return lane, err
	}
	tools, toolDirs, toolNote := workerTools(p.cfg, "architect")
	prompt := assets.Prompt(cmp.Or(p.prompt, "architecture")) + "\nYour round: " + round + ". Read the packet at " + filepath.Join(dir, "packet.json") + ".\n" + toolNote
	call := provider.Call{Route: route, Prompt: prompt, Dir: dir, Schema: schema, Timeout: p.timeout, MCP: tools, Writable: toolDirs, Persist: true,
		ReadDirs: p.reads}
	var result provider.Result
	var used config.Route
	err := errors.New("no session to continue")
	if from != nil && from.Session != "" && dirExists(from.Dir) {
		again := call
		again.Dir, again.Resume, again.ReadDirs = from.Dir, from.Session, append([]string{dir}, p.reads...)
		again.Route = from.Route // the model and the account the session belongs to
		again.Route.Effort, again.Route.SubstitutedFrom = route.Effort, ""
		again.Prompt = "Your next round: " + round + ". Its rules are in your first instructions. Read the packet at " +
			filepath.Join(dir, "packet.json") + " and return JSON matching the schema of this turn.\n" + toolNote
		if result, used, err = p.subs.invoke(p.cfg, again, nil); err == nil {
			dir = from.Dir
		} else if ops.IsCancelled(err) || errors.Is(err, ops.ErrTimeout) {
			return lane, err
		}
	}
	if err != nil { // nothing to continue, or the session could not be continued: a fresh architect
		if result, used, err = p.subs.invoke(p.cfg, call, nil); err != nil {
			return lane, err
		}
	}
	if result.Dir != "" {
		dir = result.Dir
	}
	lane = ArchLane{Round: round, Route: used, Session: result.Session, Dir: dir}
	if err := decode(result.Answer, &lane.Report); err != nil {
		return lane, err
	}
	if strings.TrimSpace(lane.Report.Summary) == "" {
		return lane, errors.New("an architecture report came back empty")
	}
	for index := range lane.Report.Blockers {
		lane.Report.Blockers[index].ID = used.Alias + "/" + lane.Report.Blockers[index].ID
	}
	_ = writePacket(cache, lane)
	return lane, nil
}

// finish stores the receipt and returns it as the lead reads it.
func (p *pass) finish() (any, error) {
	p.receipt.Substitutions = p.subs.list
	if err := p.resolved.checkArchitectureContract(p.receipt.Fingerprint); err != nil {
		return nil, err
	}
	return p.receipt.view(), p.receipt.save()
}

// Architecture runs the mode's design pass for a plan. Independent designs run in parallel on different pools;
// in ultra each architect then looks for blockers in the other's design. Two designs are closed by the first
// architect: it reads the other design and the blockers raised against its own, and returns the one design the
// task follows. Nobody else chooses between them.
func Architecture(cfg *config.Config, plan *Plan) (any, error) {
	return architecture(cfg, plan, ArchitectureInput{})
}

// architecture runs the pass of a plan; input carries the session's own design when the session writes the task.
func architecture(cfg *config.Config, plan *Plan, input ArchitectureInput) (any, error) {
	if plan.Revise != "" {
		return nil, errors.New("a revision is its own input: `agentgate architecture` with {revise, answers, notes} (`agentgate reference inputs`)")
	}
	resolved, err := Resolve(cfg, plan)
	if err != nil {
		return nil, err
	}
	mode := resolved.ModeConfig()
	// An interface with jobs is designed first: the architects then design what it needs, on the design itself.
	var ui *Receipt
	if needed := plan.UI; needed != nil && needed.Usage != nil && needed.Design == "" && !resolved.Request.ReviewFirst() && input.Revise == "" {
		if ui, err = designInterface(cfg, DesignInput{Task: resolved.Request.Task, Brief: resolved.Plan.Brief, Usage: needed.Usage,
			Criteria: needed.Criteria, Preserve: needed.Preserve, Files: resolved.Plan.Files(), Context: resolved.Plan.Context, Mode: resolved.Mode}); err != nil {
			return nil, fmt.Errorf("interface design: %w", err)
		}
		if ui.Design.Status != "READY" || (len(mode.Architecture) == 0 && !resolved.counselNeeded()) {
			return interfaceAnswer(resolved.Mode, ui), nil
		}
		designed := *plan
		designed.UI = &UI{Design: ui.ID, Acceptance: "Designed in this pass for the jobs in usage; the user has not seen it yet.", Usage: needed.Usage}
		plan = &designed
	}
	if resolved.Executor == "session" {
		if !resolved.counselNeeded() {
			return map[string]any{"mode": resolved.Mode, "decision": resolved.Decision,
				"next": "Nobody reads the plan in this mode: `start` it."}, nil
		}
		return sessionPass(cfg, plan, input, ui)
	}
	if len(mode.Architecture) == 0 {
		return map[string]any{"mode": resolved.Mode, "decision": resolved.Decision,
			"next": "This mode has no architecture pass: the brief is the design. Launch directly."}, nil
	}
	p, err := newPass(cfg, plan, runningExecutor(plan))
	if err != nil {
		return nil, err
	}
	limit, err := p.resolved.Parallelism()
	if err != nil {
		return nil, err
	}
	routes := make([]config.Route, len(mode.Architecture))
	for index, spec := range mode.Architecture {
		routes[index] = cfg.MustRoute(spec)
	}
	incomplete := func(err error) error {
		return fmt.Errorf("architecture incomplete (finished passes are kept; run it again): %w", err)
	}
	designs := make([]ArchLane, len(routes))
	errs := parallel(len(routes), limit, func(index int) (err error) {
		designs[index], err = p.run("design", fmt.Sprintf("design-%d", index), routes[index], nil, designSchema, nil)
		return err
	})
	if err := firstError(errs); err != nil {
		return nil, incomplete(err)
	}
	p.receipt.Lanes = designs
	if ui != nil {
		p.receipt.UI = ui.ID
	}
	last := &designs[0] // the first architect's session as it stands
	var against []Issue // blockers raised against the first design
	if mode.Critique {
		critiques := make([]ArchLane, len(routes))
		errs := parallel(len(routes), limit, func(index int) (err error) {
			other := designs[len(routes)-1-index].Report
			critic := routes[index]
			critic.Effort = config.Lower(critic.Effort) // judging one finished design takes less than producing one
			critiques[index], err = p.run("critique", fmt.Sprintf("critique-%d", index), critic,
				map[string]any{"design_under_review": other}, critiqueSchema, &designs[index])
			return err
		})
		if err := firstError(errs); err != nil {
			return nil, incomplete(err)
		}
		p.receipt.Lanes = append(p.receipt.Lanes, critiques...)
		last, against = &critiques[0], critiques[len(routes)-1].Report.Blockers
	}
	if len(routes) > 1 {
		final, err := p.run("final", "final", routes[0], map[string]any{"other_design": designs[1].Report, "blockers": against}, designSchema, last)
		if err != nil {
			return nil, incomplete(err)
		}
		// Every blocker gets exactly one answer, under its own id: an architect may name it without the critic's
		// alias, and a blocker the closing design passes over in silence is not resolved by that silence.
		answered := make([]BlockerAnswer, len(against))
		for index, issue := range against {
			answered[index] = BlockerAnswer{ID: issue.ID}
			for _, answer := range final.Report.Answered {
				if answer.ID == issue.ID || strings.HasSuffix(issue.ID, "/"+answer.ID) {
					answered[index].Resolved, answered[index].How = answer.Resolved, answer.How
				}
			}
		}
		final.Report.Answered = answered
		p.receipt.Lanes = append(p.receipt.Lanes, final)
	}
	return p.finish()
}

// ArchitectureInput asks the architect of a design to continue it: with the user's answers to its questions,
// or with what the user said about the design.
type ArchitectureInput struct {
	Plan    string            `json:"plan,omitempty"` // a saved plan; default: the plan of this session's task
	Revise  string            `json:"revise,omitempty"`
	Answers map[string]string `json:"answers,omitempty"` // question id -> the user's answer
	Notes   string            `json:"notes,omitempty"`
	// A task the session writes: the session's own design, and its answers to the blockers raised against it.
	Design       string          `json:"design,omitempty"`
	Alternatives []string        `json:"alternatives,omitempty"`
	Risks        []Issue         `json:"risks,omitempty"`
	Answered     []BlockerAnswer `json:"answered,omitempty"`
}

// ArchitectureRevise continues the session of the architect whose design a receipt accepts. It is how a design
// changes, before a task and during one: the lead carries the user's words, the architect reworks the design
// and says what the plan needs. The new receipt is bound to the contract as it is now.
func ArchitectureRevise(cfg *config.Config, input ArchitectureInput) (any, error) {
	gate, err := LoadCurrent()
	if err != nil {
		return nil, err
	}
	previous, err := loadReceipt(input.Revise, "architecture")
	if err != nil {
		return nil, err
	}
	if len(input.Answers) == 0 && strings.TrimSpace(input.Notes) == "" {
		return nil, errors.New("a revision needs answers {question id: the user's answer} or notes: what the user said about the design")
	}
	var plan *Plan
	executor := "" // the task's own, when the design revised is the design of this session's task
	extra := map[string]any{"notes": input.Notes}
	switch {
	case input.Plan != "":
		path, err := PlanFile(input.Plan)
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("no saved plan %q: %w", input.Plan, err)
		}
		plan = &Plan{}
		if err := json.Unmarshal(raw, plan); err != nil {
			return nil, err
		}
	case gate != nil && (gate.active() || gate.Step == StepDone && gate.Landed == "" && !gate.Dropped):
		// A finished task that is not landed yet still takes new work: what the revision changes becomes its next stage.
		plan, executor = gate.Plan, gate.executor()
		var accepted []string
		for _, record := range gate.Accepted {
			accepted = append(accepted, record.Name)
		}
		extra["task"] = map[string]any{"step": gate.Step, "position": gate.position(), "accepted_stages": accepted}
	case gate != nil:
		state := gate.Step
		if gate.Landed != "" {
			state += ", landed (" + gate.Landed + ")"
		} else if gate.Dropped {
			state += ", dropped"
		}
		return nil, fmt.Errorf("this session's task is %s: its design is no longer revised; a new task starts from a saved plan named in `plan`", state)
	default:
		return nil, errors.New("this session has no task: name the saved plan in `plan`")
	}
	base := ""
	if plan.Architecture != nil && plan.Architecture.ID == previous.ID {
		base = plan.Architecture.Base
	}
	design := previous.accepted(base)
	if design == nil {
		return nil, errors.New("that receipt holds no design to revise")
	}
	var answers []map[string]string
	for _, question := range design.Report.Questions {
		if answer, given := input.Answers[question.ID]; given {
			answers = append(answers, map[string]string{"id": question.ID, "question": question.Question, "recommended": question.Recommended, "answer": answer})
		}
	}
	if len(answers) != len(input.Answers) {
		var known []string
		for _, question := range design.Report.Questions {
			known = append(known, question.ID)
		}
		return nil, fmt.Errorf("answers name a question that design does not ask; its questions: %s", strings.Join(known, ", "))
	}
	extra["answers"], extra["prior"] = answers, design.Report
	p, err := newPass(cfg, plan, cmp.Or(executor, runningExecutor(plan)))
	if err != nil {
		return nil, err
	}
	mode := p.resolved.ModeConfig()
	if len(mode.Architecture) == 0 {
		return nil, fmt.Errorf("%s has no architect: `prepare` with notes revises the plan", p.resolved.Mode)
	}
	lane, err := p.run("revision", "revision", cfg.MustRoute(mode.Architecture[0]), extra, designSchema, design)
	if err != nil {
		return nil, err
	}
	lane.Answers, lane.Report.Answered = input.Answers, nil
	p.receipt.Lanes, p.receipt.Revised = append(previous.Lanes, lane), true
	return p.finish()
}

func (r *Receipt) blockers() map[string]Issue {
	out := map[string]Issue{}
	for _, lane := range r.Lanes {
		for _, issue := range lane.Report.Blockers {
			out[issue.ID] = issue
		}
	}
	return out
}

// disputed are the blockers the closing design rejects, in the order they were answered: the architects
// disagree, so the decision is the user's.
func (r *Receipt) disputed() []BlockerAnswer {
	var out []BlockerAnswer
	for _, lane := range r.Lanes {
		for _, answer := range lane.Report.Answered {
			if !answer.Resolved {
				out = append(out, answer)
			}
		}
	}
	return out
}

// open are the questions of a design that still wait for the user: a revision has taken the ones it was given.
func (lane *ArchLane) open() []Question {
	out := []Question{}
	for _, question := range lane.Report.Questions {
		if _, taken := lane.Answers[question.ID]; !taken {
			out = append(out, question)
		}
	}
	return out
}

// recommended says whether the user's answer is the option the design is built on. A client's question tool
// marks that option in its label, so the mark is not a difference.
func recommended(answer, option string) bool {
	plain := func(text string) string {
		text = strings.ToLower(strings.TrimSpace(text))
		return strings.TrimRight(strings.TrimSpace(strings.TrimSuffix(text, "(recommended)")), ".")
	}
	return plain(answer) == plain(option)
}

// requireArchitecture refuses to start writers before the mode's design is accepted: every question of the
// architect has the user's answer, the design is built on those answers, and a blocker the architects
// disagree about is decided.
func (r *Resolved) requireArchitecture() error {
	if r.Executor == "session" {
		return nil // the session's plan is read once, before the task starts: requireCounsel
	}
	if len(r.ModeConfig().Architecture) == 0 || r.Request.ReviewFirst() {
		return nil
	}
	accepted := r.Plan.Architecture
	if accepted == nil || accepted.ID == "" {
		return fmt.Errorf("%s needs an architecture pass: run `agentgate architecture` and add architecture {id} to the plan", r.Mode)
	}
	receipt, err := loadReceipt(accepted.ID, "architecture")
	if err != nil {
		return err
	}
	if err := r.checkArchitectureContract(receipt.Fingerprint); err != nil {
		return err
	}
	lane := receipt.accepted(accepted.Base)
	if lane == nil {
		return fmt.Errorf("architecture.base %q names no design of that receipt", accepted.Base)
	}
	var unanswered, differ []string
	for _, question := range lane.open() {
		switch answer := strings.TrimSpace(accepted.Answers[question.ID]); {
		case answer == "":
			unanswered = append(unanswered, question.ID)
		case !recommended(answer, question.Recommended):
			differ = append(differ, question.ID)
		}
	}
	if len(unanswered) > 0 {
		return fmt.Errorf("the architect's questions wait for the user: %s. Ask them as written (`agentgate show architecture`), then add "+
			"architecture.answers {id: answer}", strings.Join(unanswered, ", "))
	}
	if len(differ) > 0 {
		return fmt.Errorf("the design is built on the recommended answers, and the user chose otherwise on %s: `agentgate architecture` with "+
			"{revise: %q, answers} lets the architect rework it; accept the receipt it returns", strings.Join(differ, ", "), receipt.ID)
	}
	var open []string
	if lane.Round == "design" {
		// A receipt of 0.19 or earlier, where two designs had no closing round: every blocker needs an answer.
		for id := range receipt.blockers() {
			if strings.TrimSpace(accepted.Blockers[id]) == "" {
				open = append(open, id)
			}
		}
	} else {
		for _, answer := range receipt.disputed() {
			if strings.TrimSpace(accepted.Blockers[answer.ID]) == "" {
				open = append(open, answer.ID)
			}
		}
	}
	if len(open) > 0 {
		slices.Sort(open)
		return fmt.Errorf("the architects disagree about a blocker; record the user's decision in architecture.blockers: %s", strings.Join(open, ", "))
	}
	return nil
}

// accepted returns the one design the task follows: the latest revision, else the design that closed the pass,
// else the first design. base selects among the designs of a receipt that has no closing round.
func (r *Receipt) accepted(base string) *ArchLane {
	var design, closing *ArchLane
	for index := range r.Lanes {
		lane := &r.Lanes[index]
		switch {
		case lane.Round == "final" || lane.Round == "revision":
			closing = lane
		case lane.Round == "design" && (design == nil && base == "" || lane.Route.Alias == base):
			design = lane
		}
	}
	if closing != nil {
		return closing
	}
	return design
}

// ArchitectureView is an architecture receipt as the lead reads it: the design the task follows, the questions
// it asks the user, every round for the record, and the plan entry that accepts it. It is the receipt the plan
// accepted, or else the newest of this checkout.
func ArchitectureView(plan *Plan) (map[string]any, error) {
	var receipt *Receipt
	if plan != nil && plan.Architecture != nil {
		receipt, _ = loadReceipt(plan.Architecture.ID, "architecture")
	}
	if receipt == nil {
		paths, _ := filepath.Glob(filepath.Join(designsDir(), "*", "receipt.json"))
		var newest time.Time
		for _, path := range paths {
			info, err := os.Stat(path)
			candidate, loadErr := loadReceipt(filepath.Base(filepath.Dir(path)), "architecture")
			if err == nil && loadErr == nil && info.ModTime().After(newest) {
				receipt, newest = candidate, info.ModTime()
			}
		}
	}
	if receipt == nil {
		return nil, errors.New("no architecture pass has run in this checkout: `agentgate architecture --plan <name>` runs one")
	}
	return receipt.render(true), nil
}

// interfaceView is an interface design as the lead tells it to the user: the jobs as the designer walked them,
// the screens, what it rejected and what it could not decide.
func interfaceView(ui *Receipt) map[string]any {
	var spec struct {
		Summary string    `json:"summary"`
		Jobs    []jobWalk `json:"jobs"`
		Screens []struct {
			Name     string `json:"name"`
			Location string `json:"location"`
			Purpose  string `json:"purpose"`
		} `json:"screens"`
		Rejected []struct {
			Option string `json:"option"`
			Reason string `json:"reason"`
		} `json:"rejected_alternatives"`
		Questions []string `json:"open_questions"`
	}
	_ = json.Unmarshal(ui.Design.Spec, &spec)
	screens, rejected := []string{}, []string{}
	for _, screen := range spec.Screens {
		screens = append(screens, screen.Name+" ("+screen.Location+"): "+oneLine(screen.Purpose))
	}
	for _, option := range spec.Rejected {
		rejected = append(rejected, option.Option+" — "+oneLine(option.Reason))
	}
	out := map[string]any{"id": ui.ID, "status": ui.Design.Status, "summary": spec.Summary, "jobs": spec.Jobs, "screens": screens,
		"rejected_alternatives": rejected, "receipt_file": filepath.Join(ui.Dir, "receipt.json")}
	if ui.Route != nil {
		out["by"] = ui.Route.String()
	}
	if len(spec.Questions) > 0 {
		out["open_questions"] = spec.Questions
	}
	return out
}

// interfaceAccept is the plan entry that accepts an interface design.
func interfaceAccept(ui *Receipt) map[string]any {
	return map[string]any{"design": ui.ID, "acceptance": "<what the user approved of the interface, in their words>", "usage": ui.Design.Usage}
}

// interfaceAnswer is the answer of a design pass that stopped at the interface: its designer is waiting for
// the user, or the mode has no architect to design the rest.
func interfaceAnswer(mode string, ui *Receipt) map[string]any {
	out := map[string]any{"mode": mode, "ui_design": interfaceView(ui)}
	if ui.Design.Status != "READY" {
		out["next"] = fmt.Sprintf("The designer cannot finish the interface without the user's decisions (`open_questions`). Ask them as written, "+
			"then `design` with {revise: %q, notes: <the user's words>}. Put the READY design into the plan as ui {design, acceptance, usage} "+
			"(`plan save`) and run `architecture --plan` again: it designs the rest on it.", ui.ID)
		return out
	}
	out["input"] = map[string]any{"ui": interfaceAccept(ui)}
	out["next"] = fmt.Sprintf("Show the user the interface: every job as the designer walked it, the screens, what it rejected. Put `input.ui` into "+
		"the plan with the user's words in acceptance (`plan save`), then `start`. A doubt about the interface goes to its designer: "+
		"`design` with {revise: %q, notes: <the user's words>}.", ui.ID)
	return out
}

// view is the receipt as a person reads it, with the input that accepts it. The answer of a pass shows the
// design the task follows in full and every other round in one line; `show architecture` has them all.
func (r *Receipt) view() map[string]any { return r.render(false) }

func (r *Receipt) render(full bool) map[string]any {
	if r.Executor == "session" {
		return r.sessionView()
	}
	issues := func(list []Issue) []string {
		out := []string{}
		for _, issue := range list {
			out = append(out, issue.ID+": "+oneLine(issue.Evidence)+" → "+oneLine(issue.Remedy))
		}
		return out
	}
	lanes := []map[string]any{}
	var bases []string
	followed := r.accepted("")
	for index := range r.Lanes {
		lane := &r.Lanes[index]
		if lane.Round == "design" {
			bases = append(bases, lane.Route.Alias)
		}
		if !full && lane != followed && (followed == nil || followed.Round != "design" || lane.Round != "design") {
			lanes = append(lanes, map[string]any{"round": lane.Round, "by": lane.Route.String(), "summary": clipText(oneLine(lane.Report.Summary), 300)})
			continue
		}
		item := map[string]any{"round": lane.Round, "by": lane.Route.String(), "summary": lane.Report.Summary}
		if lane.Report.Design != "" {
			item["design"] = lane.Report.Design
		}
		if len(lane.Report.Alternatives) > 0 {
			item["alternatives"] = lane.Report.Alternatives
		}
		if len(lane.Report.Risks) > 0 {
			item["risks"] = issues(lane.Report.Risks)
		}
		if len(lane.Report.Questions) > 0 {
			item["questions"] = lane.Report.Questions
		}
		if len(lane.Report.OpenQuestions) > 0 {
			item["open_questions"] = lane.Report.OpenQuestions
		}
		if len(lane.Report.Blockers) > 0 {
			item["blockers"] = issues(lane.Report.Blockers)
		}
		if len(lane.Report.Answered) > 0 {
			item["answered"] = lane.Report.Answered
		}
		if lane.Report.Agreements != "" {
			item["agreements"] = lane.Report.Agreements
		}
		lanes = append(lanes, item)
	}
	out := map[string]any{"id": r.ID, "mode": r.Mode, "lanes": lanes, "substitutions": r.Substitutions, "receipt_file": filepath.Join(r.Dir, "receipt.json")}
	if !full {
		out["history"] = "`agentgate show architecture` prints every round in full"
	}
	accept := map[string]any{"id": r.ID}
	var ui *Receipt
	if r.UI != "" {
		if loaded, err := loadReceipt(r.UI, "ui"); err == nil {
			ui = loaded
			out["ui_design"] = interfaceView(ui)
		}
	}
	raised := r.blockers()
	decide := map[string]string{}
	design := r.accepted("")
	if design == nil {
		return out
	}
	out["follows"] = design.Round + " by " + design.Route.String()
	if design.Round == "design" && len(bases) > 1 { // a receipt of 0.19 or earlier
		accept["base"] = "<the alias of the design the task follows: " + strings.Join(bases, ", ") + ">"
		for id := range raised {
			decide[id] = "<how it is resolved, or why it is wrong>"
		}
	}
	var disputed []map[string]any
	for _, answer := range r.disputed() {
		disputed = append(disputed, map[string]any{"id": answer.ID, "blocker": oneLine(raised[answer.ID].Evidence) + " → " + oneLine(raised[answer.ID].Remedy),
			"architect": answer.How})
		decide[answer.ID] = "<the user's decision>"
	}
	if len(disputed) > 0 {
		out["disputed"] = disputed
	}
	if len(decide) > 0 {
		accept["blockers"] = decide
	}
	if len(design.Report.PlanChanges) > 0 {
		out["plan_changes"] = design.Report.PlanChanges
	}
	next := "Tell the user the design in a few lines: what it changes and what it rejected (`alternatives`). "
	if design.Round == "revision" {
		next = "Tell the user what the architect answered (`summary`) and what changed in the design. "
	}
	if questions := design.open(); len(questions) > 0 {
		answers := map[string]string{}
		for _, question := range questions {
			answers[question.ID] = "<the user's answer: an option as written, or their own words>"
		}
		out["questions"], accept["answers"] = questions, answers
		next = "Ask the user every question as it is written: the options and the recommendation are the architect's. "
	}
	if len(disputed) > 0 {
		next += "The architects disagree about `disputed`: give the user both arguments; their decision goes in `blockers`. "
	}
	next += "Put `input.architecture` into the plan"
	if len(design.Report.PlanChanges) > 0 {
		next += ", and `plan_changes` as the architect worded them"
	}
	next += fmt.Sprintf(" (`plan save`), then `start`; an active task takes the same with `amend`. An answer other than the recommended one, and anything else the user says about the design, goes back to the "+
		"architect, not into the plan: `architecture` with {revise: %q, answers, notes}.", r.ID)
	input := map[string]any{"architecture": accept}
	if ui != nil {
		input["ui"] = interfaceAccept(ui)
		next = "The interface was designed first and the architecture on it (`ui_design`): show the user every job as the designer walked it and " +
			"the screens, with the user's words in `input.ui.acceptance`. " + next + " A doubt about the interface goes to its designer: `design` with {revise, notes}."
	}
	out["input"], out["next"] = input, next
	return out
}

// architectureFor is what writers and reviewers receive: the one design the task follows and the user's
// decisions on it. The other rounds stay in the receipt; handing two designs to a writer would make it choose
// between them again.
func architectureFor(plan *Plan) any {
	if plan.Architecture == nil {
		return nil
	}
	receipt, err := loadReceipt(plan.Architecture.ID, "architecture")
	if err != nil {
		return nil
	}
	lane := receipt.accepted(plan.Architecture.Base)
	if lane == nil {
		return nil
	}
	answers := []map[string]string{}
	questions := lane.open()
	if receipt.Executor == "session" {
		questions = receipt.asked()
	}
	for _, question := range questions {
		if answer := plan.Architecture.Answers[question.ID]; answer != "" {
			answers = append(answers, map[string]string{"question": question.Question, "answer": answer})
		}
	}
	return map[string]any{"design": lane.Report.Design, "risks": lane.Report.Risks, "designed_by": lane.Route.String(),
		"answers": answers, "acceptance": plan.Architecture.Acceptance, "blockers": plan.Architecture.Blockers,
		"rule":        "Follow the design. `answers`, `acceptance` and `blockers` are the user's decisions on it: where the design says otherwise, they win.",
		"all_designs": filepath.Join(receipt.Dir, "receipt.json")}
}
