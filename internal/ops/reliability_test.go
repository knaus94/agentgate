package ops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicWritePreservesPreviousFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := WriteJSON(path, map[string]any{"value": "original"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := WriteJSON(path, make(chan int)); err == nil {
		t.Fatal("unsupported data was accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("failed serialization changed the previous file")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatal("receipt permissions expose private task data")
	}
	blocked := filepath.Join(dir, "target-directory")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(blocked, "cannot replace directory"); err == nil {
		t.Fatal("rename unexpectedly replaced a directory")
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("failed write left temporary data: %s", entry.Name())
		}
	}
}

func TestBeginFailsWhenItsReceiptCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "operation.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Begin(dir, "launch", "gate"); err == nil {
		End(nil)
		t.Fatal("operation started without a writable receipt")
	}
	if err := Begin(t.TempDir(), "launch", "gate"); err != nil {
		t.Fatalf("failed begin poisoned the next operation: %v", err)
	}
	if err := End(nil); err != nil {
		t.Fatal(err)
	}
}

func TestAlreadyCancelledOperationStartsNoChild(t *testing.T) {
	dir := t.TempDir()
	if err := Begin(dir, "launch", "gate"); err != nil {
		t.Fatal(err)
	}
	defer End(nil)
	if !RequestStop(dir, "stopped before dispatch") {
		t.Fatal("stop request failed")
	}
	marker := filepath.Join(dir, "must-not-exist")
	cmd := exec.Command("touch", marker)
	if err := Run(cmd, 0); !IsCancelled(err) || cmd.Process != nil {
		t.Fatalf("cancelled operation spawned work: pid=%v err=%v", cmd.Process, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("cancelled work changed the filesystem")
	}
}
