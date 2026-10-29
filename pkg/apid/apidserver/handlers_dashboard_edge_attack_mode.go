package apidserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// dashboardSetEdgeAttackMode switches attack mode on or off from the Edge
// protection panel (ADR-20261021220456). It goes through the JSON handlers,
// so validation, fleet convergence and audit are the API's.
func (s *server) dashboardSetEdgeAttackMode(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if !s.verifyDashboardEdgeRulesCSRF(w, r, acct.ID) {
		return
	}
	slug := r.PathValue("slug")
	page := "/dashboard/apps/" + url.PathEscape(slug) + "/edge-rules"
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/edge-protection/attack-mode"
	var resp *httptest.ResponseRecorder
	action := "attack_mode_on"
	switch r.PostFormValue("attack_mode") {
	case "on":
		seconds, convErr := strconv.Atoi(r.PostFormValue("duration_seconds"))
		if convErr != nil {
			http.Redirect(w, r, page+"?action=error", http.StatusSeeOther)
			return
		}
		resp = s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPut, path, "",
			api.EnableEdgeAttackModeRequest{DurationSeconds: seconds}, s.edgeRules.EnableAppEdgeAttackMode)
	case "off":
		action = "attack_mode_off"
		resp = s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodDelete, path, "", nil, s.edgeRules.DisableAppEdgeAttackMode)
	default:
		http.Redirect(w, r, page+"?action=error", http.StatusSeeOther)
		return
	}
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, page+"?action="+action+"#attack-mode", http.StatusSeeOther)
}
