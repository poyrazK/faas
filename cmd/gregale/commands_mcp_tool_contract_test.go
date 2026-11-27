package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// contractAPI serves the capability view and the tool contract routes.
type contractAPI struct {
	mu        sync.Mutex
	overrides []api.CreateMCPContractOverrideRequest
}

func (a *contractAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	switch {
	case r.URL.Path == "/v1/capabilities":
		_ = json.NewEncoder(w).Encode(map[string]any{"registry_version": 1, "plan": "pro", "capabilities": []any{}, "hidden_capabilities": []string{}})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/my-mcp/mcp-contract":
		_ = json.NewEncoder(w).Encode(api.MCPContractResponse{App: "my-mcp", BaselineSHA256: strings.Repeat("a", 64), ListingVersion: 2,
			Tools: []string{"fetch", "search"}, ProbeSecret: "MCP_PROBE_TOKEN", Checks: []api.MCPContractCheck{{DeploymentID: "dep-1",
				Verdict: "breaking", Overridden: true, CheckedAt: now, AcceptedAt: &now, Changes: []api.MCPContractChange{{Tool: "search",
					Path: "/tools/search", Kind: "tool_removed", Severity: "breaking"}}}},
			Overrides: []api.MCPContractOverride{}})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/my-mcp/mcp-contract/overrides":
		var req api.CreateMCPContractOverrideRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		a.mu.Lock()
		a.overrides = append(a.overrides, req)
		a.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.MCPContractOverride{ID: "ov-1", Reason: req.Reason, CreatedBy: "user:a", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	default:
		http.NotFound(w, r)
	}
}

// adr: 20261127191645 — the seller reads the contract with its checks and
// records the audited override that admits one breaking deploy.
func TestMCPContractCommands(t *testing.T) {
	fake := &contractAPI{}
	server := httptest.NewServer(fake)
	defer server.Close()
	clearCapabilityReleaseAll(t)
	t.Setenv("FAAS_API_MONETIZATION_ENABLED", "")
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))
	main.ResetCapabilityView()

	code, out, errOut := runCLI(t, "mcp", "contract", "show", "--app", "my-mcp")
	if code != 0 || !strings.Contains(out, "aaaaaaaaaaaa (from listing version 2)") || !strings.Contains(out, "fetch, search") ||
		!strings.Contains(out, "breaking   dep-1 (live 2026-11-27T12:00:00Z, overridden)") || !strings.Contains(out, "tool_removed /tools/search") ||
		!strings.Contains(out, "app secret MCP_PROBE_TOKEN") {
		t.Fatalf("show = %d\n%s\n%s", code, out, errOut)
	}

	if code, _, errOut := runCLI(t, "mcp", "contract", "allow", "--app", "my-mcp"); code == 0 || !strings.Contains(errOut, "--reason is required") {
		t.Fatalf("allow-breaking without reason = %d %q", code, errOut)
	}
	if code, _, errOut := runCLI(t, "mcp", "contract", "allow", "--app", "my-mcp", "--reason", "x", "--expires", "48h"); code == 0 ||
		!strings.Contains(errOut, "--expires") {
		t.Fatalf("allow-breaking 48h = %d %q", code, errOut)
	}
	code, out, errOut = runCLI(t, "mcp", "contract", "allow", "--app", "my-mcp", "--reason", "rename search", "--expires", "10m")
	if code != 0 || !strings.Contains(out, "override ov-1") {
		t.Fatalf("allow-breaking = %d\n%s\n%s", code, out, errOut)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.overrides) != 1 || fake.overrides[0].Reason != "rename search" || fake.overrides[0].ExpiresInSeconds != 600 {
		t.Fatalf("override requests = %+v", fake.overrides)
	}
}

// --allow-breaking and --reason go together, before anything is uploaded.
func TestMCPDeployAllowBreakingNeedsReason(t *testing.T) {
	for _, args := range [][]string{{"--allow-breaking"}, {"--reason", "rename"}} {
		code, _, errOut := runCLI(t, append([]string{"mcp", "deploy", "--name", "my-mcp"}, args...)...)
		if code == 0 || !strings.Contains(errOut, "--allow-breaking and --reason go together") {
			t.Errorf("%v = %d %q", args, code, errOut)
		}
	}
}

// A republish keeps the probe secret unless --probe-secret is given.
func TestMCPListingKeepProbeSecret(t *testing.T) {
	current := api.MCPListingResponse{Title: "Search", ProbeSecret: "MCP_PROBE_TOKEN"}
	if got := main.MCPListingKeep(api.PutMCPListingRequest{}, current, map[string]bool{}); got.ProbeSecret != "MCP_PROBE_TOKEN" {
		t.Fatalf("kept = %+v", got)
	}
	if got := main.MCPListingKeep(api.PutMCPListingRequest{}, current, map[string]bool{"probe-secret": true}); got.ProbeSecret != "" {
		t.Fatalf("cleared = %+v", got)
	}
}
