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

func (s *server) dashboardMCPFallback(ctx context.Context, account, app string, rule state.AlertRule, view *dashboard.MCPAlertView) {
	if string(rule.Metric) != api.MetricMCPReadinessUnhealthy || rule.Action != state.AlertActionWebhook {
		return
	}
	view.FallbackError = "Fallback policy unavailable."
	source, ok := s.store.(state.MCPReadinessFallbackStore)
	if !ok {
		return
	}
	policy, err := source.GetMCPReadinessFallback(ctx, account, app, rule.ID)
	if err != nil {
		return
	}
	view.Fallback = &policy
	view.FallbackError = ""
}
func (s *server) dashboardMCPFallbackAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardMCPAlertAction, account.ID, dashboardMCPAlertCookie) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid fallback CSRF token; reload the dashboard"))
		return
	}
	if r.ParseForm() != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid fallback form"))
		return
	}
	request, scope, ok := dashboardMCPFallbackForm(r.PostForm)
	if !ok {
		api.WriteProblem(w, api.ErrValidation("Supply a fallback destination, signing secret, failure threshold and current version"))
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	handler := mcpreadiness.New(s.store, s.mcpReadinessPreview, s.authMw)
	handler.FallbackURL = resolveAndCheckEgress
	handler.FallbackSeal = sealMCPFallbackSecret
	handler.PagerDutySeal = sealMCPPagerDutyKey
	if s.audit != nil {
		handler.Audit = s.audit.Emit
	}
	response := s.forwardDashboardEdgeRuleJSON(r, account, http.MethodPut, "/v1/apps/"+url.PathEscape(slug)+"/alerts/"+url.PathEscape(id)+"/mcp-readiness-fallback", id, request, dashboardJSONHandler(s.requireMFA(handler.Fallback)))
	if response.Code >= 400 {
		copyMCPAlertProblem(w, response.Code, response.Body.Bytes())
		return
	}
	http.Redirect(w, r, dashboardMCPURL(url.Values{"scope": {scope}}, "app", slug)+"#performance-alerts", http.StatusSeeOther)
}
func dashboardMCPFallbackForm(form url.Values) (api.SetMCPReadinessFallbackRequest, string, bool) {
	allowed := map[string]bool{"csrf_token": true, "scope": true, "expected_version": true, "enabled": true, "webhook_url": true, "webhook_secret": true, "after_failures": true, "provider": true, "region": true, "integration_key": true}
	for k, v := range form {
		if !allowed[k] || len(v) != 1 {
			return api.SetMCPReadinessFallbackRequest{}, "", false
		}
	}
	version, e1 := strconv.ParseInt(form.Get("expected_version"), 10, 64)
	enabled, e2 := strconv.ParseBool(form.Get("enabled"))
	scope := form.Get("scope")
	req := api.SetMCPReadinessFallbackRequest{Provider: form.Get("provider"), Region: form.Get("region"), IntegrationKey: form.Get("integration_key"), ExpectedVersion: &version, Enabled: &enabled, WebhookURL: form.Get("webhook_url"), WebhookSecret: form.Get("webhook_secret")}
	var e3 error
	if form.Has("after_failures") {
		req.AfterFailures, e3 = strconv.Atoi(form.Get("after_failures"))
	}
	return req, scope, e1 == nil && e2 == nil && e3 == nil && api.ValidateScope(scope) == nil && api.ValidateMCPReadinessFallbackRequest(req) == nil
}
