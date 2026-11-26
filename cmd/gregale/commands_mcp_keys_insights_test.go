package main_test

// adr: 20261123110137 — MCP insights and keys from the CLI, and the
// deploy-time replay gate.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPInsightsJSON(t *testing.T) {
	t.Setenv("FAAS_MCP_GATEWAY_PREVIEW_ENABLED", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/shop/mcp-tool-insights" {
			http.NotFound(w, r)
			return
		}
		main.WriteJSONTest(w, api.MCPToolInsightsResponse{App: "shop", Window: "1h", Telemetry: api.MCPToolInsightsTelemetryReporting, MemoryMB: 512,
			Tools: []api.MCPToolInsight{{Tool: "search", Calls: 4, EstimatedMillicents: 12}}})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	out, stderr, restore := main.SwapIO(t)
	defer restore()
	if code := main.Run([]string{"--json", "mcp", "insights", "--app", "shop"}); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, stderr())
	}
	var got api.MCPToolInsightsResponse
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil || len(got.Tools) != 1 || got.Tools[0].Tool != "search" {
		t.Fatalf("output = %q (%v)", out.String(), err)
	}
}

func TestMCPInsightsAndKeysHiddenWithoutPreview(t *testing.T) {
	t.Setenv("FAAS_MCP_GATEWAY_PREVIEW_ENABLED", "")
	_, _, restore := main.SwapIO(t)
	defer restore()
	for _, sub := range []string{"insights", "keys"} {
		if code := main.Run([]string{"mcp", sub, "--app", "shop"}); code == 0 {
			t.Errorf("mcp %s ran without the preview", sub)
		}
	}
}

func TestMCPKeysCreateNewConsumer(t *testing.T) {
	t.Setenv("FAAS_MCP_GATEWAY_PREVIEW_ENABLED", "1")
	var keyReq api.CreateConsumerKeyRequest
	var consumerReq api.CreateAPIConsumerRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/apps/shop/consumers":
			_ = json.NewDecoder(r.Body).Decode(&consumerReq)
			main.WriteJSONTest(w, api.APIConsumerResponse{ID: "c-1", Name: consumerReq.Name})
		case "POST /v1/apps/shop/consumers/c-1/keys":
			_ = json.NewDecoder(r.Body).Decode(&keyReq)
			main.WriteJSONTest(w, api.ConsumerKeyResponse{ID: "k-1", Prefix: "ck_abc", Scopes: keyReq.Scopes, Key: "ck_abc_secret"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	out, stderr, restore := main.SwapIO(t)
	defer restore()
	if code := main.Run([]string{"mcp", "keys", "create", "--app", "shop", "--new-consumer", "acme", "--name", "prod", "--scopes", "tools, mcp:admin"}); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, stderr())
	}
	if consumerReq.ExternalRef != "acme" || !slices.Equal(keyReq.Scopes, []string{"mcp:tools", "mcp:admin"}) || keyReq.Name != "prod" {
		t.Fatalf("consumer=%+v key=%+v", consumerReq, keyReq)
	}
	if !strings.Contains(out.String(), "ck_abc_secret") {
		t.Fatalf("the new key must be printed once: %q", out.String())
	}
}

func TestMCPKeysCreateNeedsOneConsumer(t *testing.T) {
	t.Setenv("FAAS_MCP_GATEWAY_PREVIEW_ENABLED", "1")
	t.Setenv("FAAS_TOKEN", "test-token")
	_, _, restore := main.SwapIO(t)
	defer restore()
	for _, args := range [][]string{
		{"mcp", "keys", "create", "--app", "shop", "--name", "k", "--scopes", "tools"},
		{"mcp", "keys", "create", "--app", "shop", "--name", "k", "--scopes", "tools", "--consumer", "c-1", "--new-consumer", "x"},
		{"mcp", "keys", "create", "--app", "shop", "--name", "k", "--scopes", "bad scope", "--consumer", "c-1"},
	} {
		if code := main.Run(args); code == 0 {
			t.Errorf("%v succeeded", args)
		}
	}
}

func TestConsumerKeyCreateAcceptsMCPScopes(t *testing.T) {
	var req api.CreateConsumerKeyRequest
	withConsumersTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(api.ConsumerKeyResponse{ID: "k", Scopes: req.Scopes})
	})
	if code := main.CmdConsumers([]string{"key-create", "shop", "c1", "--name", "k", "--scopes", "mcp:tools"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !slices.Equal(req.Scopes, []string{"mcp:tools"}) {
		t.Fatalf("scopes = %v", req.Scopes)
	}
}

func TestMCPReplayGate(t *testing.T) {
	t.Setenv("FAAS_MCP_GATEWAY_PREVIEW_ENABLED", "")
	if _, err := main.WithMCPReplayGate(nil, true); err == nil {
		t.Fatal("--replay must need the preview")
	}
	if gate, err := main.WithMCPReplayGate(nil, false); err != nil || gate != nil {
		t.Fatal("no --replay must leave the gate untouched")
	}
	t.Setenv("FAAS_MCP_GATEWAY_PREVIEW_ENABLED", "1")
	gate, err := main.WithMCPReplayGate(nil, true)
	if err != nil || gate == nil {
		t.Fatalf("gate=%v err=%v", gate, err)
	}
	if skipped, ok := main.RunMCPReplayGateForTest(t.Context(), "https://app.example/mcp", "https://candidate.example/mcp", "", gate); !ok || skipped == "" {
		t.Fatal("a first deployment has nothing to compare and must pass as skipped")
	}
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	if _, ok := main.RunMCPReplayGateForTest(t.Context(), down.URL+"/mcp", down.URL+"/mcp", "serving-1", gate); ok {
		t.Fatal("an unreadable deployment must block promotion")
	}
}
