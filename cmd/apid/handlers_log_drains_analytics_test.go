package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGetAppLogDrainAnalyticsReturnsHourlyCustomerSafeSummary(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "analytics-app", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	drain, err := e.store.CreateAppLogDrain(t.Context(), state.AppLogDrain{
		AppID: app.ID, AccountID: e.acct.ID, Kind: state.AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/analytics", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain: %v", err)
	}
	now := time.Now().UTC()
	for _, sample := range []state.AppLogDrainHealth{
		{DrainID: drain.ID, DeliveredTotal: 10, FailedTotal: 1, DroppedTotal: 1, RetriesTotal: 2, DeliveryLatencyNanosTotal: int64(time.Second), DeliveryLatencySamples: 10, UpdatedAt: now.Add(-2 * time.Hour)},
		{DrainID: drain.ID, DeliveredTotal: 15, FailedTotal: 2, DroppedTotal: 1, RetriesTotal: 3, DeliveryLatencyNanosTotal: int64(2 * time.Second), DeliveryLatencySamples: 15, UpdatedAt: now.Add(-time.Hour)},
		{DrainID: drain.ID, DeliveredTotal: 20, FailedTotal: 2, DroppedTotal: 1, RetriesTotal: 4, DeliveryLatencyNanosTotal: int64(3 * time.Second), DeliveryLatencySamples: 20, UpdatedAt: now},
	} {
		if err := e.store.UpsertAppLogDrainHealth(t.Context(), sample); err != nil {
			t.Fatalf("UpsertAppLogDrainHealth: %v", err)
		}
	}

	rec := e.do(t, http.MethodGet, "/v1/apps/analytics-app/log-drains/"+drain.ID+"/analytics?window=24h", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got api.AppLogDrainAnalyticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LogDrainID != drain.ID || got.Window != "24h" || got.BucketInterval != "1h" || len(got.Buckets) != 3 {
		t.Fatalf("analytics response = %+v, want three hourly buckets", got)
	}
	if got.Summary.Delivered != 20 || got.Summary.Failed != 2 || got.Summary.Dropped != 1 || got.Summary.Retries != 4 {
		t.Fatalf("analytics summary = %+v", got.Summary)
	}
	if want := 20.0 / 23.0; got.Summary.SuccessRate != want {
		t.Fatalf("success rate = %v, want %v", got.Summary.SuccessRate, want)
	}
	if got.Summary.AverageLatencyMS != 150 {
		t.Fatalf("average latency = %v, want 150ms", got.Summary.AverageLatencyMS)
	}
	if got.Buckets[1].Delivered != 5 || got.Buckets[1].Failed != 1 || got.Buckets[1].Retries != 1 {
		t.Fatalf("middle analytics bucket = %+v", got.Buckets[1])
	}
}

func TestGetAppLogDrainAnalyticsRejectsUnknownWindow(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "analytics-invalid-app", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	drain, err := e.store.CreateAppLogDrain(t.Context(), state.AppLogDrain{
		AppID: app.ID, AccountID: e.acct.ID, Kind: state.AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/analytics-invalid", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain: %v", err)
	}
	rec := e.do(t, http.MethodGet, "/v1/apps/analytics-invalid-app/log-drains/"+drain.ID+"/analytics?window=90d", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
