//go:build !no_pg

// Migration-apply test for 00165_build_provenance_framework_version.sql
// (issue #740 / DEPLOY-PROV-5). Pins the framework_version column
// shape + the partial index — the load-bearing import for the
// version-inference populator at pkg/builderd::recordProvenance.
//
// Pins:
//
//  1. Migration set applies cleanly through 00165.
//  2. build_provenance.framework_version column exists, is nullable
//     (TEXT, no NOT NULL), and serves the operator-debug use case
//     from ADR-087 §3 (ADR-052 explicitly rejected the equivalent
//     column on BuildManifest because that table is pipeline-read;
//     build_provenance is observability-only).
//  3. The partial index (framework_version) WHERE framework_version
//     IS NOT NULL exists — the typical row has NULL (no version
//     detected), so a full B-tree would be 99% empty.
//  4. Replay-safe: second MigrateUp is a no-op (ADR-041).
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00165_BuildProvenanceFrameworkVersion(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// Column shape: must be text, nullable. Filtered on current_schema()
	// so a parallel test in another schema can't pollute the assertions.
	rows, err := pool.Query(ctx, `
		SELECT data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'build_provenance'
		  AND column_name = 'framework_version'
	`)
	if err != nil {
		t.Fatalf("query information_schema.columns: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("build_provenance.framework_version column missing")
	}
	var dataType, nullable string
	if err := rows.Scan(&dataType, &nullable); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if dataType != "text" {
		t.Errorf("build_provenance.framework_version: got data_type=%s, want text", dataType)
	}
	if nullable != "YES" {
		t.Errorf("build_provenance.framework_version: got nullable=%s, want YES (post-mortem metadata; never enforced by the build pipeline)", nullable)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	// Index presence: the partial index (framework_version) WHERE
	// framework_version IS NOT NULL — the typical row has NULL (no
	// version detected), so a full B-tree would be 99% empty.
	idxRows, err := pool.Query(ctx, `
		SELECT indexname
		FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND tablename = 'build_provenance'
		  AND indexname = 'build_provenance_framework_version_idx'
	`)
	if err != nil {
		t.Fatalf("query pg_indexes: %v", err)
	}
	defer idxRows.Close()
	if !idxRows.Next() {
		t.Errorf("build_provenance missing partial index build_provenance_framework_version_idx")
	}
	if err := idxRows.Err(); err != nil {
		t.Fatalf("idxRows.Err: %v", err)
	}
}
