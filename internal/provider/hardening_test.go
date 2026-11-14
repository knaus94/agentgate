package provider

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/knaus94/agentgate/internal/config"
)

func TestSelectedAccountClearsAmbientAuthenticationAndLeadContext(t *testing.T) {
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDECODE", "CLAUDE_CODE_USE_VERTEX", "CODEX_THREAD_ID", "FIGMA_ACCESS_TOKEN", "AGENTGATE_FIGMA_HOME"} {
		t.Setenv(name, "test-only-value")
	}
	env := workerEnvironment(Call{Account: config.Account{Home: "/test/profile"}}, "CODEX_HOME=/test/lane")
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "OPENAI_API_KEY", "OPENAI_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDECODE", "CLAUDE_CODE_USE_VERTEX", "CODEX_THREAD_ID", "FIGMA_ACCESS_TOKEN", "AGENTGATE_FIGMA_HOME":
			t.Errorf("selected profile inherited forbidden variable %s", name)
		}
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "CODEX_HOME=/test/lane") || !strings.Contains(joined, WorkerEnv+"=1") {
		t.Fatal("the chosen lane and worker marker must be retained")
	}
	if !strings.Contains(strings.Join(workerEnvironment(Call{}), "\n"), "OPENAI_API_KEY=test-only-value") {
		t.Fatal("the default profile still supports explicitly configured environment authentication")
	}
}

func TestPrivateSessionsStayMaskedAfterAncestorBinds(t *testing.T) {
	_, root := setup(t)
	private := PrivateDir()
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(private, "session")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	lane := filepath.Join(root, "lane")
	if err := os.MkdirAll(lane, 0o700); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(root, "read-link")
	if err := os.Symlink(root, linkPath); err != nil {
		t.Fatal(err)
	}
	readable, writable := []string{linkPath}, []string{root}
	argv := Sandbox(lane, false, readable, writable, []string{"sh", "-c", `test ! -e "$1"`, "sh", secret})
	if !reflect.DeepEqual(readable, []string{linkPath}) || !reflect.DeepEqual(writable, []string{root}) {
		t.Fatal("sandbox normalized caller-owned shared slices in place")
	}
	if output, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("a later parent mount revealed the lead's private session: %v %s", err, output)
	}
}

func TestCustomFigmaProfileStaysMaskedThroughSymlinkAndAncestorMount(t *testing.T) {
	_, root := setup(t)
	private := filepath.Join(root, "custom-figma", "profile")
	lane := filepath.Join(root, "lane")
	for _, dir := range []string{private, lane} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(private, "cookies")
	if err := os.WriteFile(secret, []byte("test session"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "figma-link")
	if err := os.Symlink(filepath.Dir(private), alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTGATE_FIGMA_HOME", alias)
	argv := Sandbox(lane, false, []string{root}, []string{root}, []string{"sh", "-c", `test ! -e "$1" && test ! -e "$2"`, "sh", secret, filepath.Join(alias, "profile", "cookies")})
	if output, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("custom Figma session was exposed through a resolved or aliased path: %v %s", err, output)
	}
	if err := os.MkdirAll(PrivateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTGATE_FIGMA_HOME", PrivateDir())
	argv = Sandbox(lane, false, nil, nil, []string{"true"})
	masks := 0
	for index := 0; index+1 < len(argv); index++ {
		if argv[index] == "--tmpfs" && argv[index+1] == PrivateDir() {
			masks++
		}
	}
	if masks != 1 {
		t.Fatalf("the same private directory must be masked once, got %d", masks)
	}
}

func credentialFixture(t *testing.T) (Call, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "profile")
	lane := filepath.Join(root, "lane")
	for _, dir := range []string{home, filepath.Join(lane, "home")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(home, "auth.json")
	if err := os.WriteFile(source, []byte(`{"tokens":{"access_token":"old"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return Call{Dir: lane, Route: config.Route{Provider: "codex"}, Account: config.Account{Name: "test", Provider: "codex", Home: home}}, source
}

func TestCredentialWritebackPreservesConcurrentLogin(t *testing.T) {
	call, source := credentialFixture(t)
	snapshot, err := prepareCredential(call)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshot.target, []byte(`{"tokens":{"access_token":"worker"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(`{"tokens":{"access_token":"new-login"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.sync(); err == nil || !strings.Contains(err.Error(), "changed while worker ran") {
		t.Fatalf("concurrent login must win with an explicit conflict, got %v", err)
	}
	if got := read(t, source); !strings.Contains(got, "new-login") {
		t.Fatal("concurrent credentials were overwritten")
	}
	if got := read(t, snapshot.target); !strings.Contains(got, "worker") {
		t.Fatal("the recoverable refreshed copy was lost")
	}
}

func TestMissingSelectedProfileCannotReusePreviousCredentials(t *testing.T) {
	call, source := credentialFixture(t)
	snapshot, err := prepareCredential(call)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareCredential(call); err == nil {
		t.Fatal("a logged-out selected profile must fail")
	}
	if _, err := os.Lstat(snapshot.target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale credentials survived account selection")
	}
}

func TestInvalidRefreshNeverCorruptsProfile(t *testing.T) {
	call, source := credentialFixture(t)
	before := read(t, source)
	snapshot, err := prepareCredential(call)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshot.target, []byte("truncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.sync(); err == nil {
		t.Fatal("invalid credential refresh must fail")
	}
	if read(t, source) != before {
		t.Fatal("a corrupt private credential file reached the real profile")
	}
}

func TestCodexMCPUsesFullToolSetWhenNoAllowlistIsGiven(t *testing.T) {
	cfg, root := setup(t)
	lane := filepath.Join(root, "lane")
	_, err := Run(Call{Route: cfg.MustRoute("sol:high"), Dir: lane, Prompt: "p", Timeout: 10 * time.Second,
		MCP: map[string]MCPServer{"docs": {Command: "docs", Env: map[string]string{"A.B": "quoted", "Z": "last"}}}})
	if err != nil {
		t.Fatal(err)
	}
	argv := read(t, filepath.Join(lane, "argv.txt"))
	if strings.Contains(argv, "enabled_tools=") {
		t.Fatal("empty extension allowlist disabled all Codex MCP tools")
	}
	if !strings.Contains(argv, `env={"A.B"="quoted","Z"="last"}`) {
		t.Fatalf("environment keys were not literal and stable: %s", argv)
	}
}

func TestSuccessfulResultMentioningQuotaIsNotARefusal(t *testing.T) {
	dir := t.TempDir()
	for _, text := range []string{
		`{"type":"result","subtype":"success","result":"Implemented a quota and 429 limiter"}`,
		`not json; quota`,
	} {
		if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if limited(dir) {
			t.Fatalf("ordinary output was mistaken for a quota refusal: %s", text)
		}
	}
}

func TestCodexUsesTheFinalTerminalEvent(t *testing.T) {
	cfg, root := setup(t)
	fake := strings.Replace(fakeCodex, `echo '{"type":"turn.completed"}'`, `echo '{"type":"turn.completed"}'
echo '{"type":"turn.failed","error":{"message":"incomplete"}}'`, 1)
	if err := os.WriteFile("bin/codex", []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Run(Call{Route: cfg.MustRoute("sol:high"), Dir: filepath.Join(root, "lane"), Prompt: "p", Timeout: 10 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "did not complete") {
		t.Fatalf("a later failed turn must invalidate earlier completion: %v", err)
	}
}

func TestRoutingSkipsDisallowedPrimaryAndReturnsActualSessionDirectory(t *testing.T) {
	cfg, root := setup(t)
	previousRun, previousAccounts := Run, Accounts
	t.Cleanup(func() { Run, Accounts = previousRun, previousAccounts })
	Accounts = nil
	base := filepath.Join(root, "primary")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	var calls []Call
	Run = func(call Call) (Result, error) { calls = append(calls, call); return Result{Session: "s"}, nil }
	result, route, err := RunRouted(cfg, Call{Route: cfg.MustRoute("sol:high"), Dir: base}, func(route config.Route) bool { return route.Alias == "sol" })
	if err != nil || route.Alias != "sonnet" || len(calls) != 1 || result.Dir != base+"-alt1" {
		t.Fatalf("a disallowed author route ran or the actual session directory was lost: %+v %+v %v", result, route, err)
	}
	if !reflect.DeepEqual(calls[0].ReadDirs, []string{base}) {
		t.Fatal("alternate cannot read original assignment through /tmp isolation")
	}
	calls = nil
	if _, _, err := RunRouted(cfg, Call{Route: cfg.MustRoute("sol:high"), Dir: base}, func(config.Route) bool { return true }); err == nil || len(calls) != 0 {
		t.Fatal("all-disallowed routing must fail without invoking any provider")
	}
}
