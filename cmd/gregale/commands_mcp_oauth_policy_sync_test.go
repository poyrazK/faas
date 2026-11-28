package main_test

// adr: 20261124094745 — a deployed external-oauth server gets its gateway
// policy, so its OAuth callers can become customers; other modes and a
// blocked policy write leave the rules alone.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestSyncExternalOAuthGatewayPolicy(t *testing.T) {
	const resource = "https://my-mcp.example/mcp"
	for name, tc := range map[string]struct {
		mode      string
		competing bool
		writes    int
	}{
		"external-oauth":     {mode: mcphosting.AuthExternalOAuth, writes: 2},
		"open":               {mode: mcphosting.AuthOpen},
		"competing jwt rule": {mode: mcphosting.AuthExternalOAuth, competing: true},
	} {
		t.Run(name, func(t *testing.T) {
			writes := 0
			var stored json.RawMessage
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					if r.URL.Path == "/v1/apps/my-mcp" {
						_ = json.NewEncoder(w).Encode(api.AppResponse{URL: "https://my-mcp.example"})
						return
					}
					rules := []api.EdgeRuleResponse{}
					if tc.competing {
						rules = append(rules, api.EdgeRuleResponse{ID: "generic", Kind: "jwt", Enabled: true, MatchHost: "my-mcp.example", MatchPath: "/**", Action: json.RawMessage(`{}`)})
					}
					_ = json.NewEncoder(w).Encode(rules)
					return
				}
				writes++
				var body struct {
					Action json.RawMessage `json:"action"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.Action != nil {
					stored = body.Action
				}
				_ = json.NewEncoder(w).Encode(api.EdgeRuleResponse{ID: "policy", Action: stored})
			}))
			defer server.Close()
			cfg := mcphosting.Config{Endpoint: "/mcp", Auth: mcphosting.AuthConfig{Mode: tc.mode, Issuer: "https://issuer.example",
				JWKSURL: "https://issuer.example/jwks", Resource: resource, ToolScopes: mcphosting.ScopePolicy{}}}
			if tc.mode == mcphosting.AuthOpen {
				cfg.Auth = mcphosting.AuthConfig{Mode: tc.mode}
			}
			main.SyncExternalOAuthGatewayPolicy(context.Background(), main.NewClient(server.URL, "key"), ".", "my-mcp", cfg)
			if writes != tc.writes {
				t.Fatalf("writes = %d, want %d", writes, tc.writes)
			}
			if tc.writes > 0 {
				var action api.EdgeRuleJWTAction
				if err := json.Unmarshal(stored, &action); err != nil || action.MCP == nil || action.MCP.Resource != resource || action.Issuer != "https://issuer.example" {
					t.Fatalf("stored policy = %s (%v)", stored, err)
				}
			}
		})
	}
}
