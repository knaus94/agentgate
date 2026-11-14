package designkit

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentExtractionRetainsOtherBuilds(t *testing.T) {
	root := t.TempDir()
	previous := filepath.Join(root, "older-build", "running.mjs")
	if err := os.MkdirAll(filepath.Dir(previous), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previous, []byte("running"), 0o600); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	paths := make(chan string, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			dir, err := materialize(root)
			if err != nil {
				t.Error(err)
				return
			}
			paths <- dir
		}()
	}
	wait.Wait()
	close(paths)
	first := ""
	for dir := range paths {
		if first != "" && dir != first {
			t.Fatalf("different toolkit paths: %s and %s", first, dir)
		}
		first = dir
		if _, err := os.ReadFile(filepath.Join(dir, "bin", "check.mjs")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(previous); err != nil {
		t.Fatalf("removed toolkit used by an older command: %v", err)
	}
}

func TestIncompleteExtractionIsRepaired(t *testing.T) {
	root := t.TempDir()
	dir, err := materialize(root)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "lib", "fixtures.mjs")
	if err := os.WriteFile(file, []byte("interrupted extraction"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := materialize(root); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(file)
	expected, _ := files.ReadFile("lib/fixtures.mjs")
	if string(actual) != string(expected) {
		t.Fatal("a partial tool was used without repair")
	}
}
