package apidserver

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Stage alert rules (ADR-20261104120000 amendment 2026-11-22). The app's
// alert routes take ?environment=<slug>; omitted or production is production.

// alertRuleEnvironment resolves ?environment to the ID of one of the app's
// project environments, or "" for production.
func (s *server) alertRuleEnvironment(w http.ResponseWriter, r *http.Request, app state.App) (string, bool) {
	slug := strings.TrimSpace(r.URL.Query().Get("environment"))
	if slug == "" || state.IsProductionScope(slug) {
		return "", true
	}
	if app.ProjectID == "" {
		s.notFound(w, "no such environment")
		return "", false
	}
	env, err := s.store.ProjectEnvironmentBySlug(r.Context(), app.AccountID, app.ProjectID, slug)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such environment")
		return "", false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the environment"))
		return "", false
	}
	return env.ID, true
}

// stageAlertRulesOpen refuses creating or enabling a stage's rule until
// operators open stage_alert_rules_enabled, which waits for every meterd and
// gateway to run the release that evaluates a stage rule over the stage's
// own signals (ADR-20261104120000 Release).
func (s *server) stageAlertRulesOpen(w http.ResponseWriter, environmentID string) bool {
	if environmentID == "" || s.runtimeBool(runtimeConfigStageAlertRules, false) {
		return true
	}
	api.WriteProblem(w, api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented, "Stage alert rules unavailable",
		"alert rules in a stage are not available yet; create the rule in production"))
	return false
}

// stageAlertRuleProblem refuses a stage rule that could not run: stages
// count requests, errors, latency, cold starts and failed invocations of
// their own, compared with a fixed threshold and answered by a webhook.
func stageAlertRuleProblem(rule state.AlertRule) *api.Problem {
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

// checkNewStageAlertRule refuses creating a stage rule while the runtime key
// is off or when the rule could not run.
func (s *server) checkNewStageAlertRule(w http.ResponseWriter, rule state.AlertRule) bool {
	if rule.EnvironmentID == "" {
		return true
	}
	if !s.stageAlertRulesOpen(w, rule.EnvironmentID) {
		return false
	}
	if prob := stageAlertRuleProblem(rule); prob != nil {
		api.WriteProblem(w, prob)
		return false
	}
	return true
}

// checkUpdatedStageAlertRule refuses an update that leaves a stage rule
// enabled while the runtime key is off or the rule could not run. A clone's
// paused copy of a production-only rule can be edited or deleted, not
// enabled.
func (s *server) checkUpdatedStageAlertRule(w http.ResponseWriter, merged state.AlertRule) bool {
	if merged.EnvironmentID == "" || !merged.Enabled {
		return true
	}
	return s.checkNewStageAlertRule(w, merged)
}
