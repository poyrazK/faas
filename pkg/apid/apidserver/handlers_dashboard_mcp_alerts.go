package apidserver

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardMCPAlertAction = "dashboard_mcp_alert"
const dashboardMCPAlertCookie = "dashboard_mcp_alert_csrf"

func (s *server) dashboardMCPAlerts(w http.ResponseWriter, r *http.Request, ctx context.Context, account state.Account, appID, slug string, data *dashboard.MCPOperationsData) {
	if appID == "" {
		return
	}
	data.AlertsURL = "/dashboard/mcp/apps/" + url.PathEscape(slug) + "/alerts"
	rules, err := s.store.ListAlertRulesForAccount(ctx, account.ID)
	if err != nil {
		data.AlertError = "Alert configuration is unavailable."
		return
	}
	for _, rule := range rules {
		if rule.AccountID != account.ID || rule.AppID != appID || (!api.IsMCPProtocolMetric(string(rule.Metric)) && string(rule.Metric) != api.MetricMCPReadinessUnhealthy) {
			continue
		}
		if string(rule.Metric) == api.MetricMCPReadinessUnhealthy && rule.MCPReadinessScope != "" && rule.MCPReadinessScope != api.MCPConnectionScope(data.ReadinessScope) {
			continue
		}
		view := dashboard.MCPAlertView{ReadinessScope: rule.MCPReadinessScope, ID: rule.ID, Name: rule.Name, Metric: string(rule.Metric), State: string(rule.State), Window: string(rule.WindowSpec), Threshold: rule.Threshold, MinCalls: rule.MCPMinCalls, ForMinutes: rule.MCPForMinutes, Enabled: rule.Enabled, URL: data.AlertsURL + "/" + url.PathEscape(rule.ID)}
		s.dashboardMCPFallback(ctx, account.ID, appID, rule, &view)
		s.dashboardMCPPagerDuty(ctx, account.ID, appID, slug, rule, &view)
		s.dashboardMCPReminderDefaults(ctx, account.ID, appID, rule, &view)
		data.Alerts = append(data.Alerts, view)
	}
	limits, ok := api.LimitsFor(account.Plan)
	if !ok || limits.AlertRuleLimitPerApp == 0 {
		data.AlertError = "Your plan does not support customer alert rules."
		return
	}
	if s.sessions == nil {
		return
	}
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardMCPAlertAction, account.ID, dashboardMCPAlertCookie)
	if err != nil {
		return
	}
	data.AlertCSRF = token
	http.SetCookie(w, &http.Cookie{Name: dashboardMCPAlertCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
}
func (s *server) dashboardMCPAlertAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardMCPAlertAction, account.ID, dashboardMCPAlertCookie); err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid alert CSRF token; reload the dashboard"))
		return
	}
	if r.ParseForm() != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid alert form"))
		return
	}
	allowed := map[string]bool{"csrf_token": true, "metric": true, "threshold": true, "window_spec": true, "mcp_min_calls": true, "mcp_for_minutes": true, "enabled": true, "name": true, "webhook_url": true, "webhook_secret": true, "mcp_readiness_scope": true}
	for key, values := range r.PostForm {
		if !allowed[key] || len(values) != 1 {
			api.WriteProblem(w, api.ErrValidation("Invalid alert form"))
			return
		}
	}
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || app.AccountID != account.ID {
		http.NotFound(w, r)
		return
	}
	metric := r.PostForm.Get("metric")
	if !api.IsMCPProtocolMetric(metric) && metric != api.MetricMCPReadinessUnhealthy {
		api.WriteProblem(w, api.ErrValidation("Invalid MCP metric"))
		return
	}
	minCalls, forMinutes := 0, 0
	var err1, err2 error
	if api.IsMCPProtocolMetric(metric) {
		minCalls, err1 = strconv.Atoi(r.PostForm.Get("mcp_min_calls"))
		forMinutes, err2 = strconv.Atoi(r.PostForm.Get("mcp_for_minutes"))
	} else if r.PostForm.Get("mcp_min_calls") != "" && r.PostForm.Get("mcp_min_calls") != "0" || r.PostForm.Get("mcp_for_minutes") != "" && r.PostForm.Get("mcp_for_minutes") != "0" {
		api.WriteProblem(w, api.ErrValidation("Readiness alerts use policy monitor thresholds, not protocol observation settings"))
		return
	}
	threshold, err3 := strconv.ParseFloat(r.PostForm.Get("threshold"), 64)
	if err1 != nil || err2 != nil || err3 != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid MCP alert numbers"))
		return
	}
	readinessScope := r.PostForm.Get("mcp_readiness_scope")
	if p := api.ValidateMCPReadinessAlertScope(metric, readinessScope); p != nil {
		api.WriteProblem(w, p)
		return
	}
	enabled := r.PostForm.Get("enabled") == "yes"
	if r.PostForm.Get("enabled") != "yes" && r.PostForm.Get("enabled") != "no" {
		api.WriteProblem(w, api.ErrValidation("Invalid enabled setting"))
		return
	}
	window := r.PostForm.Get("window_spec")
	id := r.PathValue("id")
	if id != "" {
		rule, err := s.store.AlertRuleByID(r.Context(), id)
		if err != nil || rule.AccountID != account.ID || rule.AppID != app.ID || string(rule.Metric) != metric {
			http.NotFound(w, r)
			return
		}
		if r.PostForm.Has("mcp_readiness_scope") && readinessScope != rule.MCPReadinessScope {
			api.WriteProblem(w, api.ErrAlertRuleInvalid("readiness alert environment cannot change; create a new rule"))
			return
		}
		req := api.UpdateAlertRuleRequest{Threshold: &threshold, WindowSpec: &window, MCPMinCalls: &minCalls, MCPForMinutes: &forMinutes, Enabled: &enabled}
		response := s.forwardDashboardEdgeRuleJSON(r, account, http.MethodPatch, "/v1/alerts/"+url.PathEscape(id), id, req, s.updateAlertRule)
		if response.Code >= 400 {
			copyMCPAlertProblem(w, response.Code, response.Body.Bytes())
			return
		}
	} else {
		req := api.CreateAlertRuleRequest{MCPReadinessScope: readinessScope, Name: r.PostForm.Get("name"), Metric: metric, Comparison: "gt", Threshold: threshold, WindowSpec: window, MCPMinCalls: &minCalls, MCPForMinutes: &forMinutes, Enabled: &enabled, WebhookURL: r.PostForm.Get("webhook_url"), WebhookSecret: r.PostForm.Get("webhook_secret")}
		response := s.forwardDashboardEdgeRuleJSON(r, account, http.MethodPost, "/v1/apps/"+url.PathEscape(app.Slug)+"/alerts", "", req, s.createAlertRule)
		if response.Code >= 400 {
			copyMCPAlertProblem(w, response.Code, response.Body.Bytes())
			return
		}
	}
	http.Redirect(w, r, dashboardMCPURL(alertDashboardScopeQuery(readinessScope), "app", app.Slug)+"#performance-alerts", http.StatusSeeOther)
}
func copyMCPAlertProblem(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func alertDashboardScopeQuery(scope string) url.Values {
	query := url.Values{}
	if scope != "" {
		query.Set("scope", scope)
	}
	return query
}
