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
	"io/fs"
	"regexp"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

var migrationNameRe = regexp.MustCompile(`^(\d+)_.+\.sql$`)

// TestMigrationsApplyAndWalk runs the full migration set against a fresh
// per-test schema and walks the resulting goose_db_version table.
//
// Two assertions:
//
//  1. row_count == max(version_id) for applied rows — a gap in the
//     version sequence would mean a row was inserted out of order, which
//     goose's bookkeeping should never produce. (Sanity check on the
//     version table itself.)
//  2. max(version_id) == highest embedded migration prefix — the binary's
//     embedded set must agree with what goose recorded. Catches
//     findMissingMigrations-style failures that embed_test.go misses (e.g.,
//     a future migration whose SQL fails to apply, leaving the version
//     table short of the filename set).
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
	// migration — so for N migrations applied the table holds N+1 rows
	// and MAX(version_id) == N. A gap in the version sequence manifests
	// as MAX(version_id) < (nRows - 1), not as a row-count mismatch.
	var nRows, maxVer int64
	if err := pool.QueryRow(ctx,
		"SELECT COUNT(*), COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied",
	).Scan(&nRows, &maxVer); err != nil {
		t.Fatalf("query goose_db_version: %v", err)
	}
	if nRows != maxVer+1 {
		t.Errorf("goose_db_version row count %d != max(version_id) %d + 1: hole in the applied version sequence (the +1 is the version=0 sentinel goose inserts at table creation)", nRows, maxVer)
	}

	// Highest embedded prefix — derived the same way embed_test.go does.
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	var highest int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationNameRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			continue
		}
		if v > highest {
			highest = v
		}
	}

	if maxVer != highest {
		t.Errorf("goose_db_version max(version_id) = %d, but embedded migration set's highest prefix = %d; they must agree", maxVer, highest)
	}

	// Sanity assertion: every embedded migration is accounted for. The
	// embedded set has no version=0 row, but goose's table does (the
	// createVersionTable sentinel) — so we compare embeddedVersions
	// against (nRows - 1). A future migration whose SQL failed to
	// apply would leave (nRows - 1) short of embeddedVersions.
	var embeddedVersions int64
	for _, e := range entries {
		m := migrationNameRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		if _, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			embeddedVersions++
		}
	}
	if nRows-1 != embeddedVersions {
		t.Errorf("goose_db_version applied rows - 1 (sentinel) = %d, embedded migration count = %d; some migrations failed to apply silently", nRows-1, embeddedVersions)
	}
}
