package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientListWorkflowRunsWithOptions(t *testing.T) {
	after := time.Date(2026, 10, 1, 0, 0, 0, 123, time.FixedZone("UTC+2", 2*60*60))
	before := time.Date(2026, 10, 5, 23, 59, 59, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/billing/workflows/runs" {
			t.Errorf("request route = %s %s", r.Method, r.URL)
		}
		query := r.URL.Query()
		if query.Get("status") != "failed" || query.Get("workflow_name") != "paid + invoice" || query.Get("limit") != "20" || query.Get("offset") != "3" {
			t.Errorf("query filters = %v", query)
		}
		wantAfter := after.UTC().Format(time.RFC3339Nano)
		wantBefore := before.UTC().Format(time.RFC3339Nano)
		if query.Get("created_after") != wantAfter || query.Get("created_before") != wantBefore {
			t.Errorf("query timestamps = %v, want after=%s before=%s", query, wantAfter, wantBefore)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"runs":[],"total":0}`))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, "token").ListWorkflowRunsWithOptions(context.Background(), "billing", WorkflowRunListOptions{
		Status: "failed", WorkflowName: "paid + invoice", CreatedAfter: &after, CreatedBefore: &before, Limit: 20, Offset: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestClientRunWorkflowWithIdempotencyKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/billing/workflows/paid/runs" {
			t.Errorf("request route = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "invoice-event-42" {
			t.Errorf("Idempotency-Key = %q, want invoice-event-42", got)
		}
		_, _ = w.Write([]byte(`{"id":"run-1","status":"pending","workflow_name":"paid"}`))
	}))
	defer server.Close()

	run, err := NewClient(server.URL, "token").RunWorkflowWithIdempotencyKey(context.Background(), "billing", "paid", json.RawMessage(`{"invoice_id":"42"}`), "invoice-event-42")
	if err != nil || run.ID != "run-1" {
		t.Fatalf("RunWorkflowWithIdempotencyKey() = (%+v, %v)", run, err)
	}
}
