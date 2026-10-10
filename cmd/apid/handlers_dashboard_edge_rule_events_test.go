package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-960/964 on the dashboard: rules show their name, mode and 24 h hit
// counts; the security-events table renders sampled events and honors its
// filters; "Start enforcing" switches a log-mode rule to enforce.
func TestDashboardEdgeRuleEventsAndModeSwitch(t *testing.T) {
	h, cookie, store, sessions := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "edge-events", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: "edge.example.com", MatchPath: "/login", Priority: 10, Enabled: true,
		Name: "shadow-login-block", Mode: state.EdgeRuleModeLog, Kind: state.EdgeRuleKindHeaders,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-A", Value: "1", Action: "set"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.RecordEdgeRuleHits(ctx, []state.EdgeRuleHit{{RuleID: rule.ID, AppID: app.ID, Bucket: time.Now(), Outcome: state.EdgeRuleHitLogged, Hits: 17}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordEdgeRuleEvents(ctx, []state.EdgeRuleEvent{{
		RuleID: rule.ID, AppID: app.ID, OccurredAt: time.Now(), Outcome: state.EdgeRuleHitLogged,
		Method: "POST", Host: "edge.example.com", Path: "/login", ClientIP: "203.0.113.9", Country: "DE", UserAgent: "curl/8", RequestID: "req-42",
	}}); err != nil {
		t.Fatal(err)
	}

	get := func(path string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d\n%s", path, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	body := get("/dashboard/apps/edge-events/edge-rules")
	for _, want := range []string{"shadow-login-block", "log mode", "17 logged", "Start enforcing", "Security events",
		"POST edge.example.com/login", "203.0.113.9", "curl/8", "req-42"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if body := get("/dashboard/apps/edge-events/edge-rules?outcome=matched"); !strings.Contains(body, "No security events in this window.") {
		t.Error("outcome filter did not apply")
	}
	if body := get("/dashboard/apps/edge-events/edge-rules?since=forever"); !strings.Contains(body, "since must be a positive duration") {
		t.Error("invalid filter must render inline, not fail the page")
	}

	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardEdgeRulesAction, acct.ID, dashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatal(err)
	}
	rec := dashboardPOST(t, h, cookie, "/dashboard/apps/edge-events/edge-rules/"+rule.ID+"/mode", map[string]string{
		middleware.FormFieldName: token, "mode": state.EdgeRuleModeEnforce,
	}, &http.Cookie{Name: dashboardEdgeRulesCSRFCookie, Value: token})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "action=mode_enforce") {
		t.Fatalf("mode switch = %d %q\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	updated, _ := store.GetEdgeRuleByID(ctx, rule.ID)
	if updated.Mode != state.EdgeRuleModeEnforce {
		t.Fatalf("mode = %q, want enforce", updated.Mode)
	}
	rec = dashboardPOST(t, h, cookie, "/dashboard/apps/edge-events/edge-rules/"+rule.ID+"/mode", map[string]string{
		"mode": state.EdgeRuleModeLog,
	})
	if rec.Code == http.StatusSeeOther {
		t.Fatal("mode switch without CSRF token must be refused")
	}
}
