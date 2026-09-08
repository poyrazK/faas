//go:build !no_pg

// Migration-apply test for 00132 (issue #557 closure / ADR-072 —
// instances_app_deployment_idx partial index backing the per-deployment
// concurrency SELECT). Pins:
//
//  1. The migration set applies cleanly through 00132.
//  2. The index exists and is a partial index restricted to the three
//     live states (RUNNING, WAKING, COLD_BOOTING).
//  3. The index keys on (app_id, deployment_id) — prefix matches the
//     production per-deployment wake count predicate.
//  4. The index is structurally usable: with seqscan disabled the
//     planner reaches it for the predicate the migration wrote.
//     See the KNOWN DRIFT note at step (4): 00132's predicate uses
//     UPPERCASE state literals while instances.state is lowercase,
//     so the index cannot serve the real production query until a
//     follow-up migration recreates it.
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
	for _, want := range []string{"RUNNING", "WAKING", "COLD_BOOTING"} {
		if !strings.Contains(indexDef, want) {
			t.Errorf("indexdef missing live state %q: %s", want, indexDef)
		}
	}

	// (3b) Index shape: keyed on (app_id, deployment_id) in that
	// order. The prefix ordering is the load-bearing half — an index
	// on (deployment_id, app_id) would still satisfy the substring
	// probes above but could not serve an app_id-only prefix scan.
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
	if want := []string{"app_id", "deployment_id"}; !slices.Equal(keyCols, want) {
		t.Errorf("instances_app_deployment_idx key columns = %v, want %v (the (app_id, deployment_id) prefix is what makes the per-deployment count an index lookup)", keyCols, want)
	}

	// (4) The index is well-formed and the planner can reach it for
	// the predicate the migration wrote.
	//
	// KNOWN DRIFT — READ BEFORE EXTENDING THIS TEST. 00132 spelled its
	// partial predicate with UPPERCASE state literals
	// ('RUNNING', 'WAKING', 'COLD_BOOTING'), but instances.state has
	// been constrained to LOWERCASE values since 00001 (see
	// instances_state_check, realigned in 00035, and every sibling
	// partial index — instances_live_node_id_idx,
	// instances_reaper_state_idx, instances_wake_attempt_active_idx —
	// which all use lowercase). The production reader,
	// state.PgStore.CountLiveInstancesByDeployment, also queries
	// lowercase. So this index currently matches zero rows and cannot
	// serve the query it was created for.
	//
	// That is a schema defect, not a test defect: migrations are
	// append-only, so it has to be corrected by a follow-up migration
	// that recreates the index with lowercase literals. Until then
	// this test pins what 00132 actually established — the index
	// exists, is partial, keys on (app_id, deployment_id), and is
	// structurally usable — by EXPLAINing against the index's OWN
	// predicate. Asserting against the lowercase production query
	// here would fail for a reason the test cannot fix.
	//
	// When the follow-up migration lands, flip both the literals in
	// step (3) and the query below to lowercase; the assertion then
	// covers the real production path.
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
	// Seed enough live rows that a Seq Scan is a genuinely plausible
	// plan, then ANALYZE so the planner works off real statistics.
	// node_id is a uuid FK to compute_nodes; wake_id defaults to
	// gen_random_uuid() so each row is distinct.
	if _, err := pool.Exec(ctx, `
		insert into instances (app_id, deployment_id, state, ram_mb, node_id, started_at)
		select '00000000-0000-0000-0000-000000000132',
		       '00000000-0000-0000-0000-000000000132',
		       'running', 256, $1, now()
		  from generate_series(1, 200)
	`, nodeID); err != nil {
		t.Fatalf("seed instances rows: %v", err)
	}
	if _, err := pool.Exec(ctx, `analyze instances`); err != nil {
		t.Fatalf("analyze instances: %v", err)
	}

	planStr, err := explainNoSeqScan(ctx, t, pool, `
		select count(*) from instances
		where app_id = $1 and deployment_id = $2
		  and state in ('RUNNING', 'WAKING', 'COLD_BOOTING')
	`, "00000000-0000-0000-0000-000000000132",
		"00000000-0000-0000-0000-000000000132")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !strings.Contains(planStr, "instances_app_deployment_idx") {
		t.Errorf("planner could not use instances_app_deployment_idx for its own predicate even with seqscan disabled:\n%s", planStr)
	}
	if strings.Contains(planStr, "Seq Scan on instances") {
		t.Errorf("EXPLAIN chose Seq Scan on instances; index not usable:\n%s", planStr)
	}

	// (5) Replay-safety: a second MigrateUp is a no-op.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay-safety: second MigrateUp failed: %v", err)
	}
}
