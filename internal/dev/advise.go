package dev

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/provider"
)

// AdviseInput is a question of the session that writes a task to the advisor.
type AdviseInput struct {
	Question string   `json:"question"`
	Files    []string `json:"files,omitempty"`
	TimeoutS int      `json:"timeout_s,omitempty"`
}

// Advise puts a question to the advisor: the model the configuration names as the one that is shown the
// conversation. It reads the question, what the task changed so far and the code; it edits nothing and decides
// nothing. It is the same second opinion at any moment that the plan gets before the work starts.
func Advise(cfg *config.Config, input AdviseInput) (any, error) {
	if strings.TrimSpace(input.Question) == "" {
		return nil, errors.New("advise needs question: what you want a second opinion on")
	}
	files, err := cleanPaths(input.Files)
	if err != nil {
		return nil, err
	}
	timeout, err := timeoutSeconds(input.TimeoutS)
	if err != nil {
		return nil, err
	}
	route, err := cfg.Route(cfg.Advisor)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home(), "advice", newID())
	packet := map[string]any{"question": input.Question, "files": files}
	reads := []string{dir}
	gate, err := LoadCurrent()
	if err != nil {
		return nil, err
	}
	if gate != nil && gate.active() {
		task := map[string]any{"brief": gate.brief(), "step": gate.Step, "position": gate.position(), "needs": gate.needs(cfg)}
		if now, err := gate.snapshot(); err == nil {
			if _, _, err := gate.evidence(filepath.Join(dir, "evidence"), gate.origin(), now); err == nil {
				task["patch"] = filepath.Join(dir, "evidence", "change.patch")
			}
		}
		packet["task"] = task
		reads = append(append(reads, packDirs(gate.ContextPack)...), filepath.Join(gate.dir(), "checks"))
	} else if draft := loadDraft(); draft != nil {
		packet["request"] = draft.Request
	}
	shown := unshown(route)
	if shown == "" {
		shown = "the client keeps no record of this session that could be read"
		if digest := conversation(dir, opened()); digest != "" {
			packet["conversation"], shown = digest, "shown to the advisor"
		}
	}
	path := filepath.Join(dir, "packet.json")
	if err := writePacket(path, packet); err != nil {
		return nil, err
	}
	subs := &substitutions{}
	result, used, err := subs.invoke(cfg, provider.Call{Route: route, Prompt: assets.Prompt("advise") + "\nRead your question at " + path + ".\n",
		Dir: filepath.Join(dir, "run"), Timeout: seconds(timeout), ReadDirs: reads}, nil)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"answer": result.Answer, "route": used.String(), "artifacts": dir, "conversation": shown,
		"next": "The advice is a second opinion, not a decision: check it against the code, then go on with the task."}
	if len(subs.list) > 0 {
		out["substitutions"] = subs.list
	}
	return out, nil
}

// TakeInput names the session whose task this session takes over.
type TakeInput struct {
	From string `json:"from,omitempty"` // the session that holds the task; default: the only other session with an unfinished task the session writes
}

// Take brings a task the session writes into this session, on the user's word: a long task is continued in a
// fresh conversation without starting over. The task's record moves here; the stages already accepted, the
// plan and the open stage come with it.
func Take(cfg *config.Config, input TakeInput) (any, error) {
	if err := requireLead(nil); err != nil {
		return nil, err
	}
	if current, err := LoadCurrent(); err != nil {
		return nil, err
	} else if current != nil {
		return nil, errors.New("this session already has a task record: finish it, or `forget` it on the user's word, before taking another")
	}
	var candidates []*Gate
	paths, _ := filepath.Glob(filepath.Join(checkoutDir(""), "*", "state.json"))
	for _, path := range paths {
		gate, err := loadGateFile(path)
		if err != nil || gate == nil || !gate.active() || !gate.sessionWrites() || gate.Session == session() {
			continue
		}
		if input.From == "" || gate.Session == input.From || strings.HasPrefix(gate.Session, input.From) {
			candidates = append(candidates, gate)
		}
	}
	switch {
	case len(candidates) == 0:
		return nil, errors.New("no other session of this checkout holds an unfinished task the session writes; `agentgate status --all` lists them")
	case len(candidates) > 1:
		var names []string
		for _, gate := range candidates {
			names = append(names, fmt.Sprintf("%s (%s)", gate.Session, oneLine(gate.Request.Task)))
		}
		return nil, fmt.Errorf("several sessions hold such a task: name one in {from}: %s", strings.Join(names, "; "))
	}
	gate := candidates[0]
	from, to := sessionDir(gate.Cwd, gate.Session), home()
	unlock, err := flock(filepath.Join(from, "lock"), syscall.LOCK_EX|syscall.LOCK_NB, errors.New("an operation of that session is running: wait for it, or `stop` it there"))
	if err != nil {
		return nil, err
	}
	defer unlock()
	raw, err := os.ReadFile(filepath.Join(from, "state.json"))
	if err != nil {
		return nil, err
	}
	// The task's own directories are recorded by their full path: they move with it.
	moved, err := parseGate([]byte(strings.ReplaceAll(string(raw), from+"/", to+"/")))
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(to); err != nil { // this session has no task: what is here is an empty lock, or a draft the task replaces
		return nil, err
	}
	if err := os.Rename(from, to); err != nil {
		return nil, err
	}
	previous := moved.Session
	moved.Session = session()
	moved.Pause = nil
	if err := moved.save(); err != nil {
		return nil, err
	}
	return moved.respond(cfg, []string{"the task of session " + previous + " continues in this session"}, map[string]any{"taken_from": previous,
		"next": "Read the plan (`agentgate show plan`) and the accepted stages (`agentgate show stage`) before you write: this conversation has not " +
			"seen what the other one read. Then `run`."}), nil
}
