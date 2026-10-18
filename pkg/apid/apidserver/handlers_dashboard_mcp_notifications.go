package apidserver

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcpreadiness"
	"github.com/onebox-faas/faas/pkg/middleware"
)

func (s *server) dashboardMCPNotificationAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardMCPAlertAction, account.ID, dashboardMCPAlertCookie) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid notification CSRF token; reload the dashboard"))
		return
	}
	if r.ParseForm() != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid notification form"))
		return
	}
	req, scope, valid := dashboardMCPNotificationForm(r.PostForm)
	if !valid {
		api.WriteProblem(w, api.ErrValidation("Invalid notification form, version or environment"))
		return
	}
	slug := r.PathValue("slug")
	handler := mcpreadiness.New(s.store, s.mcpReadinessPreview, s.authMw)
	if s.audit != nil {
		handler.Audit = s.audit.Emit
	}
	response := s.forwardDashboardEdgeRuleJSON(r, account, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/mcp-readiness-notifications/actions", "", req, dashboardJSONHandler(s.requireMFA(handler.Notifications)))
	if response.Code >= 400 {
		copyMCPAlertProblem(w, response.Code, response.Body.Bytes())
		return
	}
	http.Redirect(w, r, dashboardMCPURL(url.Values{"scope": {scope}}, "app", slug)+"#readiness-notifications", http.StatusSeeOther)
}

func dashboardMCPNotificationForm(form url.Values) (api.MCPReadinessNotificationAction, string, bool) {
	for k, v := range form {
		if len(v) != 1 || k != "csrf_token" && k != "scope" && k != "request_id" && k != "event_id" && k != "expected_version" && k != "action" && k != "reason" && k != "destination" {
			return api.MCPReadinessNotificationAction{}, "", false
		}
	}
	scope := form.Get("scope")
	version, err := strconv.ParseInt(form.Get("expected_version"), 10, 64)
	if err != nil || api.ValidateScope(scope) != nil {
		return api.MCPReadinessNotificationAction{}, "", false
	}
	return api.MCPReadinessNotificationAction{Destination: form.Get("destination"), RequestID: form.Get("request_id"), EventID: form.Get("event_id"), ExpectedVersion: &version, Action: form.Get("action"), Reason: form.Get("reason")}, scope, true
}
