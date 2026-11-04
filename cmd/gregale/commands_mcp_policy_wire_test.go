package main_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPPolicyAcceptsWireActionsWithoutDroppingConstraints(t *testing.T) {
	for _, mode := range []string{"create", "update"} {
		for _, shape := range []string{"flat", "tagged", "payload-only", "wrong-kind", "extra-kind", "unknown-constraint", "no-expiration", "missing-policy", "null-payload"} {
			t.Run(mode+"/"+shape, func(t *testing.T) {
				resource := "https://my-mcp.example/mcp"
				policy := api.EdgeRuleJWTAction{Issuer: "https://issuer.example", Audience: []string{resource}, JWKSURL: "https://issuer.example/jwks", Algorithms: []string{"RS256"}, MCP: &api.MCPResourcePolicy{Resource: resource, ToolScopes: map[string][]string{}}}
				stored, _ := json.Marshal(policy)
				wire := func(flat json.RawMessage) json.RawMessage {
					var payload map[string]any
					_ = json.Unmarshal(flat, &payload)
					// apid makes the default expiration requirement explicit in its state action.
					if shape != "flat" {
						payload["require_exp"] = true
					}
					switch shape {
					case "unknown-constraint":
						payload["future_constraint"] = true
					case "no-expiration":
						payload["require_exp"] = false
					case "missing-policy":
						delete(payload, "mcp")
					case "null-payload":
						payload = nil
					}
					var body any = map[string]any{"kind": "jwt", "jwt": payload}
					switch shape {
					case "flat":
						body = payload
					case "payload-only":
						body = map[string]any{"jwt": payload}
					case "wrong-kind":
						body = map[string]any{"kind": "ip", "jwt": payload}
					case "extra-kind":
						body = map[string]any{"kind": "jwt", "jwt": payload, "ip": map[string]any{}}
					}
					data, _ := json.Marshal(body)
					return data
				}
				writes, activations := 0, 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						if r.URL.Path == "/v1/apps/my-mcp" {
							_ = json.NewEncoder(w).Encode(api.AppResponse{URL: "https://my-mcp.example"})
							return
						}
						rules := []api.EdgeRuleResponse{}
						// Existing rules use the actual API envelope. Response mutations below
						// exercise verification after both create and update writes.
						if mode == "update" {
							data, _ := json.Marshal(map[string]any{"kind": "jwt", "jwt": json.RawMessage(stored)})
							rules = append(rules, api.EdgeRuleResponse{ID: "policy", Kind: "jwt", Enabled: true, MatchHost: "my-mcp.example", MatchPath: "/**", Action: data})
						}
						_ = json.NewEncoder(w).Encode(rules)
						return
					}
					writes++
					var request struct {
						Action  json.RawMessage `json:"action"`
						Enabled *bool           `json:"enabled"`
					}
					_ = json.NewDecoder(r.Body).Decode(&request)
					if request.Action != nil {
						stored = request.Action
					}
					if request.Enabled != nil && *request.Enabled {
						activations++
					}
					_ = json.NewEncoder(w).Encode(api.EdgeRuleResponse{ID: "policy", Kind: "jwt", Action: wire(stored)})
				}))
				defer server.Close()
				cfg := mcphosting.Config{Endpoint: "/mcp", Auth: mcphosting.AuthConfig{Mode: "external-oauth", Issuer: policy.Issuer, JWKSURL: policy.JWKSURL, Resource: resource, ToolScopes: mcphosting.ScopePolicy{}}}
				_, err := main.ApplyMCPGatewayPolicy(context.Background(), main.NewClient(server.URL, "fixture-operator"), "my-mcp", cfg, []string{"RS256"})
				allowed := shape == "flat" || shape == "tagged" || shape == "payload-only"
				if (err == nil) != allowed {
					t.Fatalf("error=%v allowed=%v", err, allowed)
				}
				if !allowed && (writes != 1 || activations != 0) {
					t.Fatalf("unsupported action activated: writes=%d activations=%d", writes, activations)
				}
				if allowed && mode == "create" && (writes != 2 || activations != 1) {
					t.Fatalf("policy was not verified then activated: writes=%d activations=%d", writes, activations)
				}
			})
		}
	}
}
