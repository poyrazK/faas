package edgerules

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261122194324 — consumer-key MCP policies and tool limits need
// mcp_gateway_auth; an ordinary MCP JWT policy does not.
func TestMCPGatewayAuthRelease(t *testing.T) {
	action := func(policy string) json.RawMessage {
		return json.RawMessage(`{"issuer":"https://api.example/v1/mcp/consumer-key-issuer","audience":["https://app.example/mcp"],` +
			`"jwks_url":"https://api.example/v1/mcp/consumer-key-jwks.json","algorithms":["ES256"],"mcp":` + policy + `}`)
	}
	jwtPolicy := action(`{"resource":"https://app.example/mcp","scopes":["tools"]}`)
	keyPolicy := action(`{"resource":"https://app.example/mcp","scopes":["tools"],"credential":"consumer_key"}`)
	limited := action(`{"resource":"https://app.example/mcp","scopes":["tools"],"tool_limits":{"search":{"requests":5,"window_seconds":60}}}`)
	off := &Service{Host: releaseHost{}}
	on := &Service{Host: releaseHost{released: map[string]bool{platformflags.MCPGatewayAuth: true}}}
	disabled := false
	for _, tc := range []struct {
		name    string
		action  json.RawMessage
		refused bool
	}{
		{"JWT MCP policy", jwtPolicy, false},
		{"consumer-key MCP policy", keyPolicy, true},
		{"tool limits", limited, true},
	} {
		req := api.CreateEdgeRuleRequest{Kind: string(state.EdgeRuleKindJWT), Action: tc.action, Enabled: &disabled}
		if got := off.createReleaseProblem(state.Account{}, req) != nil; got != tc.refused {
			t.Errorf("%s: refused=%v want %v", tc.name, got, tc.refused)
		}
		if on.createReleaseProblem(state.Account{}, req) != nil {
			t.Errorf("%s: refused with the flag on", tc.name)
		}
	}
	// Turning a stored JWT policy into a consumer-key one is a new feature;
	// editing a stored consumer-key policy after the flag is withdrawn is not.
	var stored state.EdgeRuleAction
	stored.Kind = state.EdgeRuleKindJWT
	stored.JWT = &state.EdgeRuleJWTAction{MCP: &api.MCPResourcePolicy{Resource: "https://app.example/mcp"}}
	row := state.EdgeRule{Kind: state.EdgeRuleKindJWT, Enabled: true, Action: stored}
	if off.updateReleaseProblem(state.Account{}, row, api.UpdateEdgeRuleRequest{Action: &keyPolicy}) == nil {
		t.Error("switching a JWT policy to consumer keys without the flag")
	}
	row.Action.JWT.MCP.Credential = api.MCPCredentialConsumerKey
	if off.updateReleaseProblem(state.Account{}, row, api.UpdateEdgeRuleRequest{Action: &keyPolicy}) != nil {
		t.Error("an enforced consumer-key policy must stay editable")
	}
	if got := off.RuleResponse(state.Account{}, row).UnreleasedFeatures; len(got) != 1 || got[0] != api.EdgeFeatureMCPGatewayAuth {
		t.Errorf("UnreleasedFeatures = %v", got)
	}
}
