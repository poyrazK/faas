// adr: 375
package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardEdgeRuleTraceRetainsIngressDeadlineBeforeFixedResponse(t *testing.T) {
	h, cookie, store, sessions := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "deadline-trace", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []state.CreateEdgeRuleParams{
		{Kind: state.EdgeRuleKindBudget, MatchPath: "/public", Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindBudget, Budget: &state.EdgeRuleBudgetAction{BudgetMs: 1200, TotalDeadlineMs: 4000}}},
		{Kind: state.EdgeRuleKindRewrite, MatchPath: "*", Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRewrite, Rewrite: &state.EdgeRuleRewriteAction{From: "/public", To: "/backend"}}},
		{Kind: state.EdgeRuleKindRespond, MatchPath: "/backend", Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRespond, Respond: &state.EdgeRuleRespondAction{StatusCode: 200, Body: json.RawMessage(`{"ok":true}`)}}},
	} {
		rule.AccountID, rule.AppID, rule.MatchHost, rule.Enabled = acct.ID, app.ID, "edge.example.com", true
		if _, err := store.CreateEdgeRule(t.Context(), rule); err != nil {
			t.Fatal(err)
		}
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardEdgeRulesAction, acct.ID, dashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatal(err)
	}
	rec := dashboardPOST(t, h, cookie, "/dashboard/apps/deadline-trace/edge-rules/trace", map[string]string{
		middleware.FormFieldName: token, "trace_host": "edge.example.com", "trace_path": "/public", "trace_method": http.MethodGet,
	}, &http.Cookie{Name: dashboardEdgeRulesCSRFCookie, Value: token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Simulation: incomplete · fixed_response", "Ingress total deadline: 4000 ms", "original path <code>/public</code>", "enforcement <code>unverified</code>", "Execution overrides cannot increase it"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("dashboard omitted %q: %s", want, rec.Body.String())
		}
	}
}
