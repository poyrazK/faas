package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPWatchReportsDriftAndRecovery(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })

	baselineCatalog := mcphosting.Catalog{
		Capabilities: []string{"tools"},
		Tools:        []mcphosting.Tool{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}},
	}
	baseline, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, baselineCatalog)
	if err != nil {
		t.Fatal(err)
	}
	driftCatalog := baselineCatalog
	driftCatalog.Tools = []mcphosting.Tool{{Name: "lookup", Description: "Changed description", InputSchema: map[string]any{"type": "object"}}}
	if _, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, driftCatalog); err != nil {
		t.Fatal(err)
	}

	gotCatalog, gotContract, err := recordMCPWatchUpdate(baseline, baseline, baselineCatalog, driftCatalog, "notification")
	if err != nil {
		t.Fatal(err)
	}
	var appeared mcpWatchEvent
	if err := json.Unmarshal(output.Bytes(), &appeared); err != nil || appeared.Event != "catalog_drift" || appeared.Trigger != "notification" || len(appeared.Diff.Changes) == 0 {
		t.Fatalf("event=%+v err=%v output=%s", appeared, err, output.String())
	}
	output.Reset()
	_, _, err = recordMCPWatchUpdate(baseline, gotContract, gotCatalog, baselineCatalog, "reconcile")
	if err != nil {
		t.Fatal(err)
	}
	var cleared mcpWatchEvent
	if err := json.Unmarshal(output.Bytes(), &cleared); err != nil || cleared.Event != "catalog_drift_cleared" || cleared.Trigger != "reconcile" || len(cleared.Diff.Changes) != 0 {
		t.Fatalf("event=%+v err=%v output=%s", cleared, err, output.String())
	}
}

func TestMCPWatchCapabilityRefreshPreservesUnselectedCategories(t *testing.T) {
	got := updateMCPWatchCapabilityNames([]string{"prompts", "resources"}, mcphosting.ServerCapabilities{
		Tools: &mcphosting.ToolsCapability{},
	}, mcphosting.CatalogNotificationFilter{ToolsListChanged: true})
	want := []string{"prompts", "resources", "tools"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities=%v, want %v", got, want)
	}
}

func TestRunMCPWatchRefreshesOnNotificationAndReconnects(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	baselineCatalog := mcphosting.Catalog{
		Capabilities: []string{"tools"},
		Tools:        []mcphosting.Tool{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}},
	}
	baseline, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, baselineCatalog)
	if err != nil {
		t.Fatal(err)
	}
	var listens, toolLists atomic.Int32
	secondListen := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MCP request: %v", err)
			return
		}
		switch request.Method {
		case "subscriptions/listen":
			listenNumber := listens.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			writeMCPWatchSSE(t, w, map[string]any{
				"jsonrpc": "2.0", "method": "notifications/subscriptions/acknowledged",
				"params": map[string]any{"notifications": map[string]bool{"toolsListChanged": true}},
			})
			w.(http.Flusher).Flush()
			if listenNumber == 1 {
				writeMCPWatchSSE(t, w, map[string]any{"jsonrpc": "2.0", "method": string(mcphosting.CatalogToolsListChanged), "params": map[string]any{}})
				w.(http.Flusher).Flush()
				return
			}
			secondListen <- struct{}{}
			<-r.Context().Done()
		case "server/discover":
			writeWatchRPCResult(w, request.ID, fmt.Sprintf(`{"supportedVersions":[%q],"capabilities":{"tools":{}}}`, mcphosting.ProtocolVersion))
		case "tools/list":
			toolLists.Add(1)
			writeWatchRPCResult(w, request.ID, `{"tools":[{"name":"lookup","description":"Updated catalog entry","inputSchema":{"type":"object"}}]}`)
		default:
			t.Errorf("unexpected MCP method %q", request.Method)
		}
	}))
	defer server.Close()
	client, err := mcphosting.NewClient(server.URL+"/mcp", "", mcphosting.ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runMCPWatch(ctx, client, baseline, baselineCatalog, baseline, mcphosting.CatalogNotificationFilter{ToolsListChanged: true}, time.Hour)
	}()
	select {
	case <-secondListen:
		cancel()
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("watch did not reopen its subscription after a catalog notification")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || toolLists.Load() != 1 {
			t.Fatalf("run error=%v tool list calls=%d", err, toolLists.Load())
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("watch did not stop after context cancellation")
	}
	var event mcpWatchEvent
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event); err != nil || event.Event != "catalog_drift" || event.Trigger != string(mcphosting.CatalogToolsListChanged) || len(event.Diff.Changes) == 0 {
		t.Fatalf("event=%+v err=%v output=%s", event, err, output.String())
	}
}

func writeWatchRPCResult(w http.ResponseWriter, id int, result string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, id, result)
}

func writeMCPWatchSSE(t *testing.T, w http.ResponseWriter, message any) {
	t.Helper()
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Errorf("encode SSE message: %v", err)
		return
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", encoded); err != nil {
		t.Errorf("write SSE message: %v", err)
	}
}
