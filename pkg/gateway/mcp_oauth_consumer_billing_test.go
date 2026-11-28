package gateway

// adr: 20261124094745 — an MCP OAuth caller enrolled as a consumer is
// metered, priced and capped like a consumer key's caller: its usage is
// attributed to the consumer and its tool calls carry the plan route. On a
// stage it is neither billed nor capped, like a stage's consumer keys.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPOAuthConsumerIsMeteredAndCapped(t *testing.T) {
	const consumerID = "5ebbf7fa-2ac0-44ee-b6b6-557afb745bf9"
	p := &api.MCPResourcePolicy{Resource: "https://app.example/mcp", Scopes: []string{"mcp:tools"}, ToolScopes: map[string][]string{"greet": {}}}
	rule := &EdgeRuleJWTResolved{Issuer: "https://issuer.example", MCP: p}
	handler := func(store *fakeConsumerPlanStore) *Handler {
		return (&Handler{apiMonetization: monetizationOn, jwtVerifier: &countingJWTVerifier{onVerify: func(context.Context, string, *EdgeRuleJWTResolved) (*JWTClaims, error) {
			return &JWTClaims{Subject: "u1", Exp: time.Now().Add(time.Minute), Custom: map[string]string{"scope": "mcp:tools"}}, nil
		}}}).WithOAuthConsumers(&fakeOAuthEnroller{id: consumerID, active: true}).WithConsumerPlanStore(store)
	}
	call := func(h *Handler, app App, body string) (*httptest.ResponseRecorder, *http.Request) {
		r := httptest.NewRequest("POST", "https://app.example/mcp", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer jwt")
		w := httptest.NewRecorder()
		if h.applyMCPResourcePolicy(w, r, app, rule) {
			t.Fatalf("refused: %d %s", w.Code, w.Body)
		}
		return w, r
	}
	const greet = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"greet"}}`
	production := App{ID: "app-1", AccountID: "acct-1"}

	for name, tc := range map[string]struct {
		body, route string
	}{
		"tool call": {greet, api.MCPBillingToolsPrefix + "/greet"},
		// Protocol overhead carries the path label, as a consumer key's does.
		"tools/list": {`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "POST /mcp"},
		"resource":   {`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"greeting://welcome"}}`, api.MCPBillingResourcesLabel},
	} {
		_, r := call(handler(&fakeConsumerPlanStore{}), production, tc.body)
		if got := planRouteFrom(r); got != tc.route {
			t.Errorf("%s: plan route = %q, want %q", name, got, tc.route)
		}
		if got := requestUsageAttribution(r); got.ConsumerID != consumerID || !got.financial() {
			t.Errorf("%s: usage attribution = %+v, want consumer %s", name, got, consumerID)
		}
	}

	// The plan's tool limit refuses the call once it is used up.
	capped := &fakeConsumerPlanStore{
		policy:   ConsumerPlanPolicy{RouteLimits: map[string]api.APIConsumerPlanRouteLimit{api.MCPBillingToolsPrefix + "/greet": {MaxRequestsPerMonth: 3}}},
		decision: ConsumerPlanDecision{Scope: "route_month", Limit: 3, Observed: 3, RetryAfterSeconds: 3600},
	}
	h := handler(capped)
	_, r := call(h, production, greet)
	if w := httptest.NewRecorder(); h.enforceConsumerPlan(w, r, &statusRecorder{}, production, false) {
		t.Fatal("a used-up tool limit admitted the OAuth consumer")
	}
	if len(capped.admitRoutes) != 1 || capped.admitRoutes[0] != api.MCPBillingToolsPrefix+"/greet" {
		t.Fatalf("admission routes = %v", capped.admitRoutes)
	}

	// Through a stage's URL the caller is neither billed nor capped;
	// production's environment URL bills as usual.
	for name, tc := range map[string]struct {
		app    App
		billed bool
	}{
		"stage":          {App{ID: "app-1", AccountID: "acct-1", PublicEnvironmentID: "env-1", PinnedDeploymentScope: "staging"}, false},
		"production URL": {App{ID: "app-1", AccountID: "acct-1", PublicEnvironmentID: "env-0", PinnedDeploymentScope: "production"}, true},
	} {
		store := &fakeConsumerPlanStore{policy: capped.policy, decision: capped.decision}
		h := handler(store)
		_, r := call(h, tc.app, greet)
		admitted := h.enforceConsumerPlan(httptest.NewRecorder(), r, &statusRecorder{}, tc.app, false)
		if billed := requestUsageAttribution(r).financial(); billed != tc.billed || admitted == tc.billed {
			t.Errorf("%s: billed=%v admitted=%v, want billed=%v", name, billed, admitted, tc.billed)
		}
	}
}

// A closed account or a refused enrollment is not about the token's scopes:
// the 403 carries no WWW-Authenticate challenge, so clients do not sign in
// again to retry it. A missing endpoint scope still challenges.
func TestMCPOAuthConsumerRefusalHasNoChallenge(t *testing.T) {
	p := &api.MCPResourcePolicy{Resource: "https://app.example/mcp", Scopes: []string{"mcp:tools"}, ToolScopes: map[string][]string{"greet": {}}}
	rule := &EdgeRuleJWTResolved{Issuer: "https://issuer.example", MCP: p}
	app := App{ID: "app-1", AccountID: "acct-1"}
	for name, tc := range map[string]struct {
		scope     string
		enroller  *fakeOAuthEnroller
		challenge string
	}{
		"closed account": {"mcp:tools", &fakeOAuthEnroller{id: "consumer-1"}, ""},
		"missing scope":  {"openid", &fakeOAuthEnroller{id: "consumer-1", active: true}, `error="insufficient_scope"`},
	} {
		h := (&Handler{apiMonetization: monetizationOn, jwtVerifier: &countingJWTVerifier{onVerify: func(context.Context, string, *EdgeRuleJWTResolved) (*JWTClaims, error) {
			return &JWTClaims{Subject: "u1", Exp: time.Now().Add(time.Minute), Custom: map[string]string{"scope": tc.scope}}, nil
		}}}).WithOAuthConsumers(tc.enroller)
		r := httptest.NewRequest("POST", "https://app.example/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		r.Header.Set("Authorization", "Bearer jwt")
		w := httptest.NewRecorder()
		if !h.applyMCPResourcePolicy(w, r, app, rule) || w.Code != http.StatusForbidden {
			t.Fatalf("%s: status %d %s", name, w.Code, w.Body)
		}
		got := w.Header().Get("Www-Authenticate")
		if (tc.challenge == "" && got != "") || !strings.Contains(got, tc.challenge) {
			t.Errorf("%s: WWW-Authenticate = %q, want %q", name, got, tc.challenge)
		}
	}
}
