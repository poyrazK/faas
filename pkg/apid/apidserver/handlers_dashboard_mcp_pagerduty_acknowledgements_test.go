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

func TestDashboardMCPPagerDutyAcknowledgementsForms(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	handler, cookie, store, _ := apidserver.NewAuthedDashboardServerFull(t)
	owner, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateAccountPlan(t.Context(), owner.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: "pagerduty-ack-form", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.CreateAlertRule(t.Context(), state.AlertRule{ID: uuid.NewString(), AccountID: owner.ID, AppID: app.ID, Name: "readiness", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", CooldownMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetMCPReadinessPagerDuty(t.Context(), owner.ID, app.ID, rule.ID, 0, state.MCPReadinessPagerDutyConfig{Enabled: true, Region: "eu", IntegrationKeySealed: []byte("sealed"), Fingerprint: strings.Repeat("a", 64)}); err != nil {
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
	if page.Code != 200 || csrf == nil || !strings.Contains(page.Body.String(), "Save PagerDuty acknowledgement sync") || !strings.Contains(page.Body.String(), "/v1/webhooks/mcp-readiness/pagerduty/"+rule.ID) {
		t.Fatal("missing webhook setup form", page.Code)
	}
	form := url.Values{"csrf_token": {csrf.Value}, "scope": {"staging"}, "expected_version": {"0"}, "expected_pagerduty_version": {"1"}, "enabled": {"true"}, "service_id": {"PSVC001"}, "signing_secret": {strings.Repeat("s", 32)}}
	post := func(withCSRF bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/dashboard/mcp/apps/"+app.Slug+"/alerts/"+rule.ID+"/pagerduty-acknowledgements", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(cookie)
		if withCSRF {
			r.AddCookie(csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := post(false); w.Code != 400 {
		t.Fatal("CSRF bypass", w.Code)
	}
	form.Add("service_id", "OTHER")
	if w := post(true); w.Code != 400 {
		t.Fatal("duplicate accepted", w.Code)
	}
	form.Set("service_id", "PSVC001")
	form.Set("sync_unacknowledgements", "invalid")
	if w := post(true); w.Code != 400 {
		t.Fatal("invalid release option accepted", w.Code)
	}
	form.Set("sync_unacknowledgements", "true")
	form.Add("sync_unacknowledgements", "false")
	if w := post(true); w.Code != 400 {
		t.Fatal("duplicate release option accepted", w.Code)
	}
	form.Set("sync_unacknowledgements", "true")
	if w := post(true); w.Code != 303 {
		t.Fatal("save", w.Code, w.Body.String())
	}
	if w := post(true); w.Code != 409 {
		t.Fatal("stale write", w.Code)
	}
	p, err := store.GetMCPReadinessPagerDutyAcknowledgements(t.Context(), owner.ID, app.ID, rule.ID)
	if err != nil || !p.SyncUnacknowledgements || p.Region != "eu" || p.ServiceID != "PSVC001" || p.SigningSecretMasked != "***" {
		t.Fatal(p, err)
	}
	form.Set("expected_version", "1")
	form.Set("enabled", "false")
	form.Del("expected_pagerduty_version")
	form.Del("service_id")
	form.Del("signing_secret")
	form.Del("sync_unacknowledgements")
	if w := post(true); w.Code != 303 {
		t.Fatal("disable", w.Code, w.Body.String())
	}
	p, err = store.GetMCPReadinessPagerDutyAcknowledgements(t.Context(), owner.ID, app.ID, rule.ID)
	if err != nil || p.Enabled || p.SigningSecretMasked != "" {
		t.Fatal(p, err)
	}
	if _, err = store.SetMCPReadinessFallback(t.Context(), owner.ID, app.ID, rule.ID, 0, state.MCPReadinessFallbackConfig{Provider: "pagerduty", Enabled: true, Region: "us", IntegrationKeySealed: []byte("sealed-fallback"), Fingerprint: strings.Repeat("b", 64), AfterFailures: 1}); err != nil {
		t.Fatal(err)
	}
	form.Set("destination", "fallback")
	form.Set("expected_version", "0")
	form.Set("expected_fallback_version", "1")
	form.Set("enabled", "true")
	form.Set("service_id", "PFALLBACK")
	form.Set("signing_secret", strings.Repeat("f", 32))
	if w := post(true); w.Code != 303 {
		t.Fatal("save independent fallback", w.Code, w.Body.String())
	}
	p, err = store.GetMCPReadinessPagerDutyAcknowledgements(t.Context(), owner.ID, app.ID, rule.ID, "primary")
	if err != nil || p.Enabled || p.Version != 2 {
		t.Fatal("fallback form changed primary", p, err)
	}
	p, err = store.GetMCPReadinessPagerDutyAcknowledgements(t.Context(), owner.ID, app.ID, rule.ID, "fallback")
	if err != nil || !p.Enabled || p.Version != 1 || p.ServiceID != "PFALLBACK" {
		t.Fatal("fallback form lost binding", p, err)
	}

}
