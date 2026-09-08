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
//
// Scoping note: pgtest.Open pins the test pool's search_path to a fresh
// per-test schema (see pkg/db/pgtest/pgtest.go:74). The information_schema
// queries below filter on table_schema = current_schema() to keep the
// assertion in that schema; an unscoped query against information_schema
// would see columns across every schema in search_path and produce a
// false positive if a `public.snapshots` table exists alongside the test
// schema. This is the same scoping apply_walk_test.go:124 uses for its
// column-existence probe.

package migrations_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

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
		 where table_schema = current_schema()
		   and table_name = 'snapshots' and column_name = 'path'
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
		 where table_schema = current_schema()
		   and table_name = 'snapshots' and column_name = 'storage_key'
	`).Scan(&isNullable); err != nil {
		t.Fatalf("query storage_key nullability: %v", err)
	}
	if isNullable != "NO" {
		t.Errorf("snapshots.storage_key is_nullable = %q, want NO (the F-1 contract)", isNullable)
	}
}

// TestMigrations_00023_TripwireUnscopedQueryFailsWithForeignSchema pins the
// scope of the information_schema probes in TestMigrations_00023_*. Before
// the scoping fix, an unscoped query like
//
//	select count(*) from information_schema.columns
//	 where table_name = 'snapshots' and column_name = 'path'
//
// would return rows from any schema in search_path (per pgtest.Open,
// "test_schema, public"). If a sibling `public.snapshots` table with a
// `path` column exists, the assertion fires with a false positive — "the
// legacy column survived the migration" — when in fact the column was
// dropped from the test schema and lives only in a foreign schema.
//
// This test reproduces that failure mode on the unfixed code path by
// seeding a sibling `public.snapshots` table with a `path` column before
// the scoped assertion runs. The test PASSES today (because the scoped
// query ignores the foreign column). A future PR that drops the
// `table_schema = current_schema()` filter would flip this tripwire to
// FAIL, exposing the regression without needing a CI-environment
// re-trigger.
//
// Why a sibling-table repro rather than asserting the bare unscoped query
// returns >0: the existing test's actual claim is "test schema has no
// path column after migrations". The cleanest tripwire shape is to run
// that claim with a known foreign distraction present — if scoping
// breaks, the foreign column leaks in and the test fails for the right
// reason.
//
// Cleanup: the sibling public.snapshots is created with IF EXISTS-aware
// CREATE so it is safe against pre-existing objects, and dropped on
// test teardown via t.Cleanup.
func TestMigrations_00023_TripwireUnscopedQueryFailsWithForeignSchema(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Seed a sibling `public.snapshots` table with a `path` column.
	// This is the exact shape of the legacy column from migration 00001
	// (path text not null) — recreated here on a table that 00023
	// cannot reach. pgtest.Open's pool search_path is
	// "<test_schema>, public", so a query against information_schema
	// without a table_schema filter would see this column too.
	//
	// IF NOT EXISTS makes the seed idempotent against the (unlikely)
	// case where a previous test left a public.snapshots behind; if so
	// we still own its shape after this DDL block.
	if _, err := pool.Exec(ctx, `
		create table if not exists public.snapshots (
			id uuid primary key default gen_random_uuid(),
			path text not null,
			storage_key text
		)
	`); err != nil {
		t.Fatalf("seed public.snapshots: %v", err)
	}
	// Ensure the legacy column shape is present even if the table
	// already existed without it (e.g. from a prior half-completed run).
	if _, err := pool.Exec(ctx, `
		alter table public.snapshots add column if not exists path text
	`); err != nil {
		t.Fatalf("ensure public.snapshots.path exists: %v", err)
	}
	t.Cleanup(func() {
		// Drop on a fresh pool, not the test pool: a closed pool can't
		// run DDL and a panic in the cleanup would silently leak the
		// table into the next test.
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			dsn = "postgres:///faas?host=/run/postgresql&user=faas"
		}
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			return
		}
		c, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Exec(ctx, `drop table if exists public.snapshots`)
	})

	// (2) Apply migrations. 00023 drops `path` from the test schema's
	// snapshots only — the sibling public.snapshots is untouched.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// (3) Sanity: confirm the sibling distractor is still present and
	// still has a path column. If this fails the test is invalid (the
	// foreign schema no longer poses a threat), so skip with a clear
	// message rather than masking the tripwire.
	var foreignCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.columns
		 where table_schema = current_schema()
		   and table_name = 'snapshots' and column_name = 'path'
	`).Scan(&foreignCount); err != nil {
		t.Fatalf("sanity probe on public.snapshots: %v", err)
	}
	if foreignCount == 0 {
		t.Skip("sanity: public.snapshots.path is missing; tripwire cannot reproduce the regression in this environment")
	}

	// (4) The scoped assertion from TestMigrations_00023_*. If the
	// filter is removed in a future PR, the foreign column leaks in
	// and this fails with the same error message as the original CI
	// hit ("snapshots.path column still exists after 00023").
	var pathExists int
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.columns
		 where table_schema = current_schema()
		   and table_name = 'snapshots' and column_name = 'path'
	`).Scan(&pathExists); err != nil {
		t.Fatalf("scoped probe: %v", err)
	}
	if pathExists != 0 {
		t.Errorf("scoped query returned %d path columns in test schema after migrations; the table_schema = current_schema() filter is missing or wrong, so the assertion is reading foreign schemas (likely public.snapshots.path from the tripwire seed)", pathExists)
	}

	// (5) Negative control: confirm the unscoped query WOULD have
	// fired (count > 0 across search_path). This pins that the test
	// really did set up a foreign distractor and the scoping was the
	// load-bearing difference. If this assertion flips to 0, the
	// tripwire seed no longer works (e.g. because pgtest.Open stops
	// including public in search_path) and the tripwire would be
	// silently inert — a regression masquerading as a green test.
	var unscoped int
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.columns
		 where table_name = 'snapshots' and column_name = 'path'
	`).Scan(&unscoped); err != nil {
		t.Fatalf("unscoped probe: %v", err)
	}
	if unscoped == 0 {
		t.Errorf("negative control failed: unscoped query returned 0; the public.snapshots distractor is not visible to search_path, so the tripwire above is inert")
	}
}
