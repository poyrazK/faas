package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestRunMCPResourceWatchReadsAfterNotification(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	const uri = "file:///reports/current"
	initial := json.RawMessage(`{"resultType":"complete","contents":[{"uri":"file:///reports/current","mimeType":"text/plain","text":"before"}]}`)
	var listens, reads atomic.Int32
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
				"params": map[string]any{"notifications": map[string]any{"resourceSubscriptions": []string{uri}}},
			})
			w.(http.Flusher).Flush()
			if listenNumber == 1 {
				writeMCPWatchSSE(t, w, map[string]any{
					"jsonrpc": "2.0", "method": string(mcphosting.CatalogResourceUpdated),
					"params": map[string]any{"uri": uri},
				})
				w.(http.Flusher).Flush()
				return
			}
			secondListen <- struct{}{}
			<-r.Context().Done()
		case "resources/read":
			reads.Add(1)
			writeWatchRPCResult(w, request.ID, `{"resultType":"complete","contents":[{"uri":"file:///reports/current","mimeType":"text/plain","text":"after"}]}`)
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
	go func() { done <- runMCPResourceWatch(ctx, client, uri, initial, time.Hour) }()
	select {
	case <-secondListen:
		cancel()
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("resource watch did not reopen its subscription after an update")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || reads.Load() != 1 {
			t.Fatalf("run error=%v resource reads=%d", err, reads.Load())
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("resource watch did not stop after context cancellation")
	}
	var event mcpResourceWatchEvent
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event); err != nil || event.Event != "resource_updated" || event.Trigger != "notification" || event.URI != uri || !bytes.Contains(event.Result, []byte(`"after"`)) {
		t.Fatalf("event=%+v err=%v output=%s", event, err, output.String())
	}
}

func TestRecordMCPResourceWatchUpdateIgnoresJSONKeyOrder(t *testing.T) {
	current := json.RawMessage(`{"resultType":"complete","contents":[{"uri":"file:///x","text":"same"}]}`)
	next := json.RawMessage(`{"contents":[{"text":"same","uri":"file:///x"}],"resultType":"complete"}`)
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	updated, err := recordMCPResourceWatchUpdate(current, next, "file:///x", "poll")
	if err != nil || !bytes.Equal(updated, next) || output.Len() != 0 {
		t.Fatalf("updated=%s err=%v output=%s", updated, err, output.String())
	}
}
