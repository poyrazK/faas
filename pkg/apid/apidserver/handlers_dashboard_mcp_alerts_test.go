package apidserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardMCPAlertFormsAndGuards(t *testing.T) {
	t.Cleanup(withTestRecipient(t))
	handler, cookie, store, _ := newAuthedDashboardServerFull(t)
	owner, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateAccountPlan(t.Context(), owner.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	own, err := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: "alert-own", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(t.Context(), "alert-foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateApp(t.Context(), state.App{AccountID: foreign.ID, Slug: "alert-foreign", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/dashboard/mcp?app="+own.Slug, nil)
	req.AddCookie(cookie)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, req)
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == dashboardMCPAlertCookie {
			csrf = c
		}
	}
	if page.Code != 200 || csrf == nil || !strings.Contains(page.Body.String(), "Add performance alert") {
		t.Fatalf("missing alert form: %d", page.Code)
	}
	form := url.Values{"csrf_token": {csrf.Value}, "name": {"MCP failures"}, "metric": {api.MetricMCPProtocolErrorRate}, "threshold": {"15"}, "window_spec": {"5m"}, "mcp_min_calls": {"40"}, "mcp_for_minutes": {"3"}, "enabled": {"yes"}, "webhook_url": {"https://93.184.216.34/hook"}, "webhook_secret": {"never-echo-mcp-signing-secret"}}
	post := func(path string, withCSRF bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(cookie)
		if withCSRF {
			r.AddCookie(csrf)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}
	path := "/dashboard/mcp/apps/" + own.Slug + "/alerts"
	if rec := post(path, false); rec.Code != 400 {
		t.Fatalf("missing CSRF: %d", rec.Code)
	}
	if rec := post("/dashboard/mcp/apps/alert-foreign/alerts", true); rec.Code != 404 {
		t.Fatalf("foreign app: %d", rec.Code)
	}
	if rec := post(path, true); rec.Code != 303 || strings.Contains(rec.Body.String(), form.Get("webhook_secret")) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	rules, err := store.ListAlertRulesForAccount(t.Context(), owner.ID)
	if err != nil || len(rules) != 1 {
		t.Fatal("rule not created")
	}
	rule := rules[0]
	if rule.MCPMinCalls != 40 || rule.MCPForMinutes != 3 || len(rule.WebhookSecretSealed) == 0 {
		t.Fatal("settings or sealed secret missing")
	}
	form.Set("enabled", "no")
	form.Set("mcp_min_calls", "60")
	if rec := post(path+"/"+rule.ID, true); rec.Code != 303 {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	updated, err := store.AlertRuleByID(t.Context(), rule.ID)
	if err != nil || updated.Enabled || updated.MCPMinCalls != 60 {
		t.Fatal("update did not persist")
	}
	form.Set("mcp_min_calls", "19")
	if rec := post(path+"/"+rule.ID, true); rec.Code != 400 {
		t.Fatalf("invalid traffic: %d", rec.Code)
	}
	form.Set("mcp_min_calls", "60")
	form.Add("threshold", "30")
	if rec := post(path, true); rec.Code != 400 {
		t.Fatal("duplicate field accepted")
	}

	form.Del("threshold")
	form.Set("threshold", "0")
	form.Set("metric", api.MetricMCPReadinessUnhealthy)
	form.Del("mcp_min_calls")
	form.Del("mcp_for_minutes")
	form.Set("enabled", "yes")
	form.Set("name", "Staging readiness")
	form.Set("mcp_readiness_scope", "staging")
	if rec := post(path, false); rec.Code != 400 {
		t.Fatal("readiness CSRF bypass", rec.Code)
	}
	if rec := post(path, true); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "scope=staging") {
		t.Fatal("readiness create", rec.Code, rec.Body.String())
	}
	rules, err = store.ListAlertRulesForAccount(t.Context(), owner.ID)
	if err != nil || len(rules) != 2 {
		t.Fatal("readiness rule missing", err)
	}
	var readiness state.AlertRule
	for _, r := range rules {
		if r.MCPReadinessScope == "staging" {
			readiness = r
		}
	}
	if readiness.ID == "" || len(readiness.WebhookSecretSealed) == 0 {
		t.Fatal("readiness selector or secret missing")
	}
	form.Set("threshold", "1")
	if rec := post(path+"/"+readiness.ID, true); rec.Code != 303 {
		t.Fatal("readiness update", rec.Code, rec.Body.String())
	}
	form.Set("mcp_readiness_scope", "default")
	if rec := post(path+"/"+readiness.ID, true); rec.Code != 400 {
		t.Fatal("readiness environment changed", rec.Code)
	}
	for _, scope := range []string{"default", "staging"} {
		r := httptest.NewRequest("GET", "/dashboard/mcp?app="+own.Slug+"&scope="+scope, nil)
		r.AddCookie(cookie)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		if rec.Code != 200 || strings.Contains(rec.Body.String(), form.Get("webhook_secret")) {
			t.Fatal("readiness dashboard", rec.Code)
		}
		shown := strings.Contains(rec.Body.String(), "Staging readiness")
		if shown != (scope == "staging") {
			t.Fatal("readiness environment filtering", scope, shown)
		}
	}

}
func TestMCPProtocolAlertAPIRoundTrip(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "mcp-api-alert")
	req := alertRuleReq()
	req.Metric = api.MetricMCPProtocolErrorRate
	req.Threshold = 10
	created := mustCreateAlertRule(t, e, "mcp-api-alert", req)
	if created.MCPMinCalls != 20 || created.MCPForMinutes != 10 {
		t.Fatal("defaults missing")
	}
	calls, minutes := 80, 2
	response := e.do(t, http.MethodPatch, "/v1/apps/mcp-api-alert/alerts/"+created.ID, api.UpdateAlertRuleRequest{MCPMinCalls: &calls, MCPForMinutes: &minutes}, nil)
	var updated api.AlertRuleResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &updated) != nil || updated.MCPMinCalls != 80 || updated.MCPForMinutes != 2 {
		t.Fatalf("PATCH: %d %s", response.Code, response.Body.String())
	}
	action := "rollback"
	response = e.do(t, http.MethodPatch, "/v1/apps/mcp-api-alert/alerts/"+created.ID, api.UpdateAlertRuleRequest{Action: &action}, nil)
	if response.Code != 400 {
		t.Fatalf("unsafe action: %d", response.Code)
	}
	req.MCPMinCalls = new(int)
	response = e.do(t, http.MethodPost, "/v1/apps/mcp-api-alert/alerts", req, nil)
	if response.Code != 400 {
		t.Fatalf("explicit zero accepted: %d", response.Code)
	}
}

func TestMCPReadinessAlertScopeAPIRoundTrip(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "scoped-readiness-alert")
	request := alertRuleReq()
	request.Metric = api.MetricMCPReadinessUnhealthy
	request.Threshold = 0
	request.Name = "production-readiness"
	request.MCPReadinessScope = "default"
	prod := mustCreateAlertRule(t, e, "scoped-readiness-alert", request)
	if prod.MCPReadinessScope != "default" {
		t.Fatal("default environment lost", prod)
	}
	request.Name = "staging-readiness"
	request.MCPReadinessScope = "staging"
	stage := mustCreateAlertRule(t, e, "scoped-readiness-alert", request)
	request.Name = "legacy-readiness"
	request.MCPReadinessScope = ""
	legacy := mustCreateAlertRule(t, e, "scoped-readiness-alert", request)
	if stage.MCPReadinessScope != "staging" || legacy.MCPReadinessScope != "" {
		t.Fatal("selector semantics lost", stage, legacy)
	}
	threshold := float64(2)
	rec := e.do(t, http.MethodPatch, "/v1/apps/scoped-readiness-alert/alerts/"+stage.ID, api.UpdateAlertRuleRequest{Threshold: &threshold}, nil)
	var updated api.AlertRuleResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &updated) != nil || updated.MCPReadinessScope != "staging" || updated.Threshold != 2 {
		t.Fatal("patch lost selector", rec.Code, rec.Body.String())
	}
	for _, scope := range []string{"", "default", "preview"} {
		rec = e.do(t, http.MethodPatch, "/v1/apps/scoped-readiness-alert/alerts/"+stage.ID, api.UpdateAlertRuleRequest{MCPReadinessScope: &scope}, nil)
		if rec.Code != 400 {
			t.Fatal("rule environment changed", scope, rec.Code, rec.Body.String())
		}
	}
	scope := "staging"
	rec = e.do(t, http.MethodPatch, "/v1/apps/scoped-readiness-alert/alerts/"+stage.ID, api.UpdateAlertRuleRequest{MCPReadinessScope: &scope}, nil)
	if rec.Code != 200 {
		t.Fatal("identical selector rejected", rec.Code, rec.Body.String())
	}
	request.Name = "invalid-selector"
	request.MCPReadinessScope = "__all__"
	if rec = e.do(t, http.MethodPost, "/v1/apps/scoped-readiness-alert/alerts", request, nil); rec.Code != 400 {
		t.Fatal("invalid selector admitted", rec.Code)
	}
	request.MCPReadinessScope = "staging"
	request.Metric = "request_count"
	if rec = e.do(t, http.MethodPost, "/v1/apps/scoped-readiness-alert/alerts", request, nil); rec.Code != 400 {
		t.Fatal("unrelated metric accepted selector", rec.Code)
	}
}

func TestDashboardMCPReadinessIncidentTimeline(t *testing.T) {
	handler, cookie, store, _ := newAuthedDashboardServerFull(t)
	owner, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: "incident-dashboard", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.CreateAlertRule(t.Context(), state.AlertRule{AccountID: owner.ID, AppID: app.ID, Name: "readiness", Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", CooldownMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	rule.Enabled = true
	if _, err = store.UpdateAlertRule(t.Context(), rule.ID, state.UpdateAlertRuleParams{Enabled: &rule.Enabled}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	for i := 0; i < 21; i++ {
		body, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": strings.Repeat("a", 64), "mcp_readiness_scopes": []string{"staging"}, "mcp_readiness": []api.MCPReadinessMonitorStatus{}})
		if _, err = store.RecordMCPReadinessObservation(t.Context(), state.MCPReadinessObservation{Rule: rule, At: at.Add(time.Duration(i) * time.Microsecond), Breached: i%2 == 0, Coverage: strings.Repeat("a", 64), Payload: body, ObservedValue: 1}); err != nil {
			t.Fatal(err)
		}
	}

	get := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/dashboard/mcp?app="+app.Slug+query, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	// Other sections intentionally include all-environment rule incidents.
	// Check environment filtering and pagination within the timeline itself.
	timeline := func(w *httptest.ResponseRecorder) string {
		t.Helper()
		_, section, ok := strings.Cut(w.Body.String(), `<section id="readiness-incidents">`)
		if !ok {
			t.Fatal("incident timeline section missing")
		}
		section, _, _ = strings.Cut(section, "</section>")
		return section
	}
	page, err := store.ListMCPReadinessIncidents(t.Context(), owner.ID, app.ID, "staging", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	w := get("&scope=staging")
	if w.Code != 200 || !strings.Contains(timeline(w), page.Events[0].IncidentID) || !strings.Contains(timeline(w), "incident_before=") {
		t.Fatal("timeline or next link missing", w.Code)
	}
	w = get("&scope=default")
	if w.Code != 200 || strings.Contains(timeline(w), page.Events[0].IncidentID) {
		t.Fatal("environment leak", w.Code)
	}
	w = get("&scope=staging&incident_before=" + url.QueryEscape(page.NextBefore))
	if w.Code != 200 || strings.Contains(timeline(w), page.Events[0].IncidentID) {
		t.Fatal("pagination", w.Code)
	}
	if w = get("&incident_before=invalid"); w.Code != 400 {
		t.Fatal("invalid cursor", w.Code)
	}
}
