package main_test

// adr: 20261124094745 — a deployed external-oauth server gets its gateway
// policy, so its OAuth callers can become customers; other modes, a
// guest-only audit, a deploy token the policy would refuse and a blocked
// policy write leave the rules alone.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

const syncResource = "https://my-mcp.example/mcp"

// unsignedJWT builds a token with header alg and claims; the sync checks
// claims, never the signature.
func unsignedJWT(alg string, claims map[string]any) string {
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	return enc(map[string]string{"alg": alg, "typ": "JWT"}) + "." + enc(claims) + ".sig"
}

func TestSyncExternalOAuthGatewayPolicy(t *testing.T) {
	good := map[string]any{"iss": "https://issuer.example", "aud": syncResource, "sub": "seller", "exp": 4102444800, "scope": "mcp:tools"}
	with := func(key string, value any) map[string]any {
		out := map[string]any{}
		for k, v := range good {
			out[k] = v
		}
		out[key] = value
		return out
	}
	for name, tc := range map[string]struct {
		mode      string
		token     string
		audit     *main.MCPAccessAuditOptions
		competing bool
		writes    int
	}{
		"external-oauth":     {mode: mcphosting.AuthExternalOAuth, token: unsignedJWT("RS256", good), writes: 2},
		"open":               {mode: mcphosting.AuthOpen, token: unsignedJWT("RS256", good)},
		"competing jwt rule": {mode: mcphosting.AuthExternalOAuth, token: unsignedJWT("RS256", good), competing: true},
		"guest-only audit":   {mode: mcphosting.AuthExternalOAuth, token: unsignedJWT("RS256", good), audit: &main.MCPAccessAuditOptions{AuthPlacement: "guest-only"}},
		"reviewed algorithm": {mode: mcphosting.AuthExternalOAuth, token: unsignedJWT("RS256", good), audit: &main.MCPAccessAuditOptions{AuthPlacement: "gateway", Algorithms: []string{"ES256"}}},
		"other algorithm":    {mode: mcphosting.AuthExternalOAuth, token: unsignedJWT("PS256", good)},
		"other audience":     {mode: mcphosting.AuthExternalOAuth, token: unsignedJWT("RS256", with("aud", []string{"https://elsewhere.example"}))},
		"missing scope":      {mode: mcphosting.AuthExternalOAuth, token: unsignedJWT("RS256", with("scope", "openid"))},
		"audience list":      {mode: mcphosting.AuthExternalOAuth, token: unsignedJWT("ES256", with("aud", []string{"x", syncResource})), writes: 2},
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
				JWKSURL: "https://issuer.example/jwks", Resource: syncResource, Scopes: []string{"mcp:tools"}, ToolScopes: mcphosting.ScopePolicy{}}}
			if tc.mode == mcphosting.AuthOpen {
				cfg.Auth = mcphosting.AuthConfig{Mode: tc.mode}
			}
			// The deploy's verification deadline may be spent by promotion;
			// the sync still runs. A path with a space is quoted in the fix.
			spent, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			checks := main.SyncExternalOAuthGatewayPolicy(spent, main.NewClient(server.URL, "key"), "./my server", "my-mcp", cfg, tc.token, tc.audit)
			if writes != tc.writes {
				t.Fatalf("writes = %d, want %d", writes, tc.writes)
			}
			// The deploy receipt's gateway_token_checks.
			switch {
			case tc.mode == mcphosting.AuthOpen:
				if checks != nil {
					t.Fatalf("open server reported %+v", checks)
				}
			case tc.writes > 0:
				if checks == nil || checks.Status != "on" || checks.RuleID != "policy" {
					t.Fatalf("checks = %+v, want on", checks)
				}
			case tc.audit != nil && tc.audit.AuthPlacement == "guest-only":
				if checks == nil || checks.Status != "skipped" {
					t.Fatalf("checks = %+v, want skipped", checks)
				}
			default:
				if checks == nil || checks.Status != "failed" || len(checks.FixCommands) != 1 || checks.FixCommands[0] != "gregale mcp policy --path './my server' --name my-mcp" {
					t.Fatalf("checks = %+v, want failed with a fix command", checks)
				}
			}
			if tc.writes > 0 {
				var action api.EdgeRuleJWTAction
				if err := json.Unmarshal(stored, &action); err != nil || action.MCP == nil || action.MCP.Resource != syncResource || action.Issuer != "https://issuer.example" {
					t.Fatalf("stored policy = %s (%v)", stored, err)
				}
			}
		})
	}
}
