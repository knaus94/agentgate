package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/ops"
)

// record keeps the receipt of the lane's present run, and adds every finished run to the lane's history,
// so a viewer can show how long each attempt took.
func record(call Call, argv []string, cmd *exec.Cmd, state string, started int64) error {
	receipt := map[string]any{"state": state, "route": call.Route, "account": call.Account.Name, "argv": receiptArguments(argv), "writer": call.Writer,
		"resumed": call.Resume != "", "started_at": started, "observed_at": time.Now().Unix()}
	if cmd.Process != nil {
		receipt["pid"] = cmd.Process.Pid
		if identity := ops.ProcessIdentity(cmd.Process.Pid); identity != "" {
			receipt["process_identity"] = identity
		}
	}
	if cmd.ProcessState != nil {
		receipt["returncode"] = cmd.ProcessState.ExitCode()
	}
	if err := ops.WriteJSON(filepath.Join(call.Dir, "process.json"), receipt); err != nil {
		return fmt.Errorf("write process receipt: %w", err)
	}
	if state != "exited" {
		return nil
	}
	delete(receipt, "argv")
	line, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	history, err := os.OpenFile(filepath.Join(call.Dir, "runs.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer history.Close()
	_, err = history.Write(append(line, '\n'))
	return err
}

func receiptArguments(argv []string) []string {
	out := append([]string(nil), argv...)
	for index, arg := range out {
		if prefix, _, ok := strings.Cut(arg, ".env="); ok && strings.HasPrefix(prefix, "mcp_servers.") {
			out[index] = prefix + ".env=[redacted]"
		}
	}
	return out
}

// execute runs the sandboxed process with the prompt on stdin and returns its stdout.
func execute(call Call, argv []string, env []string, stdoutName string) error {
	stdout, err := os.Create(filepath.Join(call.Dir, stdoutName))
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(call.Dir, "stderr.txt"))
	if err != nil {
		return err
	}
	defer stderr.Close()
	wrapped := Sandbox(call.Dir, call.Writer, call.ReadDirs, call.Writable, argv)
	cmd := exec.Command(wrapped[0], wrapped[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = strings.NewReader(call.Prompt), stdout, stderr, env
	// The receipt of a running lane carries the pid, so it is written once the process exists.
	started := time.Now().Unix()
	if err := record(call, argv, cmd, "starting", started); err != nil {
		return err
	}
	var receiptErr error
	err = ops.RunStarted(cmd, call.Timeout, func() {
		receiptErr = record(call, argv, cmd, "starting", started)
		if receiptErr != nil {
			_ = cmd.Process.Kill()
		}
	})
	err = errors.Join(err, receiptErr, record(call, argv, cmd, "exited", started))
	if err != nil {
		if ops.IsCancelled(err) || errors.Is(err, ops.ErrTimeout) {
			return err
		}
		return fmt.Errorf("%s failed (%v); inspect %s", call.Route, err, filepath.Join(call.Dir, "stderr.txt"))
	}
	return nil
}
