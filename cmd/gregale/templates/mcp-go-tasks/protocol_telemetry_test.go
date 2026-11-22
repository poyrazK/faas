package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProtocolTelemetryWireOutcomesAndPrivacy(t *testing.T) {
	config, err := loadMCPConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.Auth = json.RawMessage(`{"mode":"open"}`)
	auth, err := newAuthenticator(config)
	if err != nil {
		t.Fatal(err)
	}
	savedWriter, savedFlags := log.Writer(), log.Flags()
	defer log.SetOutput(savedWriter)
	defer log.SetFlags(savedFlags)
	log.SetFlags(0)
	for _, tc := range []struct{ method, params, outcome, tool string }{
		{"tools/call", `{"name":"greet","arguments":{"name":"sensitive-payload"}}`, "success", "greet"},
		{"tools/call", `{"name":"add","arguments":{"a":"sensitive-payload","b":1}}`, "tool_error", "add"},
		{"resources/read", `{"uri":"missing://private-resource"}`, "rpc_error", "none"},
		{"private-method-token", `{}`, "rpc_error", "none"},
		{"tools/call", `{"name":"private-tool-token","arguments":{}}`, "tool_error", "other"},
	} {
		var logs bytes.Buffer
		log.SetOutput(&logs)
		req := httptest.NewRequest(http.MethodPost, config.Endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":"private-rpc-id","method":"`+tc.method+`","params":`+tc.params+`}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Faas-Request-Id", "a1234567-1234-4234-8234-123456789abc")
		req.Header.Set("traceparent", "00-123456789abcdef0123456789abcdef0-123456789abcdef0-01")
		req.Header.Set("Authorization", "Bearer do-not-log-this")
		w := httptest.NewRecorder()
		handleHTTP(config, auth, nil, w, req)
		var event map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &event); err != nil {
			t.Fatalf("decode event: %v, logs=%s", err, logs.String())
		}
		method := protocolMethodLabel(tc.method)
		if event["method"] != method || event["tool"] != tc.tool || event["outcome"] != tc.outcome {
			t.Fatalf("%s: %v", tc.method, event)
		}
		if event["request_id"] != "a1234567-1234-4234-8234-123456789abc" || event["trace_id"] != "123456789abcdef0123456789abcdef0" {
			t.Fatal("missing correlation")
		}
		for _, secret := range []string{"sensitive-payload", "private-rpc-id", "private-method-token", "private-tool-token", "do-not-log-this", "missing://private-resource"} {
			if strings.Contains(logs.String(), secret) {
				t.Fatalf("telemetry leaked %s", secret)
			}
		}
	}
}

func TestProtocolTelemetryCancellationAndDeadline(t *testing.T) {
	config, err := loadMCPConfig()
	if err != nil {
		t.Fatal(err)
	}
	auth, err := newAuthenticator(config)
	if err != nil {
		t.Fatal(err)
	}
	savedWriter, savedFlags := log.Writer(), log.Flags()
	defer log.SetOutput(savedWriter)
	defer log.SetFlags(savedFlags)
	log.SetFlags(0)
	for _, deadline := range []bool{false, true} {
		var logs bytes.Buffer
		log.SetOutput(&logs)
		ctx, cancel := context.WithCancel(context.Background())
		want := "cancelled"
		if deadline {
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			want = "timeout"
		}
		cancel()
		req := httptest.NewRequest(http.MethodPost, config.Endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"stream_demo","arguments":{},"_meta":{"progressToken":"private-progress"}}}`)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("X-Faas-Request-Id", "invalid-secret")
		req.Header.Set("traceparent", "00-00000000000000000000000000000000-123456789abcdef0-01")
		w := httptest.NewRecorder()
		handleHTTP(config, auth, nil, w, req)
		var event map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &event); err != nil {
			t.Fatal(err)
		}
		if event["outcome"] != want || event["stream_outcome"] != want {
			t.Fatal(event)
		}
		if _, ok := event["execution_duration_ms"]; ok {
			t.Fatal("invented execution completion")
		}
		if _, ok := event["stream_duration_ms"]; !ok {
			t.Fatal("missing stream timing")
		}
		if event["request_id"] != nil || event["trace_id"] != nil {
			t.Fatal("accepted invalid correlation")
		}
	}
}

func TestProtocolTelemetryEncodingFailure(t *testing.T) {
	savedWriter, savedFlags := log.Writer(), log.Flags()
	defer log.SetOutput(savedWriter)
	defer log.SetFlags(savedFlags)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	log.SetFlags(0)
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	w, observation := observeProtocolRequest(httptest.NewRecorder(), req, rpcRequest{Method: "tools/call", Params: json.RawMessage(`{"name":"greet"}`)})
	writeRPCResult(w, json.RawMessage(`1`), map[string]any{"content": make(chan int)})
	observation.finish()
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	var event map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &event); err != nil {
		t.Fatal(err)
	}
	if event["stream_outcome"] != "internal_error" {
		t.Fatal(event)
	}
}

func TestProtocolHeartbeatIndependentAndCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	count := 0
	runProtocolHeartbeat(ctx, func() { count++ })
	if count != 1 {
		t.Fatalf("heartbeats=%d", count)
	}
}
