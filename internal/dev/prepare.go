package dev

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

// Preparation is one conversation with a read-only worker that explores the repository and drafts the plan.
// The user's answers, the lead's notes and the validator's corrections all go into the same session, which
// still holds everything the worker read: a plan costs one exploration.

type PrepareInput struct {
	Request        string            `json:"request,omitempty"`
	Name           string            `json:"name,omitempty"`
	Mode           string            `json:"mode,omitempty"` // light, medium, heavy or ultra: the plan carries it, stronger than a flag
	Context        []string          `json:"context,omitempty"`
	Attachments    []string          `json:"attachments,omitempty"` // files the user gave, anywhere on the machine: copied and shown to the worker
	Notes          string            `json:"notes,omitempty"`
	Answers        map[string]string `json:"answers,omitempty"`         // question id -> the user's answer
	RefreshDesigns bool              `json:"refresh_designs,omitempty"` // re-read Figma when the selected design itself changed
	Fresh          bool              `json:"fresh,omitempty"`           // abandon the open preparation and explore again
}

// Setup is what a preparation hands the lead besides the plan: what to ask the user to authorize, and what to
// know. What has to exist before the work can go on is in the plan, as its prerequisites.
type Setup struct {
	Authorizations []Authorization `json:"authorizations"`
	Notes          []string        `json:"notes"`
}

// Authorization is one thing the user has to allow: a heavy check, a database, a stand, new tests.
type Authorization struct {
	What    string   `json:"what"`
	Argv    []string `json:"argv,omitempty"` // the exact command, when it is one
	Catches string   `json:"catches"`        // what running it would catch, so the user can weigh it
}

// setupView is the setup as the lead reads it, with the plan's prerequisites beside it.
func setupView(setup Setup, plan *Plan) map[string]any {
	out := map[string]any{"authorizations": setup.Authorizations, "notes": setup.Notes}
	if setup.Authorizations == nil {
		out["authorizations"] = []Authorization{}
	}
	if setup.Notes == nil {
		out["notes"] = []string{}
	}
	if plan != nil && len(plan.Prerequisites) > 0 {
		out["prerequisites"] = plan.Prerequisites
	}
	return out
}

// Question is a decision only the user can make, in the shape a lead client's question tool takes.
type Question struct {
	ID          string   `json:"id"`
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	Recommended string   `json:"recommended"`
}

// Preparation is what is kept of the conversation between calls.
type Preparation struct {
	ID          string         `json:"id"`
	Request     string         `json:"request"`
	Name        string         `json:"name,omitempty"`
	Mode        string         `json:"mode,omitempty"`        // the lead's choice, kept in every plan this preparation returns
	Attachments []string       `json:"attachments,omitempty"` // copies of the files the user gave
	Route       config.Route   `json:"route"`
	Session     string         `json:"session"`
	Dir         string         `json:"dir"` // the worker's lane, which holds its session
	Status      string         `json:"status"`
	Questions   []Question     `json:"questions,omitempty"`
	Rounds      int            `json:"rounds"`
	UpdatedAt   int64          `json:"updated_at"`
	Answer      map[string]any `json:"answer,omitempty"` // what the last call returned, for `status`
}

// corrections is how many times one call sends validation problems back into the worker's session.
const corrections = 3

const prepareTimeout = 900

var figmaURL = regexp.MustCompile(`https://(?:www\.)?figma\.com/[^\s<>"\x60]+`)

func preparationFile(id string) string {
	return filepath.Join(checkoutDir(""), "preparations", id, "preparation.json")
}

func loadPreparation(id string) *Preparation {
	if !gateIDPattern.MatchString(id) {
		return nil
	}
	raw, err := os.ReadFile(preparationFile(id))
	preparation := &Preparation{}
	if err != nil || json.Unmarshal(raw, preparation) != nil || preparation.ID != id {
		return nil
	}
	return preparation
}

func (p *Preparation) save() error {
	p.UpdatedAt = time.Now().Unix()
	return writePacket(preparationFile(p.ID), p)
}

// answered keeps what a call returns: a lead that lost the answer reads it with `status` instead of running the
// worker again.
func (p *Preparation) answered(out map[string]any) (map[string]any, error) {
	p.Answer = out
	if err := p.save(); err != nil {
		return nil, err
	}
	return out, nil
}

// AddPreparation shows the answer of this session's preparation while it waits on the lead: questions for the
// user, or a prerequisite to resolve. Nothing else says so once an earlier task of the session is done.
func AddPreparation(gate *Gate, out map[string]any) {
	preparation := openPreparation(gate)
	if preparation == nil || preparation.Answer["status"] != "BLOCKED" {
		return
	}
	out["preparation"] = preparation.Answer
	if gate == nil || !gate.active() {
		out["next"] = preparation.Answer["next"]
	}
}

// openPreparation is the conversation a call continues: the one that prepared the active task's plan, or the
// one this lead session left open.
func openPreparation(gate *Gate) *Preparation {
	if gate != nil && gate.active() {
		return loadPreparation(gate.Preparation)
	}
	raw, err := os.ReadFile(filepath.Join(home(), "preparation"))
	if err != nil {
		return nil
	}
	return loadPreparation(strings.TrimSpace(string(raw)))
}

// Prepare delegates repository discovery and plan drafting, not product decisions or execution authority.
// Figma access remains in the host tool; a worker receives only its bounded catalogue, never the profile.
func Prepare(cfg *config.Config, input PrepareInput) (any, error) {
	gate, err := LoadCurrent()
	if err != nil {
		return nil, err
	}
	revising := gate != nil && gate.active()
	// A request is a new task. While this session holds an unfinished one it is continued, not prepared again:
	// preparing the words "continue it" would have a worker rewrite the plan of the task around them.
	if revising && input.Request != "" && input.Request != gate.Plan.Request {
		return nil, fmt.Errorf("this session already has an unfinished task (%s, at %s): `agentgate status` shows where it stands and which actions are legal, "+
			"`run` continues it. To revise its plan, call `prepare` with notes only. To start another task, first end this one with `pause` {reason, cancelled: true} — "+
			"the user's word", gate.ID[:8], gate.Step)
	}
	preparation := openPreparation(gate)
	if input.Fresh || (preparation != nil && !revising && input.Request != "" && input.Request != preparation.Request) {
		preparation = nil
	}
	switch {
	case input.Request != "":
	case preparation != nil:
		input.Request = preparation.Request
	case revising:
		input.Request = gate.Plan.Request
	}
	continued := false // the call goes on with the plan this session already opened
	if draft := loadDraft(); input.Request == "" && preparation == nil && !revising && draft != nil {
		input.Request, continued = draft.Request, true
	}
	request, err := ParseRequest(input.Request)
	if err != nil {
		return nil, err
	}
	// A request that opens no task says which command it is instead, so the lead needs no separate parsing step.
	switch {
	case request.Preview:
		return nil, errors.New("the request asks for a preview: run `agentgate preview` with the same text and report it; start nothing")
	case request.Action == "status" || request.Action == "stop" || request.Action == "help":
		return nil, fmt.Errorf("the request is `%s`: run `agentgate %s`", request.Action, request.Action)
	case request.Action == "research" || request.Action == "verify":
		return nil, fmt.Errorf("`%s` opens no task: ask bounded read-only questions with `agentgate consult` (`agentgate reference inputs`)", request.Action)
	case strings.TrimSpace(request.Task) == "":
		return nil, errors.New("prepare needs the user's request: the text after /dev as it was typed, or {request: \"…\"}")
	}
	switch {
	case revising && gate.sessionWrites():
		return nil, errors.New("this session writes its task: the plan is yours. `amend` changes it — named operations or the complete plan, with `update`; " +
			"`advise` puts a doubt to the advisor")
	case !revising && executorOf(cfg, request) == "session":
		return prepareSession(cfg, input, request, continued)
	}
	context, err := cleanPaths(input.Context)
	if err != nil {
		return nil, err
	}
	if input.Mode != "" && !slices.Contains(config.ModeNames, input.Mode) {
		return nil, fmt.Errorf("mode is one of %s, not %q", strings.Join(config.ModeNames, ", "), input.Mode)
	}
	// A mode is the lead's setting, not something a worker has to find out: the saved plan takes it as it is.
	if input.onlyMode(preparation) {
		if revising {
			return nil, errors.New("the task is under way: `amend` {update, mode} changes its mode; no worker is needed")
		}
		if preparation != nil && preparation.Status == "READY" && preparation.Name != "" {
			return preparation.setMode(cfg, input.Mode)
		}
	}
	if preparation == nil {
		mode := cmp.Or(input.Mode, request.Mode, "medium")
		spec := cfg.Modes[mode].Scout
		if spec == "" {
			spec = cfg.Modes["medium"].Scout
		}
		if spec == "" {
			return nil, errors.New("prepare needs a route for its worker; configure modes.medium.scout")
		}
		id := newID()
		preparation = &Preparation{ID: id, Request: input.Request, Route: cfg.MustRoute(spec),
			Dir: filepath.Join(checkoutDir(""), "preparations", id, "worker")}
	}
	preparation.Answer = nil
	if input.Name != "" {
		preparation.Name = input.Name
	}
	if input.Mode != "" {
		preparation.Mode = input.Mode
	}
	root := filepath.Dir(preparationFile(preparation.ID))
	added, err := preparation.attach(input.Attachments, filepath.Join(root, "attachments"))
	if err != nil {
		return nil, err
	}
	// Pictures go with the prompt where the CLI takes them: all of them to a new session, the new ones after.
	images := imagesOf(added)
	if preparation.Session == "" {
		images = imagesOf(preparation.Attachments)
	}
	packet := map[string]any{"request": input.Request, "context": context, "notes": input.Notes,
		"review_rule_catalogue": reviewRuleCatalogue(cfg)}
	if len(preparation.Attachments) > 0 {
		packet["attachments"] = preparation.Attachments
		packet["attachments_note"] = "Files the user gave with the request. Read every one: a picture is attached to your prompt where your client " +
			"takes pictures, and your file reading tool shows it too. What they show is part of the request."
	}
	if len(input.Answers) > 0 {
		var answers []map[string]string
		for _, question := range preparation.Questions {
			if answer, given := input.Answers[question.ID]; given {
				answers = append(answers, map[string]string{"id": question.ID, "question": question.Question, "answer": answer})
			}
		}
		for id, answer := range input.Answers {
			if !slices.ContainsFunc(preparation.Questions, func(question Question) bool { return question.ID == id }) {
				answers = append(answers, map[string]string{"id": id, "answer": answer})
			}
		}
		packet["answers"] = answers
	}
	designed := false
	if revising {
		packet["current_plan"], packet["position"] = gate.Plan, gate.position()
		packet["step"], packet["accepted_stages"], packet["revisions"] = gate.Step, gate.Accepted, gate.Revisions
		packet["needs"], packet["check_failures"] = gate.needs(cfg), gate.failing()
		if gate.Pause != nil {
			packet["pause"] = gate.Pause
		}
		var writers []map[string]any
		for _, writer := range gate.writerLines() {
			writers = append(writers, writer)
		}
		packet["writers"] = writers
		if len(gate.Plan.Design) > 0 {
			packet["design_context"], designed = gate.designContext(), true
		}
	}
	links := figmaURL.FindAllString(input.Request, -1)
	if revising && len(gate.Plan.Design) > 0 && !input.RefreshDesigns {
		links = nil
		packet["design_source"] = "Reuse the selected packages and design_context of this task. No Figma browser was started; refresh_designs is needed only when the design selection itself must be rediscovered."
	}
	if len(links) > 0 || designed {
		type process struct {
			Command []string `json:"command"`
			Ready   string   `json:"ready,omitempty"`
		}
		var stand struct {
			Command    []string  `json:"command"`
			Ready      string    `json:"ready"`
			EnvFile    string    `json:"env_file"`
			GraphQLEnv []string  `json:"graphql_env"`
			Services   []process `json:"services"`
			Session    *process  `json:"session"`
		}
		if len(cfg.Design.Stand) > 0 {
			if err := json.Unmarshal(cfg.Design.Stand, &stand); err != nil {
				return nil, err
			}
		}
		packet["design_settings"] = map[string]any{"screens": cfg.Design.Screens, "stand": stand}
		designed = true
	}
	if preparation.Session == "" || input.RefreshDesigns {
		var designs []json.RawMessage
		seen := map[string]bool{}
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
		if len(designs) > 0 {
			packet["design_catalogues"] = designs
		}
	}
	instructions := assets.Prompt("prepare")
	if designed {
		instructions += "\n" + assets.Prompt("prepare-design")
	}
	tools, toolDirs, toolNote := workerTools(cfg, "scout")
	subs := &substitutions{}
	var problems PlanProblems
	// The Figma files a node the worker asks for is read from: the request's links, then the plan's designs.
	files := figmaURL.FindAllString(input.Request, -1)
	if revising {
		for _, design := range gate.Plan.Design {
			if design.Figma != "" {
				files = append(files, design.Figma)
			}
		}
	}
	reads := 0
	for attempt := 0; attempt <= corrections; {
		preparation.Rounds++
		path := filepath.Join(root, fmt.Sprintf("packet-%d.json", preparation.Rounds))
		if err := writePacket(path, packet); err != nil {
			return nil, err
		}
		prompt := instructions + "\nRead " + path + ".\n" + toolNote
		if preparation.Session != "" {
			prompt = "New input for the preparation you are working on is at " + path + ": answers to your questions, notes of the lead, the parts of " +
				"the design you asked for or problems the host validator found. You already read the repository; read again only what the new input makes " +
				"necessary. Return the same JSON schema.\n" + toolNote
		}
		result, used, err := subs.invoke(cfg, provider.Call{Route: preparation.Route, Prompt: prompt, Dir: preparation.Dir, Schema: prepareSchema,
			Timeout: seconds(prepareTimeout), Persist: true, Resume: preparation.Session, MCP: tools, Writable: toolDirs, ReadDirs: []string{root}, Images: images}, nil)
		images = nil
		if err != nil {
			if saveErr := preparation.save(); saveErr != nil {
				return nil, errors.Join(err, saveErr)
			}
			return nil, err
		}
		preparation.Session, preparation.Route = result.Session, used
		preparation.Route.SubstitutedFrom = ""
		if result.Dir != "" {
			preparation.Dir = result.Dir
		}
		var answer struct {
			Status    string          `json:"status"`
			Name      string          `json:"name"`
			Summary   string          `json:"summary"`
			Questions []Question      `json:"questions"`
			Setup     Setup           `json:"setup"`
			Requests  []designRequest `json:"design_requests"`
			Plan      json.RawMessage `json:"plan"`
			Pack      *contextPack    `json:"context_pack"`
		}
		if err := decode(result.Answer, &answer); err != nil {
			return nil, err
		}
		if err := preparation.remember(gate); err != nil {
			return nil, err
		}
		// A worker that needs more of the design than its catalogue asks for it, and continues with it: reading a
		// node is the tool's work, not the lead's.
		if answer.Status == "BLOCKED" && len(answer.Questions) == 0 && len(answer.Requests) > 0 && reads < designReads {
			reads++
			packet = map[string]any{"design_catalogues": readDesignRequests(answer.Requests, files),
				"rule": "The parts of the design you asked for. Continue the preparation; ask again only for what is still missing."}
			continue
		}
		if answer.Status == "BLOCKED" && strings.TrimSpace(answer.Summary) != "" {
			preparation.Status, preparation.Questions = "BLOCKED", answer.Questions
			next := "The worker is missing these decisions. Ask the user with your client's question tool, then `prepare` again with answers {id: answer}: " +
				"the same worker continues, it does not explore again. Never infer consent from an empty result."
			if len(answer.Questions) == 0 {
				next = "The worker reports a prerequisite it cannot resolve itself (see summary and setup). Resolve it, then `prepare` again with notes: the same worker continues."
			}
			if len(answer.Questions) == 0 && len(answer.Requests) > 0 {
				next = fmt.Sprintf("The worker asked for more of the design than %d readings of the file gave it (design_requests). Tell the user; a narrower "+
					"link to the frame in the request, or `prepare` with notes that name the frame, lets it continue.", designReads)
			}
			return preparation.answered(map[string]any{"status": "BLOCKED", "summary": answer.Summary, "questions": answer.Questions, "setup": setupView(answer.Setup, nil),
				"design_requests": answer.Requests, "preparation": preparation.ID, "route": used, "substitutions": subs.list, "next": next})
		}
		plan := &Plan{}
		problems = nil
		switch {
		case len(answer.Plan) == 0 || string(answer.Plan) == "null":
			problems = PlanProblems{"READY needs a plan"}
		case answer.Status != "READY" || len(answer.Questions) > 0 || strings.TrimSpace(answer.Summary) == "":
			problems = PlanProblems{"READY needs a summary and no unresolved questions; return BLOCKED with questions when a decision is missing"}
		default:
			if err := json.Unmarshal(answer.Plan, plan); err != nil {
				problems = PlanProblems{"the plan does not match its schema: " + err.Error()}
			}
		}
		var resolved *Resolved
		if problems == nil {
			plan.Request, plan.Mode = input.Request, preparation.Mode // the worker cannot relax the user's mode or request flags
			if revising {
				carry(plan, gate.Plan)
				if strings.TrimSpace(plan.Update) == "" {
					problems = append(problems, "this revises the plan of a running task: set update to what changed and why")
				}
			}
			var err error
			executor := ""
			if revising {
				executor = gate.executor()
			}
			if resolved, err = resolveAs(cfg, plan, executor); err != nil {
				var found PlanProblems
				if !errors.As(err, &found) {
					found = PlanProblems{err.Error()}
				}
				problems = append(problems, found...)
			}
		}
		if problems != nil {
			packet = map[string]any{"validation_problems": problems,
				"rule": "Correct every problem in the plan you returned and return the complete plan again. Keep its scope and decisions; this is a correction of form, not a new investigation."}
			attempt++
			continue
		}
		rules, err := loadReviewRules(cfg, resolved.Plan.Files())
		if err != nil {
			return preparation.answered(map[string]any{"status": "BLOCKED", "summary": err.Error(), "questions": []Question{},
				"setup":        setupView(Setup{Notes: []string{"Make the named project instructions readable, then `prepare` again."}}, nil),
				"review_rules": reviewRuleSummary(cfg, rules), "preparation": preparation.ID, "route": used,
				"next": "Required project instructions are unavailable. Restore the named files; no plan was saved."})
		}
		name := preparation.Name
		if name == "" && revising {
			name = gate.PlanName
		}
		if name == "" {
			name = planNameFrom(answer.Name)
		}
		if previous := savedPlan(name); previous != nil && !revising {
			plan.EnvFile = previous.EnvFile // the lead's file of the checks' variables is not the worker's to drop
		}
		saved, err := PlanSave(cfg, plan, name)
		if err != nil {
			return nil, err
		}
		preparation.Name, preparation.Status, preparation.Questions = saved.Name, "READY", nil
		proposal := &Proposal{Name: saved.Name, Digest: digest(plan), Preparation: preparation.ID, Attachments: preparation.Attachments}
		if previous := loadProposal(saved.Name); previous != nil {
			proposal.ContextPack = previous.ContextPack // a revision that draws no new map keeps the one it had
		}
		if answer.Pack.usable() {
			answer.Pack.Route = used
			proposal.ContextPack = filepath.Join(checkoutDir(""), "packs", saved.Name+".json")
			if err := writePacket(proposal.ContextPack, answer.Pack); err != nil {
				return nil, err
			}
		}
		if err := proposal.save(); err != nil {
			return nil, err
		}
		out := map[string]any{"status": "READY", "summary": answer.Summary, "plan": saved.Name, "brief": saved.Brief, "mode": resolved.Mode,
			"because": resolved.Decision.Because, "stages": outline(resolved), "requirements": len(resolved.Plan.Requirements),
			"checks": checkLines(resolved.Plan), "setup": setupView(answer.Setup, resolved.Plan), "preparation": preparation.ID, "route": used}
		if summary := reviewRuleSummary(cfg, rules); len(summary) > 0 {
			out["review_rules"] = summary
		}
		if len(subs.list) > 0 {
			out["substitutions"] = subs.list
		}
		switch {
		case revising:
			out["next"] = "Review what changed (`brief`). Apply it with `amend --plan " + saved.Name + "`; nothing changed in the task until then."
		case request.Action == "plan":
			out["next"] = "Show the user the brief and report planning complete. This request authorizes a proposal only. Once the user authorizes " +
				"the implementation: `start --plan " + saved.Name + " --implement`."
		default:
			out["next"] = "Check the brief for scope and decisions, tell the user the mode and why, get the authorizations the checks or the stand need, then `start --plan " + saved.Name + "`."
		}
		return preparation.answered(out)
	}
	return preparation.answered(map[string]any{"status": "BLOCKED", "reason": "invalid_plan", "summary": "The preparation worker could not produce a valid plan in this call.",
		"problems": []string(problems), "questions": []Question{}, "setup": setupView(Setup{}, nil), "preparation": preparation.ID,
		"next": "Report the problems. No plan was saved. `prepare` again with notes continues the same worker; do not reconstruct the technical plan yourself."})
}

// savedPlan is the plan saved under a name, or nil.
func savedPlan(name string) *Plan {
	path, err := PlanFile(name)
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	plan := &Plan{}
	if err != nil || json.Unmarshal(raw, plan) != nil {
		return nil
	}
	return plan
}

// onlyMode says that an input changes nothing but the mode of the plan the preparation already saved.
func (input PrepareInput) onlyMode(preparation *Preparation) bool {
	sameRequest := input.Request == "" || preparation != nil && input.Request == preparation.Request
	return input.Mode != "" && sameRequest && input.Name == "" && len(input.Context) == 0 && len(input.Attachments) == 0 &&
		strings.TrimSpace(input.Notes) == "" && len(input.Answers) == 0 && !input.RefreshDesigns && !input.Fresh
}

// setMode saves the plan of this preparation again with another mode, without its worker.
func (p *Preparation) setMode(cfg *config.Config, mode string) (any, error) {
	plan := savedPlan(p.Name)
	if plan == nil {
		return nil, fmt.Errorf("the saved plan %q cannot be read; `prepare` with notes has the worker save it again", p.Name)
	}
	proposal := loadProposal(p.Name)
	ours := proposal != nil && proposal.Digest == digest(plan)
	plan.Mode, p.Mode = mode, mode
	saved, err := PlanSave(cfg, plan, p.Name)
	if err != nil {
		return nil, err
	}
	if ours {
		proposal.Digest = digest(plan) // the same proposal: its worker and its map stay with it
		if err := proposal.save(); err != nil {
			return nil, err
		}
	}
	resolved, err := Resolve(cfg, plan)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"status": "READY", "summary": "The saved plan takes mode " + mode + "; no worker ran.", "plan": saved.Name, "brief": saved.Brief,
		"mode": resolved.Mode, "because": resolved.Decision.Because, "stages": outline(resolved), "requirements": len(resolved.Plan.Requirements),
		"checks": checkLines(resolved.Plan), "preparation": p.ID,
		"next": "Tell the user the mode and why. A mode with architects needs its design pass (`architecture --plan " + saved.Name + "`) before `start`."}
	if setup, ok := p.Answer["setup"]; ok {
		out["setup"] = setup // what the worker asked to authorize does not depend on the mode
	}
	return p.answered(out)
}

// remember makes this preparation the one the lead session, or the active task, continues.
func (p *Preparation) remember(gate *Gate) error {
	if err := p.save(); err != nil {
		return err
	}
	if gate != nil && gate.active() {
		if gate.Preparation == p.ID {
			return nil
		}
		gate.Preparation = p.ID
		return gate.save()
	}
	if err := os.MkdirAll(home(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home(), "preparation"), []byte(p.ID+"\n"), 0o600)
}

// carry keeps what a preparation worker does not decide when it revises the plan of a running task: the
// accepted architecture and interface design, the review base and the process ceilings.
func carry(plan, current *Plan) {
	plan.Architecture, plan.BaseRef = current.Architecture, current.BaseRef
	plan.Mode = cmp.Or(plan.Mode, current.Mode)
	plan.WriterTimeoutS, plan.ReviewerTimeoutS = current.WriterTimeoutS, current.ReviewerTimeoutS
	plan.EnvFile = current.EnvFile
	if plan.UI == nil || (current.UI != nil && current.UI.Design != "") {
		plan.UI = current.UI
	}
	if plan.Visual == nil {
		plan.Visual = current.Visual
	}
}

// outline is a plan as the lead needs it to decide: one line of facts per stage.
func outline(resolved *Resolved) []map[string]any {
	out := []map[string]any{}
	for index, stage := range resolved.Plan.Stages {
		var due []string
		for _, id := range stage.Covers {
			if resolved.Plan.lastCover(id) == index {
				due = append(due, id)
			}
		}
		streams := make([]string, len(stage.Streams))
		for position, stream := range stage.Streams {
			streams[position] = stream.Name + " (" + writerRoute(resolved.Config, resolved.Modes[index], stream, 0).String() + ")"
		}
		out = append(out, map[string]any{"name": stage.Name, "goal": oneLine(stage.Goal), "mode": resolved.Modes[index], "streams": streams,
			"covers": stage.Covers, "due": due})
	}
	return out
}

func checkLines(plan *Plan) []string {
	out := []string{}
	for _, check := range plan.checksFor(-1) {
		if !generatedDesignCheck(check) {
			out = append(out, strings.Join(check.Argv, " "))
		}
	}
	return out
}

// attach copies the files the user gave into the preparation and returns the new copies. A path may lie
// anywhere on the machine; a file given again is not copied twice.
func (p *Preparation) attach(paths []string, dir string) ([]string, error) {
	var added []string
	for _, path := range paths {
		source, err := filepath.Abs(config.ExpandHome(path))
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(source)
		switch {
		case err != nil:
			return nil, fmt.Errorf("attachment %s: %w", path, err)
		case !info.Mode().IsRegular():
			return nil, fmt.Errorf("attachment %s is not a regular file", path)
		case info.Size() > attachmentLimit:
			return nil, fmt.Errorf("attachment %s is larger than %d MiB", path, attachmentLimit>>20)
		}
		raw, err := os.ReadFile(source)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(source)
		target := filepath.Join(dir, name)
		for index := 2; slices.Contains(p.Attachments, target); index++ {
			if existing, err := os.ReadFile(target); err == nil && bytes.Equal(existing, raw) {
				break
			}
			target = filepath.Join(dir, fmt.Sprintf("%d-%s", index, name))
		}
		if slices.Contains(p.Attachments, target) {
			continue // the same file again
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, raw, 0o600); err != nil {
			return nil, err
		}
		p.Attachments = append(p.Attachments, target)
		added = append(added, target)
	}
	return added, nil
}

const attachmentLimit = 20 << 20

// imagesOf are the pictures among some files.
func imagesOf(paths []string) []string {
	var out []string
	for _, path := range paths {
		if slices.Contains([]string{".png", ".jpg", ".jpeg", ".gif", ".webp"}, strings.ToLower(filepath.Ext(path))) {
			out = append(out, path)
		}
	}
	return out
}

// copyAttachments gives a task its own copies of the files its plan was prepared with: a preparation is
// pruned on its own schedule.
func copyAttachments(paths []string, dir string) []string {
	var out []string
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		target := filepath.Join(dir, filepath.Base(path))
		if os.MkdirAll(dir, 0o700) == nil && os.WriteFile(target, raw, 0o600) == nil {
			out = append(out, target)
		}
	}
	return out
}

// designRequest is a part of a Figma file a preparation worker asks to see: a node — its id in the file of the
// request, or its own link — and how deep to list it.
type designRequest struct {
	Node  string `json:"node"`
	Depth int    `json:"depth"`
}

// designReads is how many times one preparation call reads the design for its worker.
const designReads = 3

// readDesignRequests lists the nodes a worker asked for. A node that cannot be read is answered with why,
// so the worker can go on without it.
func readDesignRequests(requests []designRequest, files []string) []map[string]any {
	out := []map[string]any{}
	for _, request := range requests[:min(len(requests), 5)] {
		depth := min(max(request.Depth, 1), 6)
		item := map[string]any{"node": request.Node, "depth": depth}
		out = append(out, item)
		link, node := strings.TrimRight(request.Node, ").,;"), ""
		if !strings.HasPrefix(link, "https://") {
			if len(files) == 0 {
				item["error"] = "no Figma link in the request to read this node from: name it by its own link"
				continue
			}
			link, node = strings.TrimRight(files[0], ").,;"), request.Node
		}
		argv := []string{"figma", "frames", link, "--json", "--depth", strconv.Itoa(depth)}
		if node != "" {
			argv = append(argv, "--node", node)
		}
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(layoutCommand(), argv...)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		switch err := ops.Run(cmd, seconds(layoutTimeout)); {
		case err != nil:
			item["error"] = toolMessage(stderr.String() + "\n" + err.Error())
		case !json.Valid(stdout.Bytes()):
			item["error"] = "figma frames returned no JSON catalogue"
		default:
			item["catalogue"] = json.RawMessage(stdout.Bytes())
		}
	}
	return out
}
