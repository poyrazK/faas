package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardMCPAttentionFormsAndGuards(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	handler, cookie, store, manager := apidserver.NewAuthedDashboardServerFull(t)
	owner, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateAccountPlan(t.Context(), owner.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: "attention-form", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.CreateAlertRule(t.Context(), state.AlertRule{AccountID: owner.ID, AppID: app.ID, Name: "notification", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", CooldownMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	coverage := strings.Repeat("a", 64)
	p, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": coverage, "mcp_readiness_scopes": []string{"staging"}})
	event, err := store.RecordMCPReadinessObservation(t.Context(), state.MCPReadinessObservation{Rule: rule, At: time.Now().Add(-time.Minute), Breached: true, Coverage: coverage, Payload: p})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(t.Context(), "attention-form-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateApp(t.Context(), state.App{AccountID: foreign.ID, Slug: "attention-form-other", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/dashboard/mcp?app="+app.Slug+"&scope=staging", nil)
	r.AddCookie(cookie)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, r)
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == apidserver.DashboardMCPAlertCookie {
			csrf = c
		}
	}
	if page.Code != 200 || csrf == nil || !strings.Contains(page.Body.String(), "Acknowledge incident") || !strings.Contains(page.Body.String(), event.IncidentID) {
		t.Fatal("missing attention form", page.Code)
	}
	form := url.Values{"csrf_token": {csrf.Value}, "scope": {"staging"}, "request_id": {uuid.NewString()}, "expected_version": {"0"}, "action": {"acknowledge"}, "reason": {"Investigating outage"}}
	post := func(path string, withCSRF bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(cookie)
		if withCSRF {
			r.AddCookie(csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	path := "/dashboard/mcp/apps/" + app.Slug + "/incidents/" + event.IncidentID + "/attention-actions"
	if w := post(path, false); w.Code != 400 {
		t.Fatal("CSRF bypass", w.Code)
	}
	if w := post("/dashboard/mcp/apps/attention-form-other/incidents/"+event.IncidentID+"/attention-actions", true); w.Code != 404 {
		t.Fatal("ownership bypass", w.Code, w.Body.String())
	}
	env, err := manager.Verify(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := manager.IssueWithSessionAndBindingHash(env.Sid, owner.ID, env.BindingHash, true)
	if err != nil {
		t.Fatal(err)
	}
	cleared := cookie
	cookie = &http.Cookie{Name: apidserver.SessionCookie, Value: pending}
	if w := post(path, true); w.Code != http.StatusFound || w.Header().Get("Location") != apidserver.DashboardMFAPath {
		t.Fatal("pending MFA bypass", w.Code)
	}
	cookie = cleared
	if w := post(path, true); w.Code != 303 {
		t.Fatal("acknowledge", w.Code, w.Body.String())
	}
	if w := post(path, true); w.Code != 303 {
		t.Fatal("browser replay", w.Code, w.Body.String())
	}
	form.Set("request_id", uuid.NewString())
	if w := post(path, true); w.Code != 409 {
		t.Fatal("stale action", w.Code, w.Body.String())
	}
	history, err := store.ListMCPReadinessIncidents(t.Context(), owner.ID, app.ID, "staging", "", 20)
	if err != nil || len(history.Events) != 1 || history.Events[0].DeliveryStatus != "pending" {
		t.Fatal(history, err)
	}
	attention, err := store.ListMCPReadinessAttention(t.Context(), owner.ID, app.ID, "staging", "", 20, false, time.Now().UTC())
	if err != nil || len(attention.Incidents) != 1 || attention.Incidents[0].State != "acknowledged" {
		t.Fatal(attention, err)
	}
	form.Add("scope", "production")
	if w := post(path, true); w.Code != 400 {
		t.Fatal("duplicate form fields", w.Code)
	}
}
