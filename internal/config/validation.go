package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var configName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func repositoryPath(path string) bool {
	clean := filepath.Clean(path)
	return strings.TrimSpace(path) != "" && !strings.ContainsRune(path, '\x00') && !filepath.IsAbs(path) &&
		clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator)) &&
		clean != ".git" && !strings.HasPrefix(clean, ".git"+string(filepath.Separator))
}

func (c *Config) validate() error {
	if c.Version != 1 {
		return fmt.Errorf("config: unsupported version %d (supported: 1)", c.Version)
	}
	if c.RetentionDays < 0 {
		return errors.New("config: retention_days must not be negative")
	}
	for name, rule := range c.ReviewRules {
		if !configName.MatchString(name) || len(rule.Instructions) == 0 {
			return fmt.Errorf("review_rules %q needs a lowercase name and instructions [local file paths]", name)
		}
		if rule.Mode == "" {
			rule.Mode = "blocking"
		}
		if rule.Mode != "blocking" && rule.Mode != "advisory" {
			return fmt.Errorf("review_rules %q: mode is blocking or advisory", name)
		}
		for _, path := range rule.Instructions {
			if strings.TrimSpace(path) == "" {
				return fmt.Errorf("review_rules %q: instruction paths must be nonempty", name)
			}
		}
		for _, pattern := range rule.Files {
			if !repositoryPath(pattern) {
				return fmt.Errorf("review_rules %q: files are repository-relative globs", name)
			}
		}
		c.ReviewRules[name] = rule
	}
	if c.MaxParallel < 1 || c.WorkerMemoryMiB < 1 {
		return errors.New("config: max_parallel and worker_memory_mib must be positive")
	}
	if !slices.Contains([]string{"auto", "always", "never"}, c.Staging.Isolate) || c.Staging.MaxFiles < 0 || c.Staging.MaxStreams < 0 {
		return errors.New("config: staging.isolate is auto, always or never, and its limits are not negative")
	}
	for _, lead := range c.Leads {
		if lead != "claude" && lead != "codex" {
			return fmt.Errorf("config: leads are claude and codex, not %q", lead)
		}
	}
	for alias, model := range c.Models {
		if !configName.MatchString(alias) || (model.Provider != "claude" && model.Provider != "codex") ||
			strings.TrimSpace(model.Model) == "" || strings.TrimSpace(model.Pool) == "" {
			return fmt.Errorf("config: model %q needs provider claude|codex, model and pool", alias)
		}
	}
	for alias, list := range c.Fallbacks {
		seen := map[string]bool{alias: true}
		for _, other := range list {
			if seen[other] {
				return fmt.Errorf("config: fallbacks for %q contain a duplicate or self-reference %q", alias, other)
			}
			seen[other] = true
		}
		for _, other := range append([]string{alias}, list...) {
			if _, ok := c.Models[other]; !ok {
				return fmt.Errorf("config: fallbacks reference unknown model %q", other)
			}
		}
	}
	for _, alias := range c.ReviewersExcluded {
		if _, ok := c.Models[alias]; !ok {
			return fmt.Errorf("config: reviewers_excluded references unknown model %q", alias)
		}
	}
	for name, server := range c.WorkerMCP {
		if !configName.MatchString(name) || strings.TrimSpace(server.Command) == "" {
			return fmt.Errorf("config: worker_mcp %q needs a lowercase name and a command", name)
		}
		for _, path := range server.Writable {
			if !repositoryPath(path) {
				return fmt.Errorf("config: worker_mcp %q: writable takes directories inside the repository: %q", name, path)
			}
		}
		for key := range server.Env {
			if key == "" || strings.ContainsAny(key, "=\x00") {
				return fmt.Errorf("config: worker_mcp %q has an invalid environment key %q", name, key)
			}
		}
		for _, tool := range server.Tools {
			if strings.TrimSpace(tool) == "" {
				return fmt.Errorf("config: worker_mcp %q tools must be nonempty names", name)
			}
		}
		if server.RefreshTimeoutS < 0 || (len(server.Refresh) > 0 && strings.TrimSpace(server.Refresh[0]) == "") {
			return fmt.Errorf("config: worker_mcp %q needs a nonnegative refresh timeout and a valid refresh command", name)
		}
		for _, role := range server.Roles {
			if !slices.Contains(Roles, role) {
				return fmt.Errorf("config: worker_mcp %q: unknown role %q (roles: %s)", name, role, strings.Join(Roles, ", "))
			}
		}
		if server.RefreshTimeoutS == 0 {
			server.RefreshTimeoutS = 300
			c.WorkerMCP[name] = server
		}
	}
	for _, path := range c.Worktree.Copy {
		if !repositoryPath(path) {
			return fmt.Errorf("config: worktree.copy takes paths inside the repository: %q", path)
		}
	}
	for _, argv := range c.Worktree.Setup {
		if len(argv) == 0 || argv[0] == "" {
			return errors.New("config: worktree.setup is a list of commands, each a non-empty argv array")
		}
	}
	if c.Worktree.SetupTimeoutS < 1 {
		return errors.New("config: worktree.setup_timeout_s must be positive")
	}
	names := map[string]bool{}
	for _, account := range c.Accounts {
		if strings.TrimSpace(account.Name) == "" || names[account.Name] || !filepath.IsAbs(ExpandHome(account.Home)) ||
			(account.Provider != "claude" && account.Provider != "codex") {
			return fmt.Errorf("config: account %q needs a unique name, provider claude|codex and a home directory", account.Name)
		}
		names[account.Name] = true
	}
	for name := range c.Modes {
		if !slices.Contains(ModeNames, name) {
			return fmt.Errorf("config: unknown mode %q", name)
		}
	}
	specs := []string{c.UIDesigner, c.VisualReviewer, c.Consultant}
	for _, name := range ModeNames {
		mode, ok := c.Modes[name]
		if !ok {
			return fmt.Errorf("config: mode %q is missing", name)
		}
		if mode.ArchitectureTimeoutS < 0 || mode.WriterTimeoutS < 0 || mode.ReviewerTimeoutS < 0 {
			return fmt.Errorf("config: mode %q timeouts must not be negative", name)
		}
		specs = append(specs, mode.Backend, mode.Frontend)
		if mode.Scout != "" {
			specs = append(specs, mode.Scout)
		}
		if mode.EscalateTo != "" && !slices.Contains(Efforts, mode.EscalateTo) {
			return fmt.Errorf("config: mode %q escalate_to must be an effort level", name)
		}
		specs = append(specs, mode.Architecture...)
		if mode.Critique && len(mode.Architecture) != 2 {
			return fmt.Errorf("config: mode %q critique needs exactly two architecture routes", name)
		}
		for _, lane := range mode.Review {
			specs = append(specs, lane.Route)
			for _, axis := range lane.Axes {
				if !slices.Contains(Axes, axis) {
					return fmt.Errorf("config: mode %q has unknown review axis %q", name, axis)
				}
			}
			if len(lane.Axes) == 0 {
				return fmt.Errorf("config: mode %q has a review lane without axes", name)
			}
		}
		for _, severity := range mode.Blocking {
			if !slices.Contains(Severities, severity) {
				return fmt.Errorf("config: mode %q has unknown severity %q", name, severity)
			}
		}
	}
	for _, spec := range specs {
		if _, err := c.Route(spec); err != nil {
			return err
		}
	}
	return nil
}
