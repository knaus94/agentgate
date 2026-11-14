package provider

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var limitPattern = regexp.MustCompile(`(?i)usage limit|rate.?limit|quota|too many requests|\b429\b|overloaded|limit reached`)

// limited reports whether the failed run was refused for an exhausted limit. Only what the CLI said about the
// failure counts: its stderr and the failure events of its stream. Ordinary events mention rate limits too
// (Claude reports the state of the limits in every run) and must not be mistaken for a refusal.
func limited(dir string) bool {
	if raw, err := readTail(filepath.Join(dir, "stderr.txt"), 8192); err == nil && limitPattern.Match(raw) {
		return true
	}
	raw, _ := readTail(filepath.Join(dir, "events.jsonl"), 65536)
	for _, line := range strings.Split(string(raw), "\n") {
		var event struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			IsError bool   `json:"is_error"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue // partial/log text is not a provider failure event
		}
		failure := event.Type == "error" || event.Type == "turn.failed" ||
			(event.Type == "result" && (event.IsError || strings.HasPrefix(event.Subtype, "error")))
		if failure && limitPattern.MatchString(line) {
			return true
		}
	}
	return false
}

func readTail(path string, size int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(max(0, info.Size()-size), io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(file, size))
}

// scanEvents bounds memory by one event rather than loading an entire long
// worker session into RAM. Oversized or unreadable streams fail explicitly.
func scanEvents(path string, visit func([]byte)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		visit(scanner.Bytes())
	}
	return scanner.Err()
}

func tail(raw []byte, n int) []byte {
	if len(raw) > n {
		return raw[len(raw)-n:]
	}
	return raw
}
