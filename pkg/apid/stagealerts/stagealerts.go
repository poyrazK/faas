// Package stagealerts holds apid's rules for a stage's alert rules
// (ADR-20261104120000 amendment 2026-11-22): which environment an alert
// route names, and which stage rules apid accepts. The app's alert routes
// take ?environment=<slug>; omitted or production is production.
package stagealerts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// EnvironmentReader reads one of a project's environments by slug.
type EnvironmentReader interface {
	ProjectEnvironmentBySlug(ctx context.Context, accountID, projectID, slug string) (state.ProjectEnvironment, error)
}

// Environment resolves an ?environment slug to the ID of one of the app's
// project environments, or "" for production.
func Environment(ctx context.Context, store EnvironmentReader, app state.App, slug string) (string, *api.Problem) {
	slug = strings.TrimSpace(slug)
	if slug == "" || state.IsProductionScope(slug) {
		return "", nil
	}
	if app.ProjectID == "" {
		return "", noSuchEnvironment()
	}
	env, err := store.ProjectEnvironmentBySlug(ctx, app.AccountID, app.ProjectID, slug)
	if errors.Is(err, state.ErrNotFound) {
		return "", noSuchEnvironment()
	}
	if err != nil {
		return "", api.ErrCapacity("could not read the environment")
	}
	return env.ID, nil
}

func noSuchEnvironment() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such environment")
}

// CheckNew refuses creating a stage rule while operators keep
// stage_alert_rules_enabled (open) off — it waits for every meterd and
// gateway to run the release that evaluates a stage rule over the stage's
// own signals — or when the rule could not run. A production rule passes.
func CheckNew(rule state.AlertRule, open bool) *api.Problem {
	if rule.EnvironmentID == "" {
		return nil
	}
	if !open {
		return api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented, "Stage alert rules unavailable",
			"alert rules in a stage are not available yet; create the rule in production")
	}
	return RuleProblem(rule)
}

// CheckUpdated refuses an update that leaves a stage rule enabled while the
// runtime key is off or the rule could not run. A clone's paused copy of a
// production-only rule can be edited or deleted, not enabled.
func CheckUpdated(merged state.AlertRule, open bool) *api.Problem {
	if !merged.Enabled {
		return nil
	}
	return CheckNew(merged, open)
}

// RuleProblem refuses a stage rule that could not run: stages count
// requests, errors, latency, cold starts and failed invocations of their
// own, compared with a fixed threshold and answered by a webhook.
func RuleProblem(rule state.AlertRule) *api.Problem {
	if rule.EnvironmentID == "" || state.StageAlertRuleRunnable(rule) {
		return nil
	}
	metrics := make([]string, 0, 8)
	for _, m := range state.StageAlertRuleMetrics() {
		metrics = append(metrics, string(m))
	}
	return api.ErrAlertRuleInvalid(fmt.Sprintf("a stage's alert rule can watch %s with gt, gte, lt or lte and a webhook action; this one stays in production",
		strings.Join(metrics, ", ")))
}
