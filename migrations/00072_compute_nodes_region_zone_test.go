//go:build !no_pg

// Migration-apply test for 00072 (multi-box placement scheduler:
// region/zone columns on compute_nodes).
//
// Pins the load-bearing contract from the placement scheduler PR
// (ADR-025/028/029, scale-out worktree):
//
//   1. The migration set applies cleanly through 00072.
//   2. compute_nodes gains nullable region text and zone text columns.
//   3. The seeded default-local row is backfilled to ('local', 'local')
//      so the chooser tie-break is deterministic on a single-box deploy.
//   4. compute_nodes_region_zone_idx exists as a partial index
//      WHERE active = true and supports lookup by (region, zone).
//   5. The columns are nullable: an INSERT that omits region/zone still
//      succeeds (lets operator-added rows accept the schema without
//      forcing a one-time UPDATE). Default-local seed is the only
//      backfilled row in this migration.
//
// Build tag mirrors 00065_compute_node_heartbeats_test.go:1 and
// 00066_usage_minutes_egress_test.go:1 — set FAAS_SKIP_PG_TESTS=1
// locally to skip.

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00072_ComputeNodesRegionZone(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// (1) Both columns exist on compute_nodes and are nullable text.
	//     Region/zone are deliberately NOT NULL=YES so pre-00072
	//     operator rows accept the schema without a backfill.
	for _, col := range []string{"region", "zone"} {
		var dataType, nullable string
		if err := pool.QueryRow(ctx, `
			select data_type, is_nullable
			  from information_schema.columns
			 where table_schema = current_schema()
			   and table_name   = 'compute_nodes'
			   and column_name  = $1
		`, col).Scan(&dataType, &nullable); err != nil {
			t.Errorf("compute_nodes.%s not present after migrations apply: %v", col, err)
			continue
		}
		if dataType != "text" {
			t.Errorf("compute_nodes.%s data_type = %q, want text", col, dataType)
		}
		if nullable != "YES" {
			t.Errorf("compute_nodes.%s is_nullable = %q, want YES (nullable so pre-00072 rows accept the schema)", col, nullable)
		}
	}

	// (2) Default-local row backfilled to ('local', 'local'). This
	//     is the load-bearing contract for single-box deploys: the
	//     chooser's tie-break on (region, name) must see a defined
	//     region rather than NULL so ordering is deterministic.
	var region, zone string
	if err := pool.QueryRow(ctx, `
		select region, zone
		  from compute_nodes
		 where name = 'default-local'
	`).Scan(&region, &zone); err != nil {
		t.Fatalf("lookup default-local compute_nodes row: %v", err)
	}
	if region != "local" {
		t.Errorf("default-local.region = %q, want \"local\" (migration backfill)", region)
	}
	if zone != "local" {
		t.Errorf("default-local.zone = %q, want \"local\" (migration backfill)", zone)
	}

	// (3) The partial index exists on (region, zone) WHERE active.
	//     pg_indexes view is the canonical probe; rename-detector.
	var idxDef string
	if err := pool.QueryRow(ctx, `
		select indexdef
		  from pg_indexes
		 where schemaname = current_schema()
		   and tablename  = 'compute_nodes'
		   and indexname  = 'compute_nodes_region_zone_idx'
	`).Scan(&idxDef); err != nil {
		t.Errorf("compute_nodes_region_zone_idx missing: %v", err)
	} else {
		// Cheap shape probe — the index name is the wire contract
		// the chooser relies on; if a future migration renames it
		// the chooser's filter-and-sort scan will fall back to a
		// full table scan, not a failure, so the rename is silent.
		// This assertion makes the rename loud.
		//
		// PostgreSQL formats partial-index predicates as
		// "WHERE (active = true)" (parenthesised boolean) starting
		// from PG 9.x; a bare "WHERE active" form is never emitted.
		// The substring probe below matches that real output while
		// still rejecting a non-partial index (which would have no
		// "WHERE" predicate at all).
		if !strings.Contains(idxDef, "WHERE (active") {
			t.Errorf("compute_nodes_region_zone_idx indexdef = %q, want substring %q (partial predicate on active)", idxDef, "WHERE (active")
		}
	}

	// (4) INSERT a row without region/zone — must succeed because
	//     the columns are nullable. This is the contract that lets
	//     operator-added rows accept the 00072 schema without a
	//     one-time backfill transaction.
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ('00072-no-region-test', 'tcp://127.0.0.1:1', 1, 256, 1, 256, 'active'::compute_node_lifecycle)
	`); err != nil {
		t.Errorf("insert compute_nodes with NULL region/zone (must succeed under nullable): %v", err)
	}
}
