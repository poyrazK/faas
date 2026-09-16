package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExecutionClientLifecycle(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet && r.URL.Path == "/v1/executions" {
			query := r.URL.Query()
			if query.Get("limit") != "10" || query.Get("offset") != "20" || query.Get("status") != string(ExecutionStatusRunning) {
				t.Fatalf("list query = %v", query)
			}
		}
		if r.Method == http.MethodPost {
			var req CreateExecutionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Runtime != ExecutionRuntimeNode22 {
				t.Fatalf("request decode = %#v, err=%v", req, err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"exec-1","status":"queued","runtime":"node22","created_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")
	ctx := context.Background()
	if _, err := c.CreateExecution(ctx, CreateExecutionRequest{Runtime: ExecutionRuntimeNode22, Source: "console.log(1)"}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if _, err := c.GetExecution(ctx, "exec-1"); err != nil {
		t.Fatalf("GetExecution: %v", err)
	}
	if _, err := c.ListExecutions(ctx, 10, 20, ExecutionStatusRunning); err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if _, err := c.CancelExecution(ctx, "exec-1"); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}
	want := []string{"POST /v1/executions", "GET /v1/executions/exec-1", "GET /v1/executions", "DELETE /v1/executions/exec-1"}
	if len(methods) != len(want) {
		t.Fatalf("methods = %v, want %v", methods, want)
	}
	for i := range want {
		if methods[i] != want[i] {
			t.Errorf("methods[%d] = %q, want %q", i, methods[i], want[i])
		}
	}
}

func TestStreamExecutionUsesCursorAndSSEAccept(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/executions/exec-1/events" || r.URL.Query().Get("after") != "7" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("headers = %v", r.Header)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: 8\nevent: terminal\ndata: {\"status\":\"succeeded\"}\n\n")
	}))
	defer srv.Close()

	body, err := NewClient(srv.URL, "token").StreamExecution(context.Background(), "exec-1", 7)
	if err != nil {
		t.Fatalf("StreamExecution: %v", err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if string(data) != "id: 8\nevent: terminal\ndata: {\"status\":\"succeeded\"}\n\n" {
		t.Fatalf("stream = %q", data)
	}
}
