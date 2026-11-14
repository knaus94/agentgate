package accounts

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// codexRateLimits uses one short-lived app-server process and one deadline for
// the complete handshake. Its reader is cancellable even if unsolicited events
// fill the queue, so failed probes never leave blocked goroutines behind.
func codexRateLimits(home string) ([]byte, error) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binary, "--no-daemon", "app-server", "--stdio", "--disable", "plugins", "--disable", "plugin_discovery_prefetch")
	cmd.Env = codexProfileEnvironment(home)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, err
	}
	stop := make(chan struct{})
	readDone := make(chan struct{})
	defer func() {
		close(stop)
		_ = stdin.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		<-readDone
	}()
	replies := make(chan []byte, 8)
	readErrors := make(chan error, 1)
	go func() {
		defer close(readDone)
		defer close(replies)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64<<10), 16<<20)
		for scanner.Scan() {
			select {
			case replies <- append([]byte(nil), scanner.Bytes()...):
			case <-stop:
				return
			}
		}
		readErrors <- scanner.Err()
	}()
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	call := func(id int, method string, params any) ([]byte, error) {
		request := struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params any    `json:"params,omitempty"`
		}{id, method, params}
		if err := json.NewEncoder(stdin).Encode(request); err != nil {
			return nil, fmt.Errorf("write codex request: %w", err)
		}
		for {
			select {
			case line, open := <-replies:
				if !open {
					if err := <-readErrors; err != nil {
						return nil, fmt.Errorf("read codex app-server: %w", err)
					}
					return nil, errors.New("codex app-server closed before replying")
				}
				var message struct {
					ID     int             `json:"id"`
					Result json.RawMessage `json:"result"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if json.Unmarshal(line, &message) != nil || message.ID != id {
					continue
				}
				if message.Error != nil {
					return nil, errors.New(message.Error.Message)
				}
				if len(message.Result) == 0 || string(message.Result) == "null" {
					return nil, fmt.Errorf("codex %s returned no result", method)
				}
				return message.Result, nil
			case <-deadline.C:
				return nil, errors.New("codex app-server timed out")
			}
		}
	}
	params := map[string]any{"clientInfo": map[string]string{"name": "agentgate", "version": "1"}}
	if _, err := call(1, "initialize", params); err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintln(stdin, `{"method":"initialized","params":{}}`); err != nil {
		return nil, err
	}
	return call(2, "account/rateLimits/read", nil)
}

func codexProfileEnvironment(home string) []string {
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "CODEX_") || name == "OPENAI_API_KEY" || name == "OPENAI_BASE_URL" {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "CODEX_HOME="+home)
}
