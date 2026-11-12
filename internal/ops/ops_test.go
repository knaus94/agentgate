package ops

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOperationReceiptAndTimings(t *testing.T) {
	dir := t.TempDir()
	if err := Begin(dir, "launch", "gate-1"); err != nil {
		t.Fatal(err)
	}
	if err := Begin(dir, "review", "gate-1"); err == nil {
		t.Fatal("a second operation in one process must be refused")
	}
	if record := Status(dir); record == nil || !record.Live || record.Action != "launch" {
		t.Fatalf("the running operation must be live: %+v", record)
	}
	BindGate("gate-2")
	if err := Run(exec.Command("true"), 0); err != nil {
		t.Fatal(err)
	}
	End(nil)
	record := Status(dir)
	if record.Live || record.State != "complete" || record.GateID != "gate-2" || len(record.Children) != 1 || *record.Children[0].ReturnCode != 0 {
		t.Fatalf("the receipt must record the outcome and the child: %+v", record)
	}
	if _, ok := Timings(dir, "gate-2")["launch"]; !ok || len(Timings(dir, "other")) != 0 {
		t.Fatal("timings are grouped by action for the gate")
	}
	if RequestStop(dir, "late") {
		t.Fatal("a finished operation cannot be stopped")
	}
}

func TestTimeoutKillsTheWholeProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild")
	if err := Begin(dir, "check", ""); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "sleep 60 & echo $! > "+pidFile+"; wait")
	started := time.Now()
	err := Run(cmd, 600*time.Millisecond)
	End(err)
	if !errors.Is(err, ErrTimeout) || time.Since(started) > 8*time.Second {
		t.Fatalf("want a timeout, got %v after %s", err, time.Since(started))
	}
	raw, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			break
		}
	}
	if pid == 0 || syscall.Kill(pid, 0) == nil {
		t.Fatalf("the grandchild %d must not survive its group", pid)
	}
	if Status(dir).State != "failed" {
		t.Fatal("a timeout is a failed operation, not a cancellation")
	}
}

func TestStopRequestCancelsTheRunningChild(t *testing.T) {
	dir := t.TempDir()
	if err := Begin(dir, "launch", ""); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		if !RequestStop(dir, "the user stopped it") {
			t.Error("the live operation must accept a stop request")
		}
	}()
	err := Run(exec.Command("sleep", "60"), 0)
	End(err)
	if !IsCancelled(err) || err.Error() != "the user stopped it" || Status(dir).State != "cancelled" {
		t.Fatalf("want a cancellation with the reason, got %v / %s", err, Status(dir).State)
	}
	// A stale request of a previous operation must not cancel the next one.
	if err := Begin(dir, "review", ""); err != nil {
		t.Fatal(err)
	}
	if err := Run(exec.Command("true"), 0); err != nil {
		t.Fatalf("an old stop request leaked into a new operation: %v", err)
	}
	End(nil)
}
