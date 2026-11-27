package faas_test

import (
	"context"
	"encoding/json"
	preview "github.com/gregale-dev/gregale-go/preview"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVersionedTaskRetryWireContract(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/run-tasks/invoke" || r.Header.Get("Idempotency-Key") != "report-call" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("lost invocation route, credential or retry identity")
		}
		var request preview.InvokeExecutionTaskRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Name != "report" || request.Version != 1 {
			t.Error("invalid invocation body", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_, _ = io.WriteString(w, `{"id":"run","status":"queued","task":{"id":"version","name":"report","version":1,"digest":"digest"}}`)
	}))
	defer srv.Close()
	c, err := preview.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	request := preview.InvokeExecutionTaskRequest{Name: "report", Version: 1, Input: json.RawMessage(`{"rows":3}`)}
	first, err := c.InvokeExecutionTask(context.Background(), request, "report-call")
	if err != nil || first.Task == nil || first.Task.Name != "report" {
		t.Fatal("missing lineage", err)
	}
	replay, err := c.InvokeExecutionTask(context.Background(), request, "report-call")
	if err != nil || replay.ID != first.ID || calls != 2 {
		t.Fatal("retry contract", err)
	}
}
