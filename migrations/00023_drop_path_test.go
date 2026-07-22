//go:build !no_pg

// Migration-apply test for 00023 (drop snapshots.path column). This is the
// load-bearing check that the deprecation-window surface from issue #96 is
// fully retired: 00022's backfill gave every pre-existing row a non-empty
// storage_key, the F-1 contract on CreateSnapshot refuses to insert a row
// without one, so the legacy column is now unreachable. The apply step
// (covered here) drops it. A future regression that re-adds a code path
// reading snapshots.path would crash on boot because the column no longer
// exists; this test pins that.
//
// Build tag mirrors 00022_backfill_test.go:18 — set FAAS_SKIP_PG_TESTS=1
// locally to skip.

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00023_SnapshotsDropPath(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply the full migration set. 00023 should run last.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// (2) The legacy path column must be gone. information_schema is the
	// portable way to assert column existence without depending on the
	// Postgres catalog names; any re-add would show up here.
	var pathExists int
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.columns
		 where table_name = 'snapshots' and column_name = 'path'
	`).Scan(&pathExists); err != nil {
		t.Fatalf("query information_schema: %v", err)
	}
	if pathExists != 0 {
		t.Errorf("snapshots.path column still exists after 00023; column drop did not apply")
	}

	// (3) The replacement column storage_key must still be present and
	// not-nullable (the F-1 contract from issue #96). A future regression
	// that drops the NOT NULL would let imaged / schedd insert unkeyed
	// rows again — exactly the bug 00022's backfill fixed.
	var isNullable string
	if err := pool.QueryRow(ctx, `
		select is_nullable from information_schema.columns
		 where table_name = 'snapshots' and column_name = 'storage_key'
	`).Scan(&isNullable); err != nil {
		t.Fatalf("query storage_key nullability: %v", err)
	}
	if isNullable != "NO" {
		t.Errorf("snapshots.storage_key is_nullable = %q, want NO (the F-1 contract)", isNullable)
	}
}
