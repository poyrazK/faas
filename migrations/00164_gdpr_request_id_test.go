//go:build !no_pg

// Migration-apply test for 00164_gdpr_request_id.sql (issue #755 /
// PR-5.2). Pins the additive shape + the load-bearing partial index.
//
// Pins:
//
//  1. Migration set applies cleanly through 00164.
//  2. gdpr_requests.request_id column exists and is nullable.
//  3. gdpr_requests_request_id_idx is a partial index
//     (WHERE request_id IS NOT NULL) — the WHERE clause is what
//     keeps NULL request_ids (the existing 100% of rows pre-PR-5.2)
//     from polluting the index, so the idempotency probe stays
//     O(log n) for accounts with thousands of legacy rows.
//  4. The (account_id, request_id) column order is preserved — the
//     account-scoped probe predicate is the index's lookup prefix.
//  5. Replay-safe: second MigrateUp is a no-op (ADR-041).
package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00164_GdprRequestId(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Column shape: nullable, TEXT.
	var nullable string
	if err := pool.QueryRow(ctx, `
		SELECT is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'gdpr_requests'
		  AND column_name = 'request_id'
	`).Scan(&nullable); err != nil {
		t.Fatalf("query request_id column: %v", err)
	}
	if nullable != "YES" {
		t.Errorf("gdpr_requests.request_id nullable = %q, want YES", nullable)
	}

	// Partial index presence + WHERE clause. pg_indexes doesn't
	// surface the predicate, so we read it from pg_get_indexdef.
	var indexdef string
	if err := pool.QueryRow(ctx, `
		SELECT pg_get_indexdef(indexrelid)
		FROM pg_index
		WHERE indexrelid = 'gdpr_requests_request_id_idx'::regclass
	`).Scan(&indexdef); err != nil {
		t.Fatalf("query pg_get_indexdef: %v", err)
	}
	// The index must be partial (predicate) AND cover (account_id, request_id).
	if !stringsContains(indexdef, "WHERE") {
		t.Errorf("index is not partial: %q", indexdef)
	}
	if !stringsContains(indexdef, "request_id") {
		t.Errorf("index does not cover request_id: %q", indexdef)
	}
	if !stringsContains(indexdef, "account_id") {
		t.Errorf("index does not cover account_id (probe is account-scoped): %q", indexdef)
	}
}

// stringsContains is a tiny no-import helper to keep this file's
// import list identical to the other migration tests.
func stringsContains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
