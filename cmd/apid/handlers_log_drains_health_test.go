package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGetAppLogDrainHealthReturnsDurableSafeProjection(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "health-app", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	drain, err := e.store.CreateAppLogDrain(t.Context(), state.AppLogDrain{
		AppID: app.ID, AccountID: e.acct.ID, Kind: state.AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/health", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain: %v", err)
	}
	updatedAt := time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)
	oldestPendingAt := updatedAt.Add(-time.Minute)
	if err := e.store.UpsertAppLogDrainHealth(t.Context(), state.AppLogDrainHealth{
		DrainID: drain.ID, Status: "future-status", Active: true, QueueDepth: 3,
		QueueCapacity: 10, PendingRecords: 2, PendingBytes: 2048,
		PendingBytesCapacity: 65536, DeadLetterTotal: 1, OldestPendingAt: oldestPendingAt,
		DeliveredTotal: 8, FailedTotal: 1,
		LastError: "dial tcp 10.0.0.1:443: secret=leaked", UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("UpsertAppLogDrainHealth: %v", err)
	}

	rec := e.do(t, http.MethodGet, "/v1/apps/health-app/log-drains/"+drain.ID+"/health", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got api.AppLogDrainHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LogDrainID != drain.ID || got.Status != api.AppLogDrainHealthStatusUnknown || !got.Active || got.QueueDepth != 3 || got.PendingRecords != 2 || got.PendingBytes != 2048 || got.PendingBytesCapacity != 65536 || got.DeadLetterTotal != 1 || got.OldestPendingAt != oldestPendingAt.Format(time.RFC3339) || got.DeliveredTotal != 8 {
		t.Fatalf("health response = %+v", got)
	}
	if got.LastError != "delivery failed" || got.UpdatedAt != updatedAt.Format(time.RFC3339) {
		t.Fatalf("health response did not sanitize/preserve fields: %+v", got)
	}
}

func TestGetAppLogDrainHealthCrossAccount404(t *testing.T) {
	e := setup(t, api.PlanPro)
	other, err := e.store.CreateAccount(t.Context(), "other-health@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "other-health-app", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	drain, err := e.store.CreateAppLogDrain(t.Context(), state.AppLogDrain{
		AppID: app.ID, AccountID: other.ID, Kind: state.AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/other-health", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain: %v", err)
	}
	rec := e.do(t, http.MethodGet, "/v1/apps/other-health-app/log-drains/"+drain.ID+"/health", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account status = %d, body = %s; want 404", rec.Code, rec.Body.String())
	}
}

func TestGetAppLogDrainHealthDisabledConfigWinsOverStaleSnapshot(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "disabled-health-app", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	drain, err := e.store.CreateAppLogDrain(t.Context(), state.AppLogDrain{
		AppID: app.ID, AccountID: e.acct.ID, Kind: state.AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/disabled-health", Enabled: false,
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain: %v", err)
	}
	if err := e.store.UpsertAppLogDrainHealth(t.Context(), state.AppLogDrainHealth{
		DrainID: drain.ID, Status: api.AppLogDrainHealthStatusHealthy, Active: true,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertAppLogDrainHealth: %v", err)
	}

	rec := e.do(t, http.MethodGet, "/v1/apps/disabled-health-app/log-drains/"+drain.ID+"/health", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got api.AppLogDrainHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != api.AppLogDrainHealthStatusInactive || got.Active {
		t.Fatalf("disabled health response = %+v, want inactive/false", got)
	}
}
