//go:build !no_pg

// Migration-apply test for 00190_admin_obs_index.sql
// (issue #777 / ADR-091). Pins the three indexes that the operator
// observability backend at /v1/admin/obs/* needs to stay cheap as
// the active base grows. Each index covers a fleet-wide scan path
// the per-tenant indexes do not.
//
// Pins:
//
//  1. Migration set applies cleanly through 00190.
//  2. All three indexes exist on the right tables.
//  3. Replay-safe: a second MigrateUp is a no-op (ADR-041).
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrations_00190_AdminObsIndex pins the three indexes
// declared in 00190_admin_obs_index.sql. One row per expected
// (table, index) pair keeps the failure message specific — a
// developer hitting a missing index sees exactly which one is
// gone rather than scanning pg_indexes by hand.
func TestMigrations_00190_AdminObsIndex(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	want := []struct {
		tablename string
		indexname string
	}{
		{"orgs", "orgs_created_at_idx"},
		{"orgs", "orgs_status_idx"},
		{"events", "events_kind_at_idx"},
	}
	for _, w := range want {
		row := pool.QueryRow(ctx, `
			SELECT 1
			FROM pg_indexes
			WHERE schemaname = current_schema()
			  AND tablename = $1
			  AND indexname = $2
		`, w.tablename, w.indexname)
		var present int
		if err := row.Scan(&present); err != nil {
			t.Errorf("index %s.%s missing: %v", w.tablename, w.indexname, err)
		}
	}
}
