package ops

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

func killGroup(pid int, signal syscall.Signal) { _ = syscall.Kill(-pid, signal) }

// Terminate stops a whole process group: grandchildren outlive their leader otherwise.
func Terminate(cmd *exec.Cmd, done <-chan error) {
	killGroup(cmd.Process.Pid, syscall.SIGTERM)
	reaped := false
	select {
	case <-done:
		reaped = true
	case <-time.After(5 * time.Second):
	}
	killGroup(cmd.Process.Pid, syscall.SIGKILL)
	// Wait owns ProcessState and the pipe-copy goroutines. Never return while
	// those can still mutate the command or leak into the next operation.
	if !reaped {
		<-done
	}
}

// ErrTimeout is returned when a child exceeded its process ceiling.
var ErrTimeout = errors.New("process timed out")

// Run starts cmd in its own process group and waits for it, honouring stop requests and the timeout.
// A zero timeout means no ceiling.
func Run(cmd *exec.Cmd, timeout time.Duration) error { return RunStarted(cmd, timeout, nil) }

// RunStarted is Run with a callback made once the process exists, for a caller that records its pid.
func RunStarted(cmd *exec.Cmd, timeout time.Duration, started func()) error {
	if err := Checkpoint(); err != nil {
		return err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	if err := cmd.Start(); err != nil {
		return err
	}
	if started != nil {
		started()
	}
	child := &Child{PID: cmd.Process.Pid, StartedAt: now()}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	mu.Lock()
	var receiptErr error
	if active != nil {
		active.Children = append(active.Children, child)
		receiptErr = save()
	}
	mu.Unlock()
	if receiptErr != nil {
		Terminate(cmd, done)
		return fmt.Errorf("record child process: %w", receiptErr)
	}
	began := time.Now()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var result error
loop:
	for {
		select {
		case result = <-done:
			break loop
		case <-ticker.C:
			if err := Checkpoint(); err != nil {
				Terminate(cmd, done)
				result = err
				break loop
			}
			if timeout > 0 && time.Since(began) >= timeout {
				Terminate(cmd, done)
				result = fmt.Errorf("%w after %s", ErrTimeout, timeout)
				break loop
			}
		}
	}
	mu.Lock()
	child.EndedAt = now()
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		child.ReturnCode = &code
	}
	if active != nil {
		receiptErr = save()
	}
	mu.Unlock()
	if receiptErr != nil {
		return errors.Join(result, fmt.Errorf("record child result: %w", receiptErr))
	}
	if result == nil {
		return Checkpoint()
	}
	return result
}
