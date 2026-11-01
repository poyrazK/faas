// adr: 20261030133644
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

// Without --socket, `mcp connection` lists and revokes the agents connected
// to the signed-in account.
func TestMCPConnectionListsAndRevokesConnectedAgents(t *testing.T) {
	main.SetupCLIRegression(t)
	var mu sync.Mutex
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/agent-connections":
			_ = json.NewEncoder(w).Encode(api.AgentConnectionsResponse{Items: []api.AgentConnectionResponse{
				{ID: "3f2c9a7e-1b4d-4c8e-9f0a-6d5e4c3b2a19", Kind: api.AgentConnectionHostedMCP, ClientName: "Claude", ClientSite: "claude.ai", Label: api.MCPLoginKeyLabel, Prefix: "fp_live_ab", Scopes: []string{api.ScopeAppsRead}, CreatedAt: "2026-10-30T11:25:00Z", LastUsedAt: "2026-10-30T12:05:00Z"},
				{ID: "8d1e2f3a-4b5c-4d6e-8f7a-9b0c1d2e3f4a", Kind: api.AgentConnectionAgentKey, Label: "ci-agent", Prefix: "fp_live_cd", Scopes: []string{api.ScopeAgent}, CreatedAt: "2026-10-29T09:00:00Z"},
			}})
		case "DELETE /v1/agent-connections/3f2c9a7e-1b4d-4c8e-9f0a-6d5e4c3b2a19":
			w.WriteHeader(http.StatusNoContent)
		case "POST /v1/agent-connections/revoke_all":
			_ = json.NewEncoder(w).Encode(api.AgentConnectionsRevokeAllResponse{Revoked: 2})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))

	code, out, errOut := main.CapturePreviewRun(t, []string{"mcp", "connection", "list"})
	if code != 0 || !strings.Contains(out, "Claude (claude.ai)") || !strings.Contains(out, "hosted MCP") || !strings.Contains(out, "ci-agent") {
		t.Fatalf("list exit=%d stdout=%s stderr=%s", code, out, errOut)
	}
	if code, out, errOut := main.CapturePreviewRun(t, []string{"mcp", "connection", "revoke", "3f2c9a7e-1b4d-4c8e-9f0a-6d5e4c3b2a19"}); code != 0 {
		t.Fatalf("revoke exit=%d stdout=%s stderr=%s", code, out, errOut)
	}
	if code, out, errOut := main.CapturePreviewRun(t, []string{"mcp", "connection", "revoke", "--all"}); code != 0 || !strings.Contains(out, "Revoked 2") {
		t.Fatalf("revoke --all exit=%d stdout=%s stderr=%s", code, out, errOut)
	}
	for _, args := range [][]string{{"mcp", "connection", "revoke"}, {"mcp", "connection", "revoke", "--all", "3f2c9a7e-1b4d-4c8e-9f0a-6d5e4c3b2a19"}, {"mcp", "connection", "list", "extra"}} {
		if code, _, _ := main.CapturePreviewRun(t, args); code == 0 {
			t.Fatalf("%v succeeded, want a usage error", args)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"GET /v1/agent-connections", "DELETE /v1/agent-connections/3f2c9a7e-1b4d-4c8e-9f0a-6d5e4c3b2a19", "POST /v1/agent-connections/revoke_all"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}
