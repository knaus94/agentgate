package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/knaus94/agentgate/internal/assets"
	"github.com/knaus94/agentgate/internal/ops"
)

func readUser() (map[string]any, error) {
	layer := map[string]any{}
	raw, err := os.ReadFile(UserPath())
	if errors.Is(err, os.ErrNotExist) {
		return layer, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &layer); err != nil {
		return nil, fmt.Errorf("%s: %w", UserPath(), err)
	}
	if layer == nil {
		return nil, fmt.Errorf("%s: configuration must be a JSON object", UserPath())
	}
	return layer, nil
}

// editUser locks the complete read/modify/validate/replace transaction. The
// candidate is checked independently of repository overrides as well: a local
// setting must not hide an invalid user setting until another project is opened.
func editUser(edit func(map[string]any) error) error {
	if err := os.MkdirAll(filepath.Dir(UserPath()), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(UserPath()+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	layer, err := readUser()
	if err != nil {
		return err
	}
	if err := edit(layer); err != nil {
		return err
	}
	if _, err := loadWithUser(layer, false); err != nil {
		return fmt.Errorf("rejected, nothing changed: %w", err)
	}
	if _, err := loadWithUser(layer, true); err != nil {
		return fmt.Errorf("rejected, nothing changed: %w", err)
	}
	return ops.WriteJSON(UserPath(), layer)
}

func keyParts(key string) ([]string, error) {
	parts := strings.Split(key, ".")
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || strings.TrimSpace(part) != part {
			return nil, fmt.Errorf("config key %q must contain nonempty dotted names", key)
		}
	}
	return parts, nil
}

// Set writes one dotted key. The value is JSON when valid, otherwise a string.
func Set(key, value string) error {
	parts, err := keyParts(key)
	if err != nil {
		return err
	}
	var parsed any
	if json.Unmarshal([]byte(value), &parsed) != nil {
		parsed = value
	}
	return editUser(func(layer map[string]any) error {
		node := layer
		for _, part := range parts[:len(parts)-1] {
			child, ok := node[part].(map[string]any)
			if !ok {
				child = map[string]any{}
				node[part] = child
			}
			node = child
		}
		node[parts[len(parts)-1]] = parsed
		return nil
	})
}

// Unset removes one user override, restoring the value from the lower layers.
func Unset(key string) error {
	parts, err := keyParts(key)
	if err != nil {
		return err
	}
	return editUser(func(layer map[string]any) error {
		node := layer
		for _, part := range parts[:len(parts)-1] {
			child, ok := node[part].(map[string]any)
			if !ok {
				return nil
			}
			node = child
		}
		delete(node, parts[len(parts)-1])
		return nil
	})
}

// SetValue writes a structured top-level value without a lossy text roundtrip.
func SetValue(key string, value any) error {
	parts, err := keyParts(key)
	if err != nil || len(parts) != 1 {
		return fmt.Errorf("config SetValue requires one top-level key, got %q", key)
	}
	return editUser(func(layer map[string]any) error {
		layer[key] = value
		return nil
	})
}

// Init creates a starter once. The existence check shares the write lock, so a
// simultaneous initialization or configuration edit cannot be overwritten.
func Init() (string, error) {
	err := editUser(func(layer map[string]any) error {
		if _, err := os.Lstat(UserPath()); err == nil {
			return errors.New("the file already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		var builtin map[string]any
		if err := json.Unmarshal(assets.Config, &builtin); err != nil {
			return err
		}
		layer["models"], layer["fallbacks"] = builtin["models"], builtin["fallbacks"]
		return nil
	})
	return UserPath(), err
}
