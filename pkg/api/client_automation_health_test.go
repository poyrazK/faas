package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientGetAutomationHealthWithWindow(t *testing.T) {
	after := time.Date(2026, 10, 1, 0, 0, 0, 123, time.FixedZone("UTC+2", 2*60*60))
	before := time.Date(2026, 10, 5, 23, 59, 59, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/billing/automations/paid invoice/health" {
			t.Errorf("request route = %s %s", r.Method, r.URL)
		}
		query := r.URL.Query()
		if query.Get("created_after") != after.UTC().Format(time.RFC3339Nano) || query.Get("created_before") != before.UTC().Format(time.RFC3339Nano) {
			t.Errorf("query timestamps = %v", query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"app_slug":"billing","automation_name":"paid invoice","run_count":0,"completed_run_count":0,"success_rate":0,"status_counts":{"pending":0,"running":0,"awaiting_event":0,"succeeded":0,"failed":0,"dead":0},"failed_steps":[]}`))
	}))
	defer server.Close()

	got, err := NewClient(server.URL, "token").GetAutomationHealth(context.Background(), "billing", "paid invoice", AutomationHealthOptions{CreatedAfter: &after, CreatedBefore: &before})
	if err != nil {
		t.Fatal(err)
	}
	if got.RunCount != 0 || got.AutomationName != "paid invoice" || len(got.FailedSteps) != 0 || got.Queue != nil {
		t.Fatalf("health response = %+v", got)
	}
}

func TestClientGetAutomationHealthDecodesQueue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"queue":{"observed_at":"2026-10-07T12:00:00Z","waiting_run_count":3,"due_run_count":2,"stale_run_count":1,"oldest_due_age_seconds":42.5,"app_running_count":2,"app_dispatch_limit":2,"tenant_dispatch_limit":1,"app_at_capacity":true,"reason_counts":{"ready":0,"scheduled":0,"retry_backoff":0,"parked_wait":1,"app_capacity":2,"tenant_capacity":0,"workflow_capacity":0}}}`))
	}))
	defer server.Close()
	got, err := NewClient(server.URL, "token").GetAutomationHealth(t.Context(), "billing", "invoice", AutomationHealthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	q := got.Queue
	if q == nil || q.ObservedAt.Format(time.RFC3339) != "2026-10-07T12:00:00Z" || q.WaitingRunCount != 3 || q.DueRunCount != 2 || q.StaleRunCount != 1 || q.OldestDueAgeSeconds != 42.5 || !q.AppAtCapacity || q.ReasonCounts[AutomationQueueAppCapacity] != 2 || len(q.ReasonCounts) != 7 {
		t.Fatalf("queue response=%+v", q)
	}
}
