package dev

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Event is one thing a worker did, read from the event stream its CLI writes while it runs.
type Event struct {
	Kind   string // say, think, run, edit, read, search, tool, done, error
	Text   string // the action in one line
	Detail string // what came back: the end of a command's output, an error
	Failed bool
}

// Glyph is the one-character mark of an event kind.
func (e Event) Glyph() string {
	switch e.Kind {
	case "run":
		return "$"
	case "edit":
		return "✎"
	case "read":
		return "◦"
	case "search":
		return "⌕"
	case "think":
		return "…"
	case "done":
		return "✔"
	case "error":
		return "✖"
	case "tool":
		return "⚙"
	}
	return "▸"
}

// readTail returns the end of a file, starting at a line boundary.
func readTail(path string, limit int64) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	cut := info.Size() > limit
	if cut {
		_, _ = file.Seek(-limit, io.SeekEnd)
	}
	raw, _ := io.ReadAll(file)
	if cut {
		if index := strings.IndexByte(string(raw), '\n'); index >= 0 {
			raw = raw[index+1:]
		}
	}
	return raw
}

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

// reportLine turns the JSON a worker answers with into one line: who reported what.
func reportLine(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "{") {
		return "", false
	}
	var report struct {
		Status  string `json:"status"`
		Summary string `json:"summary"`
		Reports []struct {
			Axis     string            `json:"axis"`
			Status   string            `json:"status"`
			Findings []json.RawMessage `json:"findings"`
		} `json:"reports"`
	}
	if json.Unmarshal([]byte(text), &report) != nil {
		return "", false
	}
	switch {
	case len(report.Reports) > 0:
		var parts []string
		for _, item := range report.Reports {
			parts = append(parts, fmt.Sprintf("%s %s, %d findings", item.Axis, item.Status, len(item.Findings)))
		}
		return "reported: " + strings.Join(parts, "; "), true
	case report.Status != "":
		return "reported " + report.Status + ": " + oneLine(report.Summary), true
	case report.Summary != "":
		return "reported: " + oneLine(report.Summary), true
	}
	return "wrote the report", true
}

func lastLines(text string, count int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > count {
		lines = append([]string{fmt.Sprintf("(%d lines above)", len(lines)-count)}, lines[len(lines)-count:]...)
	}
	return strings.Join(lines, "\n")
}

// shellCommand strips the shell wrapper Codex puts around every command.
func shellCommand(command string) string {
	for _, prefix := range []string{"/bin/bash -lc ", "bash -lc ", "/bin/sh -c ", "sh -c "} {
		if rest, ok := strings.CutPrefix(command, prefix); ok {
			return strings.Trim(rest, `'"`)
		}
	}
	return command
}

// laneEvents reads what the worker of a lane has done so far. Both CLIs write one JSON event per line;
// the two dialects are folded into one list. Only the end of a long stream is read.
func laneEvents(dir string) []Event {
	raw := readTail(filepath.Join(dir, "events.jsonl"), 1<<20)
	var out []Event
	pending := map[string]int{} // tool or command id -> index of its event, to attach the result
	for _, line := range strings.Split(string(raw), "\n") {
		var event struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			IsError bool   `json:"is_error"`
			Result  string `json:"result"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			Item *struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Text     string `json:"text"`
				Command  string `json:"command"`
				Output   string `json:"aggregated_output"`
				ExitCode *int   `json:"exit_code"`
				Server   string `json:"server"`
				Tool     string `json:"tool"`
				Changes  []struct {
					Path string `json:"path"`
					Kind string `json:"kind"`
				} `json:"changes"`
			} `json:"item"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Text string `json:"message_text"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		switch {
		case event.Item != nil: // Codex
			item := event.Item
			switch item.Type {
			case "agent_message":
				if line, ok := reportLine(item.Text); ok && event.Type == "item.completed" {
					out = append(out, Event{Kind: "done", Text: line})
				} else if event.Type == "item.completed" && strings.TrimSpace(item.Text) != "" {
					out = append(out, Event{Kind: "say", Text: strings.TrimSpace(item.Text)})
				}
			case "reasoning":
				if event.Type == "item.completed" && strings.TrimSpace(item.Text) != "" {
					out = append(out, Event{Kind: "think", Text: strings.TrimSpace(item.Text)})
				}
			case "command_execution":
				index, known := pending[item.ID]
				if !known {
					out = append(out, Event{Kind: "run", Text: strings.TrimSpace(shellCommand(item.Command))})
					index = len(out) - 1
					pending[item.ID] = index
				}
				if event.Type == "item.completed" {
					out[index].Detail = lastLines(item.Output, 8)
					out[index].Failed = item.ExitCode != nil && *item.ExitCode != 0
				}
			case "file_change":
				if event.Type == "item.completed" {
					for _, change := range item.Changes {
						out = append(out, Event{Kind: "edit", Text: strings.TrimSpace(change.Kind + " " + change.Path)})
					}
				}
			case "mcp_tool_call":
				if event.Type == "item.started" {
					out = append(out, Event{Kind: "tool", Text: item.Server + "." + item.Tool})
				}
			case "error":
				out = append(out, Event{Kind: "error", Text: oneLine(item.Text), Failed: true})
			}
		case event.Type == "assistant" || event.Type == "user": // Claude
			var parts []struct {
				Type     string          `json:"type"`
				ID       string          `json:"id"`
				Name     string          `json:"name"`
				Text     string          `json:"text"`
				Thinking string          `json:"thinking"`
				Input    map[string]any  `json:"input"`
				ToolUse  string          `json:"tool_use_id"`
				Content  json.RawMessage `json:"content"`
				IsError  bool            `json:"is_error"`
			}
			if json.Unmarshal(event.Message.Content, &parts) != nil {
				continue
			}
			for _, part := range parts {
				text := func(key string) string { value, _ := part.Input[key].(string); return value }
				switch part.Type {
				case "text":
					if strings.TrimSpace(part.Text) != "" {
						out = append(out, Event{Kind: "say", Text: strings.TrimSpace(part.Text)})
					}
				case "thinking":
					if strings.TrimSpace(part.Thinking) != "" {
						out = append(out, Event{Kind: "think", Text: strings.TrimSpace(part.Thinking)})
					}
				case "tool_use":
					made := Event{Kind: "tool", Text: part.Name}
					switch part.Name {
					case "Bash":
						made = Event{Kind: "run", Text: strings.TrimSpace(text("command"))}
					case "Read":
						made = Event{Kind: "read", Text: text("file_path")}
					case "Edit", "Write", "MultiEdit", "NotebookEdit":
						made = Event{Kind: "edit", Text: text("file_path")}
					case "Grep", "Glob":
						made = Event{Kind: "search", Text: strings.TrimSpace(text("pattern") + " " + text("path"))}
					case "StructuredOutput":
						made = Event{Kind: "done", Text: "wrote the report"}
						if raw, err := json.Marshal(part.Input); err == nil {
							if line, ok := reportLine(string(raw)); ok {
								made.Text = line
							}
						}
					}
					out = append(out, made)
					pending[part.ID] = len(out) - 1
				case "tool_result":
					index, known := pending[part.ToolUse]
					if !known || (out[index].Kind != "run" && !part.IsError) {
						continue // a file's content is not worth repeating; a command's output and any error are
					}
					var plain string
					var blocks []struct {
						Text string `json:"text"`
					}
					if json.Unmarshal(part.Content, &plain) != nil && json.Unmarshal(part.Content, &blocks) == nil {
						for _, block := range blocks {
							plain += block.Text
						}
					}
					out[index].Detail, out[index].Failed = lastLines(plain, 8), part.IsError
				}
			}
		case event.Type == "result":
			if event.IsError || (event.Subtype != "" && event.Subtype != "success") {
				out = append(out, Event{Kind: "error", Text: "ended: " + event.Subtype + " " + oneLine(event.Result), Failed: true})
			}
		case event.Type == "error" || event.Type == "turn.failed":
			message := event.Text
			if event.Error != nil {
				message = event.Error.Message
			}
			out = append(out, Event{Kind: "error", Text: oneLine(message), Failed: true})
		}
	}
	return out
}
