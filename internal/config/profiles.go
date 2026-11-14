package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/knaus94/agentgate/internal/assets"
)

// profileLayer returns the named profile: the user's file when there is one, otherwise the built-in.
func profileLayer(name string) (map[string]any, error) {
	if !configName.MatchString(name) {
		return nil, fmt.Errorf("profile %q must be a name without paths or spaces", name)
	}
	raw, err := os.ReadFile(filepath.Join(ProfileDir(), name+".json"))
	if errors.Is(err, os.ErrNotExist) {
		builtin, ok := assets.Profile(name)
		if !ok {
			return nil, fmt.Errorf("unknown profile %q; built-in: %s; your own go to %s", name, strings.Join(assets.Profiles(), ", "), ProfileDir())
		}
		raw = builtin
	} else if err != nil {
		return nil, fmt.Errorf("profile %s: %w", name, err)
	}
	var layer map[string]any
	if err := json.Unmarshal(raw, &layer); err != nil {
		return nil, fmt.Errorf("profile %s: %w", name, err)
	}
	if layer == nil {
		return nil, fmt.Errorf("profile %s: expected a JSON object", name)
	}
	delete(layer, "about")
	delete(layer, "accounts")
	return layer, nil
}

// Profiles lists the profiles that can be selected, with what each says about itself.
func Profiles() map[string]string {
	out := map[string]string{}
	read := func(name string, raw []byte) {
		var layer struct {
			About string `json:"about"`
		}
		_ = json.Unmarshal(raw, &layer)
		out[name] = layer.About
	}
	for _, name := range assets.Profiles() {
		raw, _ := assets.Profile(name)
		read(name, raw)
	}
	paths, _ := filepath.Glob(filepath.Join(ProfileDir(), "*.json"))
	for _, path := range paths {
		if raw, err := os.ReadFile(path); err == nil {
			read(strings.TrimSuffix(filepath.Base(path), ".json"), raw)
		}
	}
	return out
}
