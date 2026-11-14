package dev

import (
	"fmt"
	"github.com/knaus94/agentgate/internal/config"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// runPanel is the interactive loop: redraw every second and on every key.
func runPanel(cfg *config.Config, global, color bool) {
	restore, err := rawMode(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Print(PanelText(cfg, global, false, 110, 0))
		return
	}
	fmt.Print("\x1b[?1049h\x1b[?25l\x1b[?2004h")
	defer func() {
		fmt.Print("\x1b[?2004l\x1b[?25h\x1b[?1049l")
		restore()
	}()
	m := &panel{cfg: cfg, global: global, paint: paint{color}}
	if !global && len(loadTasks(false)) == 0 && len(loadTasks(true)) > 0 {
		m.global = true // outside a repository with tasks, show the machine
	}
	done := make(chan struct{})
	defer close(done)
	keys := terminalInput(os.Stdin, done)
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	defer signal.Stop(signals)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	decoder := keyDecoder{}
	escape := time.NewTimer(time.Hour)
	if !escape.Stop() {
		<-escape.C
	}
	defer escape.Stop()
	m.refresh()
	for {
		m.width, m.height = termSize(int(os.Stdout.Fd()))
		var out strings.Builder
		out.WriteString("\x1b[H")
		for index, line := range m.frame() {
			if index > 0 {
				out.WriteString("\r\n")
			}
			out.WriteString(line + "\x1b[K")
		}
		out.WriteString("\x1b[J")
		fmt.Print(out.String())
		select {
		case raw, open := <-keys:
			if !open {
				return
			}
			for _, name := range decoder.feed(raw) {
				if m.key(name) {
					return
				}
			}
			if !escape.Stop() {
				select {
				case <-escape.C:
				default:
				}
			}
			if len(decoder.pending) > 0 && !decoder.paste {
				escape.Reset(75 * time.Millisecond)
			}
		case <-escape.C:
			for _, name := range decoder.flush() {
				if m.key(name) {
					return
				}
			}
		case received := <-signals:
			if received != syscall.SIGWINCH {
				return
			}
		case <-ticker.C:
			m.refresh()
		}
	}
}
