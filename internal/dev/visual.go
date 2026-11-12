package dev

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/knaus94/claude-dev/internal/assets"
	"github.com/knaus94/claude-dev/internal/config"
	"github.com/knaus94/claude-dev/internal/provider"
)

type Coverage struct {
	ScenarioID string `json:"scenario_id"`
	Screen     string `json:"screen"`
	State      string `json:"state"`
	Viewport   string `json:"viewport"`
	Role       string `json:"role"`
	Observed   bool   `json:"observed"`
	Screenshot string `json:"screenshot"`
	Note       string `json:"note"`
}

type Excluded struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// VisualInput starts a browser pass, changes the user's choice, or records that the pass is skipped.
type VisualInput struct {
	URL          string     `json:"url,omitempty"`
	Origins      []string   `json:"origins,omitempty"`
	Login        string     `json:"login,omitempty"`
	Notes        string     `json:"notes,omitempty"`
	EntryPoints  []string   `json:"entry_points,omitempty"`
	StorageState string     `json:"storage_state,omitempty"`
	TimeoutS     int        `json:"timeout_s,omitempty"`
	Exclusions   []Excluded `json:"exclusions,omitempty"`
	Choice       *Visual    `json:"choice,omitempty"`
	SkipReason   string     `json:"skip_reason,omitempty"`
}

// The reviewer looks and interacts like a user: no arbitrary JavaScript, uploads or file drops.
var browserTools = []string{"browser_navigate", "browser_navigate_back", "browser_tabs", "browser_resize", "browser_close",
	"browser_snapshot", "browser_take_screenshot", "browser_find", "browser_click", "browser_hover", "browser_drag", "browser_type",
	"browser_fill_form", "browser_select_option", "browser_press_key", "browser_handle_dialog", "browser_wait_for",
	"browser_emulate_media", "browser_console_messages", "browser_network_requests", "browser_network_request"}

var visualSchema = object(map[string]any{"status": enum("PASS", "NEEDS_FIX", "UNVERIFIED"), "summary": text,
	"coverage": items(map[string]any{"scenario_id": text, "screen": text, "state": text, "viewport": text, "role": text,
		"observed": boolean, "screenshot": text, "note": text}),
	"findings": items(map[string]any{"id": text, "severity": enum(config.Severities...), "confidence": enum("high", "medium", "low"),
		"category": text, "screen": text, "state": text, "viewport": text, "screenshot": text, "observed": text, "expected": text,
		"impact": text, "remedy": text}),
	"resolutions":    items(map[string]any{"id": text, "resolved": boolean, "evidence": text, "screenshot": text}),
	"console_errors": list(text), "unverified": list(text)})

const visualTimeout = 1800

func (g *Gate) visualRequired() bool {
	return g.Plan.Visual != nil && g.Plan.Visual.Enabled && g.VisualSkip == ""
}

func (g *Gate) visualContract() string {
	return digest(map[string]any{"visual": g.Plan.Visual, "ui": g.Plan.UI})
}

// currentVisual is the browser run that observed exactly the present code and contract, or nil.
func (g *Gate) currentVisual(content string) *VisualRun {
	for index := len(g.VisualRuns) - 1; index >= 0; index-- {
		if run := &g.VisualRuns[index]; run.Digest == content && run.Contract == g.visualContract() {
			return run
		}
	}
	return nil
}

func (g *Gate) checkExclusions(values []Excluded) error {
	seen := map[string]bool{}
	for _, value := range values {
		known := slices.ContainsFunc(g.Plan.Visual.Scenarios, func(row Scenario) bool { return row.ID == value.ID })
		if !known || seen[value.ID] || strings.TrimSpace(value.Reason) == "" {
			return errors.New("scenario exclusions need a known, unique id and a concrete reason")
		}
		seen[value.ID] = true
	}
	return nil
}

func (g *Gate) unobserved(run *VisualRun, extra []Excluded) []string {
	var out []string
	for _, row := range g.Plan.Visual.Scenarios {
		observed := slices.ContainsFunc(run.Coverage, func(c Coverage) bool { return c.ScenarioID == row.ID && c.Observed })
		excluded := slices.ContainsFunc(append(slices.Clone(run.Excluded), extra...), func(e Excluded) bool { return e.ID == row.ID })
		if !observed && !excluded {
			out = append(out, row.ID)
		}
	}
	return out
}

func (g *Gate) visualSummary() map[string]any {
	out := map[string]any{"required": g.visualRequired(), "choice": g.Plan.Visual, "skip": g.VisualSkip}
	if len(g.VisualRuns) > 0 {
		last := g.VisualRuns[len(g.VisualRuns)-1]
		out["latest"] = map[string]any{"status": last.Status, "summary": last.Summary, "coverage": last.Coverage,
			"console_errors": last.Console, "current": last.Digest == g.ReviewDigest && last.Contract == g.visualContract(), "dir": last.Dir}
	}
	return out
}

// standOrigins accepts only origins that resolve to loopback: the local stand, never a shared or production host.
func standOrigins(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, errors.New("list every local stand origin the UI requests (frontend, API, websocket)")
	}
	var out []string
	for _, value := range values {
		parsed, err := url.Parse(value)
		if err != nil || !slices.Contains([]string{"http", "https", "ws", "wss"}, parsed.Scheme) || parsed.Hostname() == "" ||
			parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("stand origins are scheme://host[:port] only: %s", value)
		}
		addresses, err := net.LookupHost(parsed.Hostname())
		if err != nil || len(addresses) == 0 {
			return nil, fmt.Errorf("stand origin does not resolve: %s", value)
		}
		for _, address := range addresses {
			if ip := net.ParseIP(address); ip == nil || !ip.IsLoopback() {
				return nil, fmt.Errorf("browser verification only reaches loopback stand origins: %s", value)
			}
		}
		out = append(out, parsed.Scheme+"://"+parsed.Host)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// visualStage is a prepared browser pass: run only reads the gate, apply records the result on the lead's thread.
type visualStage struct {
	run   func() error
	apply func() error
}

func (g *Gate) prepareVisual(cfg *config.Config, input VisualInput, now Snapshot, content string) (*visualStage, error) {
	if !g.visualRequired() {
		return nil, errors.New("browser verification is not opted in; record the user's choice first")
	}
	ui, err := loadUI(g.Plan)
	if err != nil || ui == nil {
		return nil, errors.New("browser verification needs an accepted ui contract")
	}
	cli := config.ExpandHome(cfg.PlaywrightMCP)
	node, nodeErr := exec.LookPath("node")
	if _, err := os.Stat(cli); err != nil || nodeErr != nil {
		return nil, fmt.Errorf("browser tooling is missing: node and %s", cli)
	}
	origins, err := standOrigins(input.Origins)
	if err != nil {
		return nil, err
	}
	start, err := url.Parse(input.URL)
	if err != nil || !slices.Contains(origins, start.Scheme+"://"+start.Host) {
		return nil, errors.New("url must belong to the listed stand origins")
	}
	if err := g.checkExclusions(input.Exclusions); err != nil {
		return nil, err
	}
	var active []Scenario
	var viewports []string
	for _, row := range g.Plan.Visual.Scenarios {
		if !slices.ContainsFunc(input.Exclusions, func(e Excluded) bool { return e.ID == row.ID }) {
			active = append(active, row)
			if !slices.Contains(viewports, row.Viewport) {
				viewports = append(viewports, row.Viewport)
			}
		}
	}
	if len(active) == 0 {
		return nil, errors.New("no browser scenarios remain; record skip_reason instead")
	}
	timeout := visualTimeout
	if input.TimeoutS != 0 {
		if timeout, err = timeoutSeconds(input.TimeoutS); err != nil {
			return nil, err
		}
	}
	dir := filepath.Join(g.dir(), fmt.Sprintf("visual-%d-%s", g.Iteration, newID()[:8]))
	browser := filepath.Join(dir, "browser")
	shots := filepath.Join(browser, "shots")
	for _, sub := range []string{shots, filepath.Join(browser, "cache"), filepath.Join(browser, "config")} {
		if err := os.MkdirAll(sub, 0o700); err != nil {
			return nil, err
		}
	}
	args := []string{cli, "--headless", "--isolated", "--browser", "chromium", "--no-sandbox", "--block-service-workers",
		"--allowed-origins", strings.Join(origins, ";"), "--output-dir", shots, "--viewport-size", viewports[0], "--image-responses", "allow"}
	if input.StorageState != "" {
		// A disposable local session prepared by the lead; the file never enters the prompt.
		raw, err := os.ReadFile(input.StorageState)
		if err != nil {
			return nil, err
		}
		state := filepath.Join(browser, "storage-state.json")
		if err := os.WriteFile(state, raw, 0o600); err != nil {
			return nil, err
		}
		args = append(args, "--storage-state", state)
	}
	userHome, _ := os.UserHomeDir()
	env := map[string]string{"XDG_CACHE_HOME": filepath.Join(browser, "cache"), "XDG_CONFIG_HOME": filepath.Join(browser, "config"),
		"PLAYWRIGHT_BROWSERS_PATH": filepath.Join(userHome, ".cache", "ms-playwright")}
	if libs := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(cli))))), "chromium-libs/root/usr/lib/x86_64-linux-gnu"); dirExists(libs) {
		env["LD_LIBRARY_PATH"] = libs
	}
	var previous, recheck []Finding
	for _, finding := range g.Findings() {
		if finding.Axis == "visual" {
			previous = append(previous, finding)
		}
	}
	for _, finding := range g.accepted() {
		if finding.Axis == "visual" {
			recheck = append(recheck, finding)
		}
	}
	changes, err := evidence(filepath.Join(dir, "evidence"), g.Baseline, now, g.Files)
	if err != nil {
		return nil, err
	}
	packet := map[string]any{"task": g.Request.Task, "brief": g.Plan.Brief, "ui": ui, "scenarios": active, "exclusions": input.Exclusions,
		"prior_decisions": g.decisions(), "changes": changes, "recheck": recheck, "screenshots_dir": shots,
		"stand": map[string]any{"url": input.URL, "origins": origins, "viewports": viewports, "login": input.Login, "notes": input.Notes,
			"entry_points": input.EntryPoints, "session_preloaded": input.StorageState != ""}}
	if len(g.VisualRuns) > 0 {
		packet["previous_coverage"] = g.VisualRuns[len(g.VisualRuns)-1].Coverage
	}
	path := filepath.Join(dir, "packet.json")
	if err := writePacket(path, packet); err != nil {
		return nil, err
	}
	run := VisualRun{Iteration: g.Iteration, Digest: content, Contract: g.visualContract(), Excluded: input.Exclusions, Dir: dir,
		Findings: []Finding{}, Console: []string{}}
	resolved := map[string]string{}
	subs := &substitutions{}
	shot := func(name string, required bool) (string, error) {
		if name == "" && !required {
			return "", nil
		}
		if filepath.Base(name) != name || !fileExists(filepath.Join(shots, name)) {
			return "", fmt.Errorf("visual evidence must name a screenshot saved in this run: %q", name)
		}
		return filepath.Join(shots, name), nil
	}
	stage := &visualStage{}
	stage.run = func() error {
		prompt := assets.Prompt("visual") + "\nRead the packet at " + path + ". Screenshots you take are saved in " + shots + ".\n"
		result, _, err := subs.invoke(cfg, provider.Call{Route: cfg.MustRoute(cfg.VisualReviewer), Prompt: prompt, Dir: filepath.Join(dir, "reviewer"),
			Schema: visualSchema, Timeout: seconds(timeout), ReadDirs: []string{dir}, Writable: []string{browser},
			MCP: map[string]provider.MCPServer{"playwright": {Command: node, Args: args, Env: env, Tools: browserTools}}}, nil)
		if err != nil {
			return err
		}
		var report struct {
			Status   string     `json:"status"`
			Summary  string     `json:"summary"`
			Coverage []Coverage `json:"coverage"`
			Findings []struct {
				Finding
				Screen, State, Viewport, Observed, Expected string
			} `json:"findings"`
			Resolutions []struct {
				Resolution
				Screenshot string `json:"screenshot"`
			} `json:"resolutions"`
			Console    []string `json:"console_errors"`
			Unverified []string `json:"unverified"`
		}
		if err := decode(result.Answer, &report); err != nil {
			return err
		}
		if report.Status == "UNVERIFIED" {
			return fmt.Errorf("the browser pass could not verify the stand: %s %s", report.Summary, strings.Join(report.Unverified, "; "))
		}
		if report.Status == "NEEDS_FIX" && len(report.Findings) == 0 {
			return errors.New("NEEDS_FIX without visual findings")
		}
		seen := map[string]bool{}
		for index := range report.Coverage {
			row := &report.Coverage[index]
			planned := slices.IndexFunc(active, func(s Scenario) bool { return s.ID == row.ScenarioID })
			if planned < 0 || seen[row.ScenarioID] || active[planned].Screen != row.Screen || active[planned].State != row.State ||
				active[planned].Viewport != row.Viewport || active[planned].Role != row.Role {
				return errors.New("coverage must match its planned scenario, once")
			}
			seen[row.ScenarioID] = true
			if row.Screenshot, err = shot(row.Screenshot, row.Observed); err != nil {
				return err
			}
		}
		for _, raw := range report.Findings {
			finding := raw.Finding
			if strings.TrimSpace(raw.Observed) == "" || strings.TrimSpace(raw.Expected) == "" || strings.TrimSpace(finding.Remedy) == "" {
				return errors.New("a visual finding needs what was observed, what was expected and a remedy")
			}
			if !slices.ContainsFunc(previous, func(p Finding) bool { return p.ID == finding.ID }) {
				finding.ID = g.ID[:8] + "-visual-" + digest([]string{finding.Category, raw.Screen, raw.Observed, raw.Expected})[:16]
			}
			finding.Axis, finding.Path, finding.Line, finding.Preserve = "visual", "", 0, "the accepted UI contract"
			finding.Evidence = fmt.Sprintf("[%s / %s / %s] %s (expected: %s)", raw.Screen, raw.State, raw.Viewport, raw.Observed, raw.Expected)
			if finding.Screenshot, err = shot(finding.Screenshot, true); err != nil {
				return err
			}
			run.Findings = append(run.Findings, finding)
		}
		for _, vote := range report.Resolutions {
			if vote.Resolved && strings.TrimSpace(vote.Evidence) != "" && slices.ContainsFunc(recheck, func(f Finding) bool { return f.ID == vote.ID }) {
				path, err := shot(vote.Screenshot, true)
				if err != nil {
					return err
				}
				resolved[vote.ID] = vote.Evidence + " (" + path + ")"
			}
		}
		run.Status, run.Summary, run.Coverage, run.Console = report.Status, report.Summary, report.Coverage, report.Console
		return nil
	}
	stage.apply = func() error {
		if after, _, _ := g.contentDigest(); after != content {
			return errors.New("files changed during the browser pass; its evidence is stale")
		}
		for id, evidence := range resolved {
			g.Resolutions[id] = evidence
		}
		for _, finding := range run.Findings {
			delete(g.Resolutions, finding.ID)
			if decision := g.Dispositions[finding.ID].Decision; decision == "rejected" || decision == "deferred" {
				delete(g.Dispositions, finding.ID)
			}
		}
		g.VisualRuns = append(g.VisualRuns, run)
		g.Substitutions = append(g.Substitutions, subs.list...)
		return nil
	}
	return stage, nil
}

func dirExists(path string) bool { info, err := os.Stat(path); return err == nil && info.IsDir() }

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// VisualPass records the user's choice or a skip, or runs a standalone browser pass.
func VisualPass(cfg *config.Config, gate *Gate, input VisualInput) (any, error) {
	if !gate.active() || gate.Phase == "held" {
		return nil, errors.New("the gate is not active")
	}
	switch {
	case input.Choice != nil:
		if input.Choice.Enabled {
			if ui, _ := loadUI(gate.Plan); ui == nil {
				return nil, errors.New("browser verification needs an accepted ui contract")
			}
			if err := validateScenarios(input.Choice.Scenarios); err != nil {
				return nil, err
			}
		}
		gate.Plan.Visual, gate.VisualSkip = input.Choice, ""
	case input.SkipReason != "":
		// A truthful record that the stand was declined or cannot run; the report must say the UI was not seen.
		gate.VisualSkip = input.SkipReason
	default:
		content, now, err := gate.contentDigest()
		if err != nil {
			return nil, err
		}
		stage, err := gate.prepareVisual(cfg, input, now, content)
		if err != nil {
			return nil, err
		}
		if err := stage.run(); err != nil {
			return nil, err
		}
		if err := stage.apply(); err != nil {
			return nil, err
		}
	}
	return gate.Summary(cfg), gate.save()
}
