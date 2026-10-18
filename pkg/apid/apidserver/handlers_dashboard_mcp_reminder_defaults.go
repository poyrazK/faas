package apidserver

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcpreadiness"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardMCPReminderDefaults(ctx context.Context, account, app string, rule state.AlertRule, view *dashboard.MCPAlertView) {
	if string(rule.Metric) != api.MetricMCPReadinessUnhealthy || rule.Action != state.AlertActionWebhook {
		return
	}
	view.ReminderDefaultsError = "Reminder defaults unavailable."
	source, ok := s.store.(state.MCPReadinessReminderDefaultsStore)
	if !ok {
		return
	}
	policy, err := source.GetMCPReadinessReminderDefaults(ctx, account, app, rule.ID)
	if err != nil {
		return
	}
	view.ReminderDefaults = &policy
	view.ReminderDefaultsError = ""
}
func (s *server) dashboardMCPReminderDefaultsAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardMCPAlertAction, account.ID, dashboardMCPAlertCookie) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid reminder defaults CSRF token; reload the dashboard"))
		return
	}
	if r.ParseForm() != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid reminder defaults form"))
		return
	}
	request, scope, ok := dashboardMCPReminderDefaultsForm(r.PostForm)
	if !ok {
		api.WriteProblem(w, api.ErrValidation("Supply a reminder interval, budget and current defaults version"))
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	handler := mcpreadiness.New(s.store, s.mcpReadinessPreview, s.authMw)
	if s.audit != nil {
		handler.Audit = s.audit.Emit
	}
	response := s.forwardDashboardEdgeRuleJSON(r, account, http.MethodPut, "/v1/apps/"+url.PathEscape(slug)+"/alerts/"+url.PathEscape(id)+"/mcp-readiness-reminder-defaults", id, request, dashboardJSONHandler(s.requireMFA(handler.ReminderDefaults)))
	if response.Code >= 400 {
		copyMCPAlertProblem(w, response.Code, response.Body.Bytes())
		return
	}
	http.Redirect(w, r, dashboardMCPURL(url.Values{"scope": {scope}}, "app", slug)+"#performance-alerts", http.StatusSeeOther)
}
func dashboardMCPReminderDefaultsForm(form url.Values) (api.SetMCPReadinessReminderDefaultsRequest, string, bool) {
	allowed := map[string]bool{"csrf_token": true, "scope": true, "expected_version": true, "enabled": true, "interval_seconds": true, "max_reminders": true}
	for k, v := range form {
		if !allowed[k] || len(v) != 1 {
			return api.SetMCPReadinessReminderDefaultsRequest{}, "", false
		}
	}
	version, e1 := strconv.ParseInt(form.Get("expected_version"), 10, 64)
	enabled, e2 := strconv.ParseBool(form.Get("enabled"))
	scope := form.Get("scope")
	policy := api.MCPReadinessReminderPolicy{}
	var e3, e4 error
	if enabled {
		policy.IntervalSeconds, e3 = strconv.Atoi(form.Get("interval_seconds"))
		policy.MaxReminders, e4 = strconv.Atoi(form.Get("max_reminders"))
	} else if form.Has("interval_seconds") || form.Has("max_reminders") {
		return api.SetMCPReadinessReminderDefaultsRequest{}, "", false
	}
	request := api.SetMCPReadinessReminderDefaultsRequest{ExpectedVersion: &version, ReminderPolicy: &policy}
	return request, scope, e1 == nil && e2 == nil && e3 == nil && e4 == nil && api.ValidateScope(scope) == nil && api.ValidateMCPReadinessReminderDefaultsRequest(request) == nil && (!enabled || policy.MaxReminders > 0)
}
