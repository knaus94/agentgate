package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

// Extension is one drop-in file: a self-contained piece of configuration that plugs something in — an MCP
// server for workers, what a worktree needs for it — without editing the main configuration. Removing the
// file removes the integration.
type Extension struct {
	File  string   `json:"file"`
	About string   `json:"about,omitempty"`
	Adds  []string `json:"adds,omitempty"`
	Error string   `json:"error,omitempty"`
	layer map[string]any
}

// ExtensionDirs are where extensions are picked up: the user's, for every repository, and the repository's own.
func ExtensionDirs() []string {
	return []string{filepath.Join(filepath.Dir(UserPath()), "extensions"), filepath.Join(RepoDir(), "extensions")}
}

// Extensions reads every *.json of the extension directories, in name order within each directory.
func Extensions() []Extension {
	var out []Extension
	for _, dir := range ExtensionDirs() {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		sort.Strings(paths)
		for _, path := range paths {
			extension := Extension{File: path}
			raw, err := os.ReadFile(path)
			if err == nil {
				err = json.Unmarshal(raw, &extension.layer)
				if err == nil && extension.layer == nil {
					err = errors.New("extension must be a JSON object")
				}
			}
			if err != nil {
				extension.Error = err.Error()
				out = append(out, extension)
				continue
			}
			extension.About, _ = extension.layer["about"].(string)
			delete(extension.layer, "about")
			delete(extension.layer, "accounts") // an extension plugs in a tool, never a subscription
			for key, value := range extension.layer {
				if child, ok := value.(map[string]any); ok {
					for name := range child {
						extension.Adds = append(extension.Adds, key+"."+name)
					}
				} else {
					extension.Adds = append(extension.Adds, key)
				}
			}
			sort.Strings(extension.Adds)
			out = append(out, extension)
		}
	}
	return out
}

// extend merges an extension: objects merge key by key like any layer, but a list adds its items to the
// list already there, so two extensions can each bring a file for the worktree without knowing of each other.
func extend(dst, src map[string]any) {
	for key, value := range src {
		switch typed := value.(type) {
		case map[string]any:
			if existing, ok := dst[key].(map[string]any); ok {
				extend(existing, typed)
				continue
			}
		case []any:
			if existing, ok := dst[key].([]any); ok {
				for _, item := range typed {
					encoded, _ := json.Marshal(item)
					if !slices.ContainsFunc(existing, func(other any) bool { raw, _ := json.Marshal(other); return string(raw) == string(encoded) }) {
						existing = append(existing, item)
					}
				}
				dst[key] = existing
				continue
			}
		}
		dst[key] = value
	}
}
