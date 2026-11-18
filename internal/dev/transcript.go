package dev

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/config"
)

// Both lead clients keep the conversation of a session on disk under its id. The tool reads it for three
// things no worker packet can carry: what the user said, in their words, for a reader that is to judge the
// work with that knowledge; which model the session runs on; and what the session spent on a task.

// turn is one entry of the lead's conversation.
type turn struct {
	at    time.Time
	who   string // user, session or tool
	text  string
	id    string // the message it belongs to, for its usage
	model string
	spend Spend
	total bool // spend is the session's running total, not this message's own
}

// Spend is what a model consumed, in tokens: input that was read fresh, input read from the cache, and output.
type Spend struct {
	Input     int64 `json:"input"`
	CacheRead int64 `json:"cache_read"`
	Output    int64 `json:"output"`
}

func (s *Spend) add(other Spend) {
	s.Input, s.CacheRead, s.Output = s.Input+other.Input, s.CacheRead+other.CacheRead, s.Output+other.Output
}

// transcriptPath is where the lead's client keeps this session, or "".
func transcriptPath() string {
	id := session()
	if id == "" || !safeName.MatchString(id) {
		return ""
	}
	pattern := filepath.Join(claudeDir(), "projects", "*", id+".jsonl")
	if config.Lead() == "codex" {
		pattern = filepath.Join(codexDir(), "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl")
	}
	paths, _ := filepath.Glob(pattern)
	if len(paths) == 0 {
		return ""
	}
	return paths[len(paths)-1]
}

// readLines calls each for every line of a file, however long a line is.
func readLines(path string, each func(line []byte)) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			each(line)
		}
		if err != nil {
			return
		}
	}
}

// toolLine is a tool call in one line: the tool and what it was pointed at.
func toolLine(name string, input map[string]any) string {
	for _, key := range []string{"file_path", "path", "command", "cmd", "pattern", "query", "url", "description"} {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			return name + ": " + clipText(oneLine(value), 160)
		}
	}
	return name
}

// claudeTurns reads a Claude Code transcript.
func claudeTurns(path string) []turn {
	var out []turn
	readLines(path, func(line []byte) {
		var row struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Meta      bool      `json:"isMeta"`
			Sidechain bool      `json:"isSidechain"`
			Message   struct {
				ID      string          `json:"id"`
				Model   string          `json:"model"`
				Content json.RawMessage `json:"content"`
				Usage   *struct {
					Input    int64 `json:"input_tokens"`
					Creation int64 `json:"cache_creation_input_tokens"`
					Read     int64 `json:"cache_read_input_tokens"`
					Output   int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &row) != nil || row.Sidechain || row.Meta || (row.Type != "user" && row.Type != "assistant") {
			return
		}
		who := map[string]string{"user": "user", "assistant": "session"}[row.Type]
		var text string
		var parts []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		}
		if json.Unmarshal(row.Message.Content, &text) == nil {
			out = append(out, turn{at: row.Timestamp, who: who, text: text})
		} else if json.Unmarshal(row.Message.Content, &parts) == nil {
			for _, part := range parts {
				switch part.Type {
				case "text":
					out = append(out, turn{at: row.Timestamp, who: who, text: part.Text})
				case "tool_use":
					out = append(out, turn{at: row.Timestamp, who: "tool", text: toolLine(part.Name, part.Input)})
				}
			}
		}
		if usage := row.Message.Usage; usage != nil && row.Message.ID != "" {
			out = append(out, turn{at: row.Timestamp, id: row.Message.ID, model: row.Message.Model,
				spend: Spend{Input: usage.Input + usage.Creation, CacheRead: usage.Read, Output: usage.Output}})
		}
	})
	return out
}

// codexTurns reads a Codex rollout.
func codexTurns(path string) []turn {
	var out []turn
	model := ""
	readLines(path, func(line []byte) {
		var row struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Payload   struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Model   string `json:"model"`
				Name    string `json:"name"`
				Input   string `json:"input"`
				Args    string `json:"arguments"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Info *struct {
					Total *struct {
						Input  int64 `json:"input_tokens"`
						Cached int64 `json:"cached_input_tokens"`
						Output int64 `json:"output_tokens"`
					} `json:"total_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &row) != nil {
			return
		}
		payload := row.Payload
		switch {
		case row.Type == "turn_context" && payload.Model != "":
			model = payload.Model
		case row.Type == "response_item" && payload.Type == "message" && (payload.Role == "user" || payload.Role == "assistant"):
			who := map[string]string{"user": "user", "assistant": "session"}[payload.Role]
			for _, part := range payload.Content {
				if part.Text != "" {
					out = append(out, turn{at: row.Timestamp, who: who, text: part.Text})
				}
			}
		case row.Type == "response_item" && (payload.Type == "function_call" || payload.Type == "custom_tool_call"):
			out = append(out, turn{at: row.Timestamp, who: "tool", text: payload.Name + ": " + clipText(oneLine(payload.Input+payload.Args), 160)})
		case payload.Type == "token_count" && payload.Info != nil && payload.Info.Total != nil:
			total := payload.Info.Total
			out = append(out, turn{at: row.Timestamp, id: "total", model: model, total: true,
				spend: Spend{Input: total.Input - total.Cached, CacheRead: total.Cached, Output: total.Output}})
		}
	})
	return out
}

// leadTurns is the lead's conversation as its client recorded it.
func leadTurns() []turn {
	path := transcriptPath()
	switch {
	case path == "":
		return nil
	case config.Lead() == "codex":
		return codexTurns(path)
	}
	return claudeTurns(path)
}

var modelField = regexp.MustCompile(`"model":\s*"([^"<][^"]*)"`)

// leadModel is the model the lead's session last answered on, or "" when its transcript does not say. Only the
// end of the transcript is read: a session's file runs to tens of megabytes.
func leadModel() string {
	file, err := os.Open(transcriptPath())
	if err != nil {
		return ""
	}
	defer file.Close()
	const tail = 1 << 20
	if info, err := file.Stat(); err == nil && info.Size() > tail {
		_, _ = file.Seek(-tail, io.SeekEnd)
	}
	raw, _ := io.ReadAll(file)
	matches := modelField.FindAllSubmatch(raw, -1)
	if len(matches) == 0 {
		return ""
	}
	return string(matches[len(matches)-1][1])
}

const (
	digestBudget  = 200_000 // bytes of the whole digest
	digestMessage = 20_000  // characters of one message of the user
)

// conversation writes the digest of the lead's conversation since a moment into dir and returns its path: the
// user's messages as they wrote them, what the session said, and every tool call as one line. Tool results and
// reasoning are left out: they hold file contents and whatever a connected service returned. It starts at the
// user's message that led to that moment, which is the request itself. "" when there is nothing to read.
func conversation(dir string, since int64) string {
	turns := leadTurns()
	start, moment := 0, time.Unix(since, 0)
	for index, entry := range turns {
		if entry.who == "user" && !entry.at.After(moment) {
			start = index
		}
	}
	var entries []turn
	size := 0
	for _, entry := range turns[start:] {
		text := strings.TrimSpace(entry.text)
		if entry.who == "" || text == "" {
			continue
		}
		if runes := []rune(text); entry.who == "user" && len(runes) > digestMessage {
			text = string(runes[:digestMessage]) + fmt.Sprintf("\n(… %d more characters of this message are not shown)", len(runes)-digestMessage)
		}
		entry.text = text
		entries = append(entries, entry)
		size += len(text)
	}
	if len(entries) == 0 {
		return ""
	}
	// Too long: what the session said goes first, oldest first, then its oldest tool calls. The user's words stay.
	for _, who := range []string{"session", "tool"} {
		for index := 0; index < len(entries) && size > digestBudget; index++ {
			if entries[index].who == who {
				size -= len(entries[index].text)
				entries[index].text = ""
			}
		}
	}
	var out strings.Builder
	out.WriteString("# The lead's conversation\n\nFrom the request of this task to now. `[user]` is the user, in their own words: what they asked, answered and " +
		"objected to. `[session]` is what the lead's session said. `[tool]` is one line per tool call. Tool results and reasoning are not here.\n")
	for _, entry := range entries {
		if entry.text != "" {
			fmt.Fprintf(&out, "\n[%s] %s\n", entry.who, entry.text)
		}
	}
	path := filepath.Join(dir, "conversation.md")
	if os.MkdirAll(dir, 0o700) != nil || os.WriteFile(path, []byte(out.String()), 0o600) != nil {
		return ""
	}
	return path
}

// leadSpend is what the lead's session consumed between two moments, and the models it answered on.
func leadSpend(since, until int64) (Spend, []string) {
	var out, before, last Spend
	var models []string
	own := map[string]Spend{}
	for _, entry := range leadTurns() {
		if entry.id == "" {
			continue
		}
		at := entry.at.Unix()
		switch {
		case entry.total && at < since:
			before = entry.spend
		case entry.total && at <= until:
			last = entry.spend
		case !entry.total && at >= since && at <= until:
			own[entry.id] = entry.spend // a message is recorded again as it grows: its last record counts
		}
		if at >= since && at <= until && entry.model != "" && !slices.Contains(models, entry.model) {
			models = append(models, entry.model)
		}
	}
	for _, spend := range own {
		out.add(spend)
	}
	if last != (Spend{}) {
		out.add(Spend{Input: last.Input - before.Input, CacheRead: last.CacheRead - before.CacheRead, Output: last.Output - before.Output})
	}
	return out, models
}

// runSpend is what the model processes under the given directories consumed: every worker keeps its own
// session files in the home of its lane.
func runSpend(roots ...string) Spend {
	var out Spend
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				if name := entry.Name(); name == "objects" || name == "cache" || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			var turns []turn
			switch {
			case strings.Contains(path, "/home/projects/"):
				turns = claudeTurns(path)
			case strings.Contains(path, "/home/sessions/"):
				turns = codexTurns(path)
			}
			own, total := map[string]Spend{}, Spend{}
			for _, record := range turns {
				switch {
				case record.id == "":
				case record.total:
					total = record.spend
				default:
					own[record.id] = record.spend
				}
			}
			for _, spend := range own {
				out.add(spend)
			}
			out.add(total)
			return nil
		})
	}
	return out
}
