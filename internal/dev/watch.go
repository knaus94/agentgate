package dev

import (
	"fmt"
	"os"
	"time"

	"github.com/knaus94/agentgate/internal/config"
)

type paint struct{ on bool }

func (p paint) wrap(code, value string) string {
	value = terminalText(value)
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
	return terminalText(value)
}

func clock(secondsTotal float64) string {
	total := max(0, int(secondsTotal))
	switch {
	case total >= 3600:
		return fmt.Sprintf("%dh%02dm", total/3600, total%3600/60)
	case total >= 60:
		return fmt.Sprintf("%dm%02ds", total/60, total%60)
	}
	return fmt.Sprintf("%ds", total)
}

// Watch opens the interactive panel in a terminal. With --once, or when the output is not a terminal, it
// prints the same picture once as text.
func Watch(cfg *config.Config, once, global bool) {
	WatchWithOptions(cfg, once, global, false)
}

// WatchWithOptions separates interactivity from color: NO_COLOR still permits
// keyboard navigation, while TERM=dumb and redirected streams use plain text.
func WatchWithOptions(cfg *config.Config, once, global, noColor bool) {
	terminal := isTerminal(os.Stdout)
	color := terminal && !noColor && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	if !once && terminal && isTerminal(os.Stdin) && os.Getenv("TERM") != "dumb" {
		runPanel(cfg, global, color)
		return
	}
	width := 110
	if terminal {
		width, _ = termSize(int(os.Stdout.Fd()))
	}
	fmt.Print(PanelText(cfg, global, color, width, 0))
}

// WatchSnapshot is a stable, read-only JSON projection. It avoids presentation
// glyphs and the internal Gate object, so scripts do not depend on TUI text or
// accidentally ingest worker prompts and internal receipt paths.
func WatchSnapshot(cfg *config.Config, global bool) map[string]any {
	tasks := loadTasks(global)
	rows := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		task.describe(cfg)
		row := map[string]any{"task": task.Title, "repository": task.Repo, "phase": task.phase(), "running": task.live(),
			"elapsed_seconds": max(0, task.elapsed(time.Now().Unix())), "blocking_findings": task.Blocked, "next": task.Next}
		if task.Warning != "" {
			row["error"] = task.Warning
		}
		if task.Gate != nil {
			row["id"], row["mode"], row["session"] = task.Gate.ID, task.Gate.Mode, task.Gate.Session
		}
		workers := make([]map[string]any, 0, len(task.Lanes))
		for _, lane := range task.Lanes {
			workers = append(workers, map[string]any{"kind": lane.Kind, "name": lane.Name, "route": lane.Route, "status": lane.Status, "running": lane.Live, "note": lane.Note})
		}
		checks := make([]map[string]any, 0, len(task.Checks))
		for _, check := range task.Checks {
			checks = append(checks, map[string]any{"command": check.Argv, "status": check.State})
		}
		row["workers"], row["checks"] = workers, checks
		rows = append(rows, row)
	}
	scope := "checkout"
	if global {
		scope = "all"
	}
	return map[string]any{"schema_version": 1, "read_only": true, "scope": scope, "tasks": rows}
}
