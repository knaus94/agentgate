package config

import (
	"os"
	"path/filepath"
	"strings"
)

// UserPath is the optional override file merged over the built-in table.
func UserPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, "agentgate", "config.json")
}

// ProjectFile is the optional per-repository override, read from the working directory.
const ProjectFile = ".agentgate.json"

// PersonalFile is the user's own override for one repository. It lives next to the saved plans, so it is
// never committed with the team's file and travels with the plans when that directory is synced.
const PersonalFile = ".agentgate/config.json"

// Lead says which CLI the calling session is. Inside a Codex tool call it is "codex", whatever else the
// environment inherited; otherwise it is "claude".
func Lead() string {
	if os.Getenv("CODEX_THREAD_ID") != "" && (os.Getenv("CODEX_TOOL_CALL_ID") != "" || os.Getenv("CLAUDE_CODE_SESSION_ID") == "") {
		return "codex"
	}
	return "claude"
}

// The tool was called claude-dev before it could be led from Codex. A repository set up under that name keeps
// working: the old names are read where the new ones are absent.
const legacyName = "claude-dev"

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

// RepoDir is the repository's directory of plans, personal settings and extensions.
func RepoDir() string {
	if !exists(".agentgate") && exists("."+legacyName) {
		return "." + legacyName
	}
	return ".agentgate"
}

func projectFile() string {
	if !exists(ProjectFile) && exists("."+legacyName+".json") {
		return "." + legacyName + ".json"
	}
	return ProjectFile
}

func personalFile() string { return filepath.Join(RepoDir(), "config.json") }

// ProfileDir holds the user's own profiles, next to the built-in ones.
func ProfileDir() string { return filepath.Join(filepath.Dir(UserPath()), "profiles") }

// ExpandHome resolves a leading "~/".
func ExpandHome(path string) string {
	if path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, rest)
	}
	return path
}
