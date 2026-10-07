package api

// adr: 644 — verified promotion and gateway resource-policy acceptance.

import (
	"encoding/json"
	"testing"
)

func TestMCPPolicyValidationAndEmptyAllowlists(t *testing.T) {
	for _, resource := range []string{"http://app.example/mcp", "https://user:pass@app.example/mcp", "https://app.example/mcp?token=x", "https://app.example/mcp#x"} {
		if (&MCPResourcePolicy{Resource: resource}).Validate() == nil {
			t.Errorf("accepted %s", resource)
		}
	}
	p := &MCPResourcePolicy{Resource: "https://app.example/mcp", ToolScopes: map[string][]string{}}
	encoded, _ := json.Marshal(p)
	var roundtrip MCPResourcePolicy
	if err := json.Unmarshal(encoded, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if MCPEntryAllowed(roundtrip.ToolScopes, "hidden", nil) {
		t.Fatal("empty execution allowlist became unrestricted")
	}
	if (&MCPResourcePolicy{Resource: p.Resource, ResourceScopes: map[string][]string{"x://items/{+path}": {}}}).Validate() == nil {
		t.Fatal("unsupported template accepted")
	}
	a := &EdgeRuleJWTAction{Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks", Algorithms: []string{"RS256"}, Audience: []string{"wrong"}, MCP: p}
	if a.Validate() == nil {
		t.Fatal("wrong resource audience accepted")
	}
}
