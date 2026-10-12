package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRuntimeCatalogToolLifecycleNotifiesSubscribers(t *testing.T) {
	catalog := newMCPCatalog()
	updates, unsubscribe := catalog.subscribe()
	defer unsubscribe()
	handler := MCPToolHandler(func(context.Context, map[string]any, *MCPRequestContext) (map[string]any, error) {
		return map[string]any{"content": []any{}}, nil
	})
	definition := map[string]any{"name": "lookup", "inputSchema": map[string]any{"type": "object"}}
	if err := catalog.register("tool", "", definition, handler, nil, nil, false, true); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("registered tool did not publish a catalog change")
	}
	if len(catalog.list("tool")) != 1 {
		t.Fatal("registered tool was not visible or no change was published")
	}
	if err := catalog.register("tool", "lookup", map[string]any{"name": "lookup", "description": "updated"}, handler, nil, nil, true, true); err != nil {
		t.Fatalf("replace tool: %v", err)
	}
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("replaced tool did not publish a catalog change")
	}
	if !catalog.remove("tool", "lookup") {
		t.Fatal("registered tool was not removed")
	}
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("removed tool did not publish a catalog change")
	}
	if catalog.remove("tool", "lookup") {
		t.Fatal("removed tool was removed twice")
	}
}

func TestRuntimeCatalogMatchesResourceTemplateVariables(t *testing.T) {
	catalog := newMCPCatalog()
	handler := MCPResourceHandler(func(context.Context, string, map[string]string, *MCPRequestContext) ([]map[string]any, error) {
		return []map[string]any{}, nil
	})
	definition := map[string]any{
		"name":        "item",
		"uriTemplate": "catalog://items/{itemID}",
		"mimeType":    "application/json",
	}
	if err := catalog.register("resource", "", definition, nil, handler, nil, false, false); err != nil {
		t.Fatalf("register resource: %v", err)
	}
	entry, values, ok := catalog.resource("catalog://items/abc")
	if !ok || entry.resource == nil || values["itemID"] != "abc" {
		t.Fatalf("resource template did not match: ok=%v values=%v", ok, values)
	}
	if _, _, ok := catalog.resource("catalog://items/a/b"); ok {
		t.Fatal("resource template matched more than one URI segment")
	}
}

func TestRuntimeCatalogContractFingerprintIsStableAndRevisioned(t *testing.T) {
	left := newMCPCatalog()
	right := newMCPCatalog()
	first, _, err := left.contractFingerprint()
	if err != nil || first != 0 {
		t.Fatalf("initial fingerprint revision=%d err=%v", first, err)
	}
	handler := MCPToolHandler(func(context.Context, map[string]any, *MCPRequestContext) (map[string]any, error) {
		return map[string]any{"content": []any{}}, nil
	})
	definitionA := map[string]any{"name": "lookup", "description": "Lookup", "inputSchema": map[string]any{"type": "object"}}
	definitionB := map[string]any{"inputSchema": map[string]any{"type": "object"}, "description": "Lookup", "name": "lookup"}
	if err := left.register("tool", "", definitionA, handler, nil, nil, false, false); err != nil {
		t.Fatal(err)
	}
	if err := right.register("tool", "", definitionB, handler, nil, nil, false, false); err != nil {
		t.Fatal(err)
	}
	leftRevision, leftDigest, err := left.contractFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	rightRevision, rightDigest, err := right.contractFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if leftRevision != 1 || rightRevision != 1 || leftDigest != rightDigest {
		t.Fatalf("catalog contracts differ: left=(%d,%s) right=(%d,%s)", leftRevision, leftDigest, rightRevision, rightDigest)
	}
	if !left.remove("tool", "lookup") {
		t.Fatal("registered tool was not removed")
	}
	removedRevision, removedDigest, err := left.contractFingerprint()
	if err != nil || removedRevision != 2 || removedDigest == leftDigest {
		t.Fatalf("removal was not reflected in contract: revision=%d digest=%s err=%v", removedRevision, removedDigest, err)
	}
}

func TestCatalogContractPinControlsReadinessAndServing(t *testing.T) {
	_, _, digest := runtimeCatalogStatus(mcpConfig{}, nil)
	readyResponse := httptest.NewRecorder()
	handleHTTP(mcpConfig{CatalogExpectedSHA256: digest}, &authenticator{mode: "open"}, nil,
		readyResponse, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if readyResponse.Code != http.StatusOK {
		t.Fatalf("matching catalog pin returned health status %d: %s", readyResponse.Code, readyResponse.Body)
	}
	wrongDigest := "0000000000000000000000000000000000000000000000000000000000000000"
	degradedResponse := httptest.NewRecorder()
	handleHTTP(mcpConfig{CatalogExpectedSHA256: wrongDigest}, &authenticator{mode: "open"}, nil,
		degradedResponse, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if degradedResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("mismatched catalog pin returned health status %d", degradedResponse.Code)
	}
	blockedResponse := httptest.NewRecorder()
	handleHTTP(mcpConfig{Endpoint: "/mcp", CatalogExpectedSHA256: wrongDigest}, &authenticator{mode: "open"}, nil,
		blockedResponse, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if blockedResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("mismatched catalog pin served MCP request with status %d", blockedResponse.Code)
	}
}

// adr: 426 — the modern client requires complete results and private cache metadata.
func TestCatalogHTTPProtocolResultMetadata(t *testing.T) {
	config, err := loadMCPConfig()
	if err != nil {
		t.Fatal(err)
	}
	auth, err := newAuthenticator(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{currentProtocol, legacyProtocol} {
		req := httptest.NewRequest(http.MethodPost, config.Endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("MCP-Protocol-Version", version)
		req.Header.Set("Mcp-Method", "tools/list")
		response := httptest.NewRecorder()
		handleHTTP(config, auth, nil, response, req)
		if response.Code != 200 {
			t.Fatalf("status %d: %s", response.Code, response.Body.String())
		}
		var wire struct {
			Result map[string]any `json:"result"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		if version == currentProtocol {
			if wire.Result["resultType"] != "complete" || wire.Result["ttlMs"] != float64(0) || wire.Result["cacheScope"] != "private" {
				t.Fatalf("modern metadata: %#v", wire.Result)
			}
		} else if _, ok := wire.Result["resultType"]; ok {
			t.Fatalf("modern metadata leaked into legacy catalog: %#v", wire.Result)
		}
	}
}
