package main

// adr: 644 — verified promotion and gateway resource-policy acceptance.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPCandidatePromotion(t *testing.T) {
	for _, tc := range []struct {
		name                                          string
		valid, preview, first, drift, catalogDegraded bool
		want                                          int
		authorization                                 bool
		authorizationFailure                          string
		accessAudit                                   string
	}{
		{"smoke fixture passes", true, true, false, false, false, 0, false, "", ""},
		{"smoke fixture failure", true, true, false, false, false, 1, false, "", ""},
		{"valid candidate", true, true, false, false, false, 0, false, "", ""},
		{"platform readiness passes", true, true, false, false, false, 0, false, "", ""},
		{"platform readiness staging", true, true, false, false, false, 0, false, "", ""},
		{"platform readiness OAuth", true, true, false, false, false, 0, true, "", ""},
		{"platform readiness failure", true, true, false, false, false, 1, false, "", ""},
		{"public readiness failure preserves serving", true, true, false, false, false, 1, false, "", ""},
		{"first deployment", true, true, true, false, false, 0, false, "", ""},
		{"failed doctor preserves serving", false, true, false, false, false, 1, false, "", ""},
		{"missing preview fails closed", true, false, false, false, false, 1, false, "", ""},
		{"catalog expansion blocks promotion", true, true, false, true, false, 1, false, "", ""},
		{"degraded catalog blocks promotion", true, true, false, false, true, 1, false, "", ""},
		{"authorization gate passes", true, true, false, false, false, 0, true, "", ""},
		{"replica auth bypass blocks promotion", true, true, false, false, false, 1, true, "replica", ""},
		{"public scope bypass blocks promotion", true, true, false, false, false, 1, true, "public", ""},
		{"access audit passes", true, true, false, false, false, 0, true, "", "healthy"},
		{"policy changes after audit", true, true, false, false, false, 1, true, "", "changed"},
		{"access drift blocks promotion", true, true, false, false, false, 1, true, "", "drift"},
		{"missing audit evidence blocks promotion", true, true, false, false, false, 1, true, "", "denied"},
		{"missing gateway convergence blocks promotion", true, true, false, false, false, 1, true, "", "convergence-unavailable"},
		{"guest-only audit passes", true, true, false, false, false, 0, true, "", "guest-only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			var output bytes.Buffer
			osStdout, osStderr, jsonOutput = &output, &output, true
			t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
			calls, promotions, updates := 0, 0, 0
			candidate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !tc.authorization && r.Header.Get("Authorization") != "" {
					t.Error("operator key forwarded to candidate")
				}
				if tc.authorization {
					if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") {
						_ = json.NewEncoder(w).Encode(map[string]any{"resource": "https://my-mcp.example/mcp", "authorization_servers": []string{"https://identity.example"}, "scopes_supported": []string{"mcp:tools"}})
						return
					}
					bearer := r.Header.Get("Authorization")
					if bearer != "Bearer ROLE_TOKEN_SENTINEL" {
						status := http.StatusUnauthorized
						if bearer == "Bearer SCOPE_TOKEN_SENTINEL" {
							status = http.StatusForbidden
							if tc.authorizationFailure == "public" {
								status = http.StatusOK
							}
						}
						w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://my-mcp.example/.well-known/oauth-protected-resource/mcp"`)
						w.WriteHeader(status)
						_, _ = w.Write([]byte("PRIVATE_BODY_SENTINEL"))
						return
					}
				}
				if !tc.valid {
					w.WriteHeader(404)
					return
				}
				if r.Header.Get("Origin") != "" {
					w.WriteHeader(403)
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
					if tc.catalogDegraded {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusServiceUnavailable)
						_ = json.NewEncoder(w).Encode(map[string]any{
							"status": "degraded", "catalog_revision": 4,
							"catalog_sha256": strings.Repeat("a", 64),
						})
					}
					return
				}
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("MCP-Protocol-Version", mcphosting.ProtocolVersion)
				switch req.Method {
				case "ping":
					if tc.name == "public readiness failure preserves serving" {
						w.WriteHeader(503)
						return
					}
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{}}`, req.ID)
				case "initialize":
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":%q,"capabilities":{"tools":{}}}}`, req.ID, mcphosting.ProtocolVersion)
				case "notifications/initialized":
					w.WriteHeader(202)
				case "tools/call":
					calls++
					if !strings.HasPrefix(tc.name, "smoke fixture") {
						t.Error("unapproved tool execution")
					}
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[]}}`, req.ID)

				case "server/discover":
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":["%s"],"capabilities":{"tools":{}}}}`, req.ID, mcphosting.ProtocolVersion)
				case "tools/list":
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"add","inputSchema":{"type":"object"}}]}}`, req.ID)
				default:
					calls++
					t.Errorf("unexpected tool execution: %s", req.Method)
				}
			}))
			defer candidate.Close()
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer operator-only" {
					t.Error("missing control credential")
				}
				switch r.URL.Path {
				case "/v1/apps/my-mcp/policy/status":
					if tc.accessAudit == "convergence-unavailable" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					writeJSONTest(w, api.RuntimePolicyStatusResponse{AppID: "app", EdgeRules: api.RuntimePolicyComponentStatus{Scope: "account", DesiredRevision: 1, State: "active", ServingGateways: 1, AppliedGateways: 1}})
				case "/v1/edge-rules":
					if tc.accessAudit == "denied" {
						w.WriteHeader(403)
						_, _ = w.Write([]byte("PRIVATE_RULE_RESPONSE_SENTINEL"))
						return
					}
					if tc.accessAudit == "guest-only" {
						_ = json.NewEncoder(w).Encode([]api.EdgeRuleResponse{})
						return
					}
					source := mcphosting.Config{Auth: mcphosting.AuthConfig{Issuer: "https://identity.example", JWKSURL: "https://identity.example/jwks", Resource: "https://my-mcp.example/mcp", Scopes: []string{"mcp:tools"}}}
					action := mcpExpectedGatewayAction(source, []string{"RS256", "ES256"})
					if tc.accessAudit == "drift" {
						action.MCP.ToolScopes = map[string][]string{}
					}
					encoded, _ := json.Marshal(action)
					_ = json.NewEncoder(w).Encode([]api.EdgeRuleResponse{{ID: "managed", AppID: "app", Kind: "jwt", Enabled: true, Mode: "enforce", MatchHost: "my-mcp.example", MatchPath: "/**", Action: encoded}})
				case "/v1/apps/my-mcp/instances":
					_ = json.NewEncoder(w).Encode([]api.InstanceResponse{{ID: "instance-a", AppID: "app", DeploymentID: "candidate", State: "running"}})
				case "/v1/apps/my-mcp/deployments/candidate/instances/instance-a/mcp-health":
					healthStatus := http.StatusOK
					catalogStatus := "ok"
					if tc.catalogDegraded {
						healthStatus = http.StatusServiceUnavailable
						catalogStatus = "degraded"
					}
					health := fmt.Sprintf(`{"status":%q,"catalog_revision":1,"catalog_sha256":%q}`, catalogStatus, strings.Repeat("a", 64))
					_ = json.NewEncoder(w).Encode(api.MCPInstanceHealthProbeResponse{
						AppID: "app", DeploymentID: "candidate", InstanceID: "instance-a",
						HTTPStatus: healthStatus, HealthBody: health,
					})
				case "/v1/apps/my-mcp/deployments/candidate/instances/instance-a/mcp-probe":
					var probe api.MCPInstanceProtocolProbeRequest
					if err := json.NewDecoder(r.Body).Decode(&probe); err != nil {
						t.Errorf("decode exact-instance protocol request: %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					method, valid := api.MCPProbeRPCMethod(probe.RPCRequest)
					if !valid || !api.MCPProbeRPCMatchesVersion(probe.RPCRequest, probe.ProtocolVersion) {
						t.Errorf("invalid exact-instance protocol request: %s", probe.RPCRequest)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					var request struct {
						ID json.RawMessage `json:"id"`
					}
					_ = json.Unmarshal(probe.RPCRequest, &request)
					response := api.MCPInstanceProtocolProbeResponse{
						AppID: "app", DeploymentID: "candidate", InstanceID: "instance-a",
						HTTPStatus: http.StatusOK, ContentType: "application/json",
						ProtocolVersion: api.MCPProtocolVersion,
					}
					if tc.authorization {
						bearer := r.Header.Get("MCP-Probe-Authorization")
						if bearer != "ROLE_TOKEN_SENTINEL" {
							response.HTTPStatus = http.StatusUnauthorized
							response.ProtocolVersion = ""
							if bearer == "SCOPE_TOKEN_SENTINEL" {
								response.HTTPStatus = http.StatusForbidden
							}
							if tc.authorizationFailure == "replica" && bearer == "gregale-invalid-bearer-probe" {
								response.HTTPStatus = http.StatusOK
							}
							response.Body = "PRIVATE_BODY_SENTINEL"
							_ = json.NewEncoder(w).Encode(response)
							return
						}
					}
					var result any
					switch method {
					case "initialize":
						result = map[string]any{
							"protocolVersion": api.MCPProtocolVersion,
							"capabilities":    map[string]any{"tools": map[string]any{}},
						}
					case "notifications/initialized":
						response.HTTPStatus = http.StatusAccepted
						response.ContentType = ""
					case "server/discover":
						result = map[string]any{
							"supportedVersions": []string{api.MCPProtocolVersion},
							"capabilities":      map[string]any{"tools": map[string]any{}},
						}
					case "tools/list":
						result = map[string]any{"tools": []any{map[string]any{"name": "add", "inputSchema": map[string]any{"type": "object"}}}}
					default:
						t.Errorf("unexpected MCP protocol method: %s", method)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if method != "notifications/initialized" {
						encoded, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
						if err != nil {
							t.Errorf("encode MCP protocol response: %v", err)
							w.WriteHeader(http.StatusInternalServerError)
							return
						}
						response.Body = string(encoded)
					}
					_ = json.NewEncoder(w).Encode(response)
				case "/v1/apps/my-mcp/mcp-readiness-policy":
					if tc.name == "platform readiness staging" && r.URL.Query().Get("scope") != "staging" {
						t.Error("candidate environment policy not selected")
					}
					if strings.HasPrefix(tc.name, "platform readiness") {
						role := api.MCPReadinessRole{Name: "default"}
						if tc.name == "platform readiness OAuth" {
							role.Name = "reader"
							role.Monitor = &api.MCPReadinessMonitorConfig{OAuthConnection: &api.MCPReadinessOAuthConnection{Role: "reader", Revision: "7e53717e-9b82-4ea4-a594-4a1d8aa2a8bb"}}
						}
						_ = json.NewEncoder(w).Encode(api.MCPReadinessPolicy{Revision: "policy-revision", Enabled: true, Roles: []api.MCPReadinessRole{role}})
					} else {
						_ = json.NewEncoder(w).Encode(api.MCPReadinessPolicy{Enabled: false})
					}
				case "/v1/deployments/candidate/mcp-readiness":
					if r.Method != "POST" {
						t.Error("platform probe must execute on server")
					}
					expectedRole := "default"
					if tc.name == "platform readiness OAuth" {
						expectedRole = "reader"
						if r.Header.Get(api.MCPReadinessTokenHeader) != "" {
							t.Error("local credential forwarded for managed OAuth role")
						}
					}
					var probe api.MCPReadinessProbeRequest
					if e := json.NewDecoder(r.Body).Decode(&probe); e != nil || probe.Role != expectedRole {
						t.Error("wrong platform probe role")
					}
					_ = json.NewEncoder(w).Encode(api.MCPReadinessEvidence{DeploymentID: "candidate", Role: expectedRole, PolicyRevision: "policy-revision", Passed: tc.name == "platform readiness passes" || tc.name == "platform readiness OAuth" || tc.name == "platform readiness staging"})
				case "/v1/apps/my-mcp":
					if r.Method == "PATCH" {
						updates++
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app", Slug: "my-mcp", URL: "https://my-mcp.example", StreamingEnabled: true, PublicAuth: api.PublicAuthStatus{Mode: api.AppPublicAuthModeOpen}})
				case "/v1/deployments/candidate":
					scope := "default"
					if tc.name == "platform readiness staging" {
						scope = "staging"
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "candidate", AppID: "app", Status: statusLive, Scope: scope})
				case "/v1/apps/my-mcp/deployments":
					deps := []api.DeploymentResponse{{ID: "candidate", AppID: "app", Status: statusLive}}
					if !tc.first {
						deps = append(deps, api.DeploymentResponse{ID: "stable", AppID: "app", Status: statusLive, TrafficPercent: 100})
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: deps})
				case "/v1/deployments/candidate/url":
					_ = json.NewEncoder(w).Encode(api.DeploymentPreviewURL{URL: candidate.URL, Alive: tc.preview})
				case "/v1/deployments/candidate/traffic":
					var req api.UpdateDeploymentTrafficRequest
					_ = json.NewDecoder(r.Body).Decode(&req)
					expected := "stable"
					if tc.first {
						expected = ""
					}
					if req.ExpectedServingDeploymentID == nil || *req.ExpectedServingDeploymentID != expected || req.TrafficPercent != 100 {
						t.Errorf("unguarded promotion: %+v", req)
					}
					if tc.accessAudit != "" {
						if req.ExpectedMCPAccessPolicy == nil || req.ExpectedMCPAccessPolicy.Resource != "https://my-mcp.example/mcp" || req.ExpectedMCPAccessPolicy.Validate() != nil {
							t.Errorf("promotion missing policy expectation: %+v", req)
						}
					} else if req.ExpectedMCPAccessPolicy != nil {
						t.Error("legacy promotion gained policy guard")
					}
					if tc.accessAudit == "changed" {
						api.WriteProblem(w, api.ErrMCPAccessPolicyChanged())
						return
					}
					promotions++
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "candidate", TrafficPercent: 100})
				default:
					t.Errorf("unexpected control request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer control.Close()
			var roles []mcpReleaseRole
			if tc.drift {
				baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{}})
				roles = []mcpReleaseRole{{Name: "reader", contract: baseline}}
			}
			cfg := mcphosting.Config{Endpoint: "/mcp", Auth: mcphosting.AuthConfig{Mode: "open"}}
			token := ""
			if tc.authorization {
				token = "ROLE_TOKEN_SENTINEL"
				cfg.Auth = mcphosting.AuthConfig{Mode: "external-oauth", Issuer: "https://identity.example", JWKSURL: "https://identity.example/jwks", Resource: "https://my-mcp.example/mcp", Scopes: []string{"mcp:tools"}}
				baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{{Name: "add", InputSchema: map[string]any{"type": "object"}}}})
				roles = []mcpReleaseRole{{Name: "reader", TokenEnv: "ROLE_TOKEN_REF", token: token, contract: baseline, Authorization: &mcpReleaseAuthorization{AuthorizationExpectations: mcphosting.AuthorizationExpectations{DeniedTools: []string{"write"}}, insufficientToken: "SCOPE_TOKEN_SENTINEL"}}}
			}
			var audit *mcpAccessAuditOptions
			if tc.accessAudit != "" {
				audit = &mcpAccessAuditOptions{AuthPlacement: "gateway"}
				if tc.accessAudit == "guest-only" {
					audit.AuthPlacement = "guest-only"
				}
			}
			var smoke *mcpSmokeGate
			if strings.HasPrefix(tc.name, "smoke fixture") {
				expected := json.RawMessage(`{"content":[]}`)
				if tc.name == "smoke fixture failure" {
					expected = json.RawMessage(`{"content":[{"type":"text","text":"expected"}]}`)
				}
				smoke = &mcpSmokeGate{allowTools: true, suite: mcphosting.SmokeSuite{Version: 1, Cases: []mcphosting.SmokeCase{{Name: "addition", Kind: "tool", Target: "add", ExpectedResult: expected}}}}
			}
			if got := finishMCPDeployWithSmoke(context.Background(), NewClient(control.URL, "operator-only"), "my-mcp", "candidate", cfg, token, roles, audit, smoke); got != tc.want {
				t.Fatalf("exit=%d want=%d output=%s", got, tc.want, output.String())
			}
			if tc.accessAudit == "changed" && !strings.Contains(output.String(), api.CodeMCPAccessPolicyChanged) {
				t.Fatal("policy conflict not reported in receipt")
			}
			if strings.Contains(output.String(), "TOKEN_SENTINEL") || strings.Contains(output.String(), "PRIVATE_BODY_SENTINEL") {
				t.Fatal("authorization receipt leaked credentials or private body")
			}
			expectedCalls := 0
			if smoke != nil {
				expectedCalls = 1
			}
			if calls != expectedCalls || (tc.want != 0 && promotions != 0) || (!tc.first && updates != 0) {
				t.Fatalf("calls=%d promotions=%d app mutations=%d", calls, promotions, updates)
			}
		})
	}
}

func TestMCPReleasePolicyPreflight(t *testing.T) {
	dir := t.TempDir()
	baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{}})
	body, _ := mcphosting.MarshalContract(baseline)
	if err := os.WriteFile(filepath.Join(dir, "reader.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_READER_TOKEN", "reader-secret")
	policy := filepath.Join(dir, "release.json")
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"version":1,"roles":[{"name":"reader","token_env":"MCP_READER_TOKEN","baseline":"reader.json"}]}`, true},
		{`{"version":1,"roles":[{"name":"reader","baseline":"../reader.json"}]}`, false},
		{`{"version":1,"roles":[{"name":"reader","baseline":"reader.json"},{"name":"reader","baseline":"reader.json"}]}`, false},
		{`{"version":1,"roles":[{"name":"reader","token":"secret","baseline":"reader.json"}]}`, false},
	} {
		if err := os.WriteFile(policy, []byte(tc.body), 0600); err != nil {
			t.Fatal(err)
		}
		roles, err := loadMCPReleasePolicy(policy)
		if (err == nil) != tc.valid {
			t.Fatalf("roles=%v err=%v", roles, err)
		}
	}
}

func TestMCPTaskDefaultsMatchAPI(t *testing.T) {
	body, err := os.ReadFile("templates/mcp-node/task-limits.json")
	if err != nil {
		t.Fatal(err)
	}
	var defaults struct {
		MaxRunning             int
		MaxRunningPerOwner     int
		MaxOutstanding         int
		MaxOutstandingPerOwner int
	}
	if err := json.Unmarshal(body, &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.MaxOutstanding != api.MCPTaskDefaultMaxOutstanding || defaults.MaxOutstandingPerOwner != api.MCPTaskDefaultMaxOutstandingPerOwner {
		t.Fatalf("starter defaults drifted: %+v", defaults)
	}
}
