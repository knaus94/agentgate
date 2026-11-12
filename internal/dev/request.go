// Package dev implements the /dev workflow: a quality gate around sandboxed model workers.
package dev

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Request is the parsed argument string of the /dev command. The task text is data, never shell.
type Request struct {
	Action   string `json:"action"`
	Mode     string `json:"mode"` // empty: the lead chooses from the plan
	Task     string `json:"task"`
	Parallel int    `json:"parallel"` // 0: automatic
	Preview  bool   `json:"preview"`
	Fix      bool   `json:"fix"`
	Grill    string `json:"grill"`    // auto, force, skip
	Worktree bool   `json:"worktree"` // work in an isolated worktree; the checkout changes only when the user lands it
	Staged   bool   `json:"staged"`   // the plan must split the work into stages
	Single   bool   `json:"single"`   // one pass, whatever the size
	Step     bool   `json:"step"`     // wait for the user after every accepted stage
}

var actions = []string{"plan", "research", "fix", "review", "clean", "verify", "status", "stop", "help"}

var modeFlags = map[string]string{
	"-l": "light", "--light": "light", "-m": "medium", "--medium": "medium",
	"-h": "heavy", "--heavy": "heavy", "--deep": "heavy",
	"-x": "ultra", "--ultra": "ultra", "--critical": "ultra",
}

var countFlag = regexp.MustCompile(`^-[1-9][0-9]*$`)

// ParseRequest reads the flags that precede the task. "--" ends flag parsing.
func ParseRequest(raw string) (Request, error) {
	request := Request{Grill: "auto"}
	rest := strings.TrimSpace(raw)
	for rest != "" {
		token, tail := rest, ""
		if cut := strings.IndexAny(rest, " \t\n"); cut >= 0 {
			token, tail = rest[:cut], rest[cut+1:]
		}
		tail = strings.TrimSpace(tail)
		mode, isMode := modeFlags[token]
		switch {
		case token == "--":
			rest = tail
			goto done
		case token == "--preview" && !request.Preview:
			request.Preview = true
		case token == "--fix" && !request.Fix:
			request.Fix = true
		case (token == "-w" || token == "--worktree") && !request.Worktree:
			request.Worktree = true
		case token == "--staged" && !request.Staged:
			request.Staged = true
		case token == "--single" && !request.Single:
			request.Single = true
		case token == "--step" && !request.Step:
			request.Step = true
		case (token == "--grill" || token == "--no-grill") && request.Grill == "auto":
			request.Grill = map[string]string{"--grill": "force", "--no-grill": "skip"}[token]
		case isMode && request.Mode == "":
			request.Mode = mode
		case countFlag.MatchString(token) && request.Parallel == 0:
			request.Parallel, _ = strconv.Atoi(token[1:])
			if request.Parallel > 20 {
				return request, fmt.Errorf("parallelism must be 1..20")
			}
		case strings.HasPrefix(token, "-"):
			return request, fmt.Errorf("invalid, duplicate or conflicting option: %s", token)
		case request.Action == "" && slices.Contains(actions, token):
			request.Action = token
		default:
			goto done
		}
		rest = tail
	}
done:
	request.Task = strings.TrimSpace(rest)
	if request.Action == "" {
		request.Action = "implement"
	}
	if request.Fix && request.Action != "review" {
		return request, fmt.Errorf("--fix is only valid with review")
	}
	switch request.Action {
	case "status", "stop", "help":
		if request.Task != "" || request.Mode != "" || request.Parallel != 0 || request.Preview || request.Grill != "auto" || request.Worktree ||
			request.Staged || request.Single || request.Step {
			return request, fmt.Errorf("%s takes no task or flags", request.Action)
		}
	case "research", "verify":
		if request.Mode != "" {
			return request, fmt.Errorf("%s is read-only and has no mode; use plan, implement, fix, review or clean", request.Action)
		}
	}
	if request.Staged && request.Single {
		return request, fmt.Errorf("--staged and --single contradict each other")
	}
	if (request.Staged || request.Step) && request.Action != "implement" && request.Action != "fix" && request.Action != "plan" {
		return request, fmt.Errorf("stages are for work that writes code; %s has none", request.Action)
	}
	if request.Worktree && request.Action != "implement" && request.Action != "fix" {
		return request, fmt.Errorf("-w isolates a task that writes code; %s works on what is already in the checkout", request.Action)
	}
	if request.Task == "" {
		switch request.Action {
		case "review", "clean", "verify":
			request.Task = "current task changes"
		case "status", "stop", "help":
		default:
			return request, fmt.Errorf("a task is required; see /dev help")
		}
	}
	return request, nil
}

// Writes reports whether the action may change code.
func (r Request) Writes() bool {
	return r.Action == "implement" || r.Action == "fix" || r.Action == "clean" || (r.Action == "review" && r.Fix)
}

// ReviewFirst reports whether the action starts from an existing diff instead of writing one.
func (r Request) ReviewFirst() bool { return r.Action == "review" || r.Action == "clean" }
