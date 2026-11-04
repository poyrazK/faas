package state_test

import (
	"slices"
	"testing"
	"time"
)

// Request analytics reuses route removal's coverage blockers to say whether
// every gateway delivered a window (ADR-20261026143210 amendment 2).
func TestRequestTelemetryCompletenessPG(t *testing.T) {
	store, pool, ctx, _, app, _, _, _ := routeRemovalFixture(t)
	now := time.Now().UTC()
	from, until := now.Add(-time.Hour), now.Add(-2*time.Minute)

	gaps, err := store.RequestTelemetryCompleteness(ctx, app.ID, from, until)
	if err != nil || len(gaps) != 0 {
		t.Fatalf("healthy coverage: gaps %v err %v, want none", gaps, err)
	}
	// A gateway that became healthy inside the window may have lost rows
	// before it did.
	if _, err := pool.Exec(ctx, `UPDATE request_telemetry_coverage SET healthy_since=clock_timestamp()-interval '30 minutes'`); err != nil {
		t.Fatal(err)
	}
	gaps, err = store.RequestTelemetryCompleteness(ctx, app.ID, from, until)
	if err != nil || !slices.Contains(gaps, "telemetry_window_incomplete") {
		t.Fatalf("restart inside the window: gaps %v err %v, want telemetry_window_incomplete", gaps, err)
	}
	if _, err := store.RequestTelemetryCompleteness(ctx, "not-a-uuid", from, until); err == nil {
		t.Fatal("malformed app id accepted")
	}
}
