//go:build !no_pg

// Migration-apply test for 00287_pg_ratelimit_add_rule_scope.sql
// (ADR-104 amendment 5, issue #881 Phase 4 follow-up).
//
// Pins:
//
//  1. Migration set applies cleanly through 00287 (no goose
//     duplicate-version panic). Slot 00287 was picked as the next
//     free slot on origin/main past the open-PR reservations:
//       - PR #910 (00281-00283 trigger cluster)
//       - PR #916 (00265-00272 jobs PR-B)
//       - PR #939 (00266-00272 + 00276 gap-12)
//       - PR #964 MERGED (00281-00285 + 00286 data-upstreams
//         widening landed on main after the previous 00285 → 00286
//         → 00287 renumbering chain on this branch).
//       - this PR (00287 widening; the 00285 reservation fence on
//         the merged-into-main base now lives on main, claimed by
//         this PR per the merge resolution).
//     Re-verify against open PRs immediately before push via
//     scripts/ci/check_migration_slots.sh.
//  2. The pg_ratelimit_counters CHECK accepts the new value
//     `scope='rule'` (positive round-trip — the rule-scoped
//     bucket from issue #881 Phase 3 can land a counter row).
//  3. All pre-existing scopes still accept (regression guard — a
//     scratchy DROP+ADD rewrite that drops a value would break
//     every production row in `app` + `account` scopes, which
//     are still on the un-widened column per cluster today).
//  4. A typo (`scope='route'`) is rejected with pgx 23514
//     (check_violation). Pins the closed vocabulary contract;
//     `route` is the Phase 3 vocabulary on the kind=throttle
//     action, NOT a scope value — easy confusion to defend
//     against.
//  5. The CHECK is named `pg_ratelimit_counters_scope_check` —
//     the auto-name Postgres picks for the inline column CHECK
//     in 00126. If a future 00126 patch renames the inline
//     CHECK, this pin + the DROP+ADD in 00287 must update
//     together (silent breakage here means 00287 becomes a
//     no-op — exactly the bug this test exists to catch).
//  6. Replay safety: re-running db.MigrateUp is a no-op (the
//     migration is replay-safe via `IF EXISTS` + the plain
//     ADD CONSTRAINT shape — see 00229 for the canonical
//     precedent).

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// ratelimitScopeVocab is the closed vocabulary
// pg_ratelimit_counters_scope_check must carry after this
// migration. Adding a new value here without also widening the
// migration's IN list is a load-bearing failure mode.
var ratelimitScopeVocab = []string{"app", "account", "rule"}

func TestMigrations_00287_PgRateLimitAddRuleScope(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00287.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 00284 tenant_surfaces_per_host_kind and 00287 pg_ratelimit widen)", err)
	}

	// (2) + (5) CHECK constraint shape + constraint name pin.
	var def string
	err := pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'pg_ratelimit_counters_scope_check'
		   and n.nspname = current_schema()`).Scan(&def)
	if err != nil {
		t.Fatalf("query pg_ratelimit_counters_scope_check constraint: %v (closed-vocabulary CHECK must have landed)", err)
	}
	for _, v := range ratelimitScopeVocab {
		needle := "'" + v + "'"
		if !strings.Contains(def, needle) {
			t.Errorf("pg_ratelimit_counters_scope_check: missing %s in def %q (closed vocabulary must include all 3 values; a regression here means the CHECK was narrowed)", needle, def)
		}
	}
	if !strings.Contains(def, "'rule'") {
		t.Errorf("pg_ratelimit_counters_scope_check: 'rule' missing from def %q (the migration's ADD CONSTRAINT must include 'rule')", def)
	}

	// (3) Positive round-trip: scope='rule' accepts an INSERT with
	// a UUID subject_id (mirrors the 00126 column type). The
	// single-statement INSERT exercises the central-mode consume
	// path documented in ADR-104 amendment 5. The action itself
	// is not written by this test — Phase 3 + Phase 4 do not
	// require per-rule rows today; the test merely confirms the
	// CHECK admits the value.
	var dummyRuleID = "00000000-0000-0000-0000-00000002287a"
	if _, err := pool.Exec(ctx, `
		insert into pg_ratelimit_counters (scope, subject_id, plan, tokens, last_refill)
		values ('rule', $1, 'scale', 100, now())
		on conflict (scope, subject_id, plan) do nothing
	`, dummyRuleID); err != nil {
		t.Errorf("insert scope='rule': %v (CHECK must accept rule scope after 00287)", err)
	}
	// Clean up so the test is idempotent under pgtest.Open reuse.
	if _, err := pool.Exec(ctx, `delete from pg_ratelimit_counters where scope = 'rule'`); err != nil {
		t.Logf("cleanup rule scope rows: %v (non-fatal; pgtest.Open provides a fresh schema per test)", err)
	}

	// (4) Typo scope='route' (the Phase 3 action vocabulary
	// `kind=throttle`'s `route` field, NOT a scope) is rejected.
	// Defends against the easy confusion where a developer adds
	// 'route' to a new migration's IN list thinking they mean
	// `kind=route` on edge_rules.
	var accountID = "00000000-0000-0000-0000-00000002287b"
	var dummyRouteScopeErr error
	if _, err := pool.Exec(ctx, `
		insert into pg_ratelimit_counters (scope, subject_id, plan, tokens, last_refill)
		values ('route', $1, 'scale', 100, now())
	`, accountID); err != nil {
		dummyRouteScopeErr = err
	}
	if dummyRouteScopeErr == nil {
		t.Fatal("insert scope='route': expected 23514 check_violation, got nil (closed vocabulary must reject 'route' — it is the Phase 3 *action* vocabulary, not a scope)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(dummyRouteScopeErr, &pgErr) {
		t.Fatalf("insert scope='route': expected pgconn.PgError, got %T: %v", dummyRouteScopeErr, dummyRouteScopeErr)
	}
	if pgErr.Code != "23514" {
		t.Errorf("insert scope='route': expected SQLSTATE 23514 check_violation, got %s (closed vocabulary contract)", pgErr.Code)
	}

	// (6) Replay safety: re-running db.MigrateUp is a no-op.
	// pgtest.Open drops the schema between tests; on a live
	// schema goose's StrictMode would skip a no-op migration
	// that has no real change. The 00287 migration is replay-
	// safe via `IF EXISTS` on the DROP, so a re-apply on an
	// already-widened schema must not fail.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (migration must be replay-safe — DROP IF EXISTS is the load-bearing carve-out)", err)
	}
}
