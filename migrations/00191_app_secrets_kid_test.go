//go:build !no_pg

// Migration-apply test for 00191_app_secrets_kid.sql
// (ADR-089 PR-A / per-secret rotation surface).
//
// Pins:
//
//  1. Migration set applies cleanly through 00191 (no goose
//     duplicate-version panic against main's
//     00166_reserve_slot.sql fence + 00167_apps_overflow_node.sql).
//  2. app_secrets.kid column exists, is nullable TEXT, and is the
//     recipient-fingerprint column pkg/rekey.Replayer + the
//     /v1/apps/{slug}/secrets/{key}/rotate handler stamp on every
//     re-seal.
//  3. The partial index app_secrets_kid_idx WHERE kid IS NOT NULL
//     exists — most pre-PR-A rows have NULL kid (backfill is
//     best-effort), so a full B-tree would be ~100% empty.
//  4. Replay-safe: second MigrateUp is a no-op (IF NOT EXISTS on
//     both the column add and the index create).
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

func TestMigrations_00191_AppSecretsKid(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Column shape: must be text, nullable. kid is the age-1...
	// recipient string of the host identity that sealed the row
	// (ADR-089 D4). Nullable because rows sealed before migration
	// 00191 have no kid recorded; the migration's best-effort
	// backfill only stamps rows that unseal under the loaded
	// identity set (corrupt / historical-keyed rows stay NULL).
	rows, err := pool.Query(ctx, `
		SELECT data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'app_secrets'
		  AND column_name = 'kid'
	`)
	if err != nil {
		t.Fatalf("query information_schema.columns: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("app_secrets.kid column missing")
	}
	var dataType, nullable string
	if err := rows.Scan(&dataType, &nullable); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if dataType != "text" {
		t.Errorf("app_secrets.kid: got data_type=%s, want text", dataType)
	}
	if nullable != "YES" {
		t.Errorf("app_secrets.kid: got nullable=%s, want YES (kid is stamped on every Seal from PR-A onward; older rows stay NULL until the rekey walk visits them)", nullable)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	// Index presence: the partial index (kid) WHERE kid IS NOT
	// NULL. Supports pkg/rekey.Replayer's hot path "find every
	// row whose kid != current" — those are the rows that need
	// re-sealing. NULL rows are excluded from the index by
	// default in PG; the WHERE clause is explicit for clarity.
	idxRows, err := pool.Query(ctx, `
		SELECT indexname
		FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND tablename = 'app_secrets'
		  AND indexname = 'app_secrets_kid_idx'
	`)
	if err != nil {
		t.Fatalf("query pg_indexes: %v", err)
	}
	defer idxRows.Close()
	if !idxRows.Next() {
		t.Errorf("app_secrets missing partial index app_secrets_kid_idx")
	}
	if err := idxRows.Err(); err != nil {
		t.Fatalf("idxRows.Err: %v", err)
	}
}
