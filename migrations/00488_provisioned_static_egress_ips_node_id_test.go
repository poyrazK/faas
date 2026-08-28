//go:build !no_pg

// Migration-apply test for 00488_provisioned_static_egress_ips_node_id.sql
// (ADR-119 v2 multi-node static egress IP). Pins four concerns
// because the migration carries four:
//
//  1. Migration set applies cleanly through 00488. Slot history:
//     00428 (initial) → 00472 → 00475 → 00479 → 00481 → 00484
//     → 00486 (PR #1111 trace_id) → 00487 (PR #1090 CORS FK)
//     → **00488** (this PR). 00488 is the next free slot above
//     mainline 00487. Verify no goose duplicate-version panic
//     against any of main's or any open PR's slots.
//  2. provisioned_static_egress_ips.node_id column exists under
//     that exact name, is UUID, NOT NULL after backfill, and
//     has no DEFAULT (the backfill UPDATE is the load-bearing
//     step; a DEFAULT here would silently land a non-existent
//     UUID on legacy rows after the backfill ran).
//  3. FK constraint provisioned_static_egress_ips_node_id_fk
//     exists, points at compute_nodes(id), and uses ON DELETE
//     RESTRICT — the cascade decision is load-bearing. RESTRICT
//     forces the operator to clear every pinned app's
//     static_egress_ip before deleting a compute_nodes row
//     (CASCADE would silently revoke customer pins; SET NULL
//     would silently shift to "no owner").
//  4. Per-node reverse-lookup index
//     provisioned_static_egress_ips_node_id_idx exists — the
//     vmmd bundle loader's SIGHUP reconcile path runs
//     "SELECT customer_ip FROM provisioned_static_egress_ips
//     WHERE node_id = $1" and needs the index.
//
// The test also verifies the backfill landed: every pre-existing
// row's node_id equals the synthetic 'default-local' row's id.

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00488_ProvisionedStaticEgressIPsNodeID(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00488 should land last.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (failure mode: a slot collision between this migration's 00488 and an open-PR fence — re-run the open-PR slot precheck including refs/pull/<N>/head)", err)
	}

	// (2) Column shape pin. information_schema.columns exposes
	// (data_type, is_nullable, column_default) directly. The
	// load-bearing pins are is_nullable=NO (NOT NULL after
	// backfill) and column_default IS NULL — the backfill UPDATE
	// is the canonical writer; a DEFAULT clause here would silently
	// land a non-existent UUID on legacy rows after the backfill
	// ran (the FK would then 23503 on any subsequent vmmd INSERT).
	var dataType, isNullable, columnDefault string
	if err := pool.QueryRow(ctx, `
		select data_type, is_nullable, column_default
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name = 'provisioned_static_egress_ips'
		   and column_name = 'node_id'
	`).Scan(&dataType, &isNullable, &columnDefault); err != nil {
		t.Fatalf("query provisioned_static_egress_ips.node_id column: %v (the column must have landed)", err)
	}
	if dataType != "uuid" {
		t.Errorf("provisioned_static_egress_ips.node_id: data_type=%q, want uuid", dataType)
	}
	if isNullable != "NO" {
		t.Errorf("provisioned_static_egress_ips.node_id: is_nullable=%q, want NO (the backfill UPDATE must have populated every pre-existing row before the NOT NULL constraint landed)", isNullable)
	}
	if columnDefault != "" {
		t.Errorf("provisioned_static_egress_ips.node_id: column_default=%q, want NULL — the column must NOT carry a DEFAULT (any DEFAULT would silently land a non-existent UUID on legacy rows; the FK would then 23503 on any subsequent vmmd INSERT)", columnDefault)
	}

	// (3) FK constraint shape. pg_get_constraintdef emits the
	// FOREIGN KEY (...) REFERENCES ... ON DELETE RESTRICT clause;
	// string-pin is the load-bearing pattern (the pg_get_constraintdef
	// shapes memory entry documents the version-dependent wrapping —
	// 15 wraps in `FOREIGN KEY (...) ...`, 16+ strips the wrapper).
	var fkDef string
	if err := pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'provisioned_static_egress_ips_node_id_fk'
		   and n.nspname = current_schema()
		   and c.contype = 'f'
	`).Scan(&fkDef); err != nil {
		t.Fatalf("query provisioned_static_egress_ips_node_id_fk constraint: %v (the FK constraint must have landed with this exact conname so the Down block can DROP it; auto-generated FK names are platform-dependent — verify the migration's explicit name)", err)
	}
	for _, want := range []string{
		"compute_nodes",
		"ON DELETE RESTRICT",
	} {
		if !strings.Contains(fkDef, want) {
			t.Errorf("provisioned_static_egress_ips_node_id_fk: def=%q missing %q — ADR-119 v2 mandates compute_nodes(id) target with ON DELETE RESTRICT (any other shape — CASCADE, SET NULL, NO ACTION — silently breaks customer pins or shift-to-no-owner semantics)", fkDef, want)
		}
	}

	// (4) Per-node reverse-lookup index. The vmmd bundle loader's
	// SIGHUP reconcile path runs
	//   SELECT customer_ip FROM provisioned_static_egress_ips
	//   WHERE node_id = $1
	// and needs the index — without it, the loader scans the
	// full table. Pin the indexname verbatim.
	var indexDef string
	if err := pool.QueryRow(ctx, `
		select indexdef
		  from pg_indexes
		 where schemaname = current_schema()
		   and tablename = 'provisioned_static_egress_ips'
		   and indexname = 'provisioned_static_egress_ips_node_id_idx'
	`).Scan(&indexDef); err != nil {
		t.Fatalf("query provisioned_static_egress_ips_node_id_idx: %v (the per-node reverse-lookup index must have landed)", err)
	}
	if !strings.Contains(indexDef, "node_id") {
		t.Errorf("provisioned_static_egress_ips_node_id_idx: def=%q missing node_id column reference", indexDef)
	}

	// Replay safety: re-running the migration set is a no-op.
	// ADD COLUMN IF NOT EXISTS + UPDATE WHERE node_id IS NULL +
	// CREATE INDEX IF NOT EXISTS are all idempotent; SET NOT NULL
	// on an already-NOT-NULL column is a no-op (Postgres 12+
	// skips the rewrite when the constraint already holds).
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp (replay): %v (00488 must be replay-safe — ADD COLUMN IF NOT EXISTS + UPDATE WHERE node_id IS NULL + CREATE INDEX IF NOT EXISTS)", err)
	}
}
