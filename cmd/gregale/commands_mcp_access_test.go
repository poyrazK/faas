package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func accessTestConfig() mcphosting.Config {
	return mcphosting.Config{Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", Auth: mcphosting.AuthConfig{Mode: "external-oauth", Issuer: "https://identity.example", JWKSURL: "https://identity.example/jwks", Resource: "https://my-mcp.example/mcp", Scopes: []string{"mcp:tools", "mcp:read"}, ToolScopes: mcphosting.ScopePolicy{"read": []string{"mcp:read"}}, ResourceScopes: mcphosting.ScopePolicy{}, PromptScopes: mcphosting.ScopePolicy{}}, AllowedOrigins: []string{"https://client.example"}}
}

func accessTestRule(cfg mcphosting.Config) api.EdgeRuleResponse {
	action, _ := json.Marshal(mcpExpectedGatewayAction(cfg, []string{"RS256", "ES256"}))
	return api.EdgeRuleResponse{ID: "managed", AppID: "app-id", MatchHost: "my-mcp.example", MatchPath: "/**", Kind: "jwt", Enabled: true, Mode: "enforce", Action: action}
}

func TestMCPAccessAuditStoredGatewayDrift(t *testing.T) {
	for _, scenario := range []string{"healthy", "tagged API action", "reordered", "missing", "disabled", "expired", "log-only", "conditional", "header-only", "method-only", "expires later", "issuer", "audience", "jwks", "algorithms", "resource", "scopes", "origins", "tool map", "resource map", "prompt map", "claim constraints", "invalid action", "unsupported action", "earlier shadow", "equal shadow", "later jwt", "other host", "permission denied", "app denied", "wrong resource", "guest-only", "guest-only conflict", "guest-only inactive"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := accessTestConfig()
			rule := accessTestRule(cfg)
			rules := []api.EdgeRuleResponse{rule}
			options := mcpAccessAuditOptions{AuthPlacement: "gateway", Algorithms: []string{"RS256", "ES256"}}
			var action api.EdgeRuleJWTAction
			_ = json.Unmarshal(rule.Action, &action)
			switch scenario {
			case "reordered":
				action.Algorithms = []string{"ES256", "RS256"}
				action.MCP.Scopes = []string{"mcp:read", "mcp:tools"}
			case "missing":
				rules = nil
			case "disabled":
				rules[0].Enabled = false
			case "expired":
				rules[0].Expired = true
			case "log-only":
				rules[0].Mode = "log"
			case "conditional":
				rules[0].Match = &api.EdgeRuleMatchExpr{Field: "method", Op: "eq", Value: "POST"}
			case "header-only":
				rules[0].MatchHeaders = map[string]string{"x-client": "special"}
			case "method-only":
				rules[0].MatchMethods = []string{"POST"}
			case "expires later":
				expires := time.Now().Add(time.Hour)
				rules[0].ExpiresAt = &expires
			case "issuer":
				action.Issuer = "https://other.example"
			case "audience":
				action.Audience = []string{"https://other.example/mcp"}
			case "jwks":
				action.JWKSURL = "https://other.example/keys"
			case "algorithms":
				action.Algorithms = []string{"RS256"}
			case "resource":
				action.MCP.Resource = "https://other.example/mcp"
				action.Audience = []string{action.MCP.Resource}
			case "scopes":
				action.MCP.Scopes = []string{"mcp:write"}
			case "origins":
				action.MCP.AllowedOrigins = []string{"https://other.example"}
			case "tool map":
				action.MCP.ToolScopes = nil
			case "resource map":
				action.MCP.ResourceScopes = nil
			case "prompt map":
				action.MCP.PromptScopes = nil
			case "claim constraints":
				action.RequiredClaims = map[string]string{"private": "PRIVATE_CLAIM_SENTINEL"}
			case "invalid action":
				rules[0].Action = json.RawMessage(`not JSON PRIVATE_ACTION_SENTINEL`)
			case "unsupported action":
				rules[0].Action = json.RawMessage(`{"unsupported":"PRIVATE_ACTION_SENTINEL"}`)
			case "earlier shadow", "equal shadow", "later jwt":
				shadow := rule
				shadow.ID = "shadow"
				shadow.AppID = "another-app"
				shadow.MatchHost = "*"
				shadow.MatchPath = "/mcp"
				shadow.MatchHeaders = map[string]string{"x-secret": "PRIVATE_HEADER_SENTINEL"}
				if scenario == "earlier shadow" {
					shadow.Priority = -1
				} else if scenario == "later jwt" {
					shadow.Priority = 1
				}
				rules = append(rules, shadow)
			case "other host":
				other := rule
				other.ID = "other"
				other.AppID = "another-app"
				other.MatchHost = "unrelated.example"
				other.Priority = -1
				rules = append(rules, other)
			case "wrong resource":
				cfg.Auth.Resource = "https://other.example/mcp"
			case "guest-only":
				options = mcpAccessAuditOptions{AuthPlacement: "guest-only"}
				rules = nil
			case "guest-only conflict":
				options = mcpAccessAuditOptions{AuthPlacement: "guest-only"}
			case "guest-only inactive":
				options = mcpAccessAuditOptions{AuthPlacement: "guest-only"}
				rules[0].Enabled = false
			}
			if len(rules) > 0 && scenario != "invalid action" && scenario != "unsupported action" {
				rules[0].Action, _ = json.Marshal(action)
			}
			if scenario == "tagged API action" {
				rules[0].Action = json.RawMessage(`{"kind":"jwt","jwt":` + string(rule.Action) + `}`)
			}
			var reads, writes, endpoint atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer OPERATOR_TOKEN_SENTINEL" {
					t.Error("wrong platform credential")
				}
				if r.Method != http.MethodGet {
					writes.Add(1)
					w.WriteHeader(500)
					return
				}
				reads.Add(1)
				switch r.URL.Path {
				case "/v1/apps/my-mcp":
					if scenario == "app denied" {
						w.WriteHeader(403)
						_, _ = w.Write([]byte("PRIVATE_RESPONSE_SENTINEL"))
						return
					}
					writeJSONTest(w, api.AppResponse{ID: "app-id", Slug: "my-mcp", URL: "https://my-mcp.example"})
				case "/v1/apps/my-mcp/policy/status":
					writeJSONTest(w, api.RuntimePolicyStatusResponse{AppID: "app-id", EdgeRules: api.RuntimePolicyComponentStatus{Scope: "account", DesiredRevision: 1, State: "active", ServingGateways: 1, AppliedGateways: 1}})
				case "/v1/edge-rules":
					if scenario == "permission denied" {
						w.WriteHeader(403)
						_, _ = w.Write([]byte("PRIVATE_RESPONSE_SENTINEL"))
						return
					}
					writeJSONTest(w, rules)
				default:
					endpoint.Add(1)
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			// This test checks policy shapes, not HTTP scheduling latency. Deadline
			// behavior is covered by TestMCPAccessConvergence separately.
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			report := collectMCPAccessAudit(ctx, NewClient(server.URL, "OPERATOR_TOKEN_SENTINEL"), "my-mcp", cfg, nil, options)
			healthy := scenario == "healthy" || scenario == "tagged API action" || scenario == "reordered" || scenario == "later jwt" || scenario == "other host" || scenario == "guest-only" || scenario == "guest-only inactive"
			if report.OK != healthy || report.RuntimeVerified || report.RolesRequested || writes.Load() != 0 || endpoint.Load() != 0 {
				t.Fatalf("audit %s: %+v, writes=%d endpoint=%d", scenario, report, writes.Load(), endpoint.Load())
			}
			data, _ := json.Marshal(report)
			if strings.Contains(string(data), "SENTINEL") {
				t.Fatal("audit leaked credentials, actions, conditions or error bodies")
			}
			if healthy && reads.Load() != 4 {
				t.Fatalf("missing account-wide read: %d", reads.Load())
			}
			if scenario == "permission denied" && !accessHasCheck(report, "account_gateway_rules", "unknown") {
				t.Fatal("permission denied interpreted as healthy empty rules")
			}
			if scenario == "resource map" && !accessHasCheck(report, "gateway_resource_scopes", "failed") {
				t.Fatal("nil and deny-all maps collapsed")
			}
			if (scenario == "earlier shadow" || scenario == "equal shadow") && !accessHasCheck(report, "gateway_shadowing", "failed") {
				t.Fatal("wildcard shadow not detected")
			}
		})
	}
}

func accessHasCheck(report mcpAccessAuditReport, name, status string) bool {
	for _, check := range report.Checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}

func TestMCPAccessAuditRoleStructureWithoutCredentials(t *testing.T) {
	dir := t.TempDir()
	contract, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{{Name: "read", InputSchema: map[string]any{"type": "object"}}}, Resources: []mcphosting.Resource{{Name: "report", URI: "file:///reports/one"}}, ResourceTemplates: []mcphosting.ResourceTemplate{{Name: "reports", URITemplate: "file:///reports/{id}"}}, Prompts: []mcphosting.Prompt{{Name: "summary"}}})
	baseline, _ := mcphosting.MarshalContract(contract)
	if err := os.WriteFile(filepath.Join(dir, "reader.json"), baseline, 0600); err != nil {
		t.Fatal(err)
	}
	policy := `{"version":1,"roles":[{"name":"reader","token_env":"_UNSET_READER_TOKEN","baseline":"reader.json","authorization":{"denied_tools":["write"],"insufficient_scope_token_env":"unset_scope_token"}}]}`
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("_UNSET_READER_TOKEN", "")
	t.Setenv("unset_scope_token", "")
	roles, err := loadMCPReleasePolicyWithTokens(path, false)
	if err != nil || len(roles) != 1 || roles[0].token != "" || roles[0].Authorization.insufficientToken != "" {
		t.Fatalf("tokenless load failed: %v", err)
	}
	cfg := accessTestConfig()
	cfg.Auth.ResourceScopes = mcphosting.ScopePolicy{"file:///reports/{id}": []string{"mcp:read"}}
	cfg.Auth.PromptScopes = mcphosting.ScopePolicy{"summary": []string{}}
	if !mcpAuthorizationChecksPassed(mcpAccessSourceChecks(cfg, roles)) {
		t.Fatal("consistent role structure rejected")
	}
	cfg.Auth.ToolScopes = mcphosting.ScopePolicy{}
	if mcpAuthorizationChecksPassed(mcpAccessSourceChecks(cfg, roles)) {
		t.Fatal("deny-all source admitted visible role tools")
	}
	if _, err := loadMCPReleasePolicy(path); err == nil {
		t.Fatal("live loader bypassed missing credentials")
	}
}

func TestMCPAccessAuditDeadlineAndInvalidFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/my-mcp" {
			writeJSONTest(w, api.AppResponse{ID: "app-id", Slug: "my-mcp", URL: "https://my-mcp.example"})
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	report := collectMCPAccessAudit(ctx, NewClient(server.URL, "token"), "my-mcp", accessTestConfig(), nil, mcpAccessAuditOptions{AuthPlacement: "gateway"})
	if report.OK || !accessHasCheck(report, "account_gateway_rules", "unknown") {
		t.Fatal("deadline certified missing rules")
	}
	_, _, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{{"access"}, {"access", "audit"}, {"access", "audit", "--app", "my-mcp", "--auth-placement", "guest-only", "--algorithms", "RS256"}, {"access", "audit", "--app", "my-mcp", "--auth-placement", "gateway", "--timeout", "0"}, {"access", "audit", "--app", "my-mcp", "--auth-placement", "gateway", "--path", "/nonexistent"}} {
		if cmdMCP(args) == 0 {
			t.Fatal(args)
		}
	}
	if _, err := newMCPAccessAuditOptions("", "RS256"); err == nil {
		t.Fatal("algorithm flag silently enabled no gate")
	}
	if _, err := newMCPAccessAuditOptions("gateway", "HS256"); err == nil {
		t.Fatal("unsupported starter algorithm accepted")
	}
}

func TestMCPAccessAuditReleasePlanFingerprint(t *testing.T) {
	dir := t.TempDir()
	web := filepath.Join(dir, "web")
	worker := filepath.Join(dir, "worker")
	for _, source := range []string{web, worker} {
		if err := os.Mkdir(source, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "source.txt"), []byte("source"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	plan := mcpNativeReleasePlan{WebApp: "web-app", WebPath: web, WorkerApp: "worker-app", WorkerPath: worker, ObserverApp: "observer-app", ObserverMetricApp: "worker-app", TimeoutSeconds: 300}
	original, err := mcpNativeFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(plan)
	if strings.Contains(string(encoded), "access_audit") {
		t.Fatal("legacy plan serialization changed")
	}
	plan.AccessAudit = &mcpAccessAuditOptions{AuthPlacement: "gateway"}
	path := filepath.Join(dir, "release.json")
	encoded, _ = json.Marshal(plan)
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := readMCPNativePlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.AccessAudit.Algorithms) != 2 {
		t.Fatal("gateway default algorithms missing")
	}
	guarded, err := mcpNativeFingerprint(loaded)
	if err != nil || guarded == original {
		t.Fatal("audit declaration omitted from release fingerprint")
	}
	loaded.AccessAudit.AuthPlacement = "guest-only"
	loaded.AccessAudit.Algorithms = nil
	changed, err := mcpNativeFingerprint(loaded)
	if err != nil || changed == guarded {
		t.Fatal("auth placement change did not invalidate review identity")
	}
	loaded.AccessAudit.Algorithms = []string{"HS256"}
	encoded, _ = json.Marshal(loaded)
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readMCPNativePlan(path); err == nil {
		t.Fatal("invalid guest algorithm plan accepted")
	}
}
