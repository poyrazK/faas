package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type monitorOutput struct {
	bytes.Buffer
	writes int
	cancel context.CancelFunc
}

func (w *monitorOutput) Write(data []byte) (int, error) {
	n, err := w.Buffer.Write(data)
	w.writes++
	if w.writes == 2 {
		w.cancel()
	}
	return n, err
}
func TestMCPCatalogMonitorReadsOnlyAndRecovers(t *testing.T) {
	baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools", "resources", "prompts"}, Tools: []mcphosting.Tool{{Name: "reviewed", InputSchema: map[string]any{"type": "object"}}}})
	raw, _ := mcphosting.MarshalContract(baseline)
	calls := map[string]int{}
	samples := 0
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer MCP_PRIVATE_TOKEN" {
			t.Error("wrong source credential")
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		calls[request.Method]++
		if request.Method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			samples++
			result = map[string]any{"protocolVersion": mcphosting.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}, "resources": map[string]any{}, "prompts": map[string]any{}}, "serverInfo": map[string]string{"name": "monitor", "version": "1"}}
		case "server/discover":
			result = map[string]any{"supportedVersions": []string{mcphosting.ProtocolVersion}, "capabilities": map[string]any{"tools": map[string]any{}, "resources": map[string]any{}, "prompts": map[string]any{}}}
		case "tools/list":
			tools := []mcphosting.Tool{}
			if samples >= 3 {
				tools = baseline.Tools
			}
			result = map[string]any{"tools": tools}
		case "resources/list":
			result = map[string]any{"resources": []any{}}
		case "resources/templates/list":
			result = map[string]any{"resourceTemplates": []any{}}
		case "prompts/list":
			result = map[string]any{"prompts": []any{}}
		default:
			t.Errorf("mutating or unexpected method: %s", request.Method)
			http.Error(w, "PRIVATE_PROBE_ERROR", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer source.Close()
	reports := []api.MCPCatalogObservationRequest{}
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/apps/reports/mcp-catalog-monitor/observations" || r.Header.Get("Authorization") != "Bearer PLATFORM_PRIVATE_TOKEN" {
			t.Error("wrong reporting boundary")
		}
		var req api.MCPCatalogObservationRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		reports = append(reports, req)
		id, state := "drift-receipt", "drift"
		if len(reports) >= 3 {
			id, state = "recovery-receipt", "healthy"
		}
		writeJSONTest(w, api.MCPCatalogReceipt{Role: req.Role, ID: id, State: state, Revision: "reviewed"})
	}))
	defer platform.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	output := &monitorOutput{cancel: cancel}
	oldOut, oldJSON := osStdout, jsonOutput
	osStdout = output
	jsonOutput = true
	defer func() { osStdout = oldOut; jsonOutput = oldJSON }()
	err := runMCPCatalogMonitor(ctx, NewClient(platform.URL, "PLATFORM_PRIVATE_TOKEN"), "reports", source.URL+"/mcp", "MCP_PRIVATE_TOKEN", api.MCPCatalogMonitorConfig{Role: "reader", TokenEnv: "MCP_READER_TOKEN", Revision: "reviewed", Baseline: raw}, time.Millisecond, 200*time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}
	if len(reports) != 3 || output.writes != 2 || calls["resources/list"] != 3 || calls["prompts/list"] != 3 {
		t.Fatalf("incomplete checks or duplicate events: %d %d %+v", len(reports), output.writes, calls)
	}
	for _, report := range reports {
		if report.Role != "reader" || report.Revision != "reviewed" || report.Unavailable || bytes.Contains(report.Observed, []byte("PRIVATE_TOKEN")) {
			t.Fatal("invalid or leaking observation")
		}
	}
	if bytes.Contains(output.Bytes(), []byte("PRIVATE_")) {
		t.Fatal("private source data leaked")
	}
}
func TestMCPCatalogMonitorProbeFailureIsReported(t *testing.T) {
	baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools"}, Tools: []mcphosting.Tool{}})
	raw, _ := mcphosting.MarshalContract(baseline)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "PRIVATE_UPSTREAM_ERROR", 500) }))
	defer source.Close()
	reports := 0
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req api.MCPCatalogObservationRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		reports++
		if !req.Unavailable || len(req.Observed) != 0 {
			t.Error("failed probe reported healthy")
		}
		w.WriteHeader(409)
	}))
	defer platform.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err := runMCPCatalogMonitor(ctx, NewClient(platform.URL, "platform"), "reports", source.URL, "", api.MCPCatalogMonitorConfig{Revision: "reviewed", Baseline: raw}, time.Millisecond, 200*time.Millisecond)
	if err == nil || reports != 1 {
		t.Fatal("report conflict did not stop stale observer")
	}
}

func TestMCPCatalogMonitorTokenReference(t *testing.T) {
	cases := []struct {
		role, configured, override, want string
		fail                             bool
	}{
		{"reader", "MCP_READER", "", "MCP_READER", false},
		{"reader", "MCP_READER", "MCP_READER", "MCP_READER", false},
		{"reader", "MCP_READER", "MCP_ADMIN", "", true},
		{"reader", "", "MCP_ADMIN", "", true},
		{"reader", "", "", "", false},
		{"", "", "LEGACY_TOKEN", "LEGACY_TOKEN", false},
	}
	for _, tc := range cases {
		got, err := monitorTokenEnv(api.MCPCatalogMonitorConfig{Role: tc.role, TokenEnv: tc.configured}, tc.override)
		if got != tc.want || (err != nil) != tc.fail {
			t.Fatalf("%+v: got %q %v", tc, got, err)
		}
	}
}

func TestMCPCatalogManagedLifecycle(t *testing.T) {
	config := api.MCPCatalogMonitorConfig{Role: "reader", Enabled: true, Revision: "old", FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 10, Endpoint: "/mcp", SecretRef: "secret:READER_TOKEN"}}
	for _, action := range []string{"pause", "resume", "restart", "stop", "disable"} {
		req, err := managedMonitorAction(config, action)
		if err != nil {
			t.Fatal(err)
		}
		if req.Role != "reader" || req.Observer.SecretRef != "secret:READER_TOKEN" || req.Observer == config.Observer {
			t.Fatal("role binding lost or input mutated")
		}
		switch action {
		case "pause":
			if req.Enabled || !req.Observer.Paused || !req.Observer.Enabled {
				t.Fatal("pause still collects")
			}
		case "resume", "restart":
			if !req.Enabled || req.Observer.Paused || !req.Observer.Enabled {
				t.Fatal("resume not requested")
			}
		default:
			if req.Enabled || req.Observer.Enabled || req.Observer.Paused {
				t.Fatal("stop still collects")
			}
		}
	}
	if !config.Enabled || !config.Observer.Enabled || config.Observer.Paused {
		t.Fatal("lifecycle helper mutated saved intent")
	}
	if _, err := managedMonitorAction(api.MCPCatalogMonitorConfig{}, "resume"); err == nil {
		t.Fatal("manual role accepted managed resume")
	}
}

func TestMCPCatalogOAuthSetup(t *testing.T) {
	baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools"}, Tools: []mcphosting.Tool{}})
	raw, _ := mcphosting.MarshalContract(baseline)
	file := filepath.Join(t.TempDir(), "reviewed.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	expectedRefresh, expectedBrowser := "secret:REFRESH", ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPut || r.URL.Path != "/v1/apps/oauth-app/mcp-catalog-monitor" {
			t.Fatal("unexpected setup request")
		}
		var req api.MCPCatalogMonitorConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Role != "reader" || req.Observer == nil || req.Observer.OAuth == nil || req.Observer.OAuth.RefreshTokenSecretRef != expectedRefresh || req.Observer.OAuth.BrowserRedirectURI != expectedBrowser || req.Observer.OAuth.Guided != (expectedBrowser != "") || req.Observer.OAuth.ClientSecretRef != "secret:CLIENT_SECRET" || req.Observer.SecretRef != "" {
			t.Fatal("OAuth binding lost")
		}
		_ = json.NewEncoder(w).Encode(api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Role: req.Role, Enabled: true, Observer: req.Observer}})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_oauth")
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout = &output
	jsonOutput = true
	t.Cleanup(func() { osStdout = oldOut; jsonOutput = oldJSON })
	args := []string{"setup", "--app", "oauth-app", "--role", "reader", "--managed", "--baseline", file, "--freshness-seconds", "90", "--oauth-token-endpoint", "https://provider.example/token", "--oauth-client-id", "reader", "--oauth-refresh-secret-ref", "secret:REFRESH", "--oauth-client-secret-ref", "secret:CLIENT_SECRET"}
	if code := cmdMCPCatalogMonitor(args); code != 0 || calls != 1 {
		t.Fatalf("OAuth setup failed: %d requests=%d", code, calls)
	}
	// Invalid combinations must fail locally without reaching the platform API.
	if code := cmdMCPCatalogMonitor(append(args, "--secret-ref", "secret:ACCESS")); code == 0 || calls != 1 {
		t.Fatal("mixed credentials submitted")
	}
	if code := cmdMCPCatalogMonitor([]string{"status", "--app", "oauth-app", "--oauth-client-id", "reader"}); code == 0 || calls != 1 {
		t.Fatal("setup-only OAuth flags accepted")
	}
	expectedRefresh, expectedBrowser = "", "https://console.example/dashboard/mcp/oauth/callback"
	var guided []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--oauth-refresh-secret-ref" {
			i++
			continue
		}
		guided = append(guided, args[i])
	}
	guided = append(guided, "--oauth-guided", "--oauth-authorization-endpoint", "https://provider.example/authorize", "--oauth-browser-redirect-uri", expectedBrowser)
	if code := cmdMCPCatalogMonitor(guided); code != 0 || calls != 2 {
		t.Fatal("browser redirect configuration not submitted", code, calls)
	}

}

func TestMCPCatalogOAuthLifecycleOutput(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout = &output
	jsonOutput = false
	t.Cleanup(func() { osStdout = oldOut; jsonOutput = oldJSON })
	snapshot := api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Role: "reader"}, ObserverStatus: &api.MCPCatalogObserverStatus{OAuth: &api.MCPCatalogObserverOAuthStatus{State: "reauthorization_required", ReauthorizationNeeded: true}, Renewal: &api.MCPCatalogOAuthRenewal{State: "reauthorization_required", StartedAt: time.Now()}}, OAuthLifecycle: &api.MCPCatalogOAuthLifecycle{LocalState: "disconnected", CredentialState: "retirement_held", Revocation: &api.MCPCatalogOAuthRevocation{State: "pending", Attempts: 2}, History: []api.MCPCatalogOAuthEvent{{At: time.Now(), Kind: "disconnected"}}}}
	if writeMCPCatalogMonitor(snapshot) != 0 {
		t.Fatal("status failed")
	}
	for _, want := range []string{"OAuth credential recovery: retirement_held", "OAuth renewal recovery: reauthorization_required", "reconnect this role", "Reauthorization needed", "Local OAuth state: disconnected", "Provider revocation: pending (2 attempts)", "OAuth event:"} {
		if !bytes.Contains(output.Bytes(), []byte(want)) {
			t.Fatal("missing lifecycle output", want)
		}
	}
	output.Reset()
	if writeMCPConnection(api.MCPCatalogOAuthResponse{State: "disconnected", ProviderRevocation: "pending"}) != 0 || !bytes.Contains(output.Bytes(), []byte("Provider revocation: pending")) {
		t.Fatal("disconnect omitted provider state")
	}
}
