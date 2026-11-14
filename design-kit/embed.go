// Package designkit carries the Node tools that pull a Figma design and check a page against it.
package designkit

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

//go:embed bin lib skills
var files embed.FS

// Dir returns where the tools lie on disk, writing them out when this build's copy is not there yet.
func Dir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return materialize(filepath.Join(base, "agentgate", "design-kit"))
}

func materialize(root string) (string, error) {
	sum := sha256.New()
	var names []string
	if err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			raw, err := files.ReadFile(path)
			if err != nil {
				return err
			}
			sum.Write([]byte(path))
			sum.Write(raw)
			names = append(names, path)
		}
		return nil
	}); err != nil {
		return "", err
	}
	key := hex.EncodeToString(sum.Sum(nil))[:24]
	dir := filepath.Join(root, key)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	lock, err := os.OpenFile(filepath.Join(root, ".extract.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return "", err
	}
	if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
		return "", fmt.Errorf("design toolkit path is not a directory: %s", dir)
	}
	complete := true
	for _, name := range names {
		expected, _ := files.ReadFile(name)
		actual, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || sha256.Sum256(actual) != sha256.Sum256(expected) {
			complete = false
			break
		}
	}
	if complete {
		return dir, nil
	}
	staging, err := os.MkdirTemp(root, ".extract-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	for _, name := range names {
		raw, err := files.ReadFile(name)
		if err != nil {
			return "", err
		}
		target := filepath.Join(staging, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, raw, 0o600); err != nil {
			return "", err
		}
	}
	backup := staging + ".previous"
	if _, err := os.Lstat(dir); err == nil {
		if err := os.Rename(dir, backup); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(staging, dir); err != nil {
		if restoreErr := os.Rename(backup, dir); restoreErr != nil && !os.IsNotExist(restoreErr) {
			return "", fmt.Errorf("publish toolkit: %v; restore: %w", err, restoreErr)
		}
		return "", err
	}
	_ = os.RemoveAll(backup)
	// Other versions can belong to commands that are still running. They are deliberately retained.
	return dir, nil
}

// Skill returns the text of a built-in skill, without its front matter.
func Skill(name string) string {
	raw, err := files.ReadFile("skills/" + name + "/SKILL.md")
	if err != nil {
		return ""
	}
	text := string(raw)
	if len(text) > 4 && text[:4] == "---\n" {
		for index := 4; index+4 <= len(text); index++ {
			if text[index:index+4] == "---\n" {
				return text[index+4:]
			}
		}
	}
	return text
}
