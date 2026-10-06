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
	if got.RunCount != 0 || got.AutomationName != "paid invoice" || len(got.FailedSteps) != 0 {
		t.Fatalf("health response = %+v", got)
	}
}
