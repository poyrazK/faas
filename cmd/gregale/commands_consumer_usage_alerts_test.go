package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 849 — plans carry alert thresholds and consumers list crossed alerts.
func TestCmdConsumersUsageAlerts(t *testing.T) {
	var created api.CreateAPIConsumerPlanRequest
	var updated map[string]json.RawMessage
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	stdout := withConsumersTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/apps/my-api/consumer-plans":
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.APIConsumerPlanResponse{ID: "p1", Name: created.Name,
				MaxUnitsPerMonth: created.MaxUnitsPerMonth, AlertThresholdsPercent: created.AlertThresholdsPercent})
		case "GET /v1/apps/my-api/consumer-plans":
			_ = json.NewEncoder(w).Encode(api.APIConsumerPlanListResponse{Plans: []api.APIConsumerPlanResponse{
				{ID: "p1", Name: "free", MaxUnitsPerMonth: 1000, AlertThresholdsPercent: []int32{80, 100}}}})
		case "PUT /v1/apps/my-api/consumer-plans/p1":
			updated = map[string]json.RawMessage{}
			_ = json.NewDecoder(r.Body).Decode(&updated)
			_ = json.NewEncoder(w).Encode(api.APIConsumerPlanResponse{ID: "p1", Name: "free", MaxUnitsPerMonth: 1000})
		case "GET /v1/apps/my-api/consumers/c1/usage-alerts":
			_ = json.NewEncoder(w).Encode(api.APIConsumerUsageAlertListResponse{Alerts: []api.APIConsumerUsageAlertResponse{
				{ID: "a1", ConsumerID: "c1", PlanID: "p1", MonthStart: month, ThresholdPercent: 80, LimitUnits: 1000, UsedUnits: 800, CrossedAt: month.Add(time.Hour)}}})
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	if code := cmdConsumers([]string{"plan-create", "my-api", "--name", "free", "--max-units-per-month", "1000", "--alert-at", "80%,100"}); code != 0 {
		t.Fatalf("plan-create exit = %d", code)
	}
	if !slices.Equal(created.AlertThresholdsPercent, []int32{80, 100}) || !strings.Contains(stdout.String(), "80%, 100%") {
		t.Fatalf("created = %+v output:\n%s", created, stdout.String())
	}
	if code := cmdConsumers([]string{"plan-update", "my-api", "--plan", "free", "--alert-at", "none"}); code != 0 {
		t.Fatalf("plan-update exit = %d", code)
	}
	if string(updated["alert_thresholds_percent"]) != "[]" || string(updated["max_units_per_month"]) != "1000" {
		t.Fatalf("update body = %v, want cleared thresholds and kept limit", updated)
	}
	if code := cmdConsumers([]string{"plan-update", "my-api", "--plan", "free", "--max-units-per-month", "2000"}); code != 0 {
		t.Fatalf("plan-update limit exit = %d", code)
	}
	if _, sent := updated["alert_thresholds_percent"]; sent {
		t.Fatalf("update without --alert-at sent thresholds: %v", updated)
	}
	stdout.Reset()
	if code := cmdConsumers([]string{"usage-alerts", "my-api", "c1"}); code != 0 {
		t.Fatalf("usage-alerts exit = %d", code)
	}
	if out := strings.Join(strings.Fields(stdout.String()), " "); !strings.Contains(out, "2026-10 80% 800 / 1000 p1") {
		t.Fatalf("usage-alerts output:\n%s", stdout.String())
	}
	if code := cmdConsumers([]string{"plan-create", "my-api", "--name", "x", "--alert-at", "lots"}); code == 0 {
		t.Fatal("invalid --alert-at accepted")
	}
}
