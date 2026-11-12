package provider

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/knaus94/claude-dev/internal/accounts"
	"github.com/knaus94/claude-dev/internal/config"
)

// The fakes stand in for the provider CLIs inside the real sandbox. They record how they were called
// and probe what the sandbox lets them write.
const fakeCodex = `#!/bin/sh
lane="$CODEX_HOME/.."
printf '%s\n' "$@" > "$lane/argv.txt"
env > "$lane/env.txt"
cat > "$lane/stdin.txt"
(echo probe > "$PWD/probe.txt") 2>/dev/null && echo yes > "$lane/wrote-checkout" || echo no > "$lane/wrote-checkout"
(echo probe > "$HOME/.claude-dev-probe") 2>/dev/null && echo yes > "$lane/wrote-home" || echo no > "$lane/wrote-home"
[ -f "$PWD/refresh-token" ] && echo refreshed >> "$CODEX_HOME/auth.json"
if [ -f "$PWD/limit-codex" ]; then echo "You've hit your usage limit. Try again later." >&2; exit 1; fi
out=""; prev=""
for arg in "$@"; do [ "$prev" = "-o" ] && out="$arg"; prev="$arg"; done
echo '{"type":"thread.started","thread_id":"thread-1"}'
echo '{"type":"turn.completed"}'
printf '{"ok":true}' > "$out"
`

const fakeClaude = `#!/bin/sh
lane="$CLAUDE_CONFIG_DIR/.."
printf '%s\n' "$@" > "$lane/argv.txt"
cat > /dev/null
model=""; prev=""
for arg in "$@"; do [ "$prev" = "--model" ] && model="$arg"; prev="$arg"; done
[ -f "$PWD/wrong-model" ] && model="some-other-model"
echo '{"type":"system","subtype":"init"}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"a.txt"}}]}}'
printf '{"type":"result","is_error":false,"subtype":"success","session_id":"claude-1","result":"plain","structured_output":{"ok":true},"modelUsage":{"%s":{}}}\n' "$model"
echo '{"type":"system","subtype":"session_state_changed"}'
`

func setup(t *testing.T) (*config.Config, string) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap is not installed")
	}
	root := t.TempDir()
	// The sandbox hides /tmp except the checkout, so the fakes live inside the checkout.
	repo := filepath.Join(root, "repo")
	bin := filepath.Join(repo, "bin")
	for _, dir := range []string{repo, bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(bin, "codex"), []byte(fakeCodex), 0o755)
	_ = os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("CLAUDE_CODE_SESSION_ID", "lead-session")
	t.Chdir(repo)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg, root
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestReviewerCannotWriteAndWriterCanOnlyWriteTheCheckout(t *testing.T) {
	cfg, root := setup(t)
	reviewer := filepath.Join(root, "state", "reviewer")
	result, err := Run(Call{Route: cfg.MustRoute("sol:high"), Prompt: "the prompt", Dir: reviewer, Schema: map[string]any{"type": "object"}, Timeout: 20 * time.Second})
	if err != nil || result.Answer != `{"ok":true}` || result.Session != "thread-1" {
		t.Fatalf("reviewer: %+v %v", result, err)
	}
	if got := strings.TrimSpace(read(t, filepath.Join(reviewer, "wrote-checkout"))); got != "no" {
		t.Fatal("a reviewer must not be able to write the checkout")
	}
	argv := read(t, filepath.Join(reviewer, "argv.txt"))
	for _, want := range []string{"exec", "--ephemeral", "gpt-6.1-sol", `model_reasoning_effort="high"`, "--output-schema", "--ignore-user-config"} {
		if !strings.Contains(argv, want) {
			t.Errorf("reviewer argv lacks %s:\n%s", want, argv)
		}
	}
	if strings.Contains(argv, "resume") || read(t, filepath.Join(reviewer, "stdin.txt")) != "the prompt" {
		t.Fatal("a fresh call does not resume, and the prompt travels on stdin")
	}
	env := read(t, filepath.Join(reviewer, "env.txt"))
	if !strings.Contains(env, WorkerEnv+"=1") || strings.Contains(env, "CLAUDE_CODE_SESSION_ID") {
		t.Fatal("a worker is marked as a worker and never inherits the lead's session")
	}

	writer := filepath.Join(root, "state", "writer")
	if _, err := Run(Call{Route: cfg.MustRoute("sol:high"), Prompt: "p", Dir: writer, Timeout: 20 * time.Second, Writer: true, Persist: true}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(read(t, filepath.Join(writer, "wrote-checkout"))) != "yes" || strings.TrimSpace(read(t, filepath.Join(writer, "wrote-home"))) != "no" {
		t.Fatal("a writer writes the checkout and nothing else on the host")
	}
	if strings.Contains(read(t, filepath.Join(writer, "argv.txt")), "--ephemeral") {
		t.Fatal("a persistent session must not be ephemeral")
	}
	if _, err := Run(Call{Route: cfg.MustRoute("sol:high"), Prompt: "p", Dir: writer, Timeout: 20 * time.Second, Writer: true, Persist: true, Resume: "thread-1"}); err != nil {
		t.Fatal(err)
	}
	if resumed := read(t, filepath.Join(writer, "argv.txt")); !strings.Contains(resumed, "exec\nresume\nthread-1\n") {
		t.Fatalf("an answer continues the same session:\n%s", resumed)
	}
}

func TestClaudeCallIsAttestedByTheModelThatAnswered(t *testing.T) {
	cfg, root := setup(t)
	lane := filepath.Join(root, "state", "claude")
	result, err := Run(Call{Route: cfg.MustRoute("fable:high"), Prompt: "p", Dir: lane, Schema: map[string]any{"type": "object"},
		Timeout: 20 * time.Second, ReadDirs: []string{filepath.Join(root, "state")}})
	if err != nil || result.Answer != `{"ok":true}` || result.Session != "claude-1" {
		t.Fatalf("claude: %+v %v", result, err)
	}
	argv := read(t, filepath.Join(lane, "argv.txt"))
	for _, want := range []string{"claude-fable-5-2", "--effort\nhigh", "Read,Grep,Glob\n", "--no-session-persistence", "--strict-mcp-config", "--json-schema", "stream-json\n--verbose"} {
		if !strings.Contains(argv, want) {
			t.Errorf("claude argv lacks %q:\n%s", want, argv)
		}
	}
	if strings.Contains(argv, "Edit") {
		t.Fatal("a reviewer gets no editing tools")
	}
	history := read(t, filepath.Join(lane, "runs.jsonl"))
	if strings.Count(history, "\n") != 1 || !strings.Contains(history, `"started_at"`) || strings.Contains(history, `"argv"`) {
		t.Fatalf("every finished run is added to the lane's history: %s", history)
	}
	if _, err := Run(Call{Route: cfg.MustRoute("opus:high"), Prompt: "p", Dir: filepath.Join(root, "state", "w"), Timeout: 20 * time.Second, Writer: true, Persist: true, Resume: "claude-1"}); err != nil {
		t.Fatal(err)
	}
	if writer := read(t, filepath.Join(root, "state", "w", "argv.txt")); !strings.Contains(writer, "Edit,Write,Bash") || !strings.Contains(writer, "--resume\nclaude-1") {
		t.Fatalf("a writer gets editing tools and may resume:\n%s", writer)
	}
	_ = os.WriteFile("wrong-model", nil, 0o644)
	if _, err := Run(Call{Route: cfg.MustRoute("fable:high"), Prompt: "p", Dir: filepath.Join(root, "state", "x"), Timeout: 20 * time.Second}); err == nil ||
		!strings.Contains(err.Error(), "did not come from") {
		t.Fatalf("an answer from another model must be rejected, got %v", err)
	}
}

func TestExhaustedLimitMovesToTheNextPermittedAlternate(t *testing.T) {
	cfg, root := setup(t)
	_ = os.WriteFile("limit-codex", nil, 0o644)
	_, err := Run(Call{Route: cfg.MustRoute("sol:high"), Prompt: "p", Dir: filepath.Join(root, "state", "a"), Timeout: 20 * time.Second})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("an exhausted limit must be recognised, got %v", err)
	}
	// sol -> sonnet: the declared alternate on another provider
	result, used, err := RunRouted(cfg, Call{Route: cfg.MustRoute("sol:high"), Prompt: "p", Dir: filepath.Join(root, "state", "b"), Timeout: 20 * time.Second}, nil)
	if err != nil || used.Alias != "sonnet" || used.SubstitutedFrom != "sol:high" || used.Effort != "high" || result.Session != "claude-1" {
		t.Fatalf("the first working alternate answers: %+v %+v %v", result, used, err)
	}
	_, _, err = RunRouted(cfg, Call{Route: cfg.MustRoute("sol:high"), Prompt: "p", Dir: filepath.Join(root, "state", "c"), Timeout: 20 * time.Second},
		func(route config.Route) bool { return route.Provider == "claude" })
	if !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "no permitted alternate") {
		t.Fatalf("a forbidden alternate is never used, got %v", err)
	}
	// A session cannot move to another model: a resumed call is never substituted.
	if _, used, err = RunRouted(cfg, Call{Route: cfg.MustRoute("sol:high"), Prompt: "p", Dir: filepath.Join(root, "state", "d"), Timeout: 20 * time.Second,
		Persist: true, Resume: "thread-1"}, nil); !errors.Is(err, ErrLimit) || used.Alias != "sol" {
		t.Fatalf("a resumed session stays on its model: %+v %v", used, err)
	}
}

func TestSandboxBindsReadableDirectoriesBeforeTheWritableOnes(t *testing.T) {
	argv := strings.Join(Sandbox("/lane", true, []string{"/packets"}, []string{"/board"}, []string{"tool", "arg"}), " ")
	for _, want := range []string{"--ro-bind / /", "--tmpfs /tmp", "--ro-bind /packets /packets", "--bind /lane /lane", "--bind /board /board", "-- tool arg"} {
		if !strings.Contains(argv, want) {
			t.Errorf("sandbox lacks %q: %s", want, argv)
		}
	}
	if strings.Index(argv, "--ro-bind /packets") > strings.Index(argv, "--bind /lane") {
		t.Fatal("a writable lane inside a readable directory must be bound after it")
	}
	if reader := strings.Join(Sandbox("/lane", false, nil, nil, []string{"tool"}), " "); strings.Count(reader, "--bind ") != 1 {
		t.Fatalf("a reader's only writable path is its lane: %s", reader)
	}
}

func TestCallRunsUnderTheChosenProfileAndKeepsItsLoginFresh(t *testing.T) {
	cfg, root := setup(t)
	homes := map[string]string{}
	for _, name := range []string{"one", "two"} {
		homes[name] = filepath.Join(root, "repo", "profiles", name)
		_ = os.MkdirAll(homes[name], 0o700)
		_ = os.WriteFile(filepath.Join(homes[name], "auth.json"), []byte(name+"\n"), 0o600)
	}
	_ = os.WriteFile("refresh-token", nil, 0o644)
	lane := filepath.Join(root, "state", "lane")
	call := Call{Route: cfg.MustRoute("sol:high"), Prompt: "p", Dir: lane, Timeout: 20 * time.Second, Persist: true}
	call.Account = config.Account{Name: "one", Provider: "codex", Home: homes["one"]}
	if _, err := Run(call); err != nil {
		t.Fatal(err)
	}
	// The same session continues under another account: the lane's login now points at that profile.
	call.Account, call.Resume = config.Account{Name: "two", Provider: "codex", Home: homes["two"]}, "thread-1"
	if _, err := Run(call); err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(filepath.Join(lane, "home", "auth.json")); target != filepath.Join(homes["two"], "auth.json") {
		t.Fatalf("the lane authenticates from the chosen profile, got %s", target)
	}
	for name, home := range homes {
		if got := read(t, filepath.Join(home, "auth.json")); got != name+"\nrefreshed\n" {
			t.Fatalf("a token refreshed inside the sandbox must reach profile %s, got %q", name, got)
		}
	}
	if strings.TrimSpace(read(t, filepath.Join(lane, "wrote-checkout"))) != "no" {
		t.Fatal("a writable login file gives a reviewer nothing else to write")
	}
}

func TestAnExhaustedAccountHandsOverBeforeTheModelChanges(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Chdir(root)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	probe := func(account config.Account) accounts.Status {
		return accounts.Status{Account: account, LoggedIn: true, Identity: account.Name, ProbedAt: time.Now().Unix(),
			Windows: []accounts.Window{{Label: "week", Used: map[string]float64{"main": 10, "second": 40}[account.Name]}}}
	}
	previousClaude, previousCodex, previousRun := accounts.ProbeClaude, accounts.ProbeCodex, Run
	accounts.ProbeClaude, accounts.ProbeCodex = probe, probe
	t.Cleanup(func() {
		accounts.ProbeClaude, accounts.ProbeCodex, Run, Accounts = previousClaude, previousCodex, previousRun, nil
	})
	Accounts = accounts.New([]config.Account{{Name: "main", Provider: "codex", Home: "~/main"}, {Name: "second", Provider: "codex", Home: "~/second"}},
		filepath.Join(root, "accounts.json"))

	exhausted := map[string]bool{"main": true}
	var ran []string
	Run = func(call Call) (Result, error) {
		ran = append(ran, call.Route.Alias+"@"+call.Account.Name)
		if exhausted[call.Account.Name] {
			return Result{}, ErrLimit
		}
		return Result{Answer: "ok", Session: "s"}, nil
	}
	_, used, err := RunRouted(cfg, Call{Route: cfg.MustRoute("sol:high"), Dir: root}, nil)
	if err != nil || used.Alias != "sol" || used.Account != "second" || used.SubstitutedFrom != "" || strings.Join(ran, " ") != "sol@main sol@second" {
		t.Fatalf("the second account answers on the same model: %+v %v %v", used, ran, err)
	}
	ran = nil
	if _, used, _ = RunRouted(cfg, Call{Route: cfg.MustRoute("sol:high"), Dir: root}, nil); strings.Join(ran, " ") != "sol@second" {
		t.Fatalf("the exhausted account rests instead of being tried again: %v", ran)
	}
	// A continued session asks for its own account first and may move to another one, but never to another model.
	ran, exhausted = nil, map[string]bool{"second": true}
	route := cfg.MustRoute("sol:high")
	route.Account = "second"
	if _, _, err = RunRouted(cfg, Call{Route: route, Dir: root, Resume: "s"}, nil); !errors.Is(err, ErrLimit) || strings.Join(ran, " ") != "sol@second" {
		t.Fatalf("with every account resting a resumed session reports the limit: %v %v", ran, err)
	}
	// Every codex account is out: only now the declared alternate model runs, on the default claude profile.
	ran = nil
	if _, used, err = RunRouted(cfg, Call{Route: cfg.MustRoute("sol:high"), Dir: root}, nil); err != nil || used.Alias != "sonnet" || used.Account != "" ||
		strings.Join(ran, " ") != "sonnet@" {
		t.Fatalf("the model changes after the accounts are exhausted: %+v %v %v", used, ran, err)
	}
}

func TestOrdinaryLimitReportsAreNotARefusal(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) { _ = os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600) }
	write("stderr.txt", "")
	write("events.jsonl", `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"the code handles 429 and a usage limit"}]}}
{"type":"result","subtype":"error_during_execution","is_error":true,"result":"tool crashed"}
`)
	if limited(dir) {
		t.Fatal("a run that only mentions limits was not refused for one")
	}
	write("events.jsonl", `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}
{"type":"result","subtype":"error","is_error":true,"result":"Claude usage limit reached. Your limit will reset at 3pm."}
`)
	if !limited(dir) {
		t.Fatal("the failure event of the stream names the limit")
	}
	write("events.jsonl", `{"type":"error","message":"You've hit your usage limit."}`+"\n")
	if !limited(dir) {
		t.Fatal("codex reports the limit as an error event")
	}
	write("events.jsonl", "")
	write("stderr.txt", "Error: 429 Too Many Requests")
	if !limited(dir) {
		t.Fatal("stderr still counts")
	}
}

func TestClaudeWorkerGetsOnlyTheListedTools(t *testing.T) {
	cfg, root := setup(t)
	lane := filepath.Join(root, "state", "mcp")
	_, err := Run(Call{Route: cfg.MustRoute("fable:high"), Prompt: "p", Dir: lane, Timeout: 20 * time.Second,
		MCP: map[string]MCPServer{"graph": {Command: "graph-mcp", Args: []string{"graph.json"}, Tools: []string{"query", "node"}},
			"docs": {Command: "docs-mcp", Env: map[string]string{"MODE": "ro"}}}})
	if err != nil {
		t.Fatal(err)
	}
	argv := read(t, filepath.Join(lane, "argv.txt"))
	for _, want := range []string{"--mcp-config\n" + filepath.Join(lane, "mcp.json"), "--strict-mcp-config", "--permission-mode\ndontAsk",
		"--allowedTools\nRead,Grep,Glob,mcp__docs,mcp__graph__node,mcp__graph__query\n"} {
		if !strings.Contains(argv, want) {
			t.Errorf("claude argv lacks %q:\n%s", want, argv)
		}
	}
	if strings.Contains(argv, "--dangerously-skip-permissions") {
		t.Fatal("with servers attached nothing outside the list may run")
	}
	servers := read(t, filepath.Join(lane, "mcp.json"))
	for _, want := range []string{`"graph"`, `"command": "graph-mcp"`, `"graph.json"`, `"MODE": "ro"`, `"type": "stdio"`} {
		if !strings.Contains(servers, want) {
			t.Errorf("the server file lacks %s:\n%s", want, servers)
		}
	}
	plain := filepath.Join(root, "state", "plain")
	if _, err := Run(Call{Route: cfg.MustRoute("fable:high"), Prompt: "p", Dir: plain, Timeout: 20 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if argv := read(t, filepath.Join(plain, "argv.txt")); strings.Contains(argv, "--mcp-config") || !strings.Contains(argv, "--dangerously-skip-permissions") {
		t.Fatalf("a worker without servers runs as before:\n%s", argv)
	}
}
