package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRouteAdvicePolicyLegacyCoverage(t *testing.T) {
	out := api.RouteAdviceResponse{Suggestions: []api.RouteAdviceSuggestion{{Rules: []api.CreateEdgeRuleRequest{{MatchHost: "private-rule-body"}}}}}
	if err := prepareRouteAdvicePolicy(&out); err != nil {
		t.Fatal(err)
	}
	if out.PolicyContext.Status != "unavailable" || out.Suggestions[0].Policy.Status != "unavailable" || len(out.Suggestions[0].Rules) != 0 {
		t.Fatalf("missing policy evidence became an assertion: %+v", out)
	}
}

func TestRouteAdvicePolicyRejectsUnbackedClaims(t *testing.T) {
	for _, tc := range []struct {
		name, revision, id string
		count              int
	}{
		{name: "missing_revision", id: "one"},
		{name: "unknown_rule", revision: strings.Repeat("a", 64), id: "foreign"},
		{name: "match_limit", revision: strings.Repeat("a", 64), id: "one", count: api.RouteAdvicePolicyMatchLimit + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := tc.count
			if count == 0 {
				count = 1
			}
			ids := make([]string, count)
			for i := range ids {
				ids[i] = tc.id
			}
			out := api.RouteAdviceResponse{PolicyContext: &api.RouteAdvicePolicyContext{Status: "observed", Revision: tc.revision, Rules: []api.RouteAdvicePolicyRule{{ID: "one"}}}, Suggestions: []api.RouteAdviceSuggestion{{Policy: &api.RouteAdvicePolicyAssessment{Status: "duplicate", RuleIDs: ids}}}}
			if err := prepareRouteAdvicePolicy(&out); err == nil {
				t.Fatal("accepted an unbacked policy claim")
			}
		})
	}
}

func TestRouteAdvicePolicyOverMCP(t *testing.T) {
	for _, status := range []string{"observed", "unavailable"} {
		t.Run(status, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("policy inspection attempted a write")
					w.WriteHeader(405)
					return
				}
				if r.URL.Path == "/v1/apps/alpha" {
					_, _ = w.Write([]byte(`{"id":"app-one","slug":"alpha"}`))
					return
				}
				if r.URL.Path != "/v1/apps/alpha/routes/advice" || r.URL.Query().Get("scope") != "default" {
					t.Errorf("unexpected policy request: %s", r.URL)
					w.WriteHeader(404)
					return
				}
				policy := &api.RouteAdvicePolicyContext{Status: status, ObservedAt: "2026-10-15T00:00:00Z", Rules: []api.RouteAdvicePolicyRule{}, Gaps: []string{}}
				if status == "observed" {
					policy.Revision = strings.Repeat("a", 64)
					policy.Rules = []api.RouteAdvicePolicyRule{{ID: "one", Kind: "cache", Mode: "enforce", Source: "host", Path: "/catalog", UpdatedAt: "2026-10-15T00:00:00Z", Methods: []string{"GET"}}}
				}
				_ = json.NewEncoder(w).Encode(api.RouteAdviceResponse{Slug: "alpha", RouteDiagnosticsIdentity: api.RouteDiagnosticsIdentity{AppID: "app-one", Scope: "default", Coverage: "retained_deployment_history", Partial: true}, CacheabilityCoverage: "deployment_isolated_observations_only", PolicyContext: policy, Suggestions: []api.RouteAdviceSuggestion{{Kind: "cache", Method: "GET", Route: "/catalog", Policy: &api.RouteAdvicePolicyAssessment{Status: "duplicate", Reason: "matching_rule_has_same_settings", RuleIDs: []string{"one"}}, Rules: []api.CreateEdgeRuleRequest{{MatchHost: "private-rule-body"}}}}})
			}))
			defer backend.Close()
			cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, Environments: &EnvironmentConsent{}, AllowedApps: map[string]string{"alpha": "app-one"}}, "2026-07-28")
			out := callTool(t, ctx, cs, "gregale_get_route_advice", map[string]any{"app": "alpha"}, false)
			var report api.RouteAdviceResponse
			if err := json.Unmarshal(out["report"], &report); err != nil {
				t.Fatal(err)
			}
			want := "duplicate"
			if status != "observed" {
				want = "unavailable"
			}
			if report.PolicyContext.Status != status || report.Suggestions[0].Policy.Status != want || len(report.Suggestions[0].Rules) != 0 || strings.Contains(string(out["report"]), "private-rule-body") {
				t.Fatalf("policy wire response: %s", out["report"])
			}
		})
	}
}
