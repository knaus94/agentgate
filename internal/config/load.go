package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/knaus94/agentgate/internal/assets"
)

// Load returns the built-in table with the user's and then the repository's overrides merged in.
func Load() (*Config, error) {
	user, err := readUser()
	if err != nil {
		return nil, err
	}
	return loadWithUser(user, true)
}

// loadWithUser validates a prospective user layer entirely in memory. Config
// writes never expose a partially written or invalid file to parallel sessions.
func loadWithUser(user map[string]any, project bool) (*Config, error) {
	var merged map[string]any
	if err := json.Unmarshal(assets.Config, &merged); err != nil {
		return nil, fmt.Errorf("built-in config: %w", err)
	}
	sources := map[string][]string{}
	record := func(path string, layer map[string]any) {
		if rules, ok := layer["review_rules"].(map[string]any); ok {
			for name := range rules {
				sources[name] = append(sources[name], path)
			}
		}
	}
	record("builtin", merged)
	// The profile is chosen in the user's file and laid under it: a profile replaces the default table, and
	// the user's own settings still win over the profile.
	if name, _ := user["profile"].(string); name != "" {
		layer, err := profileLayer(name)
		if err != nil {
			return nil, err
		}
		merge(merged, layer)
		record("profile:"+name, layer)
	}
	extensions := Extensions()
	dirs := ExtensionDirs()
	for _, level := range []struct{ file, dir string }{{UserPath(), dirs[0]}, {projectFile(), dirs[1]}, {personalFile(), ""}} {
		if !project && level.file != UserPath() {
			continue
		}
		for _, extension := range extensions {
			if filepath.Dir(extension.File) != level.dir {
				continue
			}
			if extension.Error != "" {
				return nil, fmt.Errorf("%s: %s", extension.File, extension.Error)
			}
			extend(merged, extension.layer)
			record(extension.File, extension.layer)
		}
		path := level.file
		if path == UserPath() {
			merge(merged, user)
			record(path, user)
			continue
		}
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var layer map[string]any
		if err := json.Unmarshal(raw, &layer); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if layer == nil {
			return nil, fmt.Errorf("%s: configuration must be a JSON object", path)
		}
		if path != UserPath() {
			delete(layer, "accounts") // a repository does not choose whose subscription pays for its work
		}
		merge(merged, layer)
		record(path, layer)
	}
	raw, _ := json.Marshal(merged)
	cfg := &Config{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	cfg.ReviewRuleSources = sources
	return cfg, cfg.validate()
}

// merge overlays src on dst: objects merge key by key, everything else is replaced.
func merge(dst, src map[string]any) {
	for key, value := range src {
		if value == nil {
			delete(dst, key) // null removes what a lower layer defined, e.g. a model a profile does not use
			continue
		}
		if child, ok := value.(map[string]any); ok {
			if existing, ok := dst[key].(map[string]any); ok {
				merge(existing, child)
				continue
			}
		}
		dst[key] = value
	}
}
