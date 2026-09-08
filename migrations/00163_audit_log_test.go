//go:build !no_pg

// Migration-apply test for 00163_audit_log.sql (issue #755 / PR-5).
// Pins the audit_log table shape + the load-bearing FK-free invariant
// the events-FK-survival story depends on.
//
// Pins:
//
//  1. Migration set applies cleanly through 00163.
//  2. audit_log: column shape, nullable account_id + account_email
//     + actor + data, NOT NULL id / kind / received_at.
//  3. audit_log has NO foreign key constraints (the audit row must
//     outlive the account it relates to — a regulator can re-derive
//     the post-deletion state from the audit_log row alone).
//  4. The two partial / sorted indexes exist.
//  5. Replay-safe: second MigrateUp is a no-op (ADR-041).
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

func TestMigrations_00163_AuditLog(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Column shape + nullability. Filtered on current_schema() so a
	// parallel test in another schema can't pollute the assertions.
	rows, err := pool.Query(ctx, `
		SELECT column_name, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'audit_log'
		ORDER BY ordinal_position
	`)
	if err != nil {
		t.Fatalf("query information_schema.columns: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var name, nullable string
		if err := rows.Scan(&name, &nullable); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = nullable
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	want := map[string]string{
		"id":            "NO",
		"kind":          "NO",
		"account_id":    "YES", // nullable; no FK to accounts (survives deletion)
		"account_email": "YES", // captured at copy-time
		"actor":         "YES", // optional
		"received_at":   "NO",
		"data":          "YES", // optional
	}
	for col, nullable := range want {
		gotNullable, ok := got[col]
		if !ok {
			t.Errorf("audit_log missing column %q", col)
			continue
		}
		if gotNullable != nullable {
			t.Errorf("audit_log.%s: got nullable=%s, want %s", col, gotNullable, nullable)
		}
	}

	// Invariant: audit_log has no FK to accounts. A regulator must
	// be able to read the audit row after the account is gone.
	fkRows, err := pool.Query(ctx, `
		SELECT 1
		FROM information_schema.table_constraints
		WHERE table_schema = current_schema()
		  AND table_name = 'audit_log'
		  AND constraint_type = 'FOREIGN KEY'
		LIMIT 1
	`)
	if err != nil {
		t.Fatalf("query information_schema.table_constraints: %v", err)
	}
	defer fkRows.Close()
	if fkRows.Next() {
		t.Errorf("audit_log has a foreign key constraint — it must be FK-free so audit rows survive account deletion")
	}

	// Index presence (received_at desc + (account_id, received_at desc)).
	idxRows, err := pool.Query(ctx, `
		SELECT indexname
		FROM pg_indexes
		WHERE schemaname = current_schema() AND tablename = 'audit_log'
	`)
	if err != nil {
		t.Fatalf("query pg_indexes: %v", err)
	}
	defer idxRows.Close()
	indexes := map[string]bool{}
	for idxRows.Next() {
		var name string
		if err := idxRows.Scan(&name); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		indexes[name] = true
	}
	if err := idxRows.Err(); err != nil {
		t.Fatalf("idxRows.Err: %v", err)
	}
	for _, want := range []string{"audit_log_received_at_idx", "audit_log_account_idx"} {
		if !indexes[want] {
			t.Errorf("audit_log missing index %q", want)
		}
	}
}
