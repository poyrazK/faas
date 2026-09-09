//go:build !no_pg

// Migration-apply test for 00128 (issue #463 / ADR-069 / PR-B
// review finding #5 — events_sidecar_name_idx). Pins:
//
//  1. The migration set applies cleanly through 00128.
//  2. The new index exists (pg_indexes).
//  3. The index is a partial expression index over the two
//     closed sidecar-event kinds (matches ListEventsBySidecar's
//     predicate).
//  4. The planner uses the index for the ListEventsBySidecar
//     query (EXPLAIN must mention the index, NOT a Seq Scan).
//  5. Replay-safety: a second MigrateUp is a no-op (PR #377 /
//     ADR-041).
//
// Slot note: PR-B originally claimed 00121; main's recent merges
// reserved 00121 as a fence (main's 00121_reserve_slot.sql) and
// added 00122 framework_ready_at + 00123 compute_nodes_vcpu_budget,
// then PR #547 (open) added 00124_reserve_slot + 00125_warm_hint +
// 00126_pg_ratelimit. PR-B's sidecar layers migration renumbered to
// 00127 and this index migration followed it to 00128. Bump filename +
// test function name + ApplyUp range together if the slot
// changes again.

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00128_EventsSidecarNameIdx(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00128. A regression that drops a slot
	// between 1 and 127 surfaces here before the per-assertion
	// pins.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 127)", err)
	}

	// (2) Index existence. Pin the index name so a future
	// renamer surfaces in CI. The schema filter uses
	// current_schema() rather than 'public' literal because
	// pgtest isolates every test in its own search_path
	// schema, so the index lands in the test schema, not
	// public.
	var idxCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from pg_indexes
		where schemaname = current_schema()
		  and tablename = 'events'
		  and indexname = 'events_sidecar_name_idx'
	`).Scan(&idxCount); err != nil {
		t.Fatalf("count pg_indexes: %v", err)
	}
	if idxCount != 1 {
		t.Errorf("events_sidecar_name_idx missing (count = %d, want 1)", idxCount)
	}

	// (3) Partial + expression index. The pg_indexes row
	// exposes the indexdef verbatim; we assert the two
	// load-bearing pieces:
	//   (a) the partial WHERE clause on the closed kinds,
	//   (b) the expression key (data->>'sidecar_name') — no
	//       `::text` cast (Postgres rejects it inside CREATE
	//       INDEX; `->>` already returns text).
	// A regression that drops the partial predicate would
	// bloat the index to cover every events row (mostly
	// non-sidecar events); a regression that drops the
	// expression form would force the planner to fall back
	// to a Seq Scan.
	var indexDef string
	if err := pool.QueryRow(ctx, `
		select indexdef from pg_indexes
		where schemaname = current_schema()
		  and tablename = 'events'
		  and indexname = 'events_sidecar_name_idx'
	`).Scan(&indexDef); err != nil {
		t.Fatalf("read indexdef: %v", err)
	}
	if !strings.Contains(indexDef, "WHERE") {
		t.Errorf("indexdef missing WHERE predicate (not a partial index): %s", indexDef)
	}
	for _, want := range []string{"wake.sidecar_init_exit", "wake.sidecar_restart"} {
		if !strings.Contains(indexDef, want) {
			t.Errorf("indexdef missing closed kind %q: %s", want, indexDef)
		}
	}
	if !strings.Contains(indexDef, "data") || !strings.Contains(indexDef, "sidecar_name") {
		t.Errorf("indexdef missing (data->>'sidecar_name') expression: %s", indexDef)
	}

	// (4) The index is USABLE for ListEventsBySidecar's query, and
	// answers it correctly.
	//
	// Seed a realistic mix: sidecar events the partial predicate
	// covers, plus non-sidecar noise it must exclude, plus the one
	// row the production query is looking for. ANALYZE gives the
	// planner real statistics instead of the bootstrap defaults.
	//
	// The EXPLAIN then runs with enable_seqscan disabled (see
	// explainNoSeqScan in 00072's file). On a test schema of this
	// size a Seq Scan is genuinely the cheapest plan, so asserting
	// on the planner's unaided choice would be pinning cost
	// estimates rather than schema: the question that matters is
	// whether the planner CAN reach this index for this predicate.
	// It can only do so if the index still exists under this name,
	// still keys on the (data->>'sidecar_name') expression, and
	// still has a predicate implied by the query's `kind IN (...)`.
	// Break any of those and the plan falls back to Seq Scan even
	// with the penalty applied.
	if _, err := pool.Exec(ctx, `
		insert into events (actor, kind, data)
		select 'vmmd',
		       case when g % 2 = 0 then 'wake.sidecar_init_exit' else 'wake.sidecar_restart' end,
		       jsonb_build_object('sidecar_name', 'bulk-' || g, 'status', 'init_ok', 'exit_code', 0)
		  from generate_series(1, 500) g
	`); err != nil {
		t.Fatalf("seed sidecar events: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into events (actor, kind, data)
		select 'apid', 'deploy.created',
		       jsonb_build_object('sidecar_name', 'noise-' || g)
		  from generate_series(1, 500) g
	`); err != nil {
		t.Fatalf("seed non-sidecar noise events: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into events (actor, kind, data)
		values ('vmmd', 'wake.sidecar_init_exit',
		        '{"sidecar_name":"metrics-test","status":"init_ok","exit_code":0}'::jsonb)
	`); err != nil {
		t.Fatalf("seed events row: %v", err)
	}
	if _, err := pool.Exec(ctx, `analyze events`); err != nil {
		t.Fatalf("analyze events: %v", err)
	}

	const listEventsBySidecar = `
		select id from events
		where kind in ('wake.sidecar_init_exit', 'wake.sidecar_restart')
		  and data->>'sidecar_name' = $1
	`
	planStr, err := explainNoSeqScan(ctx, t, pool, listEventsBySidecar, "metrics-test")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !strings.Contains(planStr, "events_sidecar_name_idx") {
		t.Errorf("planner could not use events_sidecar_name_idx for ListEventsBySidecar even with seqscan disabled:\n%s", planStr)
	}
	if strings.Contains(planStr, "Seq Scan on events") {
		t.Errorf("EXPLAIN chose Seq Scan on events; index not usable:\n%s", planStr)
	}

	// The partial predicate must not change the answer: exactly one
	// row carries sidecar_name = 'metrics-test' under a covered kind,
	// and the 500 non-sidecar noise rows must stay out of it.
	var hits int
	if err := pool.QueryRow(ctx,
		`select count(*) from events
		  where kind in ('wake.sidecar_init_exit', 'wake.sidecar_restart')
		    and data->>'sidecar_name' = $1`, "metrics-test",
	).Scan(&hits); err != nil {
		t.Fatalf("count ListEventsBySidecar rows: %v", err)
	}
	if hits != 1 {
		t.Errorf("ListEventsBySidecar('metrics-test') matched %d rows, want 1", hits)
	}

	// (5) Replay-safety: a second MigrateUp is a no-op.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay-safety: second MigrateUp failed: %v", err)
	}
}
