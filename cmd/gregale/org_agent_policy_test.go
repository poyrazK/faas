// adr: 20261101225122
package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// `org agent-policy` reads and sets an organization's agent policy, keeping
// the current capabilities unless --capabilities names new ones; `mcp
// connection --org` lists and revokes the organization's agents.
func TestOrgAgentPolicyAndOrgConnections(t *testing.T) {
	main.SetupCLIRegression(t)
	var mu sync.Mutex
	var calls []string
	var sets []api.OrgAgentPolicyRequest
	current := api.OrgAgentPolicyResponse{AgentsAllowed: true, AllowedScopes: []string{api.ScopeDeployWrite, api.ScopeSecretsRequest}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/orgs/acme/agent-policy":
			_ = json.NewEncoder(w).Encode(current)
		case "PUT /v1/orgs/acme/agent-policy":
			var req api.OrgAgentPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AgentsAllowed == nil || req.AllowedScopes == nil {
				t.Errorf("PUT body = %+v, %v", req, err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			sets = append(sets, req)
			_ = json.NewEncoder(w).Encode(api.OrgAgentPolicyResponse{AgentsAllowed: *req.AgentsAllowed, AllowedScopes: *req.AllowedScopes, Revoked: 1})
		case "GET /v1/orgs/acme/agent-connections":
			_ = json.NewEncoder(w).Encode(api.AgentConnectionsResponse{Items: []api.AgentConnectionResponse{
				{ID: "3f2c9a7e-1b4d-4c8e-9f0a-6d5e4c3b2a19", Kind: api.AgentConnectionHostedMCP, ClientName: "Claude", Label: api.MCPLoginKeyLabel, Prefix: "fp_live_ab", Scopes: []string{api.ScopeAppsRead}, CreatedAt: "2026-11-01T11:25:00Z", OrgID: "org-1", Member: "dev@acme.example"},
			}})
		case "DELETE /v1/orgs/acme/agent-connections/3f2c9a7e-1b4d-4c8e-9f0a-6d5e4c3b2a19":
			w.WriteHeader(http.StatusNoContent)
		case "POST /v1/orgs/acme/agent-connections/revoke_all":
			_ = json.NewEncoder(w).Encode(api.AgentConnectionsRevokeAllResponse{Revoked: 3})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))

	if code, out, errOut := main.CapturePreviewRun(t, []string{"org", "agent", "policy", "get", "--org", "acme"}); code != 0 || !strings.Contains(out, "allowed, with deploy:write, secrets:request") {
		t.Fatalf("get exit=%d stdout=%s stderr=%s", code, out, errOut)
	}
	if code, out, errOut := main.CapturePreviewRun(t, []string{"org", "agent", "policy", "set", "--org", "acme", "--agents", "deny"}); code != 0 || !strings.Contains(out, "not allowed") || !strings.Contains(out, "Revoked 1") {
		t.Fatalf("set deny exit=%d stdout=%s stderr=%s", code, out, errOut)
	}
	if code, out, errOut := main.CapturePreviewRun(t, []string{"org", "agent", "policy", "set", "--org", "acme", "--agents", "allow", "--capabilities", ""}); code != 0 || !strings.Contains(out, "read-only") {
		t.Fatalf("set read-only exit=%d stdout=%s stderr=%s", code, out, errOut)
	}
	if code, out, errOut := main.CapturePreviewRun(t, []string{"mcp", "connection", "list", "--org", "acme"}); code != 0 || !strings.Contains(out, "MEMBER") || !strings.Contains(out, "dev@acme.example") {
		t.Fatalf("org list exit=%d stdout=%s stderr=%s", code, out, errOut)
	}
	if code, out, errOut := main.CapturePreviewRun(t, []string{"mcp", "connection", "revoke", "3f2c9a7e-1b4d-4c8e-9f0a-6d5e4c3b2a19", "--org", "acme"}); code != 0 {
		t.Fatalf("org revoke exit=%d stdout=%s stderr=%s", code, out, errOut)
	}
	if code, out, errOut := main.CapturePreviewRun(t, []string{"mcp", "connection", "revoke", "--all", "--org", "acme"}); code != 0 || !strings.Contains(out, "Revoked 3") {
		t.Fatalf("org revoke --all exit=%d stdout=%s stderr=%s", code, out, errOut)
	}
	for _, args := range [][]string{
		{"org", "agent", "policy", "get"},
		{"org", "agent", "policy", "set", "--org", "acme"},
		{"org", "agent", "policy", "set", "--org", "acme", "--agents", "maybe"},
		{"mcp", "connection", "list", "--org", "acme", "--socket", "/tmp/gregale-admin.sock"},
	} {
		if code, _, _ := main.CapturePreviewRun(t, args); code == 0 {
			t.Fatalf("%v succeeded, want a usage error", args)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sets) != 2 || *sets[0].AgentsAllowed || !slices.Equal(*sets[0].AllowedScopes, current.AllowedScopes) || !*sets[1].AgentsAllowed || len(*sets[1].AllowedScopes) != 0 {
		t.Fatalf("policy sets = %+v; want deny keeping the capabilities, then allow read-only", sets)
	}
	want := []string{
		"GET /v1/orgs/acme/agent-policy",
		"GET /v1/orgs/acme/agent-policy", "PUT /v1/orgs/acme/agent-policy",
		"GET /v1/orgs/acme/agent-policy", "PUT /v1/orgs/acme/agent-policy",
		"GET /v1/orgs/acme/agent-connections",
		"DELETE /v1/orgs/acme/agent-connections/3f2c9a7e-1b4d-4c8e-9f0a-6d5e4c3b2a19",
		"POST /v1/orgs/acme/agent-connections/revoke_all",
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}
