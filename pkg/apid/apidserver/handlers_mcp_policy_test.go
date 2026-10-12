package apidserver_test

// adr: 644 — verified promotion and gateway resource-policy acceptance.

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/edgerules"
)

func TestMCPGatewayPolicySelectorsAndPersistence(t *testing.T) {
	action := api.EdgeRuleJWTAction{Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks", Algorithms: []string{"RS256"}, Audience: []string{"https://app.example/mcp"}, MCP: &api.MCPResourcePolicy{Resource: "https://app.example/mcp", ToolScopes: map[string][]string{}}}
	body, _ := json.Marshal(action)
	for _, tc := range []struct {
		host, path string
		methods    []string
		headers    map[string]string
		valid      bool
	}{
		{"app.example", "/**", nil, nil, true},
		{"other.example", "/**", nil, nil, false},
		{"app.example", "/mcp", nil, nil, false},
		{"app.example", "/**", []string{"POST"}, nil, false},
		{"app.example", "/**", nil, map[string]string{"x-bypass": "no"}, false},
	} {
		prob := edgerules.ValidateMCPRuleSelectors("jwt", tc.host, tc.path, tc.methods, tc.headers, body)
		if (prob == nil) != tc.valid {
			t.Fatalf("%+v problem=%v", tc, prob)
		}
	}
	stored := edgerules.ActionFromBody("jwt", body)
	if stored.JWT == nil || stored.JWT.MCP == nil || stored.JWT.MCP.ToolScopes == nil {
		t.Fatal("MCP policy was discarded at the state boundary")
	}
}
