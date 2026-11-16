package dev

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

type writerReport struct {
	Status     string    `json:"status"`
	Summary    string    `json:"summary"`
	Blockers   []string  `json:"blockers"`
	Handoff    string    `json:"handoff"`
	Deviations []string  `json:"deviations"`
	Disputes   []Dispute `json:"disputes"`
}

var writerSchema = object(map[string]any{"status": enum("COMPLETED", "BLOCKED"), "summary": text,
	"blockers": list(text), "handoff": text, "deviations": list(text),
	"disputes": list(object(map[string]any{"id": text, "evidence": text}))})

// attachmentDirs is what a worker must be able to read to open the files the user gave.
func (g *Gate) attachmentDirs() []string {
	if len(g.Attachments) == 0 {
		return nil
	}
	return []string{filepath.Dir(g.Attachments[0])}
}

// board is the shared directory where writers of one task leave notes for each other and read the lead's.
func (g *Gate) board() string { return filepath.Join(g.dir(), "board") }

// latest returns each stream's most recent writer record.
func (g *Gate) latest() map[string]*Writer {
	out := map[string]*Writer{}
	for _, writer := range g.Writers {
		out[writer.Stream] = writer
	}
	return out
}

// lastReport is the stream's most recent finished report; a writer that is running now has none yet.
func (g *Gate) lastReport(stream string) *Writer {
	for index := len(g.Writers) - 1; index >= 0; index-- {
		if writer := g.Writers[index]; writer.Stream == stream && writer.Status != "RUNNING" {
			return writer
		}
	}
	return nil
}

// streams are the streams of the present position. In the pass over the whole change a stream is named after
// its stage, because two stages may each have a stream called "api", and a file belongs to the last stage
// that owned it: an earlier stream keeps what no later stage took over.
func (g *Gate) streams() []Stream {
	if !g.Whole && g.Stage < len(g.Plan.Stages) {
		return g.Plan.Stages[g.Stage].Streams
	}
	var out []Stream
	var later []string
	for index := len(g.Plan.Stages) - 1; index >= 0; index-- {
		stage := g.Plan.Stages[index]
		var own []string
		for position := len(stage.Streams) - 1; position >= 0; position-- {
			stream := stage.Streams[position]
			taken := compilePatterns(later)
			kept := Stream{Name: stage.Name + "." + stream.Name, Domain: stream.Domain, Brief: stream.Brief}
			for _, entry := range stream.Files {
				if exact(entry) && matchAny(taken, entry) {
					continue // a later stage owns this file now
				}
				kept.Files = append(kept.Files, entry)
			}
			for _, entry := range later {
				if slices.ContainsFunc(compilePatterns(kept.Files), func(mine pattern) bool { return overlap(mine, compilePattern(entry)) }) {
					kept.Except = append(kept.Except, entry)
				}
			}
			own = append(own, stream.Files...)
			if len(kept.Files) > 0 {
				out = append(out, kept)
			}
		}
		later = append(later, own...)
	}
	slices.Reverse(out)
	return out
}

func exact(entry string) bool { return !globMeta.MatchString(entry) }

// owner is the stream a path belongs to: the one a finding about it goes to. In the pass over the whole
// change it is the stream of the last stage that owned the path.
func (g *Gate) owner(path string) *Stream {
	streams := g.streams()
	for index := len(streams) - 1; index >= 0; index-- {
		if matchAny(compilePatterns(streams[index].Files), path) && !matchAny(compilePatterns(streams[index].Except), path) {
			return &streams[index]
		}
	}
	return nil
}

// pending are the streams of the present round that still have to report.
func (g *Gate) pending() []Stream {
	if g.Round == nil {
		return nil
	}
	latest := g.latest()
	var out []Stream
	for _, stream := range g.Round.Streams {
		if last := latest[stream.Name]; last == nil || last.Batch != g.Round.Batch || last.Status != "COMPLETED" || last.Stale {
			out = append(out, stream)
		}
	}
	return out
}

// openRound starts the implementation round of the present stage.
func (g *Gate) openRound() {
	var streams []Stream
	for _, stream := range g.streams() {
		if !stream.Done {
			streams = append(streams, stream)
		}
	}
	g.Rounds++
	g.Round = &Round{Batch: g.batch(), Streams: streams}
}

func (g *Gate) batch() string {
	stage := g.Stage + 1
	if g.Whole {
		return fmt.Sprintf("whole.r%d", g.Rounds)
	}
	return fmt.Sprintf("s%d.r%d", stage, g.Rounds)
}

// admit rechecks the accepted contract before any writer starts: every entry into a round passes here, so a
// continued, answered or corrected round cannot run under an architecture or a rule that has since changed.
func (g *Gate) admit(cfg *config.Config) (*Resolved, error) {
	resolved, err := g.resolve(cfg)
	if err != nil {
		return nil, err
	}
	if err := resolved.requireArchitecture(); err != nil {
		return nil, err
	}
	if _, err := loadReviewRules(cfg, g.scope()); err != nil {
		return nil, err
	}
	if _, err := loadUI(g.Plan); err != nil {
		return nil, err
	}
	return resolved, nil
}

// resolve validates the task's own plan again under the present configuration.
func (g *Gate) resolve(cfg *config.Config) (*Resolved, error) {
	resolved, err := Resolve(cfg, g.Plan)
	if err != nil {
		return nil, err
	}
	resolved.Request = g.Request
	return resolved, nil
}

// requireTools fails before any state is created when a check command is missing.
func requireTools(plan *Plan) error {
	for _, check := range plan.checksFor(-1) {
		if _, err := exec.LookPath(check.Argv[0]); err != nil {
			return fmt.Errorf("check command %q cannot be executed here: %v", check.Argv[0], err)
		}
	}
	return nil
}

// write runs the streams of the present round that are ready: not yet completed, not waiting for an answer,
// and every stream they wait for completed. A blocked or failed stream holds back only the streams that wait
// for it. Each stream runs at most once per call; it reports whether every stream of the round completed and
// how many writers ran.
func (g *Gate) write(cfg *config.Config) (bool, int, int, error) {
	if _, err := g.admit(cfg); err != nil {
		return false, 0, 0, err
	}
	limit, err := parallelism(cfg, g.Request)
	if err != nil {
		return false, 0, 0, err
	}
	for _, dir := range []string{g.board(), filepath.Join(g.dir(), "checks")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, 0, 0, err
		}
	}
	if _, err := waves(g.Round.Streams); err != nil {
		return false, 0, 0, err
	}
	subs := &substitutions{}
	var mu sync.Mutex
	tried := map[string]bool{}
	ran, completed := 0, 0
	for {
		todo := g.ready(tried)
		if len(todo) == 0 {
			break
		}
		records := make([]*Writer, len(todo))
		for index, stream := range todo {
			records[index] = g.prepare(cfg, stream)
			tried[stream.Name] = true
		}
		g.Writers = append(g.Writers, records...)
		if err := g.save(); err != nil {
			return false, ran, completed, err
		}
		names := make([]string, len(todo))
		for index, stream := range todo {
			names[index] = stream.Name + " (" + records[index].Route.String() + ")"
		}
		ops.Doing("writing: %s", strings.Join(names, ", "))
		errs := parallel(len(todo), limit, func(index int) error {
			return g.runWriter(cfg, todo[index], records[index], "", subs, &mu)
		})
		g.Substitutions = append(g.Substitutions, subs.list...)
		subs.list = nil
		ran += len(records)
		g.recordLeft()
		if stopped, err := g.settle(records, firstError(errs)); stopped && err != nil {
			return false, ran, completed, err
		}
		for _, record := range records {
			if record.Status == "COMPLETED" {
				completed++
			}
		}
	}
	return len(g.pending()) == 0, ran, completed, g.save()
}

// ready lists the pending streams of the present round that can run now.
func (g *Gate) ready(tried map[string]bool) []Stream {
	if g.Round == nil {
		return nil
	}
	pending := g.pending()
	waiting := func(name string) bool {
		return slices.ContainsFunc(pending, func(stream Stream) bool { return stream.Name == name })
	}
	blocked := g.blocked()
	var out []Stream
	for _, stream := range pending {
		if tried[stream.Name] || slices.ContainsFunc(blocked, func(writer *Writer) bool { return writer.Stream == stream.Name }) ||
			slices.ContainsFunc(stream.After, waiting) {
			continue
		}
		out = append(out, stream)
	}
	return out
}

// settle records how a wave ended. It reports whether the round has to stop here.
func (g *Gate) settle(records []*Writer, failure error) (bool, error) {
	stopped := false
	for _, record := range records {
		if record.Status == "RUNNING" && failure != nil {
			record.Status, record.Error = "FAILED", failure.Error()
			if ops.IsCancelled(failure) {
				record.Status = "CANCELLED"
			}
		}
		stopped = stopped || record.Status != "COMPLETED"
	}
	if err := g.save(); err != nil {
		return true, err
	}
	if ops.IsCancelled(failure) {
		return true, failure
	}
	return stopped || failure != nil, nil // a stream that did not complete holds back only those that wait for it
}

// prepare creates the record of a new writer process, or reuses the session of the stream's previous writer:
// the writer that wrote the code corrects it with its context intact.
func (g *Gate) prepare(cfg *config.Config, stream Stream) *Writer {
	boost := g.Round.Boost
	if slices.ContainsFunc(g.Round.Work, func(item WorkOrder) bool { return item.Stream == stream.Name && item.Escalate }) {
		boost++ // the lead asked for more effort on this stream
	}
	route := writerRoute(cfg, g.Mode, stream, boost)
	record := &Writer{Stream: stream.Name, Route: route, Status: "RUNNING", Summary: "writer running", Blockers: []string{}, Batch: g.Round.Batch}
	previous := g.previousWriter(stream.Name)
	if previous != nil && previous.Session != "" && (previous.Route.Model == route.Model || g.Round.Correction && permittedContinuation(cfg, route, previous.Route)) {
		if previous.Route.Model != route.Model {
			record.Route = previous.Route
			record.Route.Effort, record.Route.SubstitutedFrom = route.Effort, ""
		}
		record.Dir, record.Session = previous.Dir, previous.Session // same context, at this round's effort
		record.Route.Account = previous.Route.Account
	} else {
		record.Dir = filepath.Join(g.dir(), "writers", fmt.Sprintf("%s-%s", stream.Name, newID()[:8]))
	}
	return record
}

// previousWriter is the session a stream's next assignment continues: its own earlier writer in this stage,
// or, in the pass over the whole change, the writer that wrote that stream in its stage.
func (g *Gate) previousWriter(stream string) *WriterRef {
	if last := g.latest()[stream]; last != nil {
		return &WriterRef{Route: last.Route, Dir: last.Dir, Session: last.Session}
	}
	if !g.Whole {
		return nil
	}
	for _, record := range g.Accepted {
		if rest, ok := strings.CutPrefix(stream, record.Name+"."); ok {
			if ref, known := record.Writers[rest]; known && dirExists(ref.Dir) {
				return &ref
			}
		}
	}
	return nil
}

// assignment is everything one writer needs. Files are named, never copied.
func (g *Gate) assignment(stream Stream) map[string]any {
	ui, _ := loadUI(g.Plan)
	handoffs, others := map[string]string{}, []map[string]any{}
	for _, other := range g.streams() {
		if other.Name == stream.Name {
			continue
		}
		others = append(others, map[string]any{"name": other.Name, "domain": other.Domain, "files": other.Files})
		if last := g.lastReport(other.Name); last != nil && last.Handoff != "" && (slices.Contains(stream.After, other.Name) || g.Round.Correction) {
			handoffs[other.Name] = last.Handoff
		}
	}
	checks := slices.DeleteFunc(g.checks(), generatedDesignCheck)
	packet := map[string]any{"task": g.Request.Task, "brief": g.brief(), "stream": stream, "other_streams": others,
		"context": g.context(), "checks": checks, "ui": ui, "architecture": architectureFor(g.Plan),
		"handoffs": handoffs, "board": g.board(), "your_board_note": filepath.Join(g.board(), stream.Name+".md"),
		"context_pack": g.ContextPack, "design": g.designFor(stream.Name),
		"checks_note":  "You may run these exact commands to verify your stream. The lead's run is the one that counts.",
		"requirements": g.requirementsFor()}
	if g.amendment() != "" {
		packet["update"] = g.amendment()
	}
	if len(g.Attachments) > 0 {
		packet["attachments"] = g.Attachments
	}
	if previous := g.lastReport(stream.Name); previous != nil {
		packet["your_previous_report"] = map[string]any{"status": previous.Status, "summary": previous.Summary,
			"blockers": previous.Blockers, "error": previous.Error}
	}
	if g.Round.Correction {
		var mine []Finding
		for _, finding := range g.Round.Findings {
			if target := g.target(finding); target == nil || target.Name == stream.Name {
				mine = append(mine, finding)
			}
		}
		work := []string{}
		for _, item := range g.Round.Work {
			if item.Stream == stream.Name {
				work = append(work, item.Note)
			}
		}
		packet["correction"] = map[string]any{"findings": mine, "failing_checks": g.Round.Failures, "remedy": g.Round.Remedy,
			"upheld": g.upheld(mine), "work": work,
			"rule": "Do the lead's `work`. Verify each finding against the code and your brief. Fix it with the stated remedy, or, when it is wrong, " +
				"leave the code and return a dispute {id, evidence}: the reviewer answers it. Make the failing checks pass; preserve everything else."}
		var conflicts []string
		patterns := compilePatterns(stream.Files)
		for _, file := range g.Round.Conflicts {
			if matchAny(patterns, file) {
				conflicts = append(conflicts, file)
			}
		}
		if len(conflicts) > 0 {
			packet["merge_conflicts"] = map[string]any{"files": conflicts,
				"rule": "These files carry git conflict markers: your change against newer commits of the repository. " +
					"Combine both sides as the remedy says, keep what the other side added, and leave no marker."}
		}
	}
	return packet
}

// upheld names the findings of a correction that the reviewer or the lead kept after the writer disputed
// them: these are to be fixed, not disputed again.
func (g *Gate) upheld(findings []Finding) []string {
	out := []string{}
	for _, finding := range findings {
		if dispute, disputed := g.Disputes[finding.ID]; disputed && dispute.Upheld {
			out = append(out, finding.ID)
		}
	}
	return out
}

// target is the stream a finding goes to: the one the lead named, the owner of its file, or of a file the lead
// named for it.
func (g *Gate) target(finding Finding) *Stream {
	if name := g.Dispositions[finding.ID].Stream; name != "" {
		streams := g.streams()
		if index := slices.IndexFunc(streams, func(stream Stream) bool { return stream.Name == name }); index >= 0 {
			return &streams[index]
		}
	}
	if stream := g.owner(finding.Path); stream != nil {
		return stream
	}
	for _, file := range g.Dispositions[finding.ID].Files {
		if stream := g.owner(file); stream != nil {
			return stream
		}
	}
	return nil
}

// writerCaches are the build caches a sandboxed writer gets in place of the ones in the user's home, which the
// sandbox makes read-only: Go's, kept under the task so it is cold once per task, unless the inherited one is a
// directory of the checkout the writer may write anyway.
func (g *Gate) writerCaches() (env, dirs []string) {
	if inherited := os.Getenv("GOCACHE"); inherited != "" {
		here, _ := os.Getwd()
		if relative, err := filepath.Rel(here, inherited); err == nil && relative != ".." && !strings.HasPrefix(relative, "../") {
			return nil, nil
		}
	}
	dir := filepath.Join(g.dir(), "cache", "go")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil
	}
	return []string{"GOCACHE=" + dir}, []string{dir}
}

func (g *Gate) runWriter(cfg *config.Config, stream Stream, record *Writer, answer string, subs *substitutions, mu *sync.Mutex) error {
	fail := func(err error) error {
		mu.Lock()
		defer mu.Unlock()
		record.Status, record.Summary, record.Error = "FAILED", "writer did not complete", err.Error()
		if ops.IsCancelled(err) {
			record.Status = "CANCELLED"
		}
		return errors.Join(err, g.save())
	}
	mu.Lock()
	packet := g.assignment(stream)
	mu.Unlock()
	rules, err := loadReviewRules(cfg, stream.Files)
	if err != nil {
		return fail(err)
	}
	packet["review_rules"] = rules
	path := filepath.Join(record.Dir, fmt.Sprintf("assignment-%s.json", newID()[:8]))
	if err := writePacket(path, packet); err != nil {
		return fail(err)
	}
	prompt := assets.Prompt("writer") + "\nRead your assignment at " + path + ".\n"
	if g.designFor(stream.Name) != nil {
		if skill, err := designSkill(); err == nil {
			prompt += "\nYour stream implements a design; `design` in the assignment names its package.\n" + skill
		}
	}
	previous := g.lastReport(stream.Name)
	if answer != "" {
		prompt = "The coordinator answers your blockers:\n" + answer + "\n\nContinue the same assignment (refreshed at " + path +
			") and return the same JSON schema.\n"
	} else if record.Session != "" && previous != nil && previous.Batch == record.Batch && (previous.Status == "FAILED" || previous.Status == "CANCELLED") {
		prompt = "Your work on this stream was stopped (" + oneLine(previous.Error) + "). What you changed is still in the checkout. " +
			"Continue the same assignment (refreshed at " + path + ") from where you stopped, and return the same JSON schema.\n"
	} else if record.Session != "" {
		prompt = "A new assignment for the stream you already worked on is at " + path +
			". Read it, do exactly what it adds, and return the same JSON schema.\n"
	}
	// A writer that keeps working is not cut off by the clock: the ceiling is generous, and a writer that goes
	// silent is what gets stopped.
	timeout, _ := timeoutSeconds(g.Plan.WriterTimeoutS, cfg.Modes[g.Mode].WriterTimeoutS, writerCeiling)
	tools, toolDirs, toolNote := workerTools(cfg, "writer")
	reads := append(append(append(packDirs(g.ContextPack), filepath.Join(g.dir(), "checks")), g.designReadDirs(stream.Name)...), g.attachmentDirs()...)
	env, caches := g.writerCaches()
	result, used, err := subs.invoke(cfg, provider.Call{Route: record.Route, Prompt: prompt + toolNote, Dir: record.Dir,
		Schema: writerSchema, Timeout: seconds(timeout), Idle: writerIdle, Writer: true, Persist: true, Resume: record.Session, MCP: tools,
		Writable: append(append([]string{g.board()}, toolDirs...), caches...), ReadDirs: reads, Env: env}, nil)
	if used.Model != "" {
		mu.Lock()
		record.Route = used
		if result.Dir != "" {
			record.Dir = result.Dir
		}
		if result.Session != "" {
			record.Session = result.Session
		}
		mu.Unlock()
	}
	if err != nil {
		return fail(err)
	}
	report := writerReport{}
	if err := decode(result.Answer, &report); err != nil {
		return fail(err)
	}
	if strings.TrimSpace(report.Summary) == "" || (report.Status == "BLOCKED") != (len(report.Blockers) > 0) ||
		(report.Status != "COMPLETED" && report.Status != "BLOCKED") {
		return fail(errors.New("invalid writer report: COMPLETED has no blockers, BLOCKED names them"))
	}
	mu.Lock()
	defer mu.Unlock()
	record.Route, record.Session = used, result.Session
	record.Status, record.Summary, record.Blockers = report.Status, report.Summary, report.Blockers
	record.Handoff, record.Deviations, record.Error = report.Handoff, report.Deviations, ""
	record.Disputes = nil
	if g.Round.Correction {
		// A dispute counts only for a finding this writer was actually sent, and only once: a finding the
		// reviewer or the lead upheld is to be fixed.
		for _, dispute := range report.Disputes {
			known := slices.ContainsFunc(g.Round.Findings, func(finding Finding) bool { return finding.ID == dispute.ID })
			if previous, again := g.Disputes[dispute.ID]; !known || strings.TrimSpace(dispute.Evidence) == "" || (again && previous.Upheld) {
				continue
			}
			dispute.Stream = stream.Name
			record.Disputes = append(record.Disputes, dispute)
			g.Disputes[dispute.ID] = dispute
		}
	}
	// Persist each worker independently. If another lane or the coordinator crashes later, this completed
	// stream must not be dispatched again.
	return g.save()
}

// AnswerInput is the lead's reply to one blocked stream.
type AnswerInput struct {
	Stream string `json:"stream"`
	Answer string `json:"answer"`
}

// answer sends the lead's decision into the blocked writer's own session.
func (g *Gate) answer(cfg *config.Config, input AnswerInput) error {
	previous := g.latest()[input.Stream]
	if previous == nil || previous.Status != "BLOCKED" || g.Round == nil || previous.Batch != g.Round.Batch || strings.TrimSpace(input.Answer) == "" {
		var waiting []string
		for _, writer := range g.blocked() {
			waiting = append(waiting, writer.Stream)
		}
		return fmt.Errorf("stream %q is not blocked, or the answer is empty; blocked: %s", input.Stream, strings.Join(waiting, ", "))
	}
	if previous.Session == "" {
		return errors.New("that writer kept no session; `amend` its stream with the answer in the brief instead")
	}
	if _, err := g.admit(cfg); err != nil {
		return err
	}
	index := slices.IndexFunc(g.Round.Streams, func(stream Stream) bool { return stream.Name == input.Stream })
	if index < 0 {
		return fmt.Errorf("stream %q is not part of the current round", input.Stream)
	}
	record := &Writer{Stream: previous.Stream, Route: previous.Route, Dir: previous.Dir, Session: previous.Session,
		Status: "RUNNING", Summary: "writer continuing after the answer", Blockers: []string{}, Batch: g.Round.Batch}
	g.Writers = append(g.Writers, record)
	if err := g.save(); err != nil {
		return err
	}
	subs, mu := &substitutions{}, &sync.Mutex{}
	failure := g.runWriter(cfg, g.Round.Streams[index], record, input.Answer, subs, mu)
	g.Substitutions = append(g.Substitutions, subs.list...)
	g.recordLeft()
	_, err := g.settle([]*Writer{record}, failure)
	return err
}

// TellInput is a note from the lead to the running writers.
type TellInput struct {
	Message string `json:"message"`
}

// Tell appends a note to the board without taking the operation lock, so it reaches writers that are still running.
// Writers read the board at natural boundaries; this is a note, not an interrupt.
func Tell(gate *Gate, input TellInput) (map[string]any, error) {
	if strings.TrimSpace(input.Message) == "" {
		return nil, errors.New("the message is empty")
	}
	if err := os.MkdirAll(gate.board(), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(gate.board(), "lead.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	_, err = fmt.Fprintf(file, "\n---\n%s\n", strings.TrimSpace(input.Message))
	return map[string]any{"posted": filepath.Join(gate.board(), "lead.md")}, err
}

// Answer sends the lead's decision into the blocked writer's own session and runs the task on.
func Answer(cfg *config.Config, gate *Gate, input AnswerInput) (any, error) {
	if err := gate.can(ActAnswer); err != nil {
		return nil, err
	}
	gate.proceed()
	// The streams this one waits for run first: the answer may depend on what they produce.
	var did []string
	if index := slices.IndexFunc(gate.Round.Streams, func(stream Stream) bool { return stream.Name == input.Stream }); index >= 0 && len(gate.Round.Streams[index].After) > 0 {
		_, ran, completed, err := gate.write(cfg)
		if err != nil {
			return nil, err
		}
		if ran > 0 {
			did = append(did, fmt.Sprintf("writers: %d of %d streams completed (%s)", completed, ran, gate.Round.Batch))
		}
		pending := gate.pending()
		for _, dependency := range gate.Round.Streams[index].After {
			if slices.ContainsFunc(pending, func(stream Stream) bool { return stream.Name == dependency }) {
				did = append(did, fmt.Sprintf("the answer to %s was not delivered: it waits for %s, which has not completed; settle that stream, then answer again", input.Stream, dependency))
				return gate.report(cfg, did), gate.save()
			}
		}
	}
	if err := gate.answer(cfg, input); err != nil {
		return nil, err
	}
	if last := gate.latest()[input.Stream]; last == nil || last.Status != "COMPLETED" {
		// Blocked again, or the writer did not finish: that is for the lead to see before anything runs on.
		return gate.report(cfg, append(did, "answered "+input.Stream)), gate.save()
	}
	return Run(cfg, gate, RunInput{})
}
