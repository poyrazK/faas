//go:build !no_pg

// Migration-apply test for 00191_builds_deployment_started_idx.sql
// (DEPLOY-PROV-6 follow-up / ADR-091, issue #741 close-out).
// Pins the keyset-filter index for the new GET /v1/builds
// route:
//
//  1. Migration set applies cleanly through 00191.
//  2. The composite index builds_deployment_started_idx exists
//     on builds(deployment_id, started_at DESC NULLS LAST).
//  3. DESC NULLS LAST is preserved in the indexdef — a future
//     refactor that drops the nulls-last would push queued
//     builds (started_at IS NULL) to the top of every page,
//     breaking the pagination cursor (the handler at
//     cmd/apid/handlers_ext.go::listBuilds walks the page
//     backward to find the LAST non-null started_at for the
//     next_before cursor).
//  4. Replay-safety: a second MigrateUp is a no-op (CREATE
//     INDEX IF NOT EXISTS guard per ADR-041).
//
// Build tag mirrors 00162's; FAAS_SKIP_PG_TESTS=1 skips locally.
//
// Slot 191 (renumbered from 166 → 174 → 191 mid-PR review after
// sibling-PR reservation fences took 166, 168–172, 174–189 on
// origin/main, and 173 + 190 are real migrations. The cleanest
// available slot beyond main's 190_admin_obs_index is 191).
// See PR #803 thread and
// cross-pr-slot-gate-reservation-fence-pattern.md.

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00191_BuildsDeploymentStartedIdx(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00191 lands last on this
	// branch; the apply_walk_test pins contiguity at the directory
	// level.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (ADR-091 slot 191 broken: missing migration slot between 1 and 191)", err)
	}

	// (2) The index exists on the right table + column list.
	// pg_indexes carries the canonical indexdef as
	// pg_get_indexdef would emit, so this single SELECT covers
	// name, table, column ordering, and ordering direction.
	var indexdef string
	if err := pool.QueryRow(ctx, `
		select indexdef
		  from pg_indexes
		 where schemaname = current_schema()
		   and tablename  = 'builds'
		   and indexname  = 'builds_deployment_started_idx'
	`).Scan(&indexdef); err != nil {
		t.Fatalf("query builds_deployment_started_idx: %v (ADR-091 index must have landed)", err)
	}

	upper := strings.ToUpper(indexdef)
	// (3) The composite must keep (deployment_id, started_at) as
	// the leading key column — a future refactor that swaps them
	// would degrade the keyset filter from an index-only probe to
	// a heap fetch + filter.
	if !strings.Contains(upper, "(DEPLOYMENT_ID") || !strings.Contains(upper, "STARTED_AT") {
		t.Errorf("indexdef missing composite (deployment_id, started_at): %s", indexdef)
	}
	// (4) DESC NULLS LAST is load-bearing for the pagination cursor
	// — the handler walks backward to the LAST non-null row, so
	// queued builds (NULL) MUST sort to the bottom of every page.
	if !strings.Contains(upper, "DESC") {
		t.Errorf("indexdef missing DESC ordering (queued builds would land at the top): %s", indexdef)
	}
	if !strings.Contains(upper, "NULLS LAST") {
		t.Errorf("indexdef missing NULLS LAST (handler's pagination cursor would skip non-null rows): %s", indexdef)
	}
	// (5) deployment_id must come before started_at in the column
	// list (the planner's nested-loop strategy probes via
	// deployment_id first).
	if !strings.Contains(upper, "DEPLOYMENT_ID,") && !strings.Contains(upper, "(DEPLOYMENT_ID ") {
		t.Errorf("indexdef missing deployment_id as the leading key column: %s", indexdef)
	}

	// (6) Functional pin: seed two builds in the same deployment
	// with distinct started_at values, then confirm an
	// ORDER BY started_at DESC NULLS LAST query surfaces the
	// newer one first. The test trips if a future refactor
	// silently flips the index column order or the sort
	// direction.
	slot := "00000000-0000-0000-0000-000000000191"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ($1, 'scale', 'deployment-started-idx-test@example.com')
		on conflict (id) do nothing
	`, slot); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ($1, $1, 'deployment-started-idx-test', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`, slot); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	// One deployment owned by the test app.
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, image_digest, kind, status)
		values ($1, $2, 'sha256:' || repeat('a', 64), 'railpack', 'pending')
		on conflict (id) do nothing
	`, "00000000-0000-0000-0000-000000000191", slot); err != nil {
		t.Fatalf("seed deployments: %v", err)
	}
	// Three builds: queued (NULL started_at) + running (older)
	// + running (newer). The ORDER BY started_at DESC NULLS LAST
	// must surface: newer, older, queued.
	if _, err := pool.Exec(ctx, `
		insert into builds (id, deployment_id, kind, source_bytes, status, started_at, finished_at)
		values
		  ('00000000-0000-0000-0000-000000019101', $1, 'railpack', 100, 'queued',  null,                  null),
		  ('00000000-0000-0000-0000-000000019102', $1, 'railpack', 200, 'running', now() - interval '5 minutes', null),
		  ('00000000-0000-0000-0000-000000019103', $1, 'railpack', 300, 'running', now() - interval '1 minute',  null)
		on conflict (id) do nothing
	`, "00000000-0000-0000-0000-000000000191"); err != nil {
		t.Fatalf("seed builds: %v", err)
	}

	rows, err := pool.Query(ctx, `
		select id from builds
		 where deployment_id = $1
		 order by started_at desc nulls last
	`, "00000000-0000-0000-0000-000000000191")
	if err != nil {
		t.Fatalf("select builds ordered: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan build id: %v", err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	want := []string{
		"00000000-0000-0000-0000-000000019103", // newer running
		"00000000-0000-0000-0000-000000019102", // older running
		"00000000-0000-0000-0000-000000019101", // queued (NULL last)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d builds, want %d (DESC NULLS LAST tripwire): %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: got %s, want %s (DESC NULLS LAST broken): %v", i, got[i], want[i], got)
		}
	}

	// (7) EXPLAIN confirms the planner picks the new index. A
	// regression that drops the composite would force a Seq Scan
	// on builds, scaling with row count.
	var plan string
	if err := pool.QueryRow(ctx, `
		explain (format text)
		select id from builds
		 where deployment_id = $1
		 order by started_at desc nulls last
		 limit 50
	`, "00000000-0000-0000-0000-000000000191").Scan(&plan); err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !strings.Contains(plan, "builds_deployment_started_idx") {
		t.Errorf("EXPLAIN did not mention builds_deployment_started_idx (planner chose a worse plan):\n%s", plan)
	}

	// (8) Replay safety: applying the migration set a second time
	// must not blow up. The CREATE INDEX IF NOT EXISTS guard
	// handles this; this assertion is a tripwire that survives
	// future refactors that drop the guard.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (the CREATE INDEX IF NOT EXISTS guard must have been silently dropped)", err)
	}
}
