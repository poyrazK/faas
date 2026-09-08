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
//  4. The planner picks the index for the production query (EXPLAIN
//     must mention the index, NOT a Seq Scan on instances).
//  5. Replay-safety: a second MigrateUp is a no-op.
//
// Slot note: 00131 is the previous slot in this branch's embedded set;
// renumber would need filename + test name + apply range bump together.
package migrations_test

import (
	"context"
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

	// (4) Planner uses the index for the production per-deployment
	// concurrency SELECT. We seed a small instance row so the
	// planner has statistics and then EXPLAIN the production query.
	// Expected plan mentions instances_app_deployment_idx; a Seq
	// Scan on instances would surface here.
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
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, source_path, source_bytes, status, image_digest)
		values ('00000000-0000-0000-0000-000000000132',
		        '00000000-0000-0000-0000-000000000132',
		        'tarball', '/tmp/test.tar', 0, 'ready', 'sha256:0')
	`); err != nil {
		t.Fatalf("seed deployments: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into instances (app_id, deployment_id, state, ram_mb, node_id, wake_id, started_at)
		values ('00000000-0000-0000-0000-000000000132',
		        '00000000-0000-0000-0000-000000000132',
		        'RUNNING', 256, 'node-1', 'wake-1', now())
	`); err != nil {
		t.Fatalf("seed instances row: %v", err)
	}
	rows, err := pool.Query(ctx, `
		explain (format text)
		select count(*) from instances
		where app_id = $1 and deployment_id = $2
		  and state in ('RUNNING', 'WAKING', 'COLD_BOOTING')
	`, "00000000-0000-0000-0000-000000000132",
		"00000000-0000-0000-0000-000000000132")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			t.Fatalf("explain scan: %v", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	rows.Close()
	planStr := plan.String()
	if !strings.Contains(planStr, "instances_app_deployment_idx") {
		t.Errorf("EXPLAIN did not mention instances_app_deployment_idx; planner falls back to Seq Scan:\n%s", planStr)
	}
	if strings.Contains(planStr, "Seq Scan on instances") {
		t.Errorf("EXPLAIN chose Seq Scan on instances; index not picked up:\n%s", planStr)
	}

	// (5) Replay-safety: a second MigrateUp is a no-op.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay-safety: second MigrateUp failed: %v", err)
	}
}
