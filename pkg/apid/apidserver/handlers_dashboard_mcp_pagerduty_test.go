package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardMCPPagerDutyFormsAndGuards(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	handler, cookie, store, manager := apidserver.NewAuthedDashboardServerFull(t)
	owner, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateAccountPlan(t.Context(), owner.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: "pagerduty-form", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.CreateAlertRule(t.Context(), state.AlertRule{AccountID: owner.ID, AppID: app.ID, Name: "notification", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", CooldownMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(t.Context(), "pagerduty-form-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateApp(t.Context(), state.App{AccountID: foreign.ID, Slug: "pagerduty-form-other", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive}); err != nil {
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
	if page.Code != 200 || csrf == nil || !strings.Contains(page.Body.String(), "Save PagerDuty integration") || strings.Contains(page.Body.String(), "PRIVATE_SIGNING_KEY") {
		t.Fatal("missing attention form", page.Code)
	}
	form := url.Values{"csrf_token": {csrf.Value}, "scope": {"staging"}, "expected_version": {"0"}, "enabled": {"true"}, "region": {"eu"}, "integration_key": {strings.Repeat("a", 32)}}

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
	path := "/dashboard/mcp/apps/" + app.Slug + "/alerts/" + rule.ID + "/pagerduty"
	if w := post(path, false); w.Code != 400 {
		t.Fatal("CSRF bypass", w.Code)
	}
	if w := post("/dashboard/mcp/apps/pagerduty-form-other/alerts/"+rule.ID+"/pagerduty", true); w.Code != 404 {
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
		t.Fatal("save fallback", w.Code, w.Body.String())
	}
	if w := post(path, true); w.Code != 409 {
		t.Fatal("stale browser form", w.Code, w.Body.String())
	}
	policy, err := store.GetMCPReadinessPagerDuty(t.Context(), owner.ID, app.ID, rule.ID)
	if err != nil || policy.Version != 1 || !policy.Enabled || policy.IntegrationKeyMasked != "***" || policy.Region != "eu" {
		t.Fatal(policy, err)
	}
	form.Set("region", "ap")
	if w := post(path, true); w.Code != 400 {
		t.Fatal("invalid region", w.Code)
	}
	form.Set("region", "eu")
	form.Add("region", "us")
	if w := post(path, true); w.Code != 400 {
		t.Fatal("duplicate region", w.Code)
	}
	form.Set("region", "eu")
	form.Add("expected_version", "1")
	if w := post(path, true); w.Code != 400 {
		t.Fatal("duplicate field", w.Code, w.Body.String())
	}
	form.Set("expected_version", "1")
	form.Set("unknown", "1")
	if w := post(path, true); w.Code != 400 {
		t.Fatal("unknown field", w.Code, w.Body.String())
	}
	form.Del("unknown")
	form.Set("expected_version", "1")
	form.Set("enabled", "false")
	form.Del("integration_key")
	form.Del("region")
	if w := post(path, true); w.Code != 303 {
		t.Fatal("disable fallback", w.Code, w.Body.String())
	}

}
