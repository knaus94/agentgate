package accounts

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ParseClaudeUsage turns the usage payload into windows. Unknown fields are ignored: the payload grows often.
func ParseClaudeUsage(body []byte) []Window {
	type bucket struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
	}
	var usage struct {
		FiveHour *bucket `json:"five_hour"`
		Week     *bucket `json:"seven_day"`
		Opus     *bucket `json:"seven_day_opus"`
		Sonnet   *bucket `json:"seven_day_sonnet"`
		Limits   []struct {
			Kind     string   `json:"kind"`
			Percent  *float64 `json:"percent"`
			ResetsAt string   `json:"resets_at"`
			Scope    *struct {
				Model *struct {
					Name string `json:"display_name"`
				} `json:"model"`
			} `json:"scope"`
		} `json:"limits"`
	}
	if json.Unmarshal(body, &usage) != nil {
		return nil
	}
	var out []Window
	add := func(label, scope string, value *bucket) {
		if value != nil && value.Utilization != nil {
			out = append(out, Window{Label: label, Scope: scope, Used: *value.Utilization, ResetsAt: parseTime(value.ResetsAt)})
		}
	}
	add("5 hours", "", usage.FiveHour)
	add("week", "", usage.Week)
	add("week, opus", "opus", usage.Opus)
	add("week, sonnet", "sonnet", usage.Sonnet)
	seen := map[string]bool{"opus": usage.Opus != nil && usage.Opus.Utilization != nil, "sonnet": usage.Sonnet != nil && usage.Sonnet.Utilization != nil}
	for _, limit := range usage.Limits {
		if limit.Percent == nil {
			continue
		}
		if limit.Kind == "weekly_all" && (usage.Week == nil || usage.Week.Utilization == nil) {
			out = append(out, Window{Label: "week", Used: *limit.Percent, ResetsAt: parseTime(limit.ResetsAt)})
		}
		if limit.Kind != "weekly_scoped" || limit.Scope == nil || limit.Scope.Model == nil || limit.Scope.Model.Name == "" {
			continue
		}
		words := strings.Fields(limit.Scope.Model.Name)
		if len(words) == 0 {
			continue
		}
		scope := strings.ToLower(words[0])
		if !seen[scope] {
			seen[scope] = true
			out = append(out, Window{Label: "week, " + scope, Scope: scope, Used: *limit.Percent, ResetsAt: parseTime(limit.ResetsAt)})
		}
	}
	return out
}

// ParseCodexLimits preserves model-specific buckets instead of making every
// exhausted bucket block every model. A reached primary window must not mark a
// healthy weekly secondary window as exhausted.
func ParseCodexLimits(body []byte) ([]Window, string) {
	type window struct {
		Used     *float64 `json:"usedPercent"`
		Minutes  int      `json:"windowDurationMins"`
		ResetsAt int64    `json:"resetsAt"`
	}
	type bucket struct {
		ID        string  `json:"limitId"`
		Primary   *window `json:"primary"`
		Secondary *window `json:"secondary"`
		Plan      string  `json:"planType"`
		Reached   *string `json:"rateLimitReachedType"`
	}
	var reply struct {
		Allowed *bool             `json:"ordinaryUsageAllowed"`
		Limits  *bucket           `json:"rateLimits"`
		ByID    map[string]bucket `json:"rateLimitsByLimitId"`
	}
	if json.Unmarshal(body, &reply) != nil {
		return nil, ""
	}
	var buckets []bucket
	ids := make([]string, 0, len(reply.ByID))
	for id := range reply.ByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		item := reply.ByID[id]
		if item.ID == "" {
			item.ID = id
		}
		buckets = append(buckets, item)
	}
	if len(buckets) == 0 && reply.Limits != nil {
		buckets = append(buckets, *reply.Limits)
	}
	var out []Window
	plan := ""
	for _, item := range buckets {
		if item.Plan != "" {
			plan = item.Plan
		}
		scope := strings.ToLower(item.ID)
		if scope == "codex" {
			scope = ""
		}
		reached := ""
		if item.Reached != nil {
			reached = *item.Reached
		}
		for index, entry := range []*window{item.Primary, item.Secondary} {
			if entry == nil || entry.Used == nil {
				continue
			}
			label := fmt.Sprintf("%d min", entry.Minutes)
			switch {
			case entry.Minutes == 10080:
				label = "week"
			case entry.Minutes >= 1440:
				label = fmt.Sprintf("%d days", entry.Minutes/1440)
			case entry.Minutes >= 60:
				label = fmt.Sprintf("%d hours", entry.Minutes/60)
			}
			if scope != "" {
				label += ", " + item.ID
			}
			used := min(100, max(0, *entry.Used))
			if (reached == "primary" && index == 0) || (reached == "secondary" && index == 1) {
				used = 100
			}
			out = append(out, Window{Label: label, Scope: scope, LimitID: item.ID, Used: used, ResetsAt: entry.ResetsAt})
		}
		if reached != "" && reached != "primary" && reached != "secondary" {
			out = append(out, Window{Label: "usage blocked: " + reached, Scope: scope, LimitID: item.ID, Used: 100})
		}
	}
	if reply.Allowed != nil && !*reply.Allowed {
		out = append(out, Window{Label: "usage blocked", Used: 100})
	}
	return out, plan
}
