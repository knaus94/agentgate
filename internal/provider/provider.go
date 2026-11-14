// Package provider runs one model invocation as a fresh, sandboxed CLI process.
//
// Every process sees the host read-only. A reviewer can write only its own lane directory;
// a writer can additionally write the checkout. Writers keep their session, so a blocked
// writer is answered in the same context instead of being restarted.
package provider

import (
	"errors"
	"time"

	"github.com/knaus94/agentgate/internal/accounts"
	"github.com/knaus94/agentgate/internal/config"
)

// WorkerEnv marks a spawned model process; such a process may not drive the gate.
const WorkerEnv = "AGENTGATE_WORKER"

// MCPServer is one stdio MCP server exposed to a Codex process with an explicit tool allowlist.
type MCPServer struct {
	Command string
	Args    []string
	Env     map[string]string
	Tools   []string
}

type Call struct {
	Route    config.Route
	Prompt   string
	Dir      string // private lane directory: logs, provider home, result
	Schema   any    // JSON schema of the answer, or nil for free text
	Timeout  time.Duration
	Writer   bool                 // may edit the checkout
	Persist  bool                 // keep the session so it can be continued later
	Resume   string               // session to continue instead of starting a fresh one
	Writable []string             // extra writable directories
	ReadDirs []string             // directories outside the checkout the process must be able to read
	MCP      map[string]MCPServer // Codex only
	Account  config.Account       // profile to run under; empty means the CLI's default profile
}

type Result struct {
	Answer  string
	Session string
	Dir     string // actual lane directory, including a successful alternate's session home
	Account string // name of the configured account the call ran under, if any
}

// Accounts chooses the profile each call runs under. Nil, or no account for a provider, means its default profile.
var Accounts *accounts.Manager

// ErrLimit means the provider refused the call because its usage limit is exhausted.
var ErrLimit = errors.New("provider usage limit reached")

// Run is the process launcher; tests replace it.
var Run = run
