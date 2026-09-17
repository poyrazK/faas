package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestDebugRunning_ReturnsCurrentAndHistory(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{
		AccountID: e.acct.ID,
		Slug:      "running-debug-app",
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := e.store.CreateInstance(t.Context(), app.ID, "", string(state.StateRunning), 128, "node-1", "wake-debug"); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	now := time.Now().UTC()
	appendRunningEvent(t, e, app.ID, now.Add(-2*time.Minute), api.DebugRunningReasonRequestActivity)
	appendRunningEvent(t, e, app.ID, now.Add(-time.Minute), api.DebugRunningReasonOpenConnection)

	rec := e.do(t, http.MethodGet, "/v1/apps/running-debug-app/debug/running?since=3h&limit=10", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var got api.DebugRunningResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.AppID != app.ID || got.Since != "3h" {
		t.Fatalf("response identity/window = %+v", got)
	}
	if len(got.History) != 2 || got.History[0].Causes[0].Code != api.DebugRunningReasonOpenConnection {
		t.Fatalf("history = %+v, want newest open-connection observation first", got.History)
	}
	if got.CurrentObservedAt == "" || len(got.Current) != 1 || got.Current[0].Code != api.DebugRunningReasonOpenConnection {
		t.Fatalf("current = %+v observed_at=%q", got.Current, got.CurrentObservedAt)
	}
	if got.Config.IdleTimeoutSeconds <= 0 {
		t.Fatalf("config = %+v, want plan idle timeout", got.Config)
	}
}

func TestDebugRunning_ClearsCurrentAfterTerminalInstance(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state state.State
	}{
		{name: "parked", state: state.StateParked},
		{name: "stopped", state: state.StateStopped},
		{name: "failed", state: state.StateFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			app, err := e.store.CreateApp(t.Context(), state.App{
				AccountID: e.acct.ID,
				Slug:      "running-debug-" + tc.name,
				Status:    state.AppActive,
			})
			if err != nil {
				t.Fatalf("CreateApp: %v", err)
			}
			instance, err := e.store.CreateInstance(t.Context(), app.ID, "", string(state.StateRunning), 128, "node-1", "wake-debug")
			if err != nil {
				t.Fatalf("CreateInstance: %v", err)
			}
			if tc.state == state.StateParked {
				err = e.store.UpdateInstanceState(t.Context(), instance.ID, string(tc.state))
			} else {
				err = e.store.UpdateInstanceStateToTerminal(t.Context(), instance.ID, string(tc.state), time.Now().UTC())
			}
			if err != nil {
				t.Fatalf("transition to %s: %v", tc.state, err)
			}
			appendRunningEvent(t, e, app.ID, time.Now().UTC().Add(-time.Minute), api.DebugRunningReasonRequestActivity)

			rec := e.do(t, http.MethodGet, "/v1/apps/running-debug-"+tc.name+"/debug/running?since=3h", nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
			}
			var got api.DebugRunningResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(got.History) != 1 {
				t.Fatalf("history = %+v, want retained explanation", got.History)
			}
			if len(got.Current) != 0 || got.CurrentObservedAt != "" {
				t.Fatalf("current = %+v observed_at=%q, want empty after %s", got.Current, got.CurrentObservedAt, tc.state)
			}
		})
	}
}

func TestDebugRunning_SkipsMalformedEventsAndEnforcesLimit(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "running-debug-limit", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	now := time.Now().UTC()
	subject := app.ID
	appendRunningEvent(t, e, app.ID, now.Add(-3*time.Minute), api.DebugRunningReasonMinInstances)
	appendRunningEvent(t, e, app.ID, now.Add(-2*time.Minute), api.DebugRunningReasonTailTasks)
	if err := e.store.AppendEventAt(t.Context(), "schedd", debugRunningEventKind, &subject, []byte("not-json"), now.Add(-time.Minute)); err != nil {
		t.Fatalf("AppendEventAt malformed: %v", err)
	}

	rec := e.do(t, http.MethodGet, "/v1/apps/running-debug-limit/debug/running?limit=1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var got api.DebugRunningResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.History) != 1 || got.History[0].Causes[0].Code != api.DebugRunningReasonTailTasks {
		t.Fatalf("history = %+v, want one valid newest observation", got.History)
	}
	if !got.HistoryTruncated {
		t.Fatal("history_truncated = false, want true when limit is filled")
	}
}

func TestDebugRunning_IsPlanGated(t *testing.T) {
	e := setup(t, api.PlanFree)
	if _, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "running-debug-free", Status: state.AppActive}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := e.do(t, http.MethodGet, "/v1/apps/running-debug-free/debug/running", nil, nil)
	assertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanFeatureGated)
}

func TestNearestRunningTelemetryRow_BoundedAndClosest(t *testing.T) {
	target := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	rows := []sqlc.ListRequestTelemetryByAppRow{
		{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}, ReceivedAt: pgtype.Timestamptz{Time: target.Add(-1500 * time.Millisecond), Valid: true}},
		{ID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true}, ReceivedAt: pgtype.Timestamptz{Time: target.Add(900 * time.Millisecond), Valid: true}},
		{ID: pgtype.UUID{Bytes: [16]byte{3}, Valid: true}, ReceivedAt: pgtype.Timestamptz{Time: target.Add(3 * time.Minute), Valid: true}},
	}
	row, delta, ok := nearestRunningTelemetryRow(rows, target)
	if !ok {
		t.Fatal("nearestRunningTelemetryRow returned no match")
	}
	if row.ID.Bytes != [16]byte{1} {
		t.Fatalf("matched row = %v, want closest row", row.ID.Bytes)
	}
	if delta != 1500*time.Millisecond {
		t.Fatalf("delta = %s, want 1500ms", delta)
	}
	if _, _, ok := nearestRunningTelemetryRow([]sqlc.ListRequestTelemetryByAppRow{{ReceivedAt: pgtype.Timestamptz{Time: target.Add(3 * time.Minute), Valid: true}}}, target); ok {
		t.Fatal("out-of-slack telemetry row was attributed")
	}
}

func appendRunningEvent(t *testing.T, e testEnv, appID string, at time.Time, code string) {
	t.Helper()
	payload, err := json.Marshal(debugRunningEvent{
		SchemaVersion:          1,
		AppID:                  appID,
		ObservedAt:             at.UTC().Format(time.RFC3339Nano),
		RunningInstances:       1,
		ConfiguredMinInstances: 0,
		EffectiveMinInstances:  0,
		IdleTimeoutSeconds:     60,
		Causes: []api.DebugRunningCause{{
			Code:          code,
			Summary:       "observed test cause",
			InstanceCount: 1,
		}},
	})
	if err != nil {
		t.Fatalf("marshal running event: %v", err)
	}
	subject := appID
	if err := e.store.AppendEventAt(t.Context(), "schedd", debugRunningEventKind, &subject, payload, at); err != nil {
		t.Fatalf("AppendEventAt: %v", err)
	}
}
