package provider

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/knaus94/agentgate/internal/ops"
)

func run(call Call) (result Result, err error) {
	defer func() { result.Dir = call.Dir }()
	if call.Route.Provider != "claude" && call.Route.Provider != "codex" {
		return Result{}, fmt.Errorf("unknown provider %q", call.Route.Provider)
	}
	if call.Dir == "" || call.Timeout < 0 {
		return Result{}, errors.New("a worker needs a lane directory and a nonnegative timeout")
	}
	if err := ops.Checkpoint(); err != nil {
		return Result{}, err
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		return Result{}, errors.New("bwrap is not installed; refusing to run a model without the sandbox")
	}
	if err := os.MkdirAll(filepath.Join(call.Dir, "home"), 0o700); err != nil {
		return Result{}, err
	}
	// The worker is given the lane by its real path: what the CLI is told and what the sandbox mounts agree.
	if resolved, err := filepath.EvalSymlinks(call.Dir); err == nil {
		call.Dir = resolved
	}
	credentials, err := prepareCredential(call)
	if err != nil {
		return Result{}, err
	}
	defer func() { err = errors.Join(err, credentials.sync()) }()
	invoke := runCodex
	if call.Route.Provider == "claude" {
		invoke = runClaude
	}
	result, err = invoke(call)
	if err != nil && !ops.IsCancelled(err) && !errors.Is(err, ops.ErrTimeout) && limited(call.Dir) {
		return result, fmt.Errorf("%w: %s (%v)", ErrLimit, call.Route, err)
	}
	return result, err
}
