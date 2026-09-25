package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardHandler_AppEdgeRules(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "edge-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	_, err = store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: "edge.example.com", MatchPath: "/api", Priority: 50, Enabled: true,
		Kind:   state.EdgeRuleKindHeaders,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Example", Value: "on", Action: "set"}}}},
	})
	if err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/edge-app/edge-rules", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Edge rules for", "edge.example.com/api", "Security headers preset", `name="csrf_token"`, "Create an edge rule", "Trace a request", `name="trace_host"`, `value="/"`, `name="trace_body"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q\n%s", want, rec.Body.String())
		}
	}
	if got := rec.Header().Get("Set-Cookie"); !strings.Contains(got, dashboardEdgeRulesCSRFCookie) {
		t.Fatalf("GET edge rules missing %s cookie: %q", dashboardEdgeRulesCSRFCookie, got)
	}
}

func TestDashboardEdgeRuleTraceValidatesBodyWithoutEchoingIt(t *testing.T) {
	h, cookie, store, sessions := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "edge-trace-body", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	_, err = store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: "edge.example.com", MatchPath: "/submit", Priority: 10, Enabled: true,
		Kind:         state.EdgeRuleKindValidate,
		ValidateMode: "block",
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindValidate, Validate: &state.EdgeRuleValidateAction{
			Schema:       json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]}`),
			ContentTypes: []string{"application/json"}, MaxBodyBytes: 4096,
		}},
	})
	if err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardEdgeRulesAction, acct.ID, dashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	secretBody := `{"count":"private-value"}`
	rec := dashboardPOST(t, h, cookie, "/dashboard/apps/edge-trace-body/edge-rules/trace", map[string]string{
		middleware.FormFieldName: token,
		"trace_host":             "edge.example.com", "trace_path": "/submit", "trace_method": "POST",
		"trace_headers": "Content-Type: application/json", "trace_body_provided": "on", "trace_body": secretBody,
	}, &http.Cookie{Name: dashboardEdgeRulesCSRFCookie, Value: token})
	if rec.Code != http.StatusOK {
		t.Fatalf("trace status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Simulation: complete · validation_failed", "Status: 422", "Field: <code>/count</code>", "Request body:", "contents withheld"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("trace response missing %q\n%s", want, rec.Body.String())
		}
	}
	if strings.Contains(rec.Body.String(), "private-value") || strings.Contains(rec.Body.String(), secretBody) {
		t.Fatalf("trace response echoed submitted body\n%s", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	rules, err := store.ListEdgeRulesForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("ListEdgeRulesForApp: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("trace changed edge rules: %+v", rules)
	}
}

func TestDashboardEdgeRuleTraceIsReadOnlyAndRequiresNamedCSRF(t *testing.T) {
	h, cookie, store, sessions := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "edge-trace", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	_, err = store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: "edge.example.com", MatchPath: "/old", Priority: 10, Enabled: true,
		Kind:   state.EdgeRuleKindRedirect,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRedirect, Redirect: &state.EdgeRuleRedirectAction{StatusCode: http.StatusPermanentRedirect, To: "/new"}},
	})
	if err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}

	fields := map[string]string{"trace_host": "edge.example.com", "trace_path": "/old", "trace_method": "GET"}
	bad := dashboardPOST(t, h, cookie, "/dashboard/apps/edge-trace/edge-rules/trace", fields)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("missing csrf status = %d, want 400\nbody = %s", bad.Code, bad.Body.String())
	}

	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardEdgeRulesAction, acct.ID, dashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	fields[middleware.FormFieldName] = token
	good := dashboardPOST(t, h, cookie, "/dashboard/apps/edge-trace/edge-rules/trace", fields,
		&http.Cookie{Name: dashboardEdgeRulesCSRFCookie, Value: token})
	if good.Code != http.StatusOK {
		t.Fatalf("valid trace status = %d, want 200\nbody = %s", good.Code, good.Body.String())
	}
	for _, want := range []string{"Simulation: complete", "redirect", "HTTP", "/new", "Rule matches"} {
		if !strings.Contains(good.Body.String(), want) {
			t.Errorf("trace body missing %q\n%s", want, good.Body.String())
		}
	}
	rules, err := store.ListEdgeRulesForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("ListEdgeRulesForApp: %v", err)
	}
	if len(rules) != 1 || rules[0].Action.Redirect.To != "/new" {
		t.Fatalf("trace changed edge rules: %+v", rules)
	}
}

func TestDashboardEdgeRuleTraceSimulatesAppMaintenance(t *testing.T) {
	h, cookie, store, sessions := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{
		AccountID: acct.ID, Slug: "edge-trace-maintenance", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive, MaintenanceMode: true,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	_, err = store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: "edge.example.com", MatchPath: "/", Priority: 10, Enabled: true,
		Kind:   state.EdgeRuleKindMaintenance,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindMaintenance, Maintenance: &state.EdgeRuleMaintenanceAction{Message: "route maintenance"}},
	})
	if err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardEdgeRulesAction, acct.ID, dashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	rec := dashboardPOST(t, h, cookie, "/dashboard/apps/edge-trace-maintenance/edge-rules/trace", map[string]string{
		middleware.FormFieldName: token,
		"trace_host":             "edge.example.com", "trace_path": "/", "trace_method": "GET",
	}, &http.Cookie{Name: dashboardEdgeRulesCSRFCookie, Value: token})
	if rec.Code != http.StatusOK {
		t.Fatalf("trace status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		"Simulation: complete · app_maintenance", "Response status: <strong>503</strong>", api.CodeAppMaintenance,
		"Retry-After 60s", "app-wide maintenance would return HTTP 503",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("trace response missing %q\n%s", want, rec.Body.String())
		}
	}
}

func TestDashboardEdgeRuleTraceSimulatesDeclaredRoutePolicy(t *testing.T) {
	h, cookie, store, sessions := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{
		AccountID: acct.ID, Slug: "edge-trace-declared-route", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	enabled := true
	routes := []state.DeclaredRoute{{Path: "/users/{user_id}", Methods: []string{"GET"}}}
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{
		OnlyAllowDeclaredRoutes: &enabled, SetOnlyAllowDeclaredRoutes: true,
		DeclaredRoutes: &routes, SetDeclaredRoutes: true,
	}); err != nil {
		t.Fatalf("UpdateApp declared-route policy: %v", err)
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardEdgeRulesAction, acct.ID, dashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	rec := dashboardPOST(t, h, cookie, "/dashboard/apps/edge-trace-declared-route/edge-rules/trace", map[string]string{
		middleware.FormFieldName: token,
		"trace_host":             "edge.example.com", "trace_path": "/admin", "trace_method": "GET",
	}, &http.Cookie{Name: dashboardEdgeRulesCSRFCookie, Value: token})
	if rec.Code != http.StatusOK {
		t.Fatalf("trace status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		"Simulation: complete · undeclared_route", "Response status: <strong>404</strong>", api.CodeUndeclaredRoute,
		"GET /admin is not declared for this app", "declared_routes",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("trace response missing %q\n%s", want, rec.Body.String())
		}
	}
}

func TestDashboardEdgeRuleTraceResolvesCORSPreset(t *testing.T) {
	h, cookie, store, sessions := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanPro); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "edge-trace-cors-preset", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	presetID := "trace-preset-1"
	_, err = store.CreateCorsPresetIfUnderQuota(t.Context(), state.CorsPreset{
		ID: presetID, AccountID: acct.ID, Name: "trace-default", AllowOrigins: []string{"https://app.example.com"}, AllowMethods: []string{"GET"},
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateCorsPresetIfUnderQuota: %v", err)
	}
	_, err = store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: "edge.example.com", MatchPath: "/", MatchMethods: []string{"GET"}, Priority: 10, Enabled: true,
		Kind: state.EdgeRuleKindCORSA, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{CorsPresetID: &presetID}},
	})
	if err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardEdgeRulesAction, acct.ID, dashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	rec := dashboardPOST(t, h, cookie, "/dashboard/apps/edge-trace-cors-preset/edge-rules/trace", map[string]string{
		middleware.FormFieldName: token,
		"trace_host":             "edge.example.com", "trace_path": "/", "trace_method": "GET",
		"trace_headers": "Origin: https://app.example.com",
	}, &http.Cookie{Name: dashboardEdgeRulesCSRFCookie, Value: token})
	if rec.Code != http.StatusOK {
		t.Fatalf("trace status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Simulation: complete · continue", "cors_applied", "Access-Control-Allow-Origin: https://app.example.com"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("trace response missing %q\n%s", want, rec.Body.String())
		}
	}
}

func TestDashboardEdgeRuleTraceUsesAppCORSDefault(t *testing.T) {
	h, cookie, store, sessions := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "edge-trace-app-cors", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	enabled := true
	origins := []string{"https://app.example.com"}
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{
		CORSDefaultEnabled: &enabled, SetCORSDefaultEnabled: true,
		CORSDefaultOrigins: &origins, SetCORSDefaultOrigins: true,
	}); err != nil {
		t.Fatalf("UpdateApp default CORS: %v", err)
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardEdgeRulesAction, acct.ID, dashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	rec := dashboardPOST(t, h, cookie, "/dashboard/apps/edge-trace-app-cors/edge-rules/trace", map[string]string{
		middleware.FormFieldName: token,
		"trace_host":             "edge.example.com", "trace_path": "/", "trace_method": "OPTIONS",
		"trace_headers": "Origin: https://app.example.com\nAccess-Control-Request-Method: GET",
	}, &http.Cookie{Name: dashboardEdgeRulesCSRFCookie, Value: token})
	if rec.Code != http.StatusOK {
		t.Fatalf("trace status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Simulation: complete · continue", "app_default_cors", "cors_default_applied", "not short-circuited", "Access-Control-Allow-Origin: https://app.example.com"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("trace response missing %q\n%s", want, rec.Body.String())
		}
	}
}

func TestDashboardEdgeRuleTraceRendersValidationErrorWithoutMutation(t *testing.T) {
	h, cookie, store, sessions := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "edge-trace-invalid", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardEdgeRulesAction, acct.ID, dashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	rec := dashboardPOST(t, h, cookie, "/dashboard/apps/edge-trace-invalid/edge-rules/trace", map[string]string{
		middleware.FormFieldName: token, "trace_host": "example.com:443", "trace_path": "/", "trace_method": "GET",
	}, &http.Cookie{Name: dashboardEdgeRulesCSRFCookie, Value: token})
	if rec.Code != http.StatusOK {
		t.Fatalf("invalid trace status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "host must be a hostname or IP address without a port") {
		t.Fatalf("invalid input message missing\n%s", rec.Body.String())
	}
	rules, err := store.ListEdgeRulesForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("ListEdgeRulesForApp: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("invalid trace created edge rules: %+v", rules)
	}
}

func TestDashboardEdgeRuleCreateRequiresNamedCSRF(t *testing.T) {
	h, cookie, store, sessions := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "edge-create", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	bad := dashboardPOST(t, h, cookie, "/dashboard/apps/edge-create/edge-rules", map[string]string{
		"match_host": "edge.example.com", "match_path": "/", "kind": "route", "action": `{"target_app_slug":"edge-create"}`, "enabled": "on",
	})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("missing csrf status = %d, want 400\nbody = %s", bad.Code, bad.Body.String())
	}

	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardEdgeRulesAction, acct.ID, dashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	good := dashboardPOST(t, h, cookie, "/dashboard/apps/edge-create/edge-rules", map[string]string{
		middleware.FormFieldName: token, "match_host": "edge.example.com", "match_path": "/", "kind": "route", "action": `{"target_app_slug":"edge-create"}`, "enabled": "on",
	}, &http.Cookie{Name: dashboardEdgeRulesCSRFCookie, Value: token})
	if good.Code != http.StatusSeeOther {
		t.Fatalf("valid csrf status = %d, want 303\nbody = %s", good.Code, good.Body.String())
	}
	app, err := store.AppBySlug(t.Context(), "edge-create")
	if err != nil {
		t.Fatalf("AppBySlug: %v", err)
	}
	rules, err := store.ListEdgeRulesForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("ListEdgeRulesForApp: %v", err)
	}
	if len(rules) != 1 || rules[0].Kind != state.EdgeRuleKindRoute {
		t.Fatalf("rules = %+v, want one route rule", rules)
	}
}

func TestDashboardSecurityHeadersCreatesStandardRule(t *testing.T) {
	h, cookie, store, sessions := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "security-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardEdgeRulesAction, acct.ID, dashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	rec := dashboardPOST(t, h, cookie, "/dashboard/apps/security-app/edge-rules/security-headers", map[string]string{middleware.FormFieldName: token}, &http.Cookie{Name: dashboardEdgeRulesCSRFCookie, Value: token})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\nbody = %s", rec.Code, rec.Body.String())
	}
	rules, err := store.ListEdgeRulesForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("ListEdgeRulesForApp: %v", err)
	}
	if len(rules) != 1 || !isSecurityHeadersRule(rules[0]) {
		encoded, _ := json.Marshal(rules)
		t.Fatalf("rules = %s, want standard security headers rule", encoded)
	}
}
