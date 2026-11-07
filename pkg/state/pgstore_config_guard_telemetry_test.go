package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// A wake that fails before any instance serves (the app crashes on startup)
// is recorded with the nil deployment. The default-scope config guard must
// count it as new-config failure; a capacity refusal and another scope's
// guard must not.
func TestPgStore_ConfigGuardTelemetryCountsStartupCrashes(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, deploymentID := seedLiveDeploy(t, s, ctx, "-guard-crash")
	changedAt := time.Now().UTC().Add(-2 * time.Minute)
	insert := func(deployment string, status, count int, refusal bool) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry
			(account_id, app_id, deployment_id, route, method, status, latency_ms, received_at, count, instance_id, platform_refusal)
			VALUES ($1, $2, $3, 'GET /', 'GET', $4, 10, now(), $5, '', $6)`,
			accountID, appID, deployment, status, count, refusal); err != nil {
			t.Fatal(err)
		}
	}
	insert("00000000-0000-0000-0000-000000000000", 503, 7, false) // startup crash
	insert("00000000-0000-0000-0000-000000000000", 503, 5, true)  // capacity refusal
	insert(deploymentID, 200, 2, false)                           // pre-instance success on the scoped deployment
	query := state.ConfigGuardTelemetryQuery{AppID: appID, Scope: "default", OpenedAt: changedAt, ChangedAt: changedAt,
		BaselineFrom: changedAt.Add(-time.Hour), Until: time.Now().UTC().Add(time.Minute)}
	sample, err := s.ConfigGuardTelemetry(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if sample.NewRequests != 9 || sample.NewServerErrors != 7 {
		t.Fatalf("default scope sample = %+v, want 9 new requests with 7 server errors", sample)
	}
	query.Scope = "staging"
	if sample, err = s.ConfigGuardTelemetry(ctx, query); err != nil {
		t.Fatal(err)
	}
	if sample.NewServerErrors != 0 {
		t.Fatalf("staging scope counted unattributed failures: %+v", sample)
	}
}
