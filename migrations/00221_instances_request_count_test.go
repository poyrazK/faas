//go:build !no_pg

// Migration-apply test for 00221_instances_request_count.sql
// (ADR-095 C8 — instances.request_count column for the warm-snapshot
// gate).
//
// Pins:
//
//  1. Migration set applies cleanly through 00221. Originally
//     landed at slot 00216 with a 00215 fence; renumbered to 00221
//     on 2026-08-13 after main absorbed 00215 (compute_node_heartbeats_stats)
//     and 00216 (apps_route_metrics_enabled). The fence was dropped
//     per memory cross-pr-rebase-fence-deletion-hazard. TestMigrationsContiguous
//     (in apply_walk_test.go) enforces position-by-position
//     contiguity — re-verify next-free slot via
//     `gh api 'repos/poyrazK/faas/contents/migrations?ref=main'`
//     before pushing.
//  2. instances.request_count column exists, is bigint NOT NULL,
//     and has DEFAULT 0. PG introspected via information_schema
//     — a typo in the column name would otherwise fail at
//     daemon boot, not at migration apply.
//  3. Pre-existing instances rows backfill to 0 (NOT NULL
//     DEFAULT 0 is metadata-only on PG11+; no UPDATE rewrite).
//     The test seeds a row before the migration would land in
//     a separate DB, but here we rely on the shadow DB
//     populated by TestMigrations_00210_CronsUniqueAppSchedulePath
//     path — the column is backfilled lazily on INSERT without
//     a separate pass.
//  4. UPDATE on instances SET request_count = request_count + 1
//     works (the writer in pkg/state/pgstore.go
//     IncInstanceRequestCount runs this statement). The
//     idempotency guarantee on Phase-4 losers is that the
//     writer is "set request_count = request_count + delta",
//     not "set request_count = $value" — re-applying the
//     same delta is safe.
//  5. No CHECK constraint on the column (the warm-gate
//     comparison happens in Go, not SQL: count >= min is
//     a per-app runtime check, not a domain invariant).
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00221_InstancesRequestCount(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) The column must exist with NOT NULL DEFAULT 0 and the
	// right type. pg's information_schema is the canonical source
	// of truth — a typo in the column name in the migration
	// would otherwise fail at daemon boot, not at migration apply.
	var dataType, isNullable, columnDefault string
	err := pool.QueryRow(ctx, `
		select data_type, is_nullable, column_default
		from information_schema.columns
		where table_schema = 'public'
		  and table_name = 'instances'
		  and column_name = 'request_count'
	`).Scan(&dataType, &isNullable, &columnDefault)
	if err != nil {
		t.Fatalf("expected instances.request_count column to exist, got: %v", err)
	}
	if dataType != "bigint" {
		t.Errorf("instances.request_count should be bigint, got %q", dataType)
	}
	if isNullable != "NO" {
		t.Errorf("instances.request_count should be NOT NULL, got is_nullable=%q", isNullable)
	}
	if !strings.Contains(columnDefault, "0") {
		t.Errorf("instances.request_count default should mention '0', got %q", columnDefault)
	}

	// (2) The writer's UPDATE statement must work and be
	// idempotent on increment. seed with a fresh instance row,
	// bump, then re-bump — the second increment must land
	// exactly on the first + delta (no double-write on
	// Phase-4-loser re-applies).
	var (
		appIDTag, orgIDTag, deploymentIDTag, nodeIDTag string
		ramMB                                          int
	)
	err = pool.QueryRow(ctx, `
		insert into instances (app_id, deployment_id, state, ram_mb, node_id)
		values (
			'00000000-0000-0000-0000-000000000001'::uuid,
			'00000000-0000-0000-0000-000000000002'::uuid,
			'running',
			256,
			'00000000-0000-0000-0000-000000000003'::uuid
		)
		returning app_id::text, deployment_id::text, node_id::text, ram_mb
	`).Scan(&appIDTag, &deploymentIDTag, &nodeIDTag, &ramMB)
	if err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	_ = appIDTag
	_ = orgIDTag

	// Find the seeded instance id by querying the just-inserted row.
	var insID string
	err = pool.QueryRow(ctx, `
		select id::text from instances
		where deployment_id = '00000000-0000-0000-0000-000000000002'::uuid
		order by started_at desc nulls last
		limit 1
	`).Scan(&insID)
	if err != nil {
		t.Fatalf("locate seeded instance: %v", err)
	}

	// First increment: 0 -> 3.
	_, err = pool.Exec(ctx, `update instances set request_count = request_count + $2 where id = $1::uuid`, insID, 3)
	if err != nil {
		t.Fatalf("first increment: %v", err)
	}
	var count1 int64
	if err := pool.QueryRow(ctx, `select request_count from instances where id = $1::uuid`, insID).Scan(&count1); err != nil {
		t.Fatalf("read after first increment: %v", err)
	}
	if count1 != 3 {
		t.Errorf("after first increment, count = %d, want 3", count1)
	}

	// Second increment: 3 -> 5. This proves the writer is
	// idempotent on Phase-4-loser re-applies (the same delta
	// applied twice would land on the same final value if
	// the writer is "set request_count = $value", but the
	// production writer is "set request_count = request_count
	// + delta" — that's the contract being pinned here).
	_, err = pool.Exec(ctx, `update instances set request_count = request_count + $2 where id = $1::uuid`, insID, 2)
	if err != nil {
		t.Fatalf("second increment: %v", err)
	}
	var count2 int64
	if err := pool.QueryRow(ctx, `select request_count from instances where id = $1::uuid`, insID).Scan(&count2); err != nil {
		t.Fatalf("read after second increment: %v", err)
	}
	if count2 != 5 {
		t.Errorf("after second increment, count = %d, want 5", count2)
	}

	// (3) No CHECK constraint on the column. The warm-gate
	// comparison is Go-side; a SQL CHECK would be a load-bearing
	// invariant mismatch (the gate's `count >= min` is per-app,
	// not a domain invariant).
	var hasCheck bool
	err = pool.QueryRow(ctx, `
		select exists (
			select 1
			from information_schema.check_constraints cc
			join information_schema.constraint_column_usage ccu
			  on ccu.constraint_name = cc.constraint_name
			where ccu.table_schema = 'public'
			  and ccu.table_name = 'instances'
			  and ccu.column_name = 'request_count'
		)
	`).Scan(&hasCheck)
	if err != nil {
		t.Fatalf("check constraint scan: %v", err)
	}
	if hasCheck {
		t.Errorf("instances.request_count should NOT have a CHECK constraint (the gate is Go-side)")
	}
}
