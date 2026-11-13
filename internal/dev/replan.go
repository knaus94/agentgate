package dev

import (
	"errors"
	"slices"

	"github.com/knaus94/agentgate/internal/config"
)

// Replan replaces the contract at a stable boundary. It preserves source snapshots, findings and author
// sessions; it never writes code or captures a new before-image of an implementation already in progress.
func Replan(cfg *config.Config, gate *Gate, plan *Plan) (any, error) {
	if !slices.Contains([]string{"implementing", "awaiting-review", "reviewed"}, gate.Phase) {
		return nil, errors.New("replan needs an idle active task; explicitly resume a held task first")
	}
	if gate.Epic != nil || plan.staged() {
		return nil, errors.New("revise staged plans between stages through launch; accepted stages stay frozen")
	}
	for _, writer := range gate.latest() {
		if writer.Status == "RUNNING" {
			return nil, errors.New("cannot replan while a writer is running")
		}
	}
	if err := gate.auditScope(); err != nil {
		return nil, err
	}
	resolved, err := Resolve(cfg, plan)
	if err != nil {
		return nil, err
	}
	if resolved.Request.Action != gate.Request.Action || resolved.Request.Fix != gate.Request.Fix {
		return nil, errors.New("replan preserves the task's action and write authority")
	}
	if err := resolved.requireArchitecture(); err != nil {
		return nil, err
	}
	if _, err := loadReviewRules(cfg, plan.Files()); err != nil {
		return nil, err
	}
	if err := gate.extend(resolved); err != nil {
		return nil, err
	}
	gate.ReviewDigest, gate.ReviewError = "", ""
	if len(gate.Writers) > 0 || gate.Phase != "implementing" {
		gate.Phase = "awaiting-review"
	}
	return gate.Summary(cfg), gate.save()
}
