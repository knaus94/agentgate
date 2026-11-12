package provider

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
printf '{"is_error":false,"subtype":"success","session_id":"claude-1","result":"plain","structured_output":{"ok":true},"modelUsage":{"%s":{}}}' "$model"
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
	for _, want := range []string{"claude-fable-5-2", "--effort\nhigh", "Read,Grep,Glob\n", "--no-session-persistence", "--strict-mcp-config", "--json-schema"} {
		if !strings.Contains(argv, want) {
			t.Errorf("claude argv lacks %q:\n%s", want, argv)
		}
	}
	if strings.Contains(argv, "Edit") {
		t.Fatal("a reviewer gets no editing tools")
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
