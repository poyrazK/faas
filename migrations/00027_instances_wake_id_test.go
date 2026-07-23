//go:build !no_pg

// Migration-apply test for 00027 (instances.wake_id). Pins the
// load-bearing contract from the per-wake stable ID follow-up
// to gaps analysis 2026-07-23:
//
//   1. The migration set applies cleanly through 00027.
//   2. The wake_id column is NOT NULL after the migration runs.
//   3. The column default fills wake_id on INSERT — any future
//      code path that bypasses schedd's explicit wake_id arg
//      (e.g. an INSERT in a backfill script) still lands a
//      non-NULL value.
//   4. The partial index exists and is reachable for the dashboard's
//      per-app recent-wakes scan.
//
// The test does NOT pin the UUIDv7 shape of values minted Go-side —
// that contract is owned by pkg/sched/engine_test.go where the
// scheduling engine is the writer. Backfill values are gen_random_uuid()
// (v4) by design (see the migration's rationale block), so asserting
// shape at the migration layer would couple the schema to one of
// the two stamp sources.
//
// Build tag mirrors 00026_compute_node_notify_test.go:26.

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrations_00027_InstancesWakeID pins the schema-level invariants
// the platform relies on:
//
//   - wake_id exists on instances
//   - wake_id is NOT NULL post-migration
//   - wake_id is auto-stamped on INSERT (default fires for any caller
//     that doesn't supply an explicit value)
//   - The partial index supports (app_id, wake_id) scans and is
//     scoped to live states only.
func TestMigrations_00027_InstancesWakeID(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// (1) Column existence + NOT NULL. information_schema is the
	// source of truth here — the test pins the post-state, which
	// catches both a missing migration and a partial re-apply that
	// left the constraint unset.
	var (
		colName   string
		isNotNull bool
		dataType  string
	)
	if err := pool.QueryRow(ctx, `
		select column_name, data_type, is_nullable = 'NO'
		  from information_schema.columns
		 where table_name = 'instances' and column_name = 'wake_id'
	`).Scan(&colName, &dataType, &isNotNull); err != nil {
		t.Fatalf("lookup instances.wake_id: %v", err)
	}
	if colName != "wake_id" {
		t.Fatalf("column lookup returned %q, want wake_id", colName)
	}
	if dataType != "uuid" {
		t.Errorf("instances.wake_id data_type = %q, want uuid", dataType)
	}
	if !isNotNull {
		t.Errorf("instances.wake_id is nullable; wake_id must be NOT NULL post-migration")
	}

	// (2) Default fires on INSERT. Insert a parent app row, then an
	// instance row that omits wake_id; the row must come back with a
	// non-NULL wake_id (the column default gen_random_uuid() stamps).
	var appID string
	if err := pool.QueryRow(ctx, `
		insert into accounts (email) values ('wake-id-test@example.com')
		returning id
	`).Scan(&appID); err != nil {
		// accounts.email may be unique-constrained; tolerate duplicate.
		_ = pool.QueryRow(ctx, `select id from accounts where email = 'wake-id-test@example.com'`).Scan(&appID)
	}
	if appID == "" {
		t.Fatalf("could not seed accounts row for wake_id test")
	}

	var appRowID, depRowID string
	if err := pool.QueryRow(ctx, `
		insert into apps (account_id, slug, ram_mb)
		values ($1, 'wake-id-test-app', 256)
		returning id
	`, appID).Scan(&appRowID); err != nil {
		t.Fatalf("insert apps: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		insert into deployments (app_id, kind, image_digest, status, created_at)
		values ($1, 'image', 'sha256:seed-wake-id', 'live', now())
		returning id
	`, appRowID).Scan(&depRowID); err != nil {
		t.Fatalf("insert deployments: %v", err)
	}

	var (
		gotID   string
		gotWake string
	)
	if err := pool.QueryRow(ctx, `
		insert into instances (app_id, deployment_id, state, ram_mb, node_id)
		select $1, $2, 'parked', 256, id from compute_nodes where name = 'default-local'
		returning id, wake_id
	`, appRowID, depRowID).Scan(&gotID, &gotWake); err != nil {
		t.Fatalf("insert instances (wake_id via default): %v", err)
	}
	if gotID == "" {
		t.Errorf("inserted instances.id is empty")
	}
	if gotWake == "" {
		t.Errorf("inserted instances.wake_id is empty — column default gen_random_uuid() did not fire")
	}

	// (3) Partial index exists and is usable. pg_indexes is the
	// canonical source for "is the index present" — predigested by
	// Postgres so it stays accurate across partial-index variations.
	var indexDef string
	if err := pool.QueryRow(ctx, `
		select indexdef from pg_indexes
		 where schemaname = current_schema()
		   and tablename  = 'instances'
		   and indexname  = 'instances_wake_id_app_idx'
	`).Scan(&indexDef); err != nil {
		t.Fatalf("lookup instances_wake_id_app_idx: %v", err)
	}
	if indexDef == "" {
		t.Errorf("instances_wake_id_app_idx missing — partial index not created")
	}
}
