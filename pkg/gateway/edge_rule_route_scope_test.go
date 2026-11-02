package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 091 — amendment 2026-10-31 (round 6): a company app keeps its
// creator's account, so the same-account check let a route rule on the
// creator's personal host serve a company app there (and a company host
// route to the creator's personal app). A route target must share the host
// app's edge-rule scope.
func TestRouteRuleTargetMustShareTheHostAppsScope(t *testing.T) {
	for _, tc := range []struct {
		name        string
		hostScope   string
		targetScope string
		want        bool
	}{
		{"personal to personal", "", "", true},
		{"company to company", "org-acme", "org-acme", true},
		{"personal to company", "", "org-acme", false},
		{"company to personal", "org-acme", "", false},
		{"one company to another", "org-acme", "org-other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			b.app.EdgeRuleScope = tc.hostScope
			target := App{ID: "app-target", AccountID: "acct-1", Slug: "target", Plan: api.PlanPro, EdgeRuleScope: tc.targetScope}
			h.WithEdgeRules(routeRuleMatcher{route: &EdgeRuleResolved{ID: "rule-route", AccountID: "acct-1", TargetAppSlug: "target"}},
				func(context.Context, string) (App, bool) { return target, true }, &captureAuditor{})
			req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
			app := App{}
			if got := h.matchAndSubstituteRoute(req, "jane-api.apps.dom", &app); got != tc.want {
				t.Fatalf("substituted = %v, want %v", got, tc.want)
			}
			if tc.want && app.ID != "app-target" {
				t.Fatalf("substituted app = %q, want app-target", app.ID)
			}
		})
	}
}
