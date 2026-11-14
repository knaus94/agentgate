package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/knaus94/agentgate/internal/accounts"
)

// Credentials are copied, never bind-mounted from a real CLI profile. Both CLIs
// may refresh tokens by replacing a file atomically; a writable file mount in a
// read-only directory cannot support that. The worker can replace its private
// copy, and the parent reconciles it without exposing the profile to writes.
type credentialSnapshot struct {
	source, target string
	providerLock   string
	original       []byte
}

func prepareCredential(call Call) (*credentialSnapshot, error) {
	variable, fallback, filename := "CODEX_HOME", ".codex", "auth.json"
	if call.Route.Provider == "claude" {
		variable, fallback, filename = "CLAUDE_CONFIG_DIR", ".claude", ".credentials.json"
	}
	source := filepath.Join(profile(call, variable, fallback), filename)
	target := filepath.Join(call.Dir, "home", filename)
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink == 0 {
		from, sourceErr := filepath.EvalSymlinks(source)
		to, targetErr := filepath.EvalSymlinks(target)
		if sourceErr == nil && targetErr == nil && from == to {
			return nil, errors.New("the selected profile must not be the worker's private home")
		}
	}
	// A reused lane must never retain credentials from the previous account,
	// including when its new account has been logged out since the quota probe.
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("clear lane credentials: %w", err)
	}
	real, err := filepath.EvalSymlinks(source)
	if errors.Is(err, os.ErrNotExist) && call.Account.Home == "" {
		return nil, nil // a default profile may authenticate through the environment
	}
	if err != nil {
		return nil, fmt.Errorf("read credentials from selected %s profile: %w", call.Route.Provider, err)
	}
	raw, err := readCredential(real)
	if err != nil {
		return nil, err
	}
	if call.Account.Home != "" && !call.Account.Paid && call.Route.Provider == "codex" {
		var auth struct {
			Mode   string          `json:"auth_mode"`
			Key    string          `json:"OPENAI_API_KEY"`
			Tokens json.RawMessage `json:"tokens"`
		}
		_ = json.Unmarshal(raw, &auth)
		if auth.Mode == "apikey" || (auth.Key != "" && (len(auth.Tokens) == 0 || string(auth.Tokens) == "null")) {
			return nil, errors.New("selected profile now uses API-key billing; enable --paid for that account before using it")
		}
	}
	if err := replacePrivateFile(target, raw); err != nil {
		return nil, fmt.Errorf("prepare lane credentials: %w", err)
	}
	providerLock := ""
	if call.Route.Provider == "codex" {
		// Codex's FileAuthStorage uses this lock for cross-process commits.
		providerLock = filepath.Join(filepath.Dir(source), "auth.lock")
	}
	return &credentialSnapshot{source: real, target: target, original: raw, providerLock: providerLock}, nil
}

func readCredential(path string) ([]byte, error) {
	// Refuse links and nonregular files from a finished worker. A FIFO must not
	// hang writeback, and replacing the private file with a link is not refresh.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("credential path %s must be a regular file", path)
	}
	raw, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if len(raw) > 4<<20 || json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, fmt.Errorf("credential file %s must contain a JSON object smaller than 4 MiB", path)
	}
	return raw, nil
}

// sync runs even after a failed call: an otherwise unsuccessful turn can have
// refreshed its OAuth token. A concurrent login or refresh always wins over an
// older snapshot. Never silently overwrite another process's credentials.
func (s *credentialSnapshot) sync() error {
	if s == nil {
		return nil
	}
	next, err := readCredential(s.target)
	if err != nil {
		return fmt.Errorf("refreshed credentials are invalid; profile left unchanged: %w", err)
	}
	if bytes.Equal(next, s.original) {
		return nil
	}
	paths := []string{s.source + ".agentgate.lock"}
	if s.providerLock != "" {
		paths = append(paths, s.providerLock)
	}
	sort.Strings(paths)
	for _, path := range paths {
		lock, err := lockCredentialFile(path)
		if err != nil {
			return err
		}
		defer lock.Close()
	}
	current, err := readCredential(s.source)
	if err != nil {
		return fmt.Errorf("credential profile changed while worker ran; private copy retained at %s: %w", s.target, err)
	}
	if bytes.Equal(current, next) {
		return nil
	}
	if !bytes.Equal(current, s.original) {
		return fmt.Errorf("credential profile changed while worker ran; preserved it and retained refreshed credentials at %s", s.target)
	}
	if err := replacePrivateFile(s.source, next); err != nil {
		return fmt.Errorf("save refreshed credentials; private copy retained at %s: %w", s.target, err)
	}
	return nil
}

func lockCredentialFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock credentials for writeback: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return file, nil
		}
		if (!errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN)) || time.Now().After(deadline) {
			file.Close()
			return nil, fmt.Errorf("credential writeback lock unavailable; private copy retained: %w", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func replacePrivateFile(path string, raw []byte) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".agentgate-credential-*")
	if err != nil {
		return err
	}
	defer func() { file.Close(); os.Remove(file.Name()) }()
	if _, err := file.Write(raw); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// link updates a read-only supporting file and removes a stale link if its
// source disappeared. Credentials deliberately do not use this helper.
func link(source, target string) error {
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.Symlink(source, target)
}

func profile(call Call, variable, fallback string) string {
	if call.Account.Home != "" {
		return accounts.Home(call.Account)
	}
	if actual := os.Getenv(variable); actual != "" {
		return actual
	}
	return filepath.Join(userHome(), fallback)
}

func userHome() string { home, _ := os.UserHomeDir(); return home }
