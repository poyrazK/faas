package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/stagealerts"
	"github.com/onebox-faas/faas/pkg/state"
)

// Stage alert rules (ADR-20261104120000 amendment 2026-11-22): the rules
// live in pkg/apid/stagealerts; these wire them to the alert handlers.

// alertRuleEnvironment resolves ?environment to the ID of one of the app's
// project environments, or "" for production.
func (s *server) alertRuleEnvironment(w http.ResponseWriter, r *http.Request, app state.App) (string, bool) {
	id, prob := stagealerts.Environment(r.Context(), s.store, app, r.URL.Query().Get("environment"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return "", false
	}
	return id, true
}

// checkNewStageAlertRule refuses creating a stage rule while the runtime key
// is off or when the rule could not run.
func (s *server) checkNewStageAlertRule(w http.ResponseWriter, rule state.AlertRule) bool {
	return writeStageAlertProblem(w, stagealerts.CheckNew(rule, s.runtimeBool(runtimeConfigStageAlertRules, false)))
}

// checkUpdatedStageAlertRule refuses an update that leaves a stage rule
// enabled while the runtime key is off or the rule could not run.
func (s *server) checkUpdatedStageAlertRule(w http.ResponseWriter, merged state.AlertRule) bool {
	return writeStageAlertProblem(w, stagealerts.CheckUpdated(merged, s.runtimeBool(runtimeConfigStageAlertRules, false)))
}

func writeStageAlertProblem(w http.ResponseWriter, prob *api.Problem) bool {
	if prob == nil {
		return true
	}
	api.WriteProblem(w, prob)
	return false
}
