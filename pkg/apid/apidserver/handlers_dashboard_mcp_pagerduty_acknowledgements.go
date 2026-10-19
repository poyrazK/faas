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

func (s *server) dashboardMCPPagerDutyAcknowledgements(ctx context.Context, account, app, slug string, rule state.AlertRule, view *dashboard.MCPAlertView) {
	view.PagerDutyAcknowledgementsError = "PagerDuty acknowledgement settings unavailable."
	source, ok := s.store.(state.MCPReadinessPagerDutyAcknowledgementStore)
	if !ok {
		return
	}
	policy, err := source.GetMCPReadinessPagerDutyAcknowledgements(ctx, account, app, rule.ID, "primary")
	if err != nil {
		return
	}
	view.PagerDutyAcknowledgements = &policy
	fallback, err := source.GetMCPReadinessPagerDutyAcknowledgements(ctx, account, app, rule.ID, "fallback")
	if err != nil {
		return
	}
	view.PagerDutyFallbackAcknowledgements = &fallback
	view.PagerDutyAcknowledgementEventsURL = "/v1/apps/" + url.PathEscape(slug) + "/alerts/" + rule.ID + "/mcp-readiness-pagerduty-acknowledgements/events"
	recent, err := source.ListMCPReadinessPagerDutyAcknowledgementEvents(ctx, account, app, rule.ID, "", api.MCPReadinessPagerDutyWebhookDashboardEvents)
	if err != nil {
		view.PagerDutyAcknowledgementsError = "PagerDuty acknowledgement history unavailable."
		return
	}
	view.PagerDutyAcknowledgementEvents = recent.Events
	view.PagerDutyAcknowledgementsError = ""
}
func (s *server) dashboardMCPPagerDutyAcknowledgementsAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.MCPReadinessPagerDutyWebhookBodyMaxBytes)
	if middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardMCPAlertAction, account.ID, dashboardMCPAlertCookie) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid PagerDuty acknowledgement CSRF token; reload the dashboard"))
		return
	}
	if r.ParseForm() != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid PagerDuty acknowledgement form"))
		return
	}
	request, scope, ok := dashboardMCPPagerDutyAcknowledgementsForm(r.PostForm)
	if !ok {
		api.WriteProblem(w, api.ErrValidation("Supply current versions, a service ID and webhook signing secret"))
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	handler := mcpreadiness.New(s.store, s.mcpReadinessPreview, s.authMw)
	handler.PagerDutyAcknowledgementSeal = sealMCPPagerDutyWebhookSecret
	if s.audit != nil {
		handler.Audit = s.audit.Emit
	}
	response := s.forwardDashboardEdgeRuleJSON(r, account, http.MethodPut, "/v1/apps/"+url.PathEscape(slug)+"/alerts/"+url.PathEscape(id)+"/mcp-readiness-pagerduty-acknowledgements", id, request, dashboardJSONHandler(s.requireMFA(handler.PagerDutyAcknowledgements)))
	if response.Code >= 400 {
		copyMCPAlertProblem(w, response.Code, response.Body.Bytes())
		return
	}
	http.Redirect(w, r, dashboardMCPURL(url.Values{"scope": {scope}}, "app", slug)+"#performance-alerts", http.StatusSeeOther)
}
func dashboardMCPPagerDutyAcknowledgementsForm(form url.Values) (api.SetMCPReadinessPagerDutyAcknowledgementsRequest, string, bool) {
	allowed := map[string]bool{"sync_unacknowledgements": true, "csrf_token": true, "scope": true, "expected_version": true, "enabled": true, "destination": true, "expected_fallback_version": true, "expected_pagerduty_version": true, "service_id": true, "signing_secret": true}
	for k, v := range form {
		if !allowed[k] || len(v) != 1 {
			return api.SetMCPReadinessPagerDutyAcknowledgementsRequest{}, "", false
		}
	}
	version, e1 := strconv.ParseInt(form.Get("expected_version"), 10, 64)
	enabled, e2 := strconv.ParseBool(form.Get("enabled"))
	req := api.SetMCPReadinessPagerDutyAcknowledgementsRequest{Destination: form.Get("destination"), ExpectedVersion: &version, Enabled: &enabled, ServiceID: form.Get("service_id"), SigningSecret: form.Get("signing_secret")}

	if form.Has("sync_unacknowledgements") {
		v, err := strconv.ParseBool(form.Get("sync_unacknowledgements"))
		if err != nil {
			return req, "", false
		}
		req.SyncUnacknowledgements = v
	}
	for key, target := range map[string]**int64{"expected_pagerduty_version": &req.ExpectedPagerDutyVersion, "expected_fallback_version": &req.ExpectedFallbackVersion} {
		if form.Has(key) {
			v, err := strconv.ParseInt(form.Get(key), 10, 64)
			if err != nil {
				return req, "", false
			}
			*target = &v
		}
	}
	scope := form.Get("scope")
	return req, scope, e1 == nil && e2 == nil && api.ValidateScope(scope) == nil && api.ValidateMCPReadinessPagerDutyAcknowledgementsRequest(req) == nil
}
