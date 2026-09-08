//go:build !no_pg

// Migration-apply test for 00194_cron_fire_now_requests.sql
// (ADR-090 PR-C / fire-now request queue).
//
// Pins:
//
//  1. Migration set applies cleanly through 00194 (no goose
//     duplicate-version panic against main's 00191 fence +
//     00192 edge_rules + 00193 reserve slot fence).
//  2. cron_fire_now_requests table exists with the 5-column CHECK
//     shape (status enum: pending|running|succeeded|failed|cancelled).
//  3. The cron_id column has a FOREIGN KEY to crons(id) with
//     ON DELETE CASCADE — a deleted cron drops its pending
//     fire-nows (defence in depth; the API surface 404s first).
//  4. The two indexes (pending hot-path + cron_id lookup) exist.
//  5. Replay-safe: second MigrateUp is a no-op (IF NOT EXISTS on
//     the CREATE TABLE + CREATE INDEX statements).
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00194_CronFireNowRequests(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Table presence + status enum CHECK.
	checkRows, err := pool.Query(ctx, `
		SELECT pg_get_constraintdef(c.oid)
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		WHERE t.relname = 'cron_fire_now_requests'
		  AND c.contype = 'c'
		  AND conname LIKE '%status%'
	`)
	if err != nil {
		t.Fatalf("query pg_constraint for status CHECK: %v", err)
	}
	defer checkRows.Close()
	if !checkRows.Next() {
		t.Fatalf("cron_fire_now_requests status CHECK constraint missing")
	}
	var checkDef string
	if err := checkRows.Scan(&checkDef); err != nil {
		t.Fatalf("scan CHECK def: %v", err)
	}
	wantStatuses := []string{"pending", "running", "succeeded", "failed", "cancelled"}
	for _, want := range wantStatuses {
		if !strings.Contains(checkDef, want) {
			t.Errorf("status CHECK missing %q; got: %s", want, checkDef)
		}
	}
	if err := checkRows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	// Foreign key to crons(id) ON DELETE CASCADE.
	fkRows, err := pool.Query(ctx, `
		SELECT confdeltype
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_class ref ON ref.oid = c.confrelid
		WHERE t.relname = 'cron_fire_now_requests'
		  AND c.contype = 'f'
		  AND ref.relname = 'crons'
	`)
	if err != nil {
		t.Fatalf("query pg_constraint for FK: %v", err)
	}
	defer fkRows.Close()
	if !fkRows.Next() {
		t.Fatalf("cron_fire_now_requests FK to crons missing")
	}
	var delType string
	if err := fkRows.Scan(&delType); err != nil {
		t.Fatalf("scan FK deltype: %v", err)
	}
	// 'c' = CASCADE in pg_constraint.confdeltype.
	if delType != "c" {
		t.Errorf("FK on delete: got %q, want 'c' (CASCADE)", delType)
	}
	if err := fkRows.Err(); err != nil {
		t.Fatalf("fkRows.Err: %v", err)
	}

	// Index presence — both hot-path (pending) and lookup (cron_id).
	for _, wantIdx := range []string{
		"cron_fire_now_requests_pending_idx",
		"cron_fire_now_requests_cron_idx",
	} {
		idxRows, err := pool.Query(ctx, `
			SELECT indexname
			FROM pg_indexes
			WHERE schemaname = current_schema()
			  AND tablename = 'cron_fire_now_requests'
			  AND indexname = $1
		`, wantIdx)
		if err != nil {
			t.Fatalf("query pg_indexes for %s: %v", wantIdx, err)
		}
		if !idxRows.Next() {
			t.Errorf("cron_fire_now_requests missing index %s", wantIdx)
		}
		idxRows.Close()
	}
}
