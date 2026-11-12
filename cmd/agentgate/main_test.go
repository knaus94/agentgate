package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knaus94/agentgate/internal/assets"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agentgate-bin-")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "agentgate")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// The fakes answer like the provider CLIs. Either can be asked to write or to review: a review prompt
// names its axes, and the fake passes exactly those; anything else is a writer assignment.
const fakeBody = `
prompt=$(cat)
axes=$(printf '%s' "$prompt" | sed -n 's/.*your axes: \([a-z, ]*\)\..*/\1/p' | head -1)
if printf '%s' "$prompt" | grep -q 'context pack'; then
  answer='{"summary":"one text file","files":[{"path":"a.txt","role":"owned","symbols":[]}],"callers":[],"invariants":[],"conventions":[],"unknowns":[]}'
elif [ -n "$axes" ]; then
  reports=""
  for axis in $(printf '%s' "$axes" | tr ',' ' '); do
    reports="$reports${reports:+,}{\"axis\":\"$axis\",\"status\":\"PASS\",\"findings\":[],\"checked\":[\"read the diff\"],\"resolutions\":[]}"
  done
  answer="{\"reports\":[$reports]}"
else
  echo fixed > a.txt
  answer='{"status":"COMPLETED","summary":"edited a.txt","blockers":[],"handoff":"","deviations":[]}'
fi
`

const fakeCodex = "#!/bin/sh" + fakeBody + `
out=""; prev=""
for arg in "$@"; do [ "$prev" = "-o" ] && out="$arg"; prev="$arg"; done
echo '{"type":"thread.started","thread_id":"thread-1"}'
echo '{"type":"turn.completed"}'
printf '%s' "$answer" > "$out"
`

const fakeClaude = "#!/bin/sh" + fakeBody + `
model=""; prev=""
for arg in "$@"; do [ "$prev" = "--model" ] && model="$arg"; prev="$arg"; done
printf '{"type":"result","is_error":false,"subtype":"success","session_id":"claude-1","result":"","structured_output":%s,"modelUsage":{"%s":{}}}\n' "$answer" "$model"
`

type sandbox struct {
	t    *testing.T
	repo string
	env  []string
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	bin := filepath.Join(repo, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(bin, "codex"), []byte(fakeCodex), 0o755)
	_ = os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "a.txt"), []byte("before\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("bin/\n"), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=F", "-c", "user.email=f@example.invalid", "commit", "-qm", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	// The tool installs into the home directories of both CLIs; the test has its own.
	env := []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "home"),
		"CODEX_HOME=" + filepath.Join(root, "home", ".codex"),
		"XDG_STATE_HOME=" + filepath.Join(root, "state"), "XDG_CONFIG_HOME=" + filepath.Join(root, "config"),
		"CLAUDE_CONFIG_DIR=" + filepath.Join(root, "claude"), "AGENTGATE_SESSION=cli-session"}
	return &sandbox{t: t, repo: repo, env: env}
}

func (s *sandbox) run(stdin string, args ...string) (string, string, int) {
	s.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir, cmd.Env, cmd.Stdin = s.repo, s.env, strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		s.t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

func (s *sandbox) ok(stdin string, args ...string) map[string]any {
	s.t.Helper()
	stdout, stderr, code := s.run(stdin, args...)
	if code != 0 {
		s.t.Fatalf("%v failed (%d): %s", args, code, stderr)
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		s.t.Fatalf("%v did not print JSON: %s", args, stdout)
	}
	return out
}

func TestCommandLineBasics(t *testing.T) {
	s := newSandbox(t)
	for _, args := range [][]string{{}, {"help"}, {"--help"}} {
		if stdout, _, code := s.run("", args...); code != 0 || !strings.Contains(stdout, "/dev [-l|-m|-h|-x]") {
			t.Errorf("%v must print usage", args)
		}
	}
	if stdout, _, code := s.run("", "version"); code != 0 || strings.TrimSpace(stdout) == "" {
		t.Fatal("version prints the version")
	}
	if _, stderr, code := s.run("", "frobnicate"); code != 1 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("an unknown command fails with a message: %s", stderr)
	}
	if guide, _, _ := s.run("", "guide"); guide != assets.Guide || !strings.Contains(guide, "agentgate reference") {
		t.Fatal("guide prints the lead guide")
	}
	parsed := s.ok("-h fix the totals; rm -rf /", "parse", "--file", "-")
	if parsed["mode"] != "heavy" || parsed["action"] != "fix" || parsed["task"] != "the totals; rm -rf /" {
		t.Fatalf("the task is parsed as data: %v", parsed)
	}
	if _, stderr, code := s.run("--bogus task", "parse"); code != 1 || !strings.Contains(stderr, "invalid") {
		t.Fatalf("a bad request is rejected: %s", stderr)
	}
	if _, stderr, code := s.run(`{"reason":"x","reasn":"typo"}`, "hold", "--file", "-"); code != 1 || !strings.Contains(stderr, "reasn") {
		t.Fatalf("a misspelled input field is an error: %s", stderr)
	}
	if _, stderr, code := s.run("", "finish"); code != 1 || !strings.Contains(stderr, "--file") {
		t.Fatalf("a missing input is explained: %s", stderr)
	}
	if hook, _, code := s.run(`{"cwd":"`+s.repo+`","session_id":"cli-session"}`, "hook"); code != 0 || strings.TrimSpace(hook) != "{}" {
		t.Fatalf("without a task the Stop hook allows the stop: %s", hook)
	}
	if hook, _, code := s.run("not json", "hook"); code != 0 || strings.TrimSpace(hook) != "{}" {
		t.Fatal("a broken hook payload never blocks the session")
	}
	if status := s.ok("", "status"); status["gates"] != nil || status["read_only"] != true {
		t.Fatalf("status without a task is an empty overview: %v", status)
	}
}

func TestReferenceDocumentsEveryCommand(t *testing.T) {
	for _, command := range []string{"parse", "preview", "route", "architecture", "design", "consult", "launch", "answer", "tell", "check",
		"review", "visual", "triage", "fix", "finish", "hold", "resume", "stop", "status", "watch", "doctor", "install", "update", "modes",
		"config", "prune", "version"} {
		if !strings.Contains(assets.Reference, "`"+command) {
			t.Errorf("the reference does not document %q", command)
		}
		if !strings.Contains(usage, command) {
			t.Errorf("the usage text does not list %q", command)
		}
	}
	for _, field := range []string{"assessment", "streams", "checks", "baseline", "lead_review", "dispositions", "reassessment",
		"architecture_evidence", "revise_blockers", "usage_context", "skip_reason"} {
		if !strings.Contains(assets.Reference, field) {
			t.Errorf("the reference does not document the %q field", field)
		}
	}
}

func TestConfigCommands(t *testing.T) {
	s := newSandbox(t)
	if path, _, _ := s.run("", "config", "path"); !strings.HasSuffix(strings.TrimSpace(path), "agentgate/config.json") {
		t.Fatalf("config path: %s", path)
	}
	s.ok("", "config", "init")
	if _, _, code := s.run("", "config", "init"); code != 1 {
		t.Fatal("init does not overwrite")
	}
	set := s.ok("", "config", "set", "modes.heavy.backend", "sol:xhigh")
	if set["modes"].(map[string]any)["heavy"].(map[string]any)["backend"] != "sol:xhigh" {
		t.Fatalf("set returns the effective table: %v", set["modes"])
	}
	if _, stderr, code := s.run("", "config", "set", "modes.heavy.backend", "ghost:high"); code != 1 || !strings.Contains(stderr, "nothing changed") {
		t.Fatalf("an invalid value is rejected: %s", stderr)
	}
	if table, _, _ := s.run("", "modes"); !strings.Contains(table, "backend   sol:xhigh") {
		t.Fatalf("modes shows the override:\n%s", table)
	}
	unset := s.ok("", "config", "unset", "modes.heavy.backend")
	if unset["modes"].(map[string]any)["heavy"].(map[string]any)["backend"] != "opus:high" {
		t.Fatal("unset restores the default")
	}
	if _, _, code := s.run("", "config", "bogus"); code != 1 {
		t.Fatal("an unknown config subcommand fails")
	}
}

func TestWholeTaskThroughTheCommandLine(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap is not installed")
	}
	s := newSandbox(t)
	installed := s.ok("", "install")
	if len(installed["changed"].([]any)) != 2 {
		t.Fatalf("install adds the command and the hook: %v", installed)
	}
	plan := `{"request":"change the file","assessment":{"size":"ordinary","evidence":"one file"},"brief":"a.txt must say fixed",
	  "streams":[{"name":"core","domain":"backend","files":["a.txt"]}],
	  "checks":[["grep","-q","fixed","a.txt"],["sh","-c","exit 7"]]}`
	routed := s.ok(plan, "route", "--file", "-")
	if routed["decision"].(map[string]any)["mode"] != "medium" {
		t.Fatalf("route: %v", routed)
	}
	launched := s.ok(plan, "launch", "--file", "-")
	if launched["phase"] != "awaiting-review" || launched["writers"].([]any)[0].(map[string]any)["status"] != "COMPLETED" {
		t.Fatalf("launch: %v", launched)
	}
	if raw, _ := os.ReadFile(filepath.Join(s.repo, "a.txt")); string(raw) != "fixed\n" {
		t.Fatal("the writer edits the checkout")
	}
	if _, stderr, code := s.run(plan, "launch", "--file", "-"); code != 1 || !strings.Contains(stderr, "awaiting-review") {
		t.Fatalf("a second launch of a written task is refused: %s", stderr)
	}
	hook, _, _ := s.run(`{"cwd":"`+s.repo+`","session_id":"cli-session"}`, "hook")
	if !strings.Contains(hook, `"decision": "block"`) {
		t.Fatalf("the Stop hook blocks an unreviewed task: %s", hook)
	}
	stdout, _, code := s.run("", "check", "--all")
	if code != 2 || !strings.Contains(stdout, `"failed": true`) || !strings.Contains(stdout, `"returncode": 7`) {
		t.Fatalf("a failing check exits with 2 and reports its code: %d %s", code, stdout)
	}
	reviewed := s.ok(`{"skip_checks": true}`, "review", "--file", "-")
	if reviewed["phase"] != "reviewed" || len(reviewed["blocking"].([]any)) != 0 {
		t.Fatalf("review: %v", reviewed)
	}
	if _, stderr, code := s.run(`{"summary":"done"}`, "finish", "--file", "-"); code != 1 || !strings.Contains(stderr, "exit 7") {
		t.Fatalf("finish refuses while a check fails: %s", stderr)
	}
	if _, stderr, code := s.run(`{"request":"-m x","brief":"b","streams":[],"modee":"light"}`, "route", "--file", "-"); code != 1 || !strings.Contains(stderr, "modee") {
		t.Fatalf("a misspelled plan field must be an error, got %q", stderr)
	}
	panel, _, _ := s.run("", "watch", "--once")
	for _, want := range []string{"TASKS 1", "medium", "reviewed", "writer  core", "opus:medium", "review  architecture", "fable:medium", "passed", "failed (7)", "steps"} {
		if !strings.Contains(panel, want) {
			t.Errorf("watch lacks %q:\n%s", want, panel)
		}
	}
	held := s.ok(`{"reason":"the failing check needs the user's decision"}`, "hold", "--file", "-")
	if held["phase"] != "held" {
		t.Fatalf("hold: %v", held)
	}
	if hook, _, _ := s.run(`{"cwd":"`+s.repo+`","session_id":"cli-session"}`, "hook"); strings.TrimSpace(hook) != "{}" {
		t.Fatalf("an honest hold lets the session stop: %s", hook)
	}
	s.ok("", "resume")
	s.ok(`{"reason":"dropped by the user","cancelled":true}`, "hold", "--file", "-")
	if pruned := s.ok("", "prune"); pruned["removed"] != float64(0) {
		t.Fatalf("a fresh task is not pruned: %v", pruned)
	}
	removed := s.ok("", "install", "--uninstall")
	if len(removed["changed"].([]any)) != 2 {
		t.Fatalf("uninstall removes the command and the hook: %v", removed)
	}
}

func TestCodexLeadFromTheCommandLine(t *testing.T) {
	s := newSandbox(t)
	codex := &sandbox{t: t, repo: s.repo, env: append(append([]string{}, s.env...), "AGENTGATE_SESSION=", "CODEX_THREAD_ID=thread-1", "CODEX_TOOL_CALL_ID=call-1")}
	guide, _, _ := codex.run("", "guide")
	plain, _, _ := s.run("", "guide")
	if !strings.Contains(guide, "## Leading from Codex") || strings.Contains(plain, "## Leading from Codex") {
		t.Fatal("a Codex lead gets its own part of the guide, a Claude lead does not")
	}
	installed := codex.ok("", "install", "--lead", "codex")
	if fmt.Sprint(installed["leads"]) != "[codex]" {
		t.Fatalf("install --lead codex: %v", installed)
	}
	if table, _, _ := codex.run("", "modes"); !strings.Contains(table, "backend   astra:medium") || strings.Contains(table, "opus") {
		t.Fatalf("a codex-only install uses the codex profile:\n%s", table)
	}
	profiles := codex.ok("", "config", "profiles")
	if profiles["active"] != "codex" {
		t.Fatalf("the active profile is reported: %v", profiles)
	}
	// Inside Codex's own sandbox the tool says what to do instead of failing somewhere deep.
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.MkdirAll(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	// Codex sets its sandbox variables on every command, so they prove nothing; an unwritable state does.
	if _, _, code := (&sandbox{t: t, repo: s.repo, env: append(append([]string{}, codex.env...), "CODEX_SANDBOX_NETWORK_DISABLED=1")}).run("", "config", "show"); code != 0 {
		t.Fatal("the sandbox variable alone must not stop a command that a rule let out")
	}
	boxed := &sandbox{t: t, repo: s.repo, env: append(append([]string{}, codex.env...), "XDG_STATE_HOME="+filepath.Join(locked, "state"))}
	if _, stderr, code := boxed.run("{}", "launch", "--file", "-"); code != 1 || !strings.Contains(stderr, "one plain command") {
		t.Fatalf("a sandboxed call is refused with the way out: %s", stderr)
	}
	if out, _, code := boxed.run("", "version"); code != 0 || out == "" {
		t.Fatal("reading commands still work inside the sandbox")
	}
	if _, stderr, code := s.run("", "install", "--lead", "gemini"); code != 1 || !strings.Contains(stderr, "claude, codex or both") {
		t.Fatalf("an unknown lead is refused: %s", stderr)
	}
}
