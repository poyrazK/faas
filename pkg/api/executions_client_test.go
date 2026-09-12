package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExecutionClientLifecycle(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
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
	if _, err := c.CancelExecution(ctx, "exec-1"); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}
	want := []string{"POST /v1/executions", "GET /v1/executions/exec-1", "DELETE /v1/executions/exec-1"}
	if len(methods) != len(want) {
		t.Fatalf("methods = %v, want %v", methods, want)
	}
	for i := range want {
		if methods[i] != want[i] {
			t.Errorf("methods[%d] = %q, want %q", i, methods[i], want[i])
		}
	}
}
