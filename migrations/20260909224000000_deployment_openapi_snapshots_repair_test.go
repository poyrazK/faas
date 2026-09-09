//go:build !no_pg

// adr: 121 — deployment promotion requires a durable per-deployment OpenAPI
// snapshot, including databases whose migration 358 ledger row predates the
// final schema assigned to that historical slot.
package migrations_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const (
	openAPISnapshotRepairPrevious int64 = 20260909192647123
	openAPISnapshotRepairVersion  int64 = 20260909224000000
)

func TestMigrations_DeploymentOpenAPISnapshotRepair(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, openAPISnapshotRepairPrevious)

	var historicalApplied int
	if err := pool.QueryRow(ctx, `
		select count(*) from goose_db_version
		 where version_id = 358 and is_applied`).Scan(&historicalApplied); err != nil {
		t.Fatalf("check historical ledger row: %v", err)
	}
	if historicalApplied != 1 {
		t.Fatalf("migration 358 ledger rows = %d, want 1", historicalApplied)
	}

	if _, err := pool.Exec(ctx, `drop table deployment_openapi_snapshots`); err != nil {
		t.Fatalf("recreate production schema drift: %v", err)
	}
	assertOpenAPISnapshotTable(t, ctx, pool, false)

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("apply repair migration: %v", err)
	}
	assertOpenAPISnapshotTable(t, ctx, pool, true)

	var repairApplied int
	if err := pool.QueryRow(ctx, `
		select count(*) from goose_db_version
		 where version_id = $1 and is_applied`, openAPISnapshotRepairVersion).Scan(&repairApplied); err != nil {
		t.Fatalf("check repair ledger row: %v", err)
	}
	if repairApplied != 1 {
		t.Fatalf("repair migration ledger rows = %d, want 1", repairApplied)
	}

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay migrations: %v", err)
	}
	assertOpenAPISnapshotTable(t, ctx, pool, true)
}

func assertOpenAPISnapshotTable(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want bool) {
	t.Helper()
	var table, index bool
	if err := pool.QueryRow(ctx, `
		select
			to_regclass(current_schema() || '.deployment_openapi_snapshots') is not null,
			to_regclass(current_schema() || '.deployment_openapi_snapshots_app_scope_idx') is not null
	`).Scan(&table, &index); err != nil {
		t.Fatalf("inspect OpenAPI snapshot schema: %v", err)
	}
	if table != want || index != want {
		t.Fatalf("OpenAPI snapshot schema table=%t index=%t, want both %t", table, index, want)
	}
}
