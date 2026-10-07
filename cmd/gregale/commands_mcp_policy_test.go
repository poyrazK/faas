package main

// adr: 639 — verified promotion and gateway resource-policy acceptance.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPPolicyApply(t *testing.T) {
	for _, mode := range []string{"create", "update", "competing-jwt", "wrong-resource", "older-api"} {
		t.Run(mode, func(t *testing.T) {
			resource := "https://my-mcp.example/mcp"
			policy := api.EdgeRuleJWTAction{MCP: &api.MCPResourcePolicy{Resource: resource}}
			existingBody, _ := json.Marshal(policy)
			writes := 0
			var storedAction json.RawMessage
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer operator-key" {
					t.Fatal("missing operator credential")
				}
				if r.Method == "GET" {
					if r.URL.Path == "/v1/apps/my-mcp" {
						_ = json.NewEncoder(w).Encode(api.AppResponse{URL: "https://my-mcp.example"})
						return
					}
					rules := []api.EdgeRuleResponse{}
					if mode == "update" {
						rules = append(rules, api.EdgeRuleResponse{ID: "policy", Kind: "jwt", Enabled: true, MatchHost: "my-mcp.example", MatchPath: "/**", Action: existingBody})
					}
					if mode == "competing-jwt" {
						rules = append(rules, api.EdgeRuleResponse{ID: "generic", Kind: "jwt", Enabled: true, MatchHost: "my-mcp.example", MatchPath: "/**", Action: json.RawMessage(`{}`)})
					}
					_ = json.NewEncoder(w).Encode(rules)
					return
				}
				writes++
				var body struct {
					Action    json.RawMessage `json:"action"`
					MatchPath string          `json:"match_path"`
					Enabled   *bool           `json:"enabled"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.Action != nil {
					var action api.EdgeRuleJWTAction
					_ = json.Unmarshal(body.Action, &action)
					if action.MCP == nil || action.MCP.Resource != resource || action.MCP.ToolScopes == nil || len(action.MCP.ToolScopes) != 0 {
						t.Errorf("policy semantics changed: %+v", action)
					}
					storedAction = body.Action
				}
				if mode == "create" || mode == "older-api" {
					if writes == 1 && (r.Method != "POST" || body.MatchPath != "/**" || body.Enabled == nil || *body.Enabled) {
						t.Fatal("policy must be staged disabled")
					}
					if writes == 2 && (r.Method != "PATCH" || body.Enabled == nil || !*body.Enabled) {
						t.Fatal("policy must activate after verification")
					}
				}
				if mode == "update" && (r.Method != "PATCH" || r.URL.Path != "/v1/edge-rules/policy") {
					t.Fatal("invalid policy update")
				}
				echoed := storedAction
				if mode == "older-api" {
					echoed = json.RawMessage(`{}`)
				}
				_ = json.NewEncoder(w).Encode(api.EdgeRuleResponse{ID: "policy", Action: echoed})

			}))
			defer server.Close()
			if mode == "wrong-resource" {
				resource = "https://other.example/mcp"
			}
			cfg := mcphosting.Config{Endpoint: "/mcp", Auth: mcphosting.AuthConfig{Mode: "external-oauth", Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks", Resource: resource, ToolScopes: mcphosting.ScopePolicy{}}}
			_, err := applyMCPGatewayPolicy(context.Background(), NewClient(server.URL, "operator-key"), "my-mcp", cfg, []string{"RS256"})
			valid := mode == "create" || mode == "update"
			if (err == nil) != valid || (!valid && mode != "older-api" && writes != 0) || (mode == "older-api" && writes != 1) {
				t.Fatalf("mode=%s err=%v writes=%d", mode, err, writes)
			}
		})
	}
}
