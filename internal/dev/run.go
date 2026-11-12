package dev

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/knaus94/agentgate/internal/config"
	"github.com/knaus94/agentgate/internal/ops"
	"github.com/knaus94/agentgate/internal/provider"
)

// Execute runs one lead operation under the session lock and leaves a receipt.
// A stop request ends it with the gate on hold, never half-applied.
func Execute(action string, run func() (any, error)) (any, error) {
	if err := requireLead(nil); err != nil {
		return nil, err
	}
	unlock, err := lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	gateID := ""
	if gate := Current(); gate != nil {
		gateID = gate.ID
	}
	if err := ops.Begin(home(), action, gateID); err != nil {
		return nil, err
	}
	// An isolated task is driven from inside its worktree; the user's checkout is only where it was started.
	defer leave()
	if err := Current().enter(); err != nil && action != "land" {
		ops.End(err)
		return nil, err
	}
	value, err := run()
	ops.End(err)
	if ops.IsCancelled(err) {
		if gate := Current(); gate != nil && gate.active() && gate.Phase != "held" {
			gate.holdNow(err.Error())
		}
	}
	return value, err
}

// parallel runs n jobs with at most limit at once and returns their errors by index.
func parallel(n, limit int, job func(index int) error) []error {
	errs := make([]error, n)
	slots := make(chan struct{}, max(1, limit))
	var wait sync.WaitGroup
	for index := range n {
		wait.Add(1)
		go func() {
			defer wait.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			errs[index] = job(index)
		}()
	}
	wait.Wait()
	return errs
}

// firstError prefers a cancellation, so a stop is never reported as an ordinary failure.
func firstError(errs []error) error {
	var first error
	for _, err := range errs {
		if ops.IsCancelled(err) {
			return err
		}
		if first == nil {
			first = err
		}
	}
	return first
}

// substitutions collects the alternates used by concurrently running lanes.
type substitutions struct {
	mu   sync.Mutex
	list []provider.Substitution
}

// invoke runs one lane on its route or a declared alternate and records which one answered.
func (s *substitutions) invoke(cfg *config.Config, call provider.Call, skip func(config.Route) bool) (provider.Result, config.Route, error) {
	result, used, err := provider.RunRouted(cfg, call, skip)
	if err == nil && used.SubstitutedFrom != "" {
		s.mu.Lock()
		s.list = append(s.list, provider.Substitution{Wanted: used.SubstitutedFrom, Used: used.String(),
			Reason: "usage limit of the routed pool was exhausted"})
		s.mu.Unlock()
	}
	return result, used, err
}

func seconds(value int) time.Duration { return time.Duration(value) * time.Second }

func writePacket(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return ops.WriteJSON(path, value)
}

func decode(answer string, into any) error {
	if err := json.Unmarshal([]byte(answer), into); err != nil {
		return fmt.Errorf("the worker's answer is not the requested JSON: %w", err)
	}
	return nil
}

// JSON schema helpers. Providers require every property listed and no extras.
func object(properties map[string]any) map[string]any {
	required := make([]string, 0, len(properties))
	for name := range properties {
		required = append(required, name)
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func list(item any) map[string]any { return map[string]any{"type": "array", "items": item} }

func enum(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }

var text = map[string]any{"type": "string"}

var boolean = map[string]any{"type": "boolean"}
