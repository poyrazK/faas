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
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// explainNoSeqScan returns the text EXPLAIN of query with
// enable_seqscan disabled for the duration of a single transaction.
//
// Index-usage assertions in this package would otherwise be at the
// mercy of planner cost estimates: on a freshly-migrated test schema
// every table holds a handful of rows, so a Seq Scan is genuinely the
// cheapest plan and the planner is right to pick it. Disabling seqscan
// turns the question from "is the index cheaper today?" (a cost
// question, not a schema question) into "can the planner use this
// index for this predicate at all?" — which is the schema contract the
// migration actually established. If the index is dropped, renamed, or
// its partial predicate stops being implied by the query, the plan
// falls back to a Seq Scan even with the penalty applied and the
// assertion fires.
//
// The SET must be `set local` inside an explicit transaction: pgxpool
// hands out an arbitrary connection per call, so a session-level SET
// would not reliably apply to the EXPLAIN that follows.
func explainNoSeqScan(ctx context.Context, t *testing.T, pool *pgxpool.Pool, query string, args ...any) (string, error) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local enable_seqscan = off`); err != nil {
		return "", fmt.Errorf("set local enable_seqscan: %w", err)
	}
	rows, err := tx.Query(ctx, "explain (format text) "+query, args...)
	if err != nil {
		return "", fmt.Errorf("explain: %w", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", fmt.Errorf("explain scan: %w", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("explain rows: %w", err)
	}
	return plan.String(), nil
}

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

	// (3) The partial index exists, keys on (region, zone), and its
	//     predicate is restricted to active nodes.
	//
	//     The index name is the wire contract the chooser relies on; if
	//     a future migration renames it the chooser's filter-and-sort
	//     scan falls back to a full table scan — not a failure, so the
	//     rename would otherwise be silent. This makes it loud.
	//
	//     We read the catalog's *structure* (pg_index.indkey,
	//     pg_index.indpred) rather than the rendered indexdef string.
	//     Postgres normalises boolean predicates: `WHERE active = true`
	//     used to print as "WHERE (active = true)" and now prints as
	//     "WHERE active", so a substring probe on the rendering pins a
	//     formatting detail rather than the index shape.
	var isPartial bool
	var predicate string
	var keyCols []string
	if err := pool.QueryRow(ctx, `
		select i.indpred is not null                     as is_partial,
		       coalesce(pg_get_expr(i.indpred, i.indrelid), '') as predicate,
		       (select array_agg(a.attname order by k.ord)
		          from unnest(i.indkey) with ordinality as k(attnum, ord)
		          join pg_attribute a
		            on a.attrelid = i.indrelid
		           and a.attnum   = k.attnum)            as key_cols
		  from pg_index i
		  join pg_class     c on c.oid = i.indexrelid
		  join pg_namespace n on n.oid = c.relnamespace
		 where n.nspname  = current_schema()
		   and c.relname  = 'compute_nodes_region_zone_idx'
	`).Scan(&isPartial, &predicate, &keyCols); err != nil {
		t.Errorf("compute_nodes_region_zone_idx missing: %v", err)
	} else {
		if !isPartial {
			t.Errorf("compute_nodes_region_zone_idx is not a partial index; it must be restricted to active nodes so drained/unavailable rows stay out of the chooser's index")
		}
		if !strings.Contains(predicate, "active") {
			t.Errorf("compute_nodes_region_zone_idx predicate = %q, want a predicate over the active column", predicate)
		}
		if want := []string{"region", "zone"}; !slices.Equal(keyCols, want) {
			t.Errorf("compute_nodes_region_zone_idx key columns = %v, want %v (the chooser looks up by region then zone)", keyCols, want)
		}
	}

	// (3b) Semantics, not rendering: the predicate must actually be
	//      usable for the chooser's lookup. With seqscan disabled inside
	//      a transaction, the planner has to reach for the index — which
	//      it can only do if the query's predicate implies the index's
	//      partial predicate. A predicate that drifted (say, to
	//      `lifecycle = 'active'`) would still be "partial" but would no
	//      longer be implied by `active`, and this EXPLAIN would fall
	//      back to a Seq Scan.
	if plan, err := explainNoSeqScan(ctx, t, pool, `
		select id from compute_nodes
		 where region = $1 and zone = $2 and active
	`, "local", "local"); err != nil {
		t.Errorf("explain region/zone lookup: %v", err)
	} else if !strings.Contains(plan, "compute_nodes_region_zone_idx") {
		t.Errorf("planner did not reach compute_nodes_region_zone_idx for the chooser's (region, zone, active) lookup:\n%s", plan)
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
