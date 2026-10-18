package apidserver

import (
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcpreadiness"
	"github.com/onebox-faas/faas/pkg/middleware"
)

// The named MCP-alert CSRF envelope already protects configuration changes on
// this page. Delegate to the owned-app JSON handler after verification.
func (s *server) dashboardMCPMaintenanceAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardMCPAlertAction, account.ID, dashboardMCPAlertCookie) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid maintenance CSRF token; reload the dashboard"))
		return
	}
	if r.ParseForm() != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid maintenance form"))
		return
	}
	for k, v := range r.PostForm {
		if len(v) != 1 || k != "csrf_token" && k != "scope" && k != "starts_at" && k != "ends_at" && k != "reason" {
			api.WriteProblem(w, api.ErrValidation("Invalid maintenance form"))
			return
		}
	}
	scope := r.PostForm.Get("scope")
	if api.ValidateScope(scope) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid environment"))
		return
	}
	slug := r.PathValue("slug")
	id := r.PathValue("id")
	base := "/v1/apps/" + url.PathEscape(slug) + "/mcp-readiness-maintenance"
	var payload any
	if id != "" {
		base += "/" + url.PathEscape(id) + "/cancel"
		payload = api.CancelMCPReadinessMaintenanceRequest{Reason: r.PostForm.Get("reason")}
	} else {
		req := api.CreateMCPReadinessMaintenanceRequest{Scope: scope, Reason: r.PostForm.Get("reason")}
		var err error
		req.EndsAt, err = time.Parse(time.RFC3339, r.PostForm.Get("ends_at"))
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("Supply an RFC3339 end time with timezone"))
			return
		}
		if start := r.PostForm.Get("starts_at"); start != "" {
			req.StartsAt, err = time.Parse(time.RFC3339, start)
			if err != nil {
				api.WriteProblem(w, api.ErrValidation("Supply an RFC3339 start time with timezone"))
				return
			}
		}
		payload = req
	}
	handler := mcpreadiness.New(s.store, s.mcpReadinessPreview, s.authMw)
	if s.audit != nil {
		handler.Audit = s.audit.Emit
	}
	// sessionAuth has verified the live browser session and established its
	// implicit-admin account. It stamps MFA state, but deliberately does not
	// create the bearer principal required by the REST scope middleware.
	// REST routes keep their admin-scope gate; browser requests use sessionAuth.
	guarded := s.requireMFA(handler.Maintenance)
	response := s.forwardDashboardEdgeRuleJSON(r, account, http.MethodPost, base, id, payload, dashboardJSONHandler(guarded))
	if response.Code >= 400 {
		copyMCPAlertProblem(w, response.Code, response.Body.Bytes())
		return
	}
	http.Redirect(w, r, dashboardMCPURL(url.Values{"scope": {scope}}, "app", slug)+"#readiness-maintenance", http.StatusSeeOther)
}
