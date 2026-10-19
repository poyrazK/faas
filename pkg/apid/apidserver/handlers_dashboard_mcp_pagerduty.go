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

func (s *server) dashboardMCPPagerDuty(ctx context.Context, account, app, slug string, rule state.AlertRule, view *dashboard.MCPAlertView) {
	if string(rule.Metric) != api.MetricMCPReadinessUnhealthy || rule.Action != state.AlertActionWebhook {
		return
	}
	view.PagerDutyError = "PagerDuty policy unavailable."
	source, ok := s.store.(state.MCPReadinessPagerDutyStore)
	if !ok {
		return
	}
	policy, err := source.GetMCPReadinessPagerDuty(ctx, account, app, rule.ID)
	if err != nil {
		return
	}
	view.PagerDuty = &policy
	view.PagerDutyError = ""
	s.dashboardMCPPagerDutyAcknowledgements(ctx, account, app, slug, rule, view)
}
func (s *server) dashboardMCPPagerDutyAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardMCPAlertAction, account.ID, dashboardMCPAlertCookie) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid pagerduty CSRF token; reload the dashboard"))
		return
	}
	if r.ParseForm() != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid pagerduty form"))
		return
	}
	request, scope, ok := dashboardMCPPagerDutyForm(r.PostForm)
	if !ok {
		api.WriteProblem(w, api.ErrValidation("Supply a pagerduty destination, integration key and current version"))
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	handler := mcpreadiness.New(s.store, s.mcpReadinessPreview, s.authMw)
	handler.PagerDutySeal = sealMCPPagerDutyKey
	if s.audit != nil {
		handler.Audit = s.audit.Emit
	}
	response := s.forwardDashboardEdgeRuleJSON(r, account, http.MethodPut, "/v1/apps/"+url.PathEscape(slug)+"/alerts/"+url.PathEscape(id)+"/mcp-readiness-pagerduty", id, request, dashboardJSONHandler(s.requireMFA(handler.PagerDuty)))
	if response.Code >= 400 {
		copyMCPAlertProblem(w, response.Code, response.Body.Bytes())
		return
	}
	http.Redirect(w, r, dashboardMCPURL(url.Values{"scope": {scope}}, "app", slug)+"#performance-alerts", http.StatusSeeOther)
}
func dashboardMCPPagerDutyForm(form url.Values) (api.SetMCPReadinessPagerDutyRequest, string, bool) {
	allowed := map[string]bool{"csrf_token": true, "scope": true, "expected_version": true, "enabled": true, "integration_key": true, "region": true}
	for k, v := range form {
		if !allowed[k] || len(v) != 1 {
			return api.SetMCPReadinessPagerDutyRequest{}, "", false
		}
	}
	version, e1 := strconv.ParseInt(form.Get("expected_version"), 10, 64)
	enabled, e2 := strconv.ParseBool(form.Get("enabled"))
	scope := form.Get("scope")
	req := api.SetMCPReadinessPagerDutyRequest{ExpectedVersion: &version, Enabled: &enabled, IntegrationKey: form.Get("integration_key"), Region: form.Get("region")}
	return req, scope, e1 == nil && e2 == nil && api.ValidateScope(scope) == nil && api.ValidateMCPReadinessPagerDutyRequest(req) == nil
}
