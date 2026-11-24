package gateway

// adr: 20261123192254 — MCP billing labels and free protocol overhead.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPBillingLabel(t *testing.T) {
	p := &api.MCPResourcePolicy{
		ToolScopes:   map[string][]string{"search": {}, "bad name": {}},
		ToolLimits:   map[string]api.MCPToolLimit{"generate": {Requests: 1, WindowSeconds: 1}},
		PromptScopes: map[string][]string{"summary": {}},
	}
	for _, tc := range []struct {
		call mcpCall
		want string
	}{
		{mcpCall{method: "tools/call", name: "search"}, "MCP /tools/search"},
		{mcpCall{method: "tools/call", name: "generate"}, "MCP /tools/generate"},
		{mcpCall{method: "tools/call", name: "unlisted"}, "MCP /tools"},
		{mcpCall{method: "tools/call", name: "bad name"}, "MCP /tools"},
		{mcpCall{method: "prompts/get", name: "summary"}, "MCP /prompts/summary"},
		{mcpCall{method: "prompts/get", name: "other"}, "MCP /prompts"},
		{mcpCall{method: "resources/read"}, "MCP /resources"},
		{mcpCall{method: "tools/list"}, ""},
		{mcpCall{method: "initialize"}, ""},
		{mcpCall{method: "notifications/initialized"}, ""},
		{mcpCall{method: "completion/complete"}, ""},
		{mcpCall{}, ""},
	} {
		if got := mcpBillingLabel(p, tc.call); got != tc.want {
			t.Errorf("%+v: got %q want %q", tc.call, got, tc.want)
		}
	}
	for _, label := range []string{"MCP /tools/search", "MCP /tools", "MCP /prompts/summary", "MCP /resources"} {
		if !validBillingLabel(label) {
			t.Errorf("%q must be a storable billing route", label)
		}
	}
}

// validBillingLabel mirrors state.ValidBillingRoute, which the gateway does
// not import: "METHOD /path" without control or query characters.
func validBillingLabel(label string) bool {
	return observedRouteLabel("MCP", label[len("MCP "):]) == label
}

func TestMCPConsumerPolicyLabelsBillableCallsAndFreesOverhead(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1"}
	rule := consumerKeyMCPRule()
	for _, tc := range []struct {
		name, method, body, route string
		overhead                  bool
	}{
		{"listed tool", "POST", `{"method":"tools/call","params":{"name":"search"}}`, "MCP /tools/search", false},
		{"tools/list", "POST", `{"method":"tools/list"}`, "POST /mcp", true},
		{"initialize", "POST", `{"method":"initialize","params":{}}`, "POST /mcp", true},
		{"stream", "GET", ``, "POST /mcp", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{routeConsumerLimiter: NewLimiterWithLRU(EdgeRuleConsumerCacheCap)}
			r := mcpConsumerRequest(tc.method, "/mcp", tc.body, "mcp:tools")
			r = withBillingRoute(r, "POST /mcp")
			if h.applyMCPResourcePolicy(httptest.NewRecorder(), r, app, rule) {
				t.Fatal("request refused")
			}
			if got := billingRouteFrom(r); got != tc.route {
				t.Fatalf("billing route = %q want %q", got, tc.route)
			}
			if mcpProtocolOverheadFrom(r) != tc.overhead || usageUnbillable(r, http.StatusOK) != tc.overhead {
				t.Fatalf("overhead=%v unbillable=%v want %v", mcpProtocolOverheadFrom(r), usageUnbillable(r, http.StatusOK), tc.overhead)
			}
			policy := ConsumerPlanPolicy{RouteWeights: map[string]int64{"MCP /tools/search": 25}}
			want := int64(1)
			if tc.overhead {
				want = 0
			} else if tc.route == "MCP /tools/search" {
				want = 25
			}
			if got := policy.units(r); got != want {
				t.Fatalf("plan units = %d want %d", got, want)
			}
		})
	}
}

func TestMCPBillingRelabelsJWTTenantCandidate(t *testing.T) {
	h := &Handler{}
	r := withBillingRouteCandidate(httptest.NewRequest("POST", "https://app.example/mcp", nil), "POST /mcp")
	h.applyMCPBilling(r, App{ID: "app-1"}, consumerKeyMCPRule().MCP, mcpCall{method: "tools/call", name: "search"})
	if got, _ := r.Context().Value(billingRouteCandidateKey{}).(string); got != "MCP /tools/search" {
		t.Fatalf("candidate = %q", got)
	}
	if billingRouteFrom(r) != "" {
		t.Fatal("an unattributed request must not gain a billing route")
	}
	anonymous := httptest.NewRequest("POST", "https://app.example/mcp", nil).WithContext(context.Background())
	h.applyMCPBilling(anonymous, App{ID: "app-1"}, consumerKeyMCPRule().MCP, mcpCall{method: "tools/call", name: "search"})
	if billingRouteFrom(anonymous) != "" || anonymous.Context().Value(billingRouteCandidateKey{}) != nil {
		t.Fatal("anonymous traffic carries no billing label")
	}
}

func TestEnforceConsumerPlanRouteLimitDenial(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1"}
	store := &fakeConsumerPlanStore{
		policy:   ConsumerPlanPolicy{RouteLimits: map[string]api.APIConsumerPlanRouteLimit{"MCP /tools/generate": {MaxRequestsPerMonth: 100}}},
		decision: ConsumerPlanDecision{Scope: "route_month", Limit: 100, Observed: 100, RetryAfterSeconds: 3600},
	}
	h := (&Handler{apiMonetization: monetizationOn}).WithConsumerPlanStore(store)
	w, r := httptest.NewRecorder(), planRequest("MCP /tools/generate")
	if h.enforceConsumerPlan(w, r, &statusRecorder{}, app, false) {
		t.Fatal("capped tool admitted")
	}
	if len(store.admitRoutes) != 1 || store.admitRoutes[0] != "MCP /tools/generate" {
		t.Fatalf("admission routes = %v", store.admitRoutes)
	}
	body := w.Body.String()
	if w.Code != http.StatusTooManyRequests || w.Header().Get("X-Faas-Rate-Limit-Scope") != "consumer-plan-route-month" ||
		!strings.Contains(body, "100 requests per month on MCP /tools/generate") || !strings.Contains(body, `"limit":100`) ||
		!strings.Contains(body, "route-limits") {
		t.Fatalf("code=%d scope=%q body=%s", w.Code, w.Header().Get("X-Faas-Rate-Limit-Scope"), body)
	}
}

// A full billing label set folds new labels into the overflow label, which
// carries no route; plan caps must still see the tool, listed or not.
func TestMCPPlanRouteSurvivesFullBillingLabelSet(t *testing.T) {
	h := &Handler{}
	set := h.billingRouteSetFor("app-1")
	for i := range 200 {
		set.admit(fmt.Sprintf("GET /junk/%d", i))
	}
	for _, tc := range []struct{ tool, plan string }{
		{"search", "MCP /tools/search"},     // listed in the policy
		{"generate", "MCP /tools/generate"}, // not listed: still capped
		{"bad/name", "MCP /tools"},
	} {
		r := withBillingRoute(planRequest("POST /mcp"), "POST /mcp")
		h.applyMCPBilling(r, App{ID: "app-1"}, consumerKeyMCPRule().MCP, mcpCall{method: "tools/call", name: tc.tool})
		if got := planRouteFrom(r); got != tc.plan {
			t.Fatalf("%s: plan route = %q want %q (billing %q)", tc.tool, got, tc.plan, billingRouteFrom(r))
		}
	}
}

// A plan that caps only some routes admits other routes without a counter.
func TestEnforceConsumerPlanSkipsUncappedRoutes(t *testing.T) {
	store := &fakeConsumerPlanStore{policy: ConsumerPlanPolicy{RouteLimits: map[string]api.APIConsumerPlanRouteLimit{"MCP /tools/generate": {MaxRequestsPerMonth: 100}}},
		decision: ConsumerPlanDecision{Allowed: true}}
	h := (&Handler{apiMonetization: monetizationOn}).WithConsumerPlanStore(store)
	if !h.enforceConsumerPlan(httptest.NewRecorder(), planRequest("MCP /tools/search"), &statusRecorder{}, App{ID: "app-1", AccountID: "acct-1"}, false) {
		t.Fatal("uncapped route refused")
	}
	if len(store.admitRoutes) != 0 {
		t.Fatalf("uncapped route took a counter: %v", store.admitRoutes)
	}
}
