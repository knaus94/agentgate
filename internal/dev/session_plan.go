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
	"slices"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
)

// Draft is a task the session is planning: the request as it was handed over and the working tree at that
// moment. Whatever changes between then and `start` is the task's own change, not part of what it found.
type Draft struct {
	ID          string   `json:"id"` // the id the task takes when it starts: its trees are already kept under it
	Request     string   `json:"request"`
	Mode        string   `json:"mode"`
	Tree        string   `json:"tree"`
	At          int64    `json:"at"`
	Attachments []string `json:"attachments,omitempty"` // copies of the files the user gave
}

func draftFile() string { return filepath.Join(home(), "draft.json") }

// loadDraft is the plan this session is working on, or nil.
func loadDraft() *Draft {
	raw, err := os.ReadFile(draftFile())
	draft := &Draft{}
	if err != nil || json.Unmarshal(raw, draft) != nil || !gateIDPattern.MatchString(draft.ID) {
		return nil
	}
	return draft
}

// opened is when the request of the present work was handed over: the plan in progress, else this session's
// task. 0 when there is neither.
func opened() int64 {
	if draft := loadDraft(); draft != nil {
		return draft.At
	}
	if gate := Current(); gate != nil {
		return cmp.Or(gate.Opened, gate.CreatedAt)
	}
	return 0
}

// designCatalogues reads the bounded listing of every linked Figma file: reading a design is the tool's work.
func designCatalogues(links []string) ([]json.RawMessage, error) {
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
	return designs, nil
}

// sessionPage is what a phase asks of the session in a mode: the part every mode shares, then the mode's own.
func sessionPage(mode string) string {
	common, rest, _ := strings.Cut(assets.Prompt("session-plan"), "\n## In ")
	for _, section := range strings.Split(rest, "\n## In ") {
		if name, body, _ := strings.Cut(section, "\n"); strings.TrimSpace(name) == mode {
			return strings.TrimSpace(common) + "\n\n## In " + mode + "\n" + body
		}
	}
	return strings.TrimSpace(common)
}

// prepareSession opens the plan of a task the session writes. No worker runs: the session that holds the
// conversation asks, grounds and plans. The call records the working tree and answers with what the phase
// asks in this mode.
func prepareSession(cfg *config.Config, input PrepareInput, request Request, continued bool) (any, error) {
	if err := atRoot(); err != nil {
		return nil, err
	}
	if input.Mode != "" && !slices.Contains(config.ModeNames, input.Mode) {
		return nil, fmt.Errorf("mode is one of %s, not %q", strings.Join(config.ModeNames, ", "), input.Mode)
	}
	// A request is a new task: its tree is recorded now, also when the same words were handed over before and
	// that plan was abandoned. Only a call without a request — another mode, another attachment — goes on.
	draft := loadDraft()
	if draft == nil || !continued || input.Fresh {
		draft = &Draft{ID: newID(), Request: input.Request, At: time.Now().Unix()}
		tree, err := snapshotTree(filepath.Join(home(), draft.ID, "objects"), nil)
		if err != nil {
			return nil, err
		}
		draft.Tree = tree
	}
	draft.Mode = cmp.Or(input.Mode, request.Mode, draft.Mode, "medium")
	kept := &Preparation{Attachments: draft.Attachments}
	if _, err := kept.attach(input.Attachments, filepath.Join(home(), draft.ID, "attachments")); err != nil {
		return nil, err
	}
	draft.Attachments = kept.Attachments
	if err := writePacket(draftFile(), draft); err != nil {
		return nil, err
	}
	grill := "the mode's"
	switch request.Grill {
	case "force":
		grill = "full, whatever the mode: the request carries --grill"
	case "skip":
		grill = "only what blocks: the request carries --no-grill; an approval is never skipped"
	}
	out := map[string]any{"status": "PLAN", "executor": "session", "mode": draft.Mode, "draft": draft.ID, "grill": grill,
		"instructions": sessionPage(draft.Mode),
		"next": "Ask, ground and plan as `instructions` say, then `plan save <name> --file <plan.json>`. Write no code before the task starts: a file " +
			"changed from now on is part of the task's change, and one no stage owns has to be settled."}
	if request.Action == "plan" {
		out["next"] = "Ask, ground and plan as `instructions` say, then `plan save <name> --file <plan.json>`, show the user the brief and report planning " +
			"complete. This request authorizes a proposal only."
	}
	if len(draft.Attachments) > 0 {
		out["attachments"] = draft.Attachments
	}
	if links := figmaURL.FindAllString(input.Request, -1); len(links) > 0 {
		catalogues, err := designCatalogues(links)
		if err != nil {
			return nil, err
		}
		out["design_catalogues"], out["design_settings"] = catalogues, map[string]any{"screens": cfg.Design.Screens, "stand": cfg.Design.Stand}
		out["design_instructions"] = "The request links a design. The page below was written for a planning worker and holds the rules of a design entry in the " +
			"plan; where it has the worker ask for more of the file (`design_requests`), read the node yourself: `agentgate figma node <link>`.\n\n" +
			assets.Prompt("prepare-design")
	}
	return out, nil
}

// price is what a plan will start besides the session: model runs, counted from its mode and its stages. A
// correction adds a recheck of what it changed, and a stall a question to the advisor; neither is known ahead.
func (r *Resolved) price() (int, []string) {
	if r.Executor != "session" {
		return 0, nil
	}
	process, plan := r.ModeConfig(), r.Plan
	total := 0
	var lines []string
	add := func(count int, what string) {
		if count > 0 {
			total += count
			lines = append(lines, fmt.Sprintf("%d — %s", count, what))
		}
	}
	if ui := plan.UI; ui != nil && ui.Usage != nil && ui.Design == "" {
		add(1, "the interface designer ("+r.Config.UIDesignerFor(r.Mode)+")")
	}
	if r.counselNeeded() {
		add(1, "the advisor reads the plan ("+r.Config.Advisor+")")
		rounds := 1
		if process.Critique {
			rounds = 2
		}
		for _, critic := range process.Architecture {
			add(rounds, "an independent critic of the design ("+critic+")")
		}
	}
	if readings := r.readings(); len(process.Review) > 0 {
		var routes []string
		for _, lane := range process.Review {
			routes = append(routes, lane.Route)
		}
		add(len(process.Review)*readings, fmt.Sprintf("%d readings of the change (%s): %s", readings, strings.Join(routes, " + "), r.readingPlaces()))
	}
	return total, lines
}

// checkpoints are the stages the mode's reader reads at, besides the whole change at the end.
func (r *Resolved) checkpoints() []string {
	var out []string
	gate := &Gate{Mode: r.Mode}
	for index, stage := range r.Plan.Stages {
		if index < len(r.Plan.Stages)-1 && gate.checkpoint(stage) {
			out = append(out, stage.Name)
		}
	}
	return out
}

func (r *Resolved) readings() int { return len(r.checkpoints()) + 1 }

func (r *Resolved) readingPlaces() string {
	if stages := r.checkpoints(); len(stages) > 0 {
		return "at " + strings.Join(stages, ", ") + " and of the whole change"
	}
	return "of the whole change"
}

// counselNeeded says whether the plan is read by a second model before the work starts: every mode above light,
// for a task the session writes from scratch.
func (r *Resolved) counselNeeded() bool {
	return r.Executor == "session" && modeRank[r.Mode] >= modeRank["medium"] && !r.Request.ReviewFirst() &&
		(r.Request.Writes() || r.Request.Action == "plan")
}

// finalReader says who reads the whole change after the last stage.
func (r *Resolved) finalReader() string {
	lanes := r.ModeConfig().Review
	if len(lanes) == 0 {
		return "by the session itself, with a proof for every requirement"
	}
	var routes []string
	for _, lane := range lanes {
		routes = append(routes, lane.Route)
	}
	return "by " + strings.Join(routes, " + ")
}
