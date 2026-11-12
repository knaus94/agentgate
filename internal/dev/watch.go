package dev

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/knaus94/claude-dev/internal/config"
	"github.com/knaus94/claude-dev/internal/ops"
)

type paint struct{ on bool }

func (p paint) wrap(code, value string) string {
	if !p.on {
		return value
	}
	return "\x1b[" + code + "m" + value + "\x1b[0m"
}

func (p paint) bold(value string) string   { return p.wrap("1", value) }
func (p paint) dim(value string) string    { return p.wrap("2", value) }
func (p paint) green(value string) string  { return p.wrap("32", value) }
func (p paint) yellow(value string) string { return p.wrap("33", value) }
func (p paint) red(value string) string    { return p.wrap("31", value) }
func (p paint) cyan(value string) string   { return p.wrap("36", value) }

func (p paint) status(value string) string {
	switch value {
	case "COMPLETED", "complete", "reviewed", "PASS":
		return p.green(value)
	case "RUNNING", "implementing", "correcting", "reviewing", "awaiting-review":
		return p.cyan(value)
	case "BLOCKED", "held", "NEEDS_FIX":
		return p.yellow(value)
	case "FAILED", "CANCELLED", "cancelled":
		return p.red(value)
	}
	return value
}

func clock(secondsTotal float64) string {
	total := int(secondsTotal)
	if total >= 3600 {
		return fmt.Sprintf("%dh%02dm", total/3600, total%3600/60)
	}
	return fmt.Sprintf("%dm%02ds", total/60, total%60)
}

// running lists the model processes that are alive right now, read from their receipts.
func running(gateDir string) []string {
	var out []string
	_ = filepath.WalkDir(gateDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != "process.json" {
			return nil
		}
		var receipt struct {
			State string `json:"state"`
			PID   int    `json:"pid"`
			Route struct {
				Alias, Effort, Pool string
			} `json:"route"`
			Writer     bool  `json:"writer"`
			Resumed    bool  `json:"resumed"`
			ObservedAt int64 `json:"observed_at"`
		}
		raw, _ := os.ReadFile(path)
		if json.Unmarshal(raw, &receipt) != nil || receipt.State != "starting" {
			return nil
		}
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", receipt.PID)); err != nil {
			return nil
		}
		lane, _ := filepath.Rel(gateDir, filepath.Dir(path))
		kind := "reads"
		if receipt.Writer {
			kind = "writes"
		}
		if receipt.Resumed {
			kind += ", same session"
		}
		out = append(out, fmt.Sprintf("%-34s %-14s pool %-7s %s  %s", lane, receipt.Route.Alias+":"+receipt.Route.Effort,
			receipt.Route.Pool, clock(float64(time.Now().Unix()-receipt.ObservedAt)), kind))
		return nil
	})
	sort.Strings(out)
	return out
}

// Render draws the live panel of one task as text.
func Render(cfg *config.Config, gate *Gate, color bool) string {
	p := paint{color}
	var out strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&out, format+"\n", args...) }
	if gate == nil {
		line("%s", p.dim("No /dev task in this checkout yet."))
		return out.String()
	}
	dir := sessionDir(gate.Cwd, gate.Session)
	task := gate.Request.Task
	if len([]rune(task)) > 90 {
		task = string([]rune(task)[:90]) + "…"
	}
	line("%s  %s  %s  %s", p.bold("/dev"), p.bold(gate.Mode), p.status(gate.Phase), p.dim("round "+fmt.Sprint(gate.Iteration)))
	line("%s", task)
	if gate.Phase == "held" {
		line("%s %s", p.yellow("on hold:"), gate.HoldReason)
	}
	if record := ops.Status(dir); record != nil && record.Live {
		line("\n%s %s  %s", p.cyan("▶"), p.bold(record.Action), clock(float64(time.Now().UnixNano())/1e9-record.StartedAt))
		for _, row := range running(dir) {
			line("  %s", row)
		}
	}
	latest := gate.latest()
	if len(latest) > 0 {
		line("\n%s", p.bold("writers"))
		names := make([]string, 0, len(latest))
		for name := range latest {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			writer := latest[name]
			line("  %-18s %-14s %s", name, writer.Route.String(), p.status(writer.Status))
			for _, blocker := range writer.Blockers {
				line("      %s %s", p.yellow("?"), blocker)
			}
		}
	}
	if len(gate.Reviews) > 0 {
		last := gate.Reviews[len(gate.Reviews)-1]
		line("\n%s %s", p.bold("review"), p.dim(fmt.Sprintf("#%d", len(gate.Reviews))))
		for _, lane := range last.Lanes {
			status := "PASS"
			count := 0
			for _, report := range lane.Reports {
				count += len(report.Findings)
				if report.Status != "PASS" {
					status = report.Status
				}
			}
			line("  %-26s %-14s %s  %d findings", strings.Join(lane.Axes, "+"), lane.Route.String(), p.status(status), count)
		}
	}
	counts := map[string]int{}
	blocking, notes := 0, 0
	for _, finding := range gate.Findings() {
		if _, resolved := gate.Resolutions[finding.ID]; resolved {
			continue
		}
		if gate.isNote(finding) {
			notes++
			continue
		}
		counts[finding.Severity]++
		if gate.blocking(cfg, finding) {
			blocking++
		}
	}
	if len(counts) > 0 || notes > 0 {
		line("\n%s  P0 %d  P1 %d  P2 %d   blocking %s   notes %d", p.bold("findings"), counts["P0"], counts["P1"], counts["P2"],
			p.status(map[bool]string{true: "BLOCKED", false: "PASS"}[blocking > 0])+fmt.Sprintf(" %d", blocking), notes)
	}
	if len(gate.Plan.Checks) > 0 {
		line("\n%s", p.bold("checks"))
		for index := range gate.Plan.Checks {
			check := &gate.Plan.Checks[index]
			state := p.dim("not run")
			if receipt := gate.currentReceipt(check); receipt != nil {
				switch {
				case receipt.ReturnCode == 0:
					state = p.green("passed")
				case receipt.Preexisting:
					state = p.yellow("failed before the task too")
				default:
					state = p.red(fmt.Sprintf("failed (%d)", receipt.ReturnCode))
				}
			}
			line("  %-60s %s", strings.Join(check.Argv, " "), state)
		}
	}
	for _, substitution := range gate.Substitutions {
		line("%s %s → %s (%s)", p.yellow("substituted"), substitution.Wanted, substitution.Used, substitution.Reason)
	}
	if timings := ops.Timings(dir, gate.ID); len(timings) > 0 {
		var parts []string
		for action, total := range timings {
			parts = append(parts, fmt.Sprintf("%s %s", action, clock(total)))
		}
		sort.Strings(parts)
		line("\n%s %s", p.dim("time"), p.dim(strings.Join(parts, " · ")))
	}
	if next := gate.next(cfg, blocking > 0); next != "" && gate.Phase != "complete" {
		line("%s %s", p.dim("next"), next)
	}
	return out.String()
}

// Watch redraws the panel until interrupted, or prints it once.
func Watch(cfg *config.Config, once bool) {
	info, _ := os.Stdout.Stat()
	terminal := info != nil && info.Mode()&os.ModeCharDevice != 0
	for {
		// Tolerate a state file caught mid-rename: draw the previous picture again on the next tick.
		text := Render(cfg, Visible(), terminal)
		if once || !terminal {
			fmt.Print(text)
			return
		}
		fmt.Print("\x1b[H\x1b[2J" + text)
		time.Sleep(time.Second)
	}
}
