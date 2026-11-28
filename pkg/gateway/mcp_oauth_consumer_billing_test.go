package gateway

// adr: 20261124094745 — an MCP OAuth caller enrolled as a consumer is
// metered, priced and capped like a consumer key's caller: its usage is
// attributed to the consumer and its tool calls carry the plan route.

import (
	"context"
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
	app := App{ID: "app-1", AccountID: "acct-1"}
	h := (&Handler{apiMonetization: monetizationOn, jwtVerifier: &countingJWTVerifier{onVerify: func(context.Context, string, *EdgeRuleJWTResolved) (*JWTClaims, error) {
		return &JWTClaims{Subject: "u1", Exp: time.Now().Add(time.Minute), Custom: map[string]string{"scope": "mcp:tools"}}, nil
	}}}).WithOAuthConsumers(&fakeOAuthEnroller{id: consumerID, active: true})

	for name, tc := range map[string]struct {
		body, route string
	}{
		"tool call": {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"greet"}}`, api.MCPBillingToolsPrefix + "/greet"},
		"resource":  {`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"greeting://welcome"}}`, api.MCPBillingResourcesLabel},
	} {
		r := httptest.NewRequest("POST", "https://app.example/mcp", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer jwt")
		w := httptest.NewRecorder()
		if h.applyMCPResourcePolicy(w, r, app, rule) {
			t.Fatalf("%s: refused: %d %s", name, w.Code, w.Body)
		}
		if got := planRouteFrom(r); got != tc.route {
			t.Errorf("%s: plan route = %q, want %q", name, got, tc.route)
		}
		if got := requestUsageAttribution(r); got.ConsumerID != consumerID || !got.financial() {
			t.Errorf("%s: usage attribution = %+v, want consumer %s", name, got, consumerID)
		}
	}
}
