// Package ops keeps a receipt for every long operation and cancels it cooperatively.
//
// The CLI serializes operations with a session lock. Stop writes a request bound to the
// running operation without taking that lock; only the owning process kills its children.
package ops

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Cancelled is returned when the user stopped the running operation.
type Cancelled struct{ Reason string }

func (c *Cancelled) Error() string { return c.Reason }

// IsCancelled reports whether err came from a stop request.
func IsCancelled(err error) bool {
	var cancelled *Cancelled
	return errors.As(err, &cancelled)
}

type Child struct {
	PID        int     `json:"pid"`
	StartedAt  float64 `json:"started_at"`
	EndedAt    float64 `json:"ended_at,omitempty"`
	ReturnCode *int    `json:"returncode,omitempty"`
}

type Record struct {
	ID        string   `json:"id"`
	Action    string   `json:"action"`
	GateID    string   `json:"gate_id,omitempty"`
	PID       int      `json:"pid"`
	Identity  string   `json:"process_identity"`
	State     string   `json:"state"`
	StartedAt float64  `json:"started_at"`
	EndedAt   float64  `json:"ended_at,omitempty"`
	DurationS float64  `json:"duration_s,omitempty"`
	Error     string   `json:"error,omitempty"`
	Children  []*Child `json:"children"`
	Live      bool     `json:"live"`
}

var (
	mu     sync.Mutex
	active *Record
	root   string
)

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// WriteJSON replaces a file atomically with private permissions.
func WriteJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// identity is the process start time, so a recycled PID is never mistaken for the owner.
func identity(pid int) string {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	_, rest, ok := strings.Cut(string(raw), ") ")
	fields := strings.Fields(rest)
	if !ok || len(fields) < 20 {
		return ""
	}
	return fields[19]
}

// Status returns the last operation of a session directory, or nil.
func Status(dir string) *Record {
	raw, err := os.ReadFile(filepath.Join(dir, "operation.json"))
	if err != nil {
		return nil
	}
	record := &Record{}
	if json.Unmarshal(raw, record) != nil {
		return nil
	}
	record.Live = record.State == "running" && record.Identity != "" && identity(record.PID) == record.Identity
	return record
}

// RequestStop asks the live operation of a session directory to stop.
func RequestStop(dir, reason string) bool {
	record := Status(dir)
	if record == nil || !record.Live {
		return false
	}
	request := map[string]any{"operation_id": record.ID, "reason": reason, "requested_at": now()}
	return WriteJSON(filepath.Join(dir, "cancel.json"), request) == nil
}

// Checkpoint fails with Cancelled once a stop was requested for the running operation.
func Checkpoint() error {
	mu.Lock()
	defer mu.Unlock()
	if active == nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(root, "cancel.json"))
	if err != nil {
		return nil
	}
	var request struct {
		OperationID string `json:"operation_id"`
		Reason      string `json:"reason"`
	}
	if json.Unmarshal(raw, &request) == nil && request.OperationID == active.ID {
		return &Cancelled{Reason: request.Reason}
	}
	return nil
}

func save() {
	_ = WriteJSON(filepath.Join(root, "operation.json"), active)
}

// Begin records the start of an operation owned by this process.
func Begin(dir, action, gateID string) error {
	mu.Lock()
	defer mu.Unlock()
	if active != nil {
		return errors.New("nested operations are not supported")
	}
	if err := os.MkdirAll(filepath.Join(dir, "operations"), 0o700); err != nil {
		return err
	}
	root = dir
	active = &Record{ID: fmt.Sprintf("%x", time.Now().UnixNano()), Action: action, GateID: gateID, PID: os.Getpid(),
		Identity: identity(os.Getpid()), State: "running", StartedAt: now(), Children: []*Child{}}
	save()
	return nil
}

// End closes the running operation with its outcome.
func End(err error) {
	mu.Lock()
	defer mu.Unlock()
	if active == nil {
		return
	}
	active.State = "complete"
	if err != nil {
		active.State, active.Error = "failed", err.Error()
		if IsCancelled(err) {
			active.State = "cancelled"
		}
	}
	active.EndedAt = now()
	active.DurationS = float64(int((active.EndedAt-active.StartedAt)*1000)) / 1000
	_ = WriteJSON(filepath.Join(root, "operations", active.ID+".json"), active)
	save()
	active = nil
}

// BindGate attaches the running operation to a gate created inside it.
func BindGate(gateID string) {
	mu.Lock()
	defer mu.Unlock()
	if active != nil {
		active.GateID = gateID
		save()
	}
}

func killGroup(pid int, signal syscall.Signal) { _ = syscall.Kill(-pid, signal) }

// Terminate stops a whole process group: grandchildren outlive their leader otherwise.
func Terminate(cmd *exec.Cmd, done <-chan error) {
	killGroup(cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	killGroup(cmd.Process.Pid, syscall.SIGKILL)
}

// ErrTimeout is returned when a child exceeded its process ceiling.
var ErrTimeout = errors.New("process timed out")

// Run starts cmd in its own process group and waits for it, honouring stop requests and the timeout.
// A zero timeout means no ceiling.
func Run(cmd *exec.Cmd, timeout time.Duration) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	child := &Child{PID: cmd.Process.Pid, StartedAt: now()}
	mu.Lock()
	if active != nil {
		active.Children = append(active.Children, child)
		save()
	}
	mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	started := time.Now()
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
			if timeout > 0 && time.Since(started) >= timeout {
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
	mu.Unlock()
	if result == nil {
		return Checkpoint()
	}
	return result
}

// Timings sums the duration of finished operations of a gate by action.
func Timings(dir, gateID string) map[string]float64 {
	totals := map[string]float64{}
	paths, _ := filepath.Glob(filepath.Join(dir, "operations", "*.json"))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		record := Record{}
		if err != nil || json.Unmarshal(raw, &record) != nil || record.GateID != gateID {
			continue
		}
		totals[record.Action] = float64(int((totals[record.Action]+record.DurationS)*1000)) / 1000
	}
	return totals
}
