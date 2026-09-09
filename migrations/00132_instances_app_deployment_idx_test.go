//go:build !no_pg

// Migration-apply test for 00132 (issue #557 closure / ADR-072 —
// instances_app_deployment_idx partial index backing the per-deployment
// concurrency SELECT). Pins:
//
//  1. The migration set applies cleanly through 00132.
//  2. The index exists and is a partial index restricted to the three
//     live states (RUNNING, WAKING, COLD_BOOTING).
//  3. The index keys on (deployment_id, app_id) — the order both
//     production readers need.
//  4. The index serves the REAL production queries: with seqscan
//     disabled the planner reaches it for both
//     CountLiveInstancesByDeployment and ConcurrencyForDeployment.
//  5. Replay-safety: a second MigrateUp is a no-op.
//
// Slot note: 00131 is the previous slot in this branch's embedded set;
// renumber would need filename + test name + apply range bump together.
package migrations_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00132_InstancesAppDeploymentIdx(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00132.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 131)", err)
	}

	// (2) Index existence. pg_indexes scoping uses current_schema()
	// because pgtest isolates each test in its own search_path.
	var idxCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from pg_indexes
		where schemaname = current_schema()
		  and tablename = 'instances'
		  and indexname = 'instances_app_deployment_idx'
	`).Scan(&idxCount); err != nil {
		t.Fatalf("count pg_indexes: %v", err)
	}
	if idxCount != 1 {
		t.Errorf("instances_app_deployment_idx missing (count = %d, want 1)", idxCount)
	}

	// (3) Partial index restricted to the three live states. The
	// pg_indexes row exposes indexdef verbatim; we assert the
	// closed-state set is encoded in the WHERE clause. A regression
	// that drops the partial predicate would bloat the index to
	// cover every instances row (dominated by PARKED/STOPPED).
	var indexDef string
	if err := pool.QueryRow(ctx, `
		select indexdef from pg_indexes
		where schemaname = current_schema()
		  and tablename = 'instances'
		  and indexname = 'instances_app_deployment_idx'
	`).Scan(&indexDef); err != nil {
		t.Fatalf("read indexdef: %v", err)
	}
	if !strings.Contains(indexDef, "WHERE") {
		t.Errorf("indexdef missing WHERE predicate (not a partial index): %s", indexDef)
	}
	// Lowercase, matching instances_state_check. 00132 shipped these
	// UPPERCASE, which made the index match zero rows; migration
	// 20260908174300244 recreated it.
	for _, want := range []string{"'running'", "'waking'", "'cold_booting'"} {
		if !strings.Contains(indexDef, want) {
			t.Errorf("indexdef missing live state %q: %s", want, indexDef)
		}
	}

	// (3b) Index shape: keyed on (deployment_id, app_id) in that
	// order. The ordering is the load-bearing half.
	// CountLiveInstancesByDeployment filters on deployment_id ALONE, so a
	// btree leading on app_id cannot serve it — its leading column would
	// be unconstrained. Leading on deployment_id serves that query
	// directly and ConcurrencyForDeployment (app_id AND deployment_id)
	// as a scan plus a cheap filter.
	var keyCols []string
	if err := pool.QueryRow(ctx, `
		select (select array_agg(a.attname order by k.ord)
		          from unnest(i.indkey) with ordinality as k(attnum, ord)
		          join pg_attribute a
		            on a.attrelid = i.indrelid
		           and a.attnum   = k.attnum)
		  from pg_index i
		  join pg_class     c on c.oid = i.indexrelid
		  join pg_namespace n on n.oid = c.relnamespace
		 where n.nspname = current_schema()
		   and c.relname = 'instances_app_deployment_idx'
	`).Scan(&keyCols); err != nil {
		t.Fatalf("read instances_app_deployment_idx key columns: %v", err)
	}
	if want := []string{"deployment_id", "app_id"}; !slices.Equal(keyCols, want) {
		t.Errorf("instances_app_deployment_idx key columns = %v, want %v (leading on deployment_id is what makes CountLiveInstancesByDeployment an index lookup)", keyCols, want)
	}

	// (4) The index serves the real production queries. 00132's own
	// predicate used UPPERCASE state literals while instances.state has
	// been lowercase since 00001, so the index matched zero rows and this
	// step could only EXPLAIN against the dead predicate. Migration
	// 20260908174300244 recreated the index; the assertions below now
	// EXPLAIN the two queries production actually issues.
	var nodeID string
	if err := pool.QueryRow(ctx,
		`select id from compute_nodes where name = 'default-local'`,
	).Scan(&nodeID); err != nil {
		t.Fatalf("look up default-local compute node (instances.node_id FK target): %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ('00000000-0000-0000-0000-000000000132', 'scale', 'idx-test@example.com')
	`); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, ram_mb)
		values ('00000000-0000-0000-0000-000000000132',
		        '00000000-0000-0000-0000-000000000132',
		        'idx-test', 256)
	`); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	// status 'live' — 'ready' is not in deployments_status_check
	// (pending/building/imaging/snapshotting/live/failed/superseded/
	// cancelled). This app has no other live deployment, so the
	// deployments_app_scope_live_uniq partial unique index is happy.
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, source_path, source_bytes, status, image_digest)
		values ('00000000-0000-0000-0000-000000000132',
		        '00000000-0000-0000-0000-000000000132',
		        'tarball', '/tmp/test.tar', 0, 'live', 'sha256:0')
	`); err != nil {
		t.Fatalf("seed deployments: %v", err)
	}
	// 19 more deployments, superseded so deployments_app_scope_live_uniq
	// stays satisfied. Selectivity is the point: the planner only prefers
	// a deployment_id-leading index when deployment_id actually narrows
	// the live set. Seeding every live row under ONE deployment made the
	// predicate match 100% of live rows, and the planner reasonably chose
	// the cheaper single-column instances_live_node_id_idx instead —
	// which is the production shape only if the fleet runs one deployment.
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, source_path, source_bytes, status, image_digest)
		select ('00000000-0000-0000-0000-0001320000'::text || lpad(g::text, 2, '0'))::uuid,
		       '00000000-0000-0000-0000-000000000132',
		       'tarball', '/tmp/test.tar', 0, 'superseded', 'sha256:0'
		  from generate_series(1, 19) g
	`); err != nil {
		t.Fatalf("seed sibling deployments: %v", err)
	}
	// 50 live rows per deployment across all 20 => 1000 live rows, of
	// which the query's deployment_id matches 5%. node_id is a uuid FK to
	// compute_nodes; wake_id defaults to gen_random_uuid() so each row is
	// distinct. ANALYZE afterwards so the planner works off real stats.
	if _, err := pool.Exec(ctx, `
		insert into instances (app_id, deployment_id, state, ram_mb, node_id, started_at)
		select '00000000-0000-0000-0000-000000000132', d.id, 'running', 256, $1, now()
		  from deployments d, generate_series(1, 50)
		 where d.app_id = '00000000-0000-0000-0000-000000000132'
	`, nodeID); err != nil {
		t.Fatalf("seed instances rows: %v", err)
	}
	if _, err := pool.Exec(ctx, `analyze instances`); err != nil {
		t.Fatalf("analyze instances: %v", err)
	}

	// state.PgStore.CountLiveInstancesByDeployment — deployment_id only.
	// This is the query 00132's index could never serve, on two counts:
	// the dead uppercase predicate and the app_id-leading key.
	planStr, err := explainNoSeqScan(ctx, t, pool, `
		select count(*) from instances
		where deployment_id = $1
		  and state in ('waking', 'cold_booting', 'running')
	`, "00000000-0000-0000-0000-000000000132")
	if err != nil {
		t.Fatalf("explain CountLiveInstancesByDeployment shape: %v", err)
	}
	if !strings.Contains(planStr, "instances_app_deployment_idx") {
		t.Errorf("planner could not use instances_app_deployment_idx for CountLiveInstancesByDeployment:\n%s", planStr)
	}
	if strings.Contains(planStr, "Seq Scan on instances") {
		t.Errorf("EXPLAIN chose Seq Scan on instances for CountLiveInstancesByDeployment:\n%s", planStr)
	}

	// state.PgStore.ConcurrencyForDeployment — app_id AND deployment_id.
	concStr, err := explainNoSeqScan(ctx, t, pool, `
		select count(*) from instances
		where app_id = $1 and deployment_id = $2
		  and state in ('waking', 'cold_booting', 'running')
	`, "00000000-0000-0000-0000-000000000132",
		"00000000-0000-0000-0000-000000000132")
	if err != nil {
		t.Fatalf("explain ConcurrencyForDeployment shape: %v", err)
	}
	if !strings.Contains(concStr, "instances_app_deployment_idx") {
		t.Errorf("planner could not use instances_app_deployment_idx for ConcurrencyForDeployment:\n%s", concStr)
	}
	if strings.Contains(concStr, "Seq Scan on instances") {
		t.Errorf("EXPLAIN chose Seq Scan on instances for ConcurrencyForDeployment:\n%s", concStr)
	}

	// (5) Replay-safety: a second MigrateUp is a no-op.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay-safety: second MigrateUp failed: %v", err)
	}
}
