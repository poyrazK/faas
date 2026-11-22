package gateway

// adr: 20261122194324 — api-key MCP policies and tool limits.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
)

func consumerKeyMCPRule() *EdgeRuleJWTResolved {
	return &EdgeRuleJWTResolved{
		ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Issuer: "https://api.example/v1/mcp/consumer-key-issuer",
		MCP: &api.MCPResourcePolicy{
			Resource: "https://app.example/mcp", Scopes: []string{"tools"}, Credential: api.MCPCredentialConsumerKey,
			ToolScopes: map[string][]string{"search": {}, "delete": {"admin"}},
			ToolLimits: map[string]api.MCPToolLimit{"search": {Requests: 2, WindowSeconds: 60}},
		},
	}
}

func mcpConsumerRequest(method, path, body string, scopes ...string) *http.Request {
	r := httptest.NewRequest(method, "https://app.example"+path, strings.NewReader(body))
	if scopes != nil {
		r = r.WithContext(authmw.WithConsumer(r.Context(), authmw.ConsumerIdentity{ID: "consumer-1", AppID: "app-1", KeyID: "key-1", Scopes: scopes}))
	}
	return r
}

func TestMCPConsumerKeyPolicy(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1"}
	call := func(name string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `"}}`
	}
	for _, tc := range []struct {
		name   string
		scopes []string
		body   string
		status int
	}{
		{"no consumer key", nil, call("search"), 401},
		{"key without endpoint scope", []string{"write"}, call("search"), 403},
		{"endpoint scope from mcp namespace", []string{"mcp:tools"}, call("search"), 0},
		{"bare scope name is not an mcp grant", []string{"tools"}, call("search"), 403},
		{"tool scope missing", []string{"mcp:tools"}, call("delete"), 403},
		{"tool scope granted", []string{"mcp:tools", "mcp:admin"}, call("delete"), 0},
		{"tool outside allowlist", []string{"mcp:tools", "mcp:admin"}, call("unlisted"), 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{routeConsumerLimiter: NewLimiterWithLRU(EdgeRuleConsumerCacheCap)}
			w := httptest.NewRecorder()
			handled := h.applyMCPResourcePolicy(w, mcpConsumerRequest("POST", "/mcp", tc.body, tc.scopes...), app, consumerKeyMCPRule())
			if handled != (tc.status != 0) || handled && w.Code != tc.status {
				t.Fatalf("handled=%v status=%d want %d body=%s", handled, w.Code, tc.status, w.Body.String())
			}
			if tc.status == 401 && !strings.Contains(w.Header().Get("WWW-Authenticate"), `resource_metadata="https://app.example/.well-known/oauth-protected-resource/mcp"`) {
				t.Fatalf("401 must point at the protected-resource metadata: %q", w.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestMCPConsumerKeyMetadataNamesNoAuthorizationServer(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()
	if !h.applyMCPResourcePolicy(w, mcpConsumerRequest("GET", "/.well-known/oauth-protected-resource/mcp", ""), App{}, consumerKeyMCPRule()) || w.Code != 200 {
		t.Fatalf("metadata status=%d", w.Code)
	}
	var metadata map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if _, ok := metadata["authorization_servers"]; ok || metadata["resource"] != "https://app.example/mcp" {
		t.Fatalf("metadata = %v", metadata)
	}
}

func TestMCPToolLimitPerCallerAndTool(t *testing.T) {
	h := &Handler{routeConsumerLimiter: NewLimiterWithLRU(EdgeRuleConsumerCacheCap)}
	rule, app := consumerKeyMCPRule(), App{ID: "app-1", AccountID: "acct-1"}
	search := `{"method":"tools/call","params":{"name":"search"}}`
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		if h.applyMCPResourcePolicy(w, mcpConsumerRequest("POST", "/mcp", search, "mcp:tools"), app, rule) {
			t.Fatalf("call %d refused: %d %s", i, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	if !h.applyMCPResourcePolicy(w, mcpConsumerRequest("POST", "/mcp", search, "mcp:tools"), app, rule) || w.Code != http.StatusTooManyRequests {
		t.Fatalf("third call status=%d", w.Code)
	}
	if w.Header().Get("Retry-After") != "30" || !strings.Contains(w.Body.String(), api.CodeMCPToolRateLimited) {
		t.Fatalf("Retry-After=%q body=%s", w.Header().Get("Retry-After"), w.Body.String())
	}
	// Another tool and another caller keep their own budgets.
	list := `{"method":"tools/list"}`
	if h.applyMCPResourcePolicy(httptest.NewRecorder(), mcpConsumerRequest("POST", "/mcp", list, "mcp:tools"), app, rule) {
		t.Fatal("tools/list must not spend the search limit")
	}
	other := mcpConsumerRequest("POST", "/mcp", search)
	other = other.WithContext(authmw.WithConsumer(other.Context(), authmw.ConsumerIdentity{ID: "consumer-2", Scopes: []string{"mcp:tools"}}))
	if h.applyMCPResourcePolicy(httptest.NewRecorder(), other, app, rule) {
		t.Fatal("a second caller must have its own budget")
	}
}

func TestMCPConsumerKeyEndpointMatchesOnlyOwnedEndpoint(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1"}
	h := &Handler{edgeRules: stubEdgeRuleMatcher{jwt: consumerKeyMCPRule()}}
	for path, want := range map[string]bool{"/mcp": true, "/.well-known/oauth-protected-resource/mcp": true, "/admin": false, "/mcp/other": false} {
		if got := h.mcpConsumerKeyEndpoint(httptest.NewRequest("POST", "https://app.example"+path, nil), app); got != want {
			t.Errorf("%s: got %v want %v", path, got, want)
		}
	}
	if h.mcpConsumerKeyEndpoint(httptest.NewRequest("POST", "https://app.example/mcp", nil), App{ID: "app-2", AccountID: "acct-1"}) {
		t.Error("another app's policy must not admit mcp: scopes")
	}
	jwtRule := consumerKeyMCPRule()
	jwtRule.MCP.Credential = ""
	h.edgeRules = stubEdgeRuleMatcher{jwt: jwtRule}
	if h.mcpConsumerKeyEndpoint(httptest.NewRequest("POST", "https://app.example/mcp", nil), app) {
		t.Error("a JWT MCP policy must not admit mcp: scopes")
	}
}

func TestConsumerAuthAdmitsMCPScopesOnlyAtTheMCPEndpoint(t *testing.T) {
	store, token := consumerAuthFixture()
	store.key.Scopes = []string{"mcp:tools"}
	app := App{ID: "app-1", AccountID: "acct-1", ConsumerAuthMode: api.ConsumerAuthModeOptional}
	h := &Handler{consumerAuthStore: store, edgeRules: stubEdgeRuleMatcher{jwt: consumerKeyMCPRule()}}
	for path, want := range map[string]bool{"/mcp": true, "/resource": false} {
		r := httptest.NewRequest("POST", "https://app.example"+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		ok := h.enforceConsumerAuth(rr, r, &statusRecorder{ResponseWriter: rr, status: http.StatusOK, request: r}, app)
		if ok != want {
			t.Fatalf("%s: admitted=%v want %v status=%d", path, ok, want, rr.Code)
		}
		if !want && rr.Code != http.StatusForbidden {
			t.Fatalf("%s: status=%d", path, rr.Code)
		}
		if identity, found := authmw.ConsumerFromContext(r); want && (!found || identity.ID != "consumer-1") {
			t.Fatalf("%s: consumer identity not stamped", path)
		}
	}
}

func TestMCPPolicyMarksAdmittedRequests(t *testing.T) {
	h := &Handler{routeConsumerLimiter: NewLimiterWithLRU(EdgeRuleConsumerCacheCap)}
	app := App{ID: "app-1", AccountID: "acct-1"}
	admitted := mcpConsumerRequest("POST", "/mcp", `{"method":"tools/list"}`, "mcp:tools")
	if h.applyMCPResourcePolicy(httptest.NewRecorder(), admitted, app, consumerKeyMCPRule()) || admitted.Header.Get(api.MCPGatewayAuthorizedHeader) != "rule-1" {
		t.Fatalf("admitted request mark = %q", admitted.Header.Get(api.MCPGatewayAuthorizedHeader))
	}
	refused := mcpConsumerRequest("POST", "/mcp", `{"method":"tools/list"}`)
	if !h.applyMCPResourcePolicy(httptest.NewRecorder(), refused, app, consumerKeyMCPRule()) || refused.Header.Get(api.MCPGatewayAuthorizedHeader) != "" {
		t.Fatal("a refused request must not carry the mark")
	}
}
