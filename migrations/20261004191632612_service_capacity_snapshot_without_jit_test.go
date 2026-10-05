//go:build !no_pg

// adr: 422 — the capacity snapshot runs without JIT.
package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// ADR-422: a JIT-enabled server compiled service_capacity_snapshot() on every
// call (6-8 s in production, ~10 ms interpreted). The check runs after the
// complete migration set, so a later CREATE OR REPLACE that drops the
// function-level setting fails here instead of silently restoring the stall.
func TestMigrationServiceCapacitySnapshotRunsWithoutJIT(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20261001084654053)
	if got := snapshotFunctionConfig(t, ctx, pool); strings.Contains(got, "jit=off") {
		t.Fatalf("precondition: ADR-422 definition already disables JIT: %q", got)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	if got := snapshotFunctionConfig(t, ctx, pool); !strings.Contains(got, "jit=off") {
		t.Fatalf("service_capacity_snapshot() config = %q, want jit=off", got)
	}
}

func snapshotFunctionConfig(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var config string
	if err := pool.QueryRow(ctx, `SELECT coalesce(array_to_string(proconfig, ','), '')
FROM pg_proc WHERE oid = to_regprocedure('service_capacity_snapshot()')`).Scan(&config); err != nil {
		t.Fatalf("read service_capacity_snapshot() config: %v", err)
	}
	return config
}
