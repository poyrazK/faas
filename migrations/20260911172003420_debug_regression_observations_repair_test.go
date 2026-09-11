//go:build !no_pg

// Production repair coverage for the debugger regression read surface.  The
// legacy 00436 ledger row can survive a restore even when its table does not;
// this test recreates that drift and proves the append-only repair migration
// restores both the table and its read index.
package migrations_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const (
	debugRegressionRepairPrevious int64 = 20260911155728451
	debugRegressionRepairVersion  int64 = 20260911172003420
)

func TestMigrations_DebugRegressionObservationRepair(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, debugRegressionRepairPrevious)

	if _, err := pool.Exec(ctx, `drop table debug_regression_observations`); err != nil {
		t.Fatalf("recreate production schema drift: %v", err)
	}
	assertDebugRegressionSchema(t, ctx, pool, false)

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("apply repair migration: %v", err)
	}
	assertDebugRegressionSchema(t, ctx, pool, true)

	var repairApplied int
	if err := pool.QueryRow(ctx, `
		select count(*) from goose_db_version
		 where version_id = $1 and is_applied`, debugRegressionRepairVersion).Scan(&repairApplied); err != nil {
		t.Fatalf("check repair ledger row: %v", err)
	}
	if repairApplied != 1 {
		t.Fatalf("repair migration ledger rows = %d, want 1", repairApplied)
	}

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay migrations: %v", err)
	}
	assertDebugRegressionSchema(t, ctx, pool, true)
}

func assertDebugRegressionSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want bool) {
	t.Helper()
	var table, index bool
	if err := pool.QueryRow(ctx, `
		select
			to_regclass(current_schema() || '.debug_regression_observations') is not null,
			to_regclass(current_schema() || '.debug_regression_observations_app_idx') is not null
	`).Scan(&table, &index); err != nil {
		t.Fatalf("inspect debug regression schema: %v", err)
	}
	if table != want || index != want {
		t.Fatalf("debug regression schema table=%t index=%t, want both %t", table, index, want)
	}
}
