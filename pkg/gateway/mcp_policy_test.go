package gateway

// adr: 639 — verified promotion and gateway resource-policy acceptance.

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPGatewayResourceAuthentication(t *testing.T) {
	p := &api.MCPResourcePolicy{Resource: "https://app.example/mcp", Scopes: []string{"mcp:read"}, ToolScopes: map[string][]string{"read": {}, "write": {"mcp:write"}}}
	for _, tc := range []struct {
		name, path, token, origin, scope string
		invalid, expired                 bool
		status                           int
	}{
		{"public metadata", "/.well-known/oauth-protected-resource/mcp", "", "", "", false, false, 200},
		{"missing token", "/mcp", "", "", "", false, false, 401},
		{"malformed token", "/mcp", "not-a-jwt", "", "", true, false, 401},
		{"expired token", "/mcp", "jwt", "", "mcp:read", false, true, 401},
		{"missing scope", "/mcp", "jwt", "", "", false, false, 403},
		{"untrusted origin", "/mcp", "jwt", "https://evil.example", "mcp:read", false, false, 403},
		{"authorized", "/mcp", "jwt", "", "mcp:read", false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exp := time.Now().Add(time.Minute)
			if tc.expired {
				exp = time.Now().Add(-time.Second)
			}
			h := &Handler{jwtVerifier: &countingJWTVerifier{onVerify: func(_ context.Context, _ string, rule *EdgeRuleJWTResolved) (*JWTClaims, error) {
				if len(rule.ExtractClaims) != 1 || rule.ExtractClaims[0] != "scope" {
					t.Fatalf("scope not extracted: %+v", rule)
				}
				if tc.invalid {
					return nil, errors.New("invalid")
				}
				return &JWTClaims{Subject: "caller", Exp: exp, Custom: map[string]string{"scope": tc.scope}}, nil
			}}}
			r := httptest.NewRequest("POST", "https://app.example"+tc.path, strings.NewReader(`{"method":"tools/call","params":{"name":"read"}}`))
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			r = r.WithContext(ctx)
			if tc.path != "/mcp" {
				r.Method = "GET"
			}
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			handled := h.applyMCPResourcePolicy(w, r, App{}, &EdgeRuleJWTResolved{Issuer: "https://issuer.example", MCP: p})
			if handled != (tc.status != 0) {
				t.Fatalf("handled=%v status=%d body=%s", handled, w.Code, w.Body.String())
			}
			if handled && w.Code != tc.status {
				t.Fatalf("status=%d want=%d", w.Code, tc.status)
			}
			if tc.status == 401 && !strings.Contains(w.Header().Get("WWW-Authenticate"), p.MetadataURL()) {
				t.Fatal("missing resource challenge")
			}
			if tc.status == 0 {
				deadline, ok := r.Context().Deadline()
				if !ok || !deadline.Equal(exp) {
					t.Fatal("stream has no JWT expiry deadline")
				}
			}
		})
	}
}

func TestMCPPolicyUsesJSONRPCBody(t *testing.T) {
	p := &api.MCPResourcePolicy{ToolScopes: map[string][]string{"read": {}, "write": {"write"}}, PromptScopes: map[string][]string{}, ResourceScopes: map[string][]string{"customer://records/{id}": {"read"}, "customer://records/private": {"admin"}}}
	for _, tc := range []struct {
		body   string
		scope  []string
		status int
	}{
		{`{"method":"tools/call","params":{"name":"read"}}`, nil, 0},
		{`{"method":"tools/call","Method":"tools/list","params":{"name":"write","Name":"read"}}`, nil, 403},
		{`{"method":"tools/call","params":{"name":"write"}}`, []string{"write"}, 0},
		{`{"method":"prompts/get","params":{"name":"read"}}`, nil, 403},
		{`{"method":"resources/read","params":{"uri":"customer://records/42"}}`, []string{"read"}, 0},
		{`{"method":"resources/read","params":{"uri":"customer://records/private"}}`, []string{"read"}, 403},
		{`{"method":"subscriptions/listen","params":{"notifications":{"resourceSubscriptions":["customer://records/42"]}}}`, nil, 403},
		{`[{"method":"tools/list"},{"method":"tools/call"}]`, nil, 400},
	} {
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(tc.body))
		r.Header.Set("MCP-Method", "tools/list")
		r.Header.Set("MCP-Name", "read")
		status, _ := mcpPolicyRequest(r, p, tc.scope)
		if status != tc.status {
			t.Fatalf("%s status=%d want=%d", tc.body, status, tc.status)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != tc.body {
			t.Fatal("proxy body changed")
		}
	}
}

type mcpAliasMatcher struct {
	stubEdgeRuleMatcher
	canonical string
	policy    *EdgeRuleJWTResolved
}

func (m mcpAliasMatcher) MatchJWT(_ context.Context, host, _, _ string) *EdgeRuleJWTResolved {
	if host == m.canonical {
		return m.policy
	}
	return nil
}

func TestMCPPolicyProtectsPreviewAndCustomAliases(t *testing.T) {
	for _, host := range []string{"deploy-42-app.example", "customer.example"} {
		policy := &EdgeRuleJWTResolved{ID: "policy", AccountID: "owner", AppID: "app", MCP: &api.MCPResourcePolicy{Resource: "https://app.example/mcp"}}
		h := &Handler{edgeRules: mcpAliasMatcher{canonical: "app.example", policy: policy}, jwtVerifier: &countingJWTVerifier{onVerify: func(context.Context, string, *EdgeRuleJWTResolved) (*JWTClaims, error) {
			return nil, errors.New("invalid")
		}}}
		r := httptest.NewRequest("POST", "https://"+host+"/mcp", strings.NewReader(`{"method":"tools/list"}`))
		w := httptest.NewRecorder()
		if !h.applyEdgeRuleJWT(w, r, App{ID: "app", AccountID: "owner", CanonicalHost: "app.example"}) || w.Code != 401 {
			t.Fatalf("alias %s bypassed policy: %d", host, w.Code)
		}
	}
	for _, host := range []string{"app.example", "deploy-42-app.example"} {
		h := &Handler{edgeRules: mcpAliasMatcher{canonical: "app.example", policy: &EdgeRuleJWTResolved{Unavailable: true}}}
		r := httptest.NewRequest("POST", "https://"+host+"/mcp", strings.NewReader(`{"method":"tools/list"}`))
		w := httptest.NewRecorder()
		if !h.applyEdgeRuleJWT(w, r, App{CanonicalHost: "app.example"}) || w.Code != 503 {
			t.Fatalf("JWT policy read failure allowed access: %d", w.Code)
		}
	}
}
