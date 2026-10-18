package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardMCPFallbackFormsAndGuards(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	handler, cookie, store, manager := apidserver.NewAuthedDashboardServerFull(t)
	owner, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateAccountPlan(t.Context(), owner.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: "fallback-form", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.CreateAlertRule(t.Context(), state.AlertRule{ID: uuid.NewString(), AccountID: owner.ID, AppID: app.ID, Name: "notification", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", CooldownMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(t.Context(), "fallback-form-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateApp(t.Context(), state.App{AccountID: foreign.ID, Slug: "fallback-form-other", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive}); err != nil {
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
	if page.Code != 200 || csrf == nil || !strings.Contains(page.Body.String(), "Save fallback") || strings.Contains(page.Body.String(), "PRIVATE_SIGNING_KEY") {
		t.Fatal("missing attention form", page.Code)
	}
	form := url.Values{"csrf_token": {csrf.Value}, "scope": {"staging"}, "expected_version": {"0"}, "enabled": {"true"}, "webhook_url": {"https://example.com/fallback"}, "webhook_secret": {"PRIVATE_SIGNING_KEY"}, "after_failures": {"1"}}

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
	path := "/dashboard/mcp/apps/" + app.Slug + "/alerts/" + rule.ID + "/fallback"
	if w := post(path, false); w.Code != 400 {
		t.Fatal("CSRF bypass", w.Code)
	}
	if w := post("/dashboard/mcp/apps/fallback-form-other/alerts/"+rule.ID+"/fallback", true); w.Code != 404 {
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
	policy, err := store.GetMCPReadinessFallback(t.Context(), owner.ID, app.ID, rule.ID)
	if err != nil || !policy.Enabled || policy.Version != 1 || policy.WebhookSecretSealedMasked != "***" {
		t.Fatal(policy, err)
	}
	form.Set("expected_version", "1")
	form.Set("enabled", "false")
	form.Del("webhook_url")
	form.Del("webhook_secret")
	form.Del("after_failures")
	if w := post(path, true); w.Code != 303 {
		t.Fatal("disable fallback", w.Code, w.Body.String())
	}

	form = url.Values{"csrf_token": {csrf.Value}, "scope": {"staging"}, "expected_version": {"2"}, "enabled": {"true"}, "provider": {"pagerduty"}, "region": {"eu"}, "integration_key": {strings.Repeat("a", 32)}, "after_failures": {"2"}}
	if w := post(path, false); w.Code != 400 {
		t.Fatal("native fallback CSRF bypass", w.Code)
	}
	form.Add("provider", "webhook")
	if w := post(path, true); w.Code != 400 {
		t.Fatal("duplicate provider accepted", w.Code)
	}
	form.Set("provider", "pagerduty")
	if w := post(path, true); w.Code != 303 {
		t.Fatal("native fallback save", w.Code, w.Body.String())
	}
	policy, err = store.GetMCPReadinessFallback(t.Context(), owner.ID, app.ID, rule.ID)
	if err != nil || policy.Provider != "pagerduty" || policy.Region != "eu" || policy.Version != 3 || policy.IntegrationKeyMasked != "***" {
		t.Fatal(policy, err)
	}
	r = httptest.NewRequest("GET", "/dashboard/mcp?app="+app.Slug+"&scope=staging", nil)
	r.AddCookie(cookie)
	page = httptest.NewRecorder()
	handler.ServeHTTP(page, r)
	for _, want := range []string{"Save PagerDuty fallback", "Save PagerDuty fallback acknowledgement sync", `name="destination" value="fallback"`, `name="expected_fallback_version" value="3"`, `type="password" name="integration_key"`} {
		if page.Code != 200 || !strings.Contains(page.Body.String(), want) {
			t.Fatal("missing fallback control", want, page.Code)
		}
	}
	if strings.Contains(page.Body.String(), strings.Repeat("a", 32)) {
		t.Fatal("integration key echoed in dashboard")
	}
	form = url.Values{"csrf_token": {csrf.Value}, "scope": {"staging"}, "expected_version": {"0"}, "enabled": {"true"}, "destination": {"fallback"}, "expected_fallback_version": {"3"}, "service_id": {"PSVC001"}, "signing_secret": {strings.Repeat("s", 32)}}
	ackPath := "/dashboard/mcp/apps/" + app.Slug + "/alerts/" + rule.ID + "/pagerduty-acknowledgements"
	if w := post(ackPath, false); w.Code != 400 {
		t.Fatal("fallback ACK CSRF bypass", w.Code)
	}
	if w := post(ackPath, true); w.Code != 303 {
		t.Fatal("fallback ACK save", w.Code, w.Body.String())
	}
	bound, err := store.GetMCPReadinessPagerDutyAcknowledgements(t.Context(), owner.ID, app.ID, rule.ID)
	if err != nil || bound.Destination != "fallback" || bound.FallbackVersion != 3 || bound.PagerDutyVersion != 0 {
		t.Fatal(bound, err)
	}
	form.Set("expected_version", "1")
	form.Set("expected_pagerduty_version", "1")
	if w := post(ackPath, true); w.Code != 400 {
		t.Fatal("crossed receiver versions accepted", w.Code)
	}

}
