// Package designkit carries the Node tools that pull a Figma design and check a page against it.
package designkit

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed bin lib skills
var files embed.FS

// Dir returns where the tools lie on disk, writing them out when this build's copy is not there yet.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	var names []string
	_ = fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			raw, _ := files.ReadFile(path)
			sum.Write([]byte(path))
			sum.Write(raw)
			names = append(names, path)
		}
		return nil
	})
	dir := filepath.Join(home, ".local", "share", "agentgate", "design-kit", hex.EncodeToString(sum.Sum(nil))[:12])
	if _, err := os.Stat(filepath.Join(dir, ".complete")); err == nil {
		return dir, nil
	}
	for _, name := range names {
		raw, _ := files.ReadFile(name)
		target := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, raw, 0o644); err != nil {
			return "", err
		}
	}
	// Each build writes its own copy; the copies of earlier builds are of no use to anyone.
	if siblings, err := os.ReadDir(filepath.Dir(dir)); err == nil {
		for _, sibling := range siblings {
			if sibling.Name() != filepath.Base(dir) {
				_ = os.RemoveAll(filepath.Join(filepath.Dir(dir), sibling.Name()))
			}
		}
	}
	return dir, os.WriteFile(filepath.Join(dir, ".complete"), nil, 0o644)
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
