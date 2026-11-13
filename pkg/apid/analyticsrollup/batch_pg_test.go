//go:build !no_pg

// adr: 20261103113952

package analyticsrollup_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A gateway batch is applied once per batch_id: a resend after a lost
// answer adds nothing, and the ledger row commits with the rollups
// (amendment 5).
func TestGatewayRollupBatchAppliesOnce(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "rollup-batch@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollup-batch"})
	if err != nil {
		t.Fatal(err)
	}
	appID, accountID := uuid.MustParse(app.ID), uuid.MustParse(account.ID)
	minute := time.Now().UTC().Truncate(time.Minute).Add(-5 * time.Minute)
	row := state.RequestAnalyticsRollup{
		AppID: appID, AccountID: accountID, DeploymentID: uuid.New(), BucketStart: minute,
		DimensionKind: "route", Dimension: "GET /", Method: "GET", Status: 200, Requests: 4, LatencyMaxMS: 9,
	}
	row.LatencyHist[0] = 4
	rows := []state.RequestAnalyticsRollup{row}
	coverage := []state.RequestAnalyticsCoverage{{AppID: appID, CoveredSince: minute}}
	batchID := uuid.New()

	requests := func() int64 {
		t.Helper()
		var n int64
		if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(requests), 0) FROM request_analytics_minutes WHERE app_id = $1 AND dimension_kind = 'route'`, appID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for i, wantReplayed := range []bool{false, true} {
		replayed, err := store.AddRequestAnalyticsRollupBatch(ctx, batchID, "node-a", rows, coverage)
		if err != nil || replayed != wantReplayed {
			t.Fatalf("send %d: replayed = %v, err %v; want %v", i, replayed, err, wantReplayed)
		}
		if got := requests(); got != 4 {
			t.Fatalf("send %d: requests = %d, want 4", i, got)
		}
	}
	// Another batch with the same rows adds to them.
	if replayed, err := store.AddRequestAnalyticsRollupBatch(ctx, uuid.New(), "node-a", rows, coverage); err != nil || replayed {
		t.Fatalf("second batch: replayed = %v, err %v", replayed, err)
	}
	if got := requests(); got != 8 {
		t.Fatalf("requests after a second batch = %d, want 8", got)
	}
	// A batch that fails leaves no ledger row, so its resend applies.
	bad := row
	bad.Dimension = string([]byte{0xff})
	failing := uuid.New()
	if _, err := store.AddRequestAnalyticsRollupBatch(ctx, failing, "node-a", []state.RequestAnalyticsRollup{bad}, nil); err == nil {
		t.Fatal("a row Postgres refuses was applied")
	}
	var claimed bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM request_analytics_rollup_batches WHERE batch_id = $1)`, failing).Scan(&claimed); err != nil || claimed {
		t.Fatalf("failed batch claimed = %v, err %v; want no ledger row", claimed, err)
	}
}
