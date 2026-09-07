package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	for _, want := range []string{"Edge rules for", "edge.example.com/api", "Security headers preset", `name="csrf_token"`, "Create an edge rule"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q\n%s", want, rec.Body.String())
		}
	}
	if got := rec.Header().Get("Set-Cookie"); !strings.Contains(got, dashboardEdgeRulesCSRFCookie) {
		t.Fatalf("GET edge rules missing %s cookie: %q", dashboardEdgeRulesCSRFCookie, got)
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
