package apidserver

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcpreadiness"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardMCPAttentionAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardMCPAlertAction, account.ID, dashboardMCPAlertCookie) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid incident attention CSRF token; reload the dashboard"))
		return
	}
	if r.ParseForm() != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid incident attention form"))
		return
	}
	req, scope, ok := dashboardMCPAttentionForm(r.PostForm)
	if !ok {
		api.WriteProblem(w, api.ErrValidation("Supply a valid attention action, version, environment and optional RFC3339 expiry"))
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	handler := mcpreadiness.New(s.store, s.mcpReadinessPreview, s.authMw)
	if s.audit != nil {
		handler.Audit = s.audit.Emit
	}
	response := s.forwardDashboardEdgeRuleJSON(r, account, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/mcp-readiness-incidents/"+url.PathEscape(id)+"/attention-actions", id, req, dashboardJSONHandler(s.requireMFA(handler.Attention)))
	if response.Code >= 400 {
		copyMCPAlertProblem(w, response.Code, response.Body.Bytes())
		return
	}
	http.Redirect(w, r, dashboardMCPURL(url.Values{"scope": {scope}}, "app", slug)+"#readiness-attention", http.StatusSeeOther)
}
func dashboardMCPAttentionForm(form url.Values) (api.MCPReadinessAttentionAction, string, bool) {
	allowed := map[string]bool{"csrf_token": true, "scope": true, "request_id": true, "expected_version": true, "action": true, "reason": true}
	action := form.Get("action")
	if action == "acknowledge" {
		allowed["expires_at"] = true
	}
	if action == "configure_reminders" {
		allowed["interval_seconds"] = true
		allowed["max_reminders"] = true
	}
	for k, v := range form {
		if !allowed[k] || len(v) != 1 {
			return api.MCPReadinessAttentionAction{}, "", false
		}
	}
	scope := form.Get("scope")
	version, err := strconv.ParseInt(form.Get("expected_version"), 10, 64)
	if err != nil || api.ValidateScope(scope) != nil {
		return api.MCPReadinessAttentionAction{}, "", false
	}
	req := api.MCPReadinessAttentionAction{RequestID: form.Get("request_id"), ExpectedVersion: &version, Action: action, Reason: form.Get("reason")}
	if action == "acknowledge" && form.Get("expires_at") != "" {
		at, err := time.Parse(time.RFC3339, form.Get("expires_at"))
		if err != nil {
			return req, scope, false
		}
		req.ExpiresAt = &at
	}
	if action == "configure_reminders" {
		interval, e1 := strconv.Atoi(form.Get("interval_seconds"))
		maximum, e2 := strconv.Atoi(form.Get("max_reminders"))
		if e1 != nil || e2 != nil {
			return req, scope, false
		}
		req.ReminderPolicy = &api.MCPReadinessReminderPolicy{IntervalSeconds: interval, MaxReminders: maximum}
	}
	return req, scope, api.ValidateMCPReadinessAttentionAction(req) == nil
}

func (s *server) dashboardMCPAttention(ctx context.Context, account, app string, data *dashboard.MCPOperationsData) {
	data.ReadinessAttentionError = "Incident attention is unavailable."
	source, ok := s.store.(state.MCPReadinessAttentionStore)
	if !ok {
		return
	}
	page, err := source.ListMCPReadinessAttention(ctx, account, app, api.MCPConnectionScope(data.ReadinessScope), data.ReadinessAttentionAfter, 20, false, time.Now().UTC())
	if err != nil {
		return
	}
	data.ReadinessAttentionError = ""
	for _, i := range page.Incidents {
		view := dashboard.MCPReadinessAttentionView{MCPReadinessIncidentAttention: i, AcknowledgeRequestID: uuid.NewString(), ReleaseRequestID: uuid.NewString(), ConfigureRequestID: uuid.NewString(), HistoryURL: "/v1/apps/" + url.PathEscape(data.AppFilter) + "/mcp-readiness-incidents/" + url.PathEscape(i.IncidentID) + "/attention-actions"}
		history, err := source.ListMCPReadinessAttentionHistory(ctx, account, app, i.IncidentID, "", 5)
		if err != nil {
			view.HistoryError = "Attention history is unavailable."
		} else {
			view.History = history.Actions
		}
		data.ReadinessAttention = append(data.ReadinessAttention, view)
	}
	if page.NextAfter != "" {
		data.ReadinessAttentionNextURL = "/dashboard/mcp?app=" + url.QueryEscape(data.AppFilter) + "&scope=" + url.QueryEscape(api.MCPConnectionScope(data.ReadinessScope)) + "&attention_after=" + url.QueryEscape(page.NextAfter) + "#readiness-attention"
	}
}
