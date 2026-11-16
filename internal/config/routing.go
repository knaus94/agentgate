package config

import (
	"fmt"
	"slices"
	"strings"
)

// Route resolves "alias:effort".
func (c *Config) Route(spec string) (Route, error) {
	alias, effort, ok := strings.Cut(spec, ":")
	model, known := c.Models[alias]
	if !ok || !known || !slices.Contains(Efforts, effort) {
		return Route{}, fmt.Errorf("config: route %q must be <model alias>:<effort>", spec)
	}
	return Route{Alias: alias, Provider: model.Provider, Model: model.Model, Effort: effort, Pool: model.Pool}, nil
}

// MustRoute resolves a spec already checked by validate.
func (c *Config) MustRoute(spec string) Route {
	route, err := c.Route(spec)
	if err != nil {
		panic(err)
	}
	return route
}

// Alternates lists the declared substitutes for a route, same effort, in order.
func (c *Config) Alternates(route Route) []Route {
	var out []Route
	for _, alias := range c.Fallbacks[route.Alias] {
		model := c.Models[alias]
		out = append(out, Route{Alias: alias, Provider: model.Provider, Model: model.Model, Effort: route.Effort,
			Pool: model.Pool, SubstitutedFrom: route.String()})
	}
	return out
}

// ReviewerFor is the first route among a reviewer's route and its fallbacks that did not write the code and is
// not kept from review; ok is false when there is none.
func (c *Config) ReviewerFor(spec string, authors []string) (Route, bool) {
	route, err := c.Route(spec)
	if err != nil {
		return Route{}, false
	}
	for _, candidate := range append([]Route{route}, c.Alternates(route)...) {
		if !slices.Contains(authors, candidate.Model) && !slices.Contains(c.ReviewersExcluded, candidate.Alias) {
			return candidate, true
		}
	}
	return Route{}, false
}

// BrowserGaps names the modes whose browser verification cannot run: the visual reviewer and every fallback of
// it are the model that writes code in that mode, or are kept from review.
func (c *Config) BrowserGaps() []string {
	route, err := c.Route(c.VisualReviewer)
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range ModeNames {
		var authors []string
		for _, spec := range []string{c.Modes[name].Backend, c.Modes[name].Frontend} {
			if writer, err := c.Route(spec); err == nil {
				authors = append(authors, writer.Model)
			}
		}
		if _, ok := c.ReviewerFor(c.VisualReviewer, authors); !ok {
			out = append(out, fmt.Sprintf("%s: browser verification cannot run — visual_reviewer %s and its fallbacks are the model that writes there (%s) "+
				"or are kept from review; give %s a fallback on another model, or choose another visual_reviewer", name, c.VisualReviewer, route.Model, route.Alias))
		}
	}
	return out
}

// Lower returns the effort one step down, never below medium: a recheck reads a delta, not the whole change.
func Lower(effort string) string {
	if index := slices.Index(Efforts, effort); index > 1 {
		return Efforts[index-1]
	}
	return effort
}

// Raise returns the effort the given number of steps up, never above the ceiling.
func Raise(effort string, steps int, ceiling string) string {
	index, limit := slices.Index(Efforts, effort), slices.Index(Efforts, ceiling)
	if index < 0 {
		return effort
	}
	if limit < index {
		limit = index // a missing or lower ceiling never reduces the configured effort
	}
	return Efforts[min(index+max(steps, 0), limit)]
}

// Blocks reports whether a severity blocks completion in the mode.
func (m Mode) Blocks(severity string) bool { return slices.Contains(m.Blocking, severity) }
