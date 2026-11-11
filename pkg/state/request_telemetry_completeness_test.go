package state_test

import (
	"slices"
	"testing"
	"time"
)

// Historical analytics uses retained interval evidence; missing proof remains
// unknown rather than assigning a current restart to an older window.
func TestRequestTelemetryCompletenessPG(t *testing.T) {
	store, pool, ctx, _, app, _, _, _ := routeRemovalFixture(t)
	now := time.Now().UTC()
	from, until := now.Add(-time.Hour), now.Add(-2*time.Minute)

	if _, err := pool.Exec(ctx, "INSERT INTO request_analytics_rollup_audit(app_id,audited_since) VALUES($1,$2)", app.ID, from.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	gaps, err := store.RequestTelemetryCompleteness(ctx, app.ID, from, until)
	if err != nil || len(gaps) != 0 {
		t.Fatalf("healthy coverage: gaps %v err %v, want none", gaps, err)
	}
	// A gateway that became healthy inside the window may have lost rows
	// before it did.
	if _, err := pool.Exec(ctx, "DELETE FROM request_analytics_coverage_intervals"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE request_telemetry_coverage SET healthy_since=clock_timestamp()-interval '30 minutes'`); err != nil {
		t.Fatal(err)
	}
	gaps, err = store.RequestTelemetryCompleteness(ctx, app.ID, from, until)
	if err != nil || !slices.Contains(gaps, "telemetry_coverage_unknown") {
		t.Fatalf("restart inside the window: gaps %v err %v, want telemetry_coverage_unknown", gaps, err)
	}
	if _, err := store.RequestTelemetryCompleteness(ctx, "not-a-uuid", from, until); err == nil {
		t.Fatal("malformed app id accepted")
	}
}
