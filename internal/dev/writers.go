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

	"github.com/knaus94/claude-dev/internal/assets"
	"github.com/knaus94/claude-dev/internal/config"
	"github.com/knaus94/claude-dev/internal/ops"
	"github.com/knaus94/claude-dev/internal/provider"
)

type writerReport struct {
	Status     string   `json:"status"`
	Summary    string   `json:"summary"`
	Blockers   []string `json:"blockers"`
	Handoff    string   `json:"handoff"`
	Deviations []string `json:"deviations"`
}

var writerSchema = object(map[string]any{"status": enum("COMPLETED", "BLOCKED"), "summary": text,
	"blockers": list(text), "handoff": text, "deviations": list(text)})

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

func (g *Gate) streamOf(file string) *Stream {
	for index := range g.Plan.Streams {
		if slices.Contains(g.Plan.Streams[index].Files, file) {
			return &g.Plan.Streams[index]
		}
	}
	return nil
}

// Launch validates the plan, opens or continues the gate and runs the writers.
func Launch(cfg *config.Config, plan *Plan) (any, error) {
	resolved, err := Resolve(cfg, plan)
	if err != nil {
		return nil, err
	}
	if !resolved.Request.Writes() && !resolved.Request.ReviewFirst() {
		return nil, fmt.Errorf("%s is read-only; use `claude-dev consult`", resolved.Request.Action)
	}
	if err := requireTools(resolved); err != nil {
		return nil, err
	}
	gate := Current()
	if gate != nil && gate.Phase == "held" {
		return nil, errors.New("the task is on hold; answer the blocker with `answer`, or `resume` and launch an updated plan")
	}
	if resolved.Request.ReviewFirst() {
		if gate, err = begin(resolved); err != nil {
			return nil, err
		}
		if err := gate.gatherContext(resolved); err != nil {
			return nil, err
		}
		return gate.Summary(cfg), nil
	}
	if err := resolved.requireArchitecture(); err != nil {
		return nil, err
	}
	if _, err := loadUI(plan); err != nil {
		return nil, err
	}
	switch {
	case gate == nil || !gate.active():
		if gate, err = begin(resolved); err != nil {
			return nil, err
		}
		if err := gate.baselineChecks(); err != nil {
			return nil, err
		}
	case gate.Phase == "implementing":
		if err := gate.extend(resolved); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("the task is %s; finish it or use `fix` for accepted findings", gate.Phase)
	}
	var streams []Stream
	for _, stream := range plan.Streams {
		if !stream.Done {
			streams = append(streams, stream)
		}
	}
	gate.Round = &Round{Batch: fmt.Sprintf("v%d", gate.PlanVersion), Streams: streams}
	gate.Phase = "implementing"
	if err := gate.gatherContext(resolved); err != nil {
		return nil, err
	}
	return gate.write(cfg)
}

// gatherContext runs the mode's scout once per contract and remembers where the pack is.
func (g *Gate) gatherContext(resolved *Resolved) error {
	subs := &substitutions{}
	path, note := scout(resolved, subs)
	if note == "cancelled" {
		return ops.Checkpoint()
	}
	g.ContextPack, g.ScoutNote = path, note
	g.Substitutions = append(g.Substitutions, subs.list...)
	return g.save()
}

// requireTools fails before any state is created when a provider CLI or a check command is missing.
func requireTools(resolved *Resolved) error {
	for _, check := range resolved.Plan.Checks {
		if _, err := exec.LookPath(check.Argv[0]); err != nil {
			return fmt.Errorf("check command %q cannot be executed here: %v", check.Argv[0], err)
		}
	}
	return nil
}

// write runs the pending streams of the current round wave by wave, then moves the gate on.
func (g *Gate) write(cfg *config.Config) (any, error) {
	resolved, err := Resolve(cfg, g.Plan)
	if err != nil {
		return nil, err
	}
	resolved.Mode = g.Mode
	limit, err := resolved.Parallelism()
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{g.board(), filepath.Join(g.dir(), "checks")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	order, err := waves(g.Round.Streams)
	if err != nil {
		return nil, err
	}
	subs := &substitutions{}
	var mu sync.Mutex
	for _, wave := range order {
		var pending []Stream
		for _, stream := range wave {
			if last := g.latest()[stream.Name]; last == nil || last.Batch != g.Round.Batch || last.Status != "COMPLETED" {
				pending = append(pending, stream)
			}
		}
		records := make([]*Writer, len(pending))
		for index, stream := range pending {
			records[index] = g.prepare(resolved, stream)
		}
		g.Writers = append(g.Writers, records...)
		if err := g.save(); err != nil {
			return nil, err
		}
		errs := parallel(len(pending), limit, func(index int) error {
			err := g.runWriter(resolved, pending[index], records[index], "", subs, &mu)
			return err
		})
		g.Substitutions = append(g.Substitutions, subs.list...)
		subs.list = nil
		if done, value, err := g.settle(cfg, records, firstError(errs)); done {
			return value, err
		}
	}
	g.Phase, g.ResumePhase, g.HoldReason = "awaiting-review", "", ""
	if err := g.auditScope(); err != nil {
		return nil, err
	}
	return g.Summary(cfg), g.save()
}

// settle puts the gate on hold when a wave did not finish; later waves never start on top of a blocked one.
func (g *Gate) settle(cfg *config.Config, records []*Writer, failure error) (bool, any, error) {
	var reasons []string
	for _, record := range records {
		switch record.Status {
		case "BLOCKED":
			reasons = append(reasons, fmt.Sprintf("%s is blocked: %s", record.Stream, strings.Join(record.Blockers, "; ")))
		case "FAILED", "CANCELLED":
			reasons = append(reasons, fmt.Sprintf("%s %s: %s", record.Stream, strings.ToLower(record.Status), record.Error))
		}
	}
	if len(reasons) == 0 {
		return false, nil, nil
	}
	g.holdNow(strings.Join(reasons, " | "))
	if failure != nil {
		return true, nil, failure
	}
	return true, g.Summary(cfg), nil
}

// prepare creates the record of a new writer process, or reuses the session of the stream's previous writer
// when this round is a correction: the writer that wrote the code fixes it with its context intact.
func (g *Gate) prepare(resolved *Resolved, stream Stream) *Writer {
	route := resolved.WriterRoute(stream, g.Round.Boost)
	record := &Writer{Stream: stream.Name, Route: route, Status: "RUNNING", Summary: "writer running", Blockers: []string{}, Batch: g.Round.Batch}
	if previous := g.latest()[stream.Name]; previous != nil && previous.Session != "" && previous.Route.Model == route.Model {
		record.Dir, record.Session = previous.Dir, previous.Session // same context, at this round's effort
	} else {
		record.Dir = filepath.Join(g.dir(), "writers", fmt.Sprintf("%s-%s", stream.Name, newID()[:8]))
	}
	return record
}

// assignment is everything one writer needs. Files are named, never copied.
func (g *Gate) assignment(stream Stream) map[string]any {
	ui, _ := loadUI(g.Plan)
	handoffs, others := map[string]string{}, []map[string]any{}
	for _, other := range g.Plan.Streams {
		if other.Name == stream.Name {
			continue
		}
		others = append(others, map[string]any{"name": other.Name, "domain": other.Domain, "files": other.Files})
		if last := g.lastReport(other.Name); last != nil && last.Handoff != "" && (slices.Contains(stream.After, other.Name) || g.Round.Correction) {
			handoffs[other.Name] = last.Handoff
		}
	}
	packet := map[string]any{"task": g.Request.Task, "brief": g.Plan.Brief, "stream": stream, "other_streams": others,
		"context": g.Context, "checks": g.Plan.Checks, "ui": ui, "architecture": architectureFor(g.Plan),
		"handoffs": handoffs, "board": g.board(), "your_board_note": filepath.Join(g.board(), stream.Name+".md"),
		"context_pack": g.ContextPack,
		"checks_note":  "You may run these exact commands to verify your stream. The lead's run is the one that counts."}
	if g.Plan.Update != "" {
		packet["update"] = g.Plan.Update
	}
	if previous := g.lastReport(stream.Name); previous != nil {
		packet["your_previous_report"] = map[string]any{"status": previous.Status, "summary": previous.Summary,
			"blockers": previous.Blockers, "error": previous.Error}
	}
	if g.Round.Correction {
		var mine []Finding
		for _, finding := range g.Round.Findings {
			files := append([]string{finding.Path}, g.Dispositions[finding.ID].Files...)
			if slices.ContainsFunc(files, func(file string) bool { return slices.Contains(stream.Files, file) }) || finding.Path == "" {
				mine = append(mine, finding)
			}
		}
		packet["correction"] = map[string]any{"findings": mine, "failing_checks": g.Round.Failures, "remedy": g.Round.Remedy,
			"rule": "Fix exactly these accepted findings and make the failing checks pass; preserve everything else."}
	}
	return packet
}

func (g *Gate) runWriter(resolved *Resolved, stream Stream, record *Writer, answer string, subs *substitutions, mu *sync.Mutex) error {
	fail := func(err error) error {
		mu.Lock()
		defer mu.Unlock()
		record.Status, record.Summary, record.Error = "FAILED", "writer did not complete", err.Error()
		if ops.IsCancelled(err) {
			record.Status = "CANCELLED"
		}
		return err
	}
	mu.Lock()
	packet := g.assignment(stream)
	mu.Unlock()
	path := filepath.Join(record.Dir, fmt.Sprintf("assignment-%s.json", newID()[:8]))
	if err := writePacket(path, packet); err != nil {
		return fail(err)
	}
	prompt := assets.Prompt("writer") + "\nRead your assignment at " + path + ".\n"
	if answer != "" {
		prompt = "The coordinator answers your blockers:\n" + answer + "\n\nContinue the same assignment (refreshed at " + path +
			") and return the same JSON schema.\n"
	} else if record.Session != "" {
		prompt = "A new assignment for the stream you already worked on is at " + path +
			". Read it, do exactly what it adds, and return the same JSON schema.\n"
	}
	timeout, _ := timeoutSeconds(g.Plan.WriterTimeoutS, resolved.ModeConfig().WriterTimeoutS)
	result, used, err := subs.invoke(resolved.Config, provider.Call{Route: record.Route, Prompt: prompt, Dir: record.Dir,
		Schema: writerSchema, Timeout: seconds(timeout), Writer: true, Persist: true, Resume: record.Session,
		Writable: []string{g.board()}, ReadDirs: append(packDirs(g.ContextPack), filepath.Join(g.dir(), "checks"))}, nil)
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
	return nil
}

// AnswerInput is the lead's reply to one blocked stream.
type AnswerInput struct {
	Stream string `json:"stream"`
	Answer string `json:"answer"`
}

// Answer sends the lead's decision into the blocked writer's own session and then continues the round.
func Answer(cfg *config.Config, gate *Gate, input AnswerInput) (any, error) {
	if gate.Phase != "held" || gate.Round == nil || (gate.ResumePhase != "implementing" && gate.ResumePhase != "correcting") {
		return nil, errors.New("there is no blocked writer to answer")
	}
	previous := gate.latest()[input.Stream]
	if previous == nil || previous.Status != "BLOCKED" || strings.TrimSpace(input.Answer) == "" {
		return nil, fmt.Errorf("stream %q is not blocked, or the answer is empty", input.Stream)
	}
	if previous.Session == "" {
		return nil, errors.New("that writer kept no session; `resume` and launch an updated plan instead")
	}
	resolved, err := Resolve(cfg, gate.Plan)
	if err != nil {
		return nil, err
	}
	resolved.Mode = gate.Mode
	index := slices.IndexFunc(gate.Round.Streams, func(stream Stream) bool { return stream.Name == input.Stream })
	if index < 0 {
		return nil, fmt.Errorf("stream %q is not part of the current round", input.Stream)
	}
	record := &Writer{Stream: previous.Stream, Route: previous.Route, Dir: previous.Dir, Session: previous.Session,
		Status: "RUNNING", Summary: "writer continuing after the answer", Blockers: []string{}, Batch: gate.Round.Batch}
	gate.Writers = append(gate.Writers, record)
	gate.Phase, gate.HoldReason = gate.ResumePhase, ""
	if err := gate.save(); err != nil {
		return nil, err
	}
	subs, mu := &substitutions{}, &sync.Mutex{}
	failure := gate.runWriter(resolved, gate.Round.Streams[index], record, input.Answer, subs, mu)
	if done, value, err := gate.settle(cfg, []*Writer{record}, failure); done {
		return value, err
	}
	return gate.write(cfg)
}

// TellInput is a note from the lead to the running writers.
type TellInput struct {
	Message string `json:"message"`
}

// Tell appends a note to the board without taking the operation lock, so it reaches writers that are still running.
// Writers read the board at natural boundaries; this is a note, not an interrupt.
func Tell(gate *Gate, input TellInput) (any, error) {
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
