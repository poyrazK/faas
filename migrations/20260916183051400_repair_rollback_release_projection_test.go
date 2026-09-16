//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const repairRollbackProjectionVersion int64 = 20260916183051400

func TestMigrations_RepairRollbackReleaseProjection(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	accountID := seedAccount(t, ctx, pool)
	appID := seedApp(t, ctx, pool, accountID)
	newer := seedDeployment(t, ctx, pool, appID, "live")
	stale := seedDeployment(t, ctx, pool, appID, "superseded")
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `
		update deployments
		   set traffic_percent = 0,
		       created_at = $2,
		       rollout_state = 'rolling_out', rollout_completed_at = null
		 where id = $1`, newer, now); err != nil {
		t.Fatalf("seed invalid live projection: %v", err)
	}
	if _, err := pool.Exec(ctx, `update deployments set traffic_percent = 100 where id = $1`, stale); err != nil {
		t.Fatalf("seed invalid stale projection: %v", err)
	}
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id = $1`, repairRollbackProjectionVersion); err != nil {
		t.Fatalf("rewind repair migration: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("apply repair migration: %v", err)
	}

	type projection struct {
		status  string
		traffic int
		rollout string
	}
	read := func(id string) projection {
		t.Helper()
		var got projection
		if err := pool.QueryRow(ctx, `select status, traffic_percent, rollout_state from deployments where id = $1`, id).
			Scan(&got.status, &got.traffic, &got.rollout); err != nil {
			t.Fatalf("read deployment %s: %v", id, err)
		}
		return got
	}
	if got := read(newer); got.status != "live" || got.traffic != 100 || got.rollout != "complete" {
		t.Fatalf("newest live projection = %+v", got)
	}
	if got := read(stale); got.status != "superseded" || got.traffic != 0 {
		t.Fatalf("stale terminal traffic = %+v", got)
	}
}
