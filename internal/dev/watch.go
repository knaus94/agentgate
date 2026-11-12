package dev

import (
	"fmt"
	"os"

	"github.com/knaus94/agentgate/internal/config"
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
	terminal := isTerminal(os.Stdout)
	if !once && terminal && isTerminal(os.Stdin) {
		runPanel(cfg, global)
		return
	}
	width := 110
	if terminal {
		width, _ = termSize(int(os.Stdout.Fd()))
	}
	fmt.Print(PanelText(cfg, global, terminal, width, 0))
}
