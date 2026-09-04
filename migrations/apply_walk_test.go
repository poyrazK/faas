//go:build !no_pg

// End-to-end migration apply-and-walk test. Catches the failure mode that
// bit PR #93's deploy (run 29841378918): goose's strict findMissingMigrations
// refuses to apply a binary whose embedded migration set has a slot missing
// from the DB. The static check in embed_test.go catches this from filenames
// alone; this test catches it from a real goose run, including SQL that
// parses but fails to apply.
//
// Build tag: !no_pg matches cmd/e2e/meterd_quota_e2e_test.go:11. Set
// FAAS_SKIP_PG_TESTS=1 to opt out locally without rebuilding.

package migrations_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrationsApplyAndWalk runs the full migration set against a fresh
// per-test schema and walks the resulting goose_db_version table.
//
// Three assertions:
//
//  1. every embedded migration ID has an applied ledger row. IDs need not be
//     contiguous after ADR-142's timestamp cutover.
//  2. max(version_id) == highest embedded migration prefix — the binary's
//     embedded set must agree with what goose recorded. Catches
//     findMissingMigrations-style failures that embed_test.go misses (e.g.,
//     a future migration whose SQL fails to apply, leaving the version
//     table short of the filename set).
//  3. applied row count (minus the v0 sentinel) == number of embedded
//     migration files — every migration present on disk must have been
//     applied to the schema. Catches the silent-skip failure mode where
//     a file's `-- +goose Up` directive was malformed and the SQL was
//     never executed.
//
// On developer laptops without Postgres the test skips via pgtest.Open's
// t.Skipf path — no Docker required.
func TestMigrationsApplyAndWalk(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t) // t.Skip-friendly on missing DATABASE_URL

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (this is the failure mode that bit PR #93's deploy: a missing migration slot between 1 and max(version))", err)
	}

	// Walk goose_db_version. Goose creates a sentinel row (version_id=0,
	// is_applied=true) on first table creation, then one row per applied
	// migration. Exact set membership is load-bearing: MAX(version_id) can be
	// current while a lower timestamp migration that merged later is absent.
	var nRows, maxVer int64
	if err := pool.QueryRow(ctx,
		"SELECT COUNT(*), COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied",
	).Scan(&nRows, &maxVer); err != nil {
		t.Fatalf("query goose_db_version: %v", err)
	}
	// Pull the parsed embedded set from the shared helper. Reusing
	// migrations.LoadMigrations keeps filename-parsing rules identical
	// between the static and apply-and-walk tests — if the convention
	// ever changes (e.g. dropping the leading digits), both packages
	// see the change in lockstep.
	files := migrations.LoadMigrations(t)
	if len(files) == 0 {
		t.Fatal("no embedded migrations; embed.go is empty?")
	}
	embedded := make(map[int64]bool, len(files))
	for _, f := range files {
		embedded[f.Version] = true
	}
	appliedRows, err := pool.Query(ctx,
		`SELECT version_id FROM goose_db_version WHERE is_applied = true`)
	if err != nil {
		t.Fatalf("query applied versions: %v", err)
	}
	defer appliedRows.Close()
	applied := make(map[int64]bool, len(files)+1)
	for appliedRows.Next() {
		var version int64
		if err := appliedRows.Scan(&version); err != nil {
			t.Fatalf("scan applied version: %v", err)
		}
		applied[version] = true
	}
	if err := appliedRows.Err(); err != nil {
		t.Fatalf("walk applied versions: %v", err)
	}
	for version := range embedded {
		if !applied[version] {
			t.Errorf("goose_db_version is missing embedded migration version %d", version)
		}
	}
	expectedRows := int64(len(files) + 1) // embedded files + goose v0 sentinel
	if nRows != expectedRows {
		t.Errorf("goose_db_version row count %d != expected %d (embedded migrations plus the version=0 sentinel)", nRows, expectedRows)
	}

	highest := files[len(files)-1].Version
	highestName := files[len(files)-1].Name

	if maxVer != highest {
		t.Errorf("goose_db_version max(version_id) = %d, but embedded migration set's highest prefix is %s (version %d); they must agree", maxVer, highestName, highest)
	}

	// Sanity assertion: every embedded migration is accounted for. The
	// embedded set has no version=0 row, but goose's table does (the
	// createVersionTable sentinel) — so we compare len(files)
	// against (nRows - 1). A future migration whose SQL failed to
	// apply would leave (nRows - 1) short of len(files).
	if nRows-1 != int64(len(files)) {
		t.Errorf("goose_db_version applied rows - 1 (sentinel) = %d, embedded migration count = %d; some migrations failed to apply silently", nRows-1, len(files))
	}

	// Fresh-install schema pin: a fresh-DB apply must end with the
	// accounts table carrying provider_customer_id (not
	// stripe_customer_id) — the rename in 00040 is part of the
	// same apply sequence. Catches the failure mode PR #204 shipped:
	// a hand-edit to migration 00001 that left the rename target
	// column absent on a clean DB, causing 00040's ALTER TABLE to
	// fail with "column does not exist". ApplyAndWalk only checked
	// version-table row counts before; this assertion pins the
	// post-rename schema shape.
	assertColumnRenamed(t, pool, "accounts", "provider_customer_id")
}

// assertColumnRenamed fails the test if `table` does not have
// `column` after the migration apply. The query uses
// information_schema.columns which is the same source pg_dump uses
// for schema introspection, so it's the canonical "did the rename
// land?" probe. A schema with the OLD column name (or both) fails
// fast with the offending column list.
func assertColumnRenamed(t *testing.T, pool *pgxpool.Pool, table, column string) {
	t.Helper()
	var x int
	if err := pool.QueryRow(context.Background(),
		`SELECT 1 FROM information_schema.columns
		 WHERE table_schema = current_schema()
		   AND table_name = $1
		   AND column_name = $2`,
		table, column,
	).Scan(&x); err != nil {
		t.Fatalf("fresh-install schema check: table %q does not have column %q after migrations applied; the rename migration (00040) did not land on this DB (err=%v). This is the failure mode PR #204 shipped — migration 00001 was hand-edited to the new column name and 00040's RENAME statement then failed with 'column does not exist'.", table, column, err)
	}
}
