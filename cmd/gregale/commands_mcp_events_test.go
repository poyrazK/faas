package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const mcpEventRequest = "b9ddc41d-60c6-4f22-8d16-f6c3248c0453"
const mcpEventFixture = `{"event_version":1,"event":"mcp_request","request_id":"` + mcpEventRequest + `","protocol":"2026-07-28","rpc_method":"tools/call","tool":"add","outcome":"denied","reason":"insufficient_scope","http_status":403,"duration_ms":2,"token":"private-token","arguments":{"name":"private-input"}}`

func mcpEventOutput(t *testing.T) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	var output, stderr bytes.Buffer
	osStdout, osStderr, jsonOutput = &output, &stderr, true
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	return &output, &stderr
}

func writeMCPLog(w http.ResponseWriter, line string) {
	data, _ := json.Marshal(api.LogEvent{Line: line})
	_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
}

func TestMCPEventsCLIReadAndFilters(t *testing.T) {
	output, _ := mcpEventOutput(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/v1/apps/my-mcp/logs" || r.Header.Get("Authorization") != "Bearer operator-token" {
			t.Errorf("wrong control-plane request %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("follow") != "0" || q.Get("deployment") != "dep-1" || q.Get("grep") != "mcp_" {
			t.Errorf("query=%v", q)
		}
		if _, err := time.Parse(time.RFC3339Nano, q.Get("since")); err != nil {
			t.Errorf("since=%s", q.Get("since"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeMCPLog(w, "private-unstructured-log")
		writeMCPLog(w, `{"event":"mcp_tool_call","tool":"add"}`) // Old, uncorrelated logs are skipped.
		writeMCPLog(w, strings.ReplaceAll(mcpEventFixture, `"tool":"add"`, `"tool":"greet"`))
		writeMCPLog(w, strings.ReplaceAll(mcpEventFixture, `"outcome":"denied"`, `"outcome":"success"`))
		writeMCPLog(w, strings.ReplaceAll(mcpEventFixture, mcpEventRequest, "b9ddc41d-60c6-4f22-8d16-f6c3248c0454"))
		writeMCPLog(w, mcpEventFixture)
		_, _ = fmt.Fprint(w, "event: heartbeat\ndata: private-heartbeat\n\nevent: end\ndata: {}\n\n")
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "operator-token")
	args := []string{"events", "--app", "my-mcp", "--tool", "add", "--outcome", "denied", "--request", mcpEventRequest, "--since", "15m", "--deployment", "dep-1"}
	if code := cmdMCP(args); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if hits.Load() != 1 || strings.Count(output.String(), "\n") != 1 || strings.Contains(output.String(), "private-") {
		t.Fatalf("unsafe output %s; hits=%d", output, hits.Load())
	}
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil || event["reason"] != "insufficient_scope" {
		t.Fatalf("event=%v err=%v", event, err)
	}
	output.Reset()
	jsonOutput = false
	if code := cmdMCP(args); code != 0 || !strings.Contains(output.String(), "request="+mcpEventRequest) {
		t.Fatalf("human output %s exit=%d", output, code)
	}
}

func TestMCPEventsPartialStreams(t *testing.T) {
	for _, terminal := range []string{"gap", "error", "degraded", "timeout", "EOF"} {
		t.Run(terminal, func(t *testing.T) {
			output, stderr := mcpEventOutput(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				writeMCPLog(w, mcpEventFixture)
				switch terminal {
				case "EOF":
					return
				case "timeout":
					_, _ = fmt.Fprint(w, "event: end\ndata: {\"reason\":\"timeout\"}\n\n")
				default:
					_, _ = fmt.Fprintf(w, "event: %s\ndata: {\"reason\":\"private-frame-data\"}\n\nevent: end\ndata: {}\n\n", terminal)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "operator-token")
			if code := cmdMCPEvents([]string{"--app", "my-mcp"}); code != 3 {
				t.Fatalf("partial exit=%d", code)
			}
			if output.Len() == 0 || stderr.Len() == 0 || strings.Contains(output.String()+stderr.String(), "private-") {
				t.Fatalf("unsafe or silent output=%s stderr=%s", output, stderr)
			}
		})
	}
}

func TestMCPEventsPreflightAndCancellation(t *testing.T) {
	mcpEventOutput(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Query().Get("follow") != "1" {
			t.Error("missing follow")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "operator-token")
	for _, args := range [][]string{{"--outcome", "private-outcome"}, {"--request", "forged-id"}, {"--tool", "bad\nname"}, {"--since", "yesterday"}, {"--url", "https://customer.example/mcp"}, {"unexpected"}} {
		if code := cmdMCPEvents(append([]string{"--app", "my-mcp"}, args...)); code == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("invalid flags reached API")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if code := runMCPEvents(ctx, "my-mcp", "", "", true, mcpEventFilter{}); code != 130 {
		t.Fatalf("cancel exit=%d", code)
	}
}
