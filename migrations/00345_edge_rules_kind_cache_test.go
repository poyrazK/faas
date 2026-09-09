//go:build !no_pg

// Migration-apply test for 00345_edge_rules_kind_cache.sql
// (ADR-122). Two concerns are pinned here because the migration
// carries two: the kind=cache widening, and the restoration of
// 'budget' that 00265 dropped.
//
// Slot: 00345 is the first free slot above main's current claim
// (main ships reserve_slot fences at 00314-00317 + 00320 +
// 00328-00340 and real migrations at 00318 (deployments_actor),
// 00319 (actor_validate_fk), 00341 (repair_app_secrets_scope),
// plus PR #984 holds 00342 (deployments_annotation) + 00343
// reservation and PR #1005 holds 00342 reservation + 00344 real
// (deployment_openapi_snapshots)). Earlier fences at 00314-00320
// + 00330 accompanying this PR have been dropped: main now ships
// its own identical reserve_slot fences at those positions and
// the slot reserved by #1000 (#1000 holds 00329 as a reservation
// per [[pr-1000-cherry-pick-rebuild-shipped-2026-08-20]]).
//
// Vacated-slot fences at 00342 / 00343 / 00344 ship in this
// branch to satisfy the local TestMigrationsContiguous check;
// they are no-op `SELECT 1;` files stripped by the cross-PR
// precheck's `slots_from_paths` carve-out. See the
// local-embed-vs-synthetic-merge-contiguity memory entry.
// Future renumbering must re-verify `git ls-tree origin/main
// migrations/` AND enumerate open-PR claims including
// refs/pull/<N>/head — `git ls-tree origin/main` alone misses
// open-PR fences (cross-pr-slot-precheck-pr-867-collision).
//
// Pins:
//
//  1. Migration set applies cleanly through 00345 (no goose
//     duplicate-version panic across the 00314-00342 cross-PR
//     + main claim range — PR #984 holds 00342 real + 00343
//     reservation, PR #1005 holds 00342 reservation + 00344
//     real; 00345 is the next free slot above every open claim).
//  2. edge_rules_kind_check exists under that exact name with all
//     14 closed-vocabulary values present as quoted literals.
//  3. REGRESSION PIN: 'budget' is present. 00254 added it; 00265
//     rewrote the same constraint omitting it (00265:69-75
//     assumed budget occupied slot 00231, but it shipped at
//     00254 — i.e. BEFORE 00265 — so the DROP+ADD silently
//     narrowed the vocabulary). Evidence on main: schema.sql:1353
//     lists 12 kinds with 'budget' absent, while
//     cmd/apid/handlers_edge_rules.go:170,506 accepts it. Any
//     kind=budget create 23514'd at the database. This test fails
//     if a future widening re-narrows it.
//  4. Positive round-trip: a kind='cache' row inserts and reads
//     back with its action jsonb preserved verbatim.
//  5. Positive round-trip for kind='budget' — the fix is
//     load-bearing, not cosmetic.
//  6. Every one of the 12 pre-existing kinds still accepts, so
//     this widening did not narrow anything.
//  7. A typo kind='cache_typo' is rejected with 23514.
//  8. Replay safety: re-running db.MigrateUp is a no-op.

package migrations_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// cacheMigrationVocab is the closed vocabulary edge_rules_kind_check
// must carry after 00345: every shipped kind (13, including the
// restored 'budget') plus 'cache'. A future widening MUST carry all
// 14 forward — see the 00265 regression documented above for what
// happens when a rewrite drops a value.
var cacheMigrationVocab = []string{
	"route", "rewrite", "redirect", "headers",
	"cors", "jwt", "ip", "validate", "limit", "geo",
	"maintenance", "throttle", "budget", "cache",
}

func TestMigrations_00345_EdgeRulesKindCache(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00345 should land last.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (failure mode: a slot collision between main's 00313 and this migration's 00314-00320 fences — re-run the open-PR slot precheck including refs/pull/<N>/head)", err)
	}

	// (2)+(3) CHECK shape + constraint-name pin. Scoped to
	// current_schema() per migrations-info-schema-scoping-pattern
	// so a parallel pgtest run on the same box doesn't bleed rows
	// in. pg_get_constraintdef emits either `(... IN (...))` or
	// `CHECK (kind IN (...))` depending on Postgres version (15
	// emits the wrapper, 16+ strips it), so a substring pin is the
	// load-bearing pattern per pg-get-constraintdef-shapes.
	var def string
	if err := pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'edge_rules_kind_check'
		   and n.nspname = current_schema()`).Scan(&def); err != nil {
		t.Fatalf("query edge_rules_kind_check constraint: %v (closed-vocabulary CHECK must have landed)", err)
	}
	for _, v := range cacheMigrationVocab {
		needle := "'" + v + "'"
		if !strings.Contains(def, needle) {
			t.Errorf("edge_rules_kind_check: missing %s in def %q (all 14 values must be present; a regression here means the CHECK was narrowed — exactly the 00265 failure mode this migration repairs)", needle, def)
		}
	}

	// (3, explicit) The budget regression pin, called out
	// separately from the loop so a failure names the bug rather
	// than reading as one missing string among fourteen.
	if !strings.Contains(def, "'budget'") {
		t.Errorf("edge_rules_kind_check: 'budget' missing from def %q — 00265's narrowing has been reintroduced. kind=budget is wired end-to-end above the database (apid validate+marshal, CLI vocab, openapi schema); dropping it here makes every kind=budget create fail with SQLSTATE 23514", def)
	}

	accountID := "00000000-0000-0000-0000-000000003450"
	appID := "00000000-0000-0000-0000-000000013210"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ($1, 'scale', 'cache-kind-test@example.com')
		on conflict (id) do nothing
	`, accountID); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ($1, $2, 'cache-kind-test', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`, appID, accountID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}

	// (4) Positive round-trip for kind='cache'. The action shape
	// mirrors api.EdgeRuleCacheAction: max_age_seconds,
	// stale_if_error_seconds, vary_on, methods. vary_on carries
	// only non-credential headers by construction (ADR-122 D3 —
	// credentialed requests are a hard bypass, never a key
	// dimension), so a jsonb round-trip that preserved
	// 'Authorization' here would be a design violation, not just a
	// serialization bug.
	ruleID := "00000000-0000-0000-0000-000000023210"
	action := map[string]any{"cache": map[string]any{
		"max_age_seconds":        60,
		"stale_if_error_seconds": 300,
		"vary_on":                []any{"Accept-Language"},
		"methods":                []any{"GET", "HEAD"},
	}}
	actionJSON, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path,
		                        priority, enabled, kind, action)
		values ($1, $2, $3, 'shop.example.com', '/catalog/*', 100, true, 'cache', $4)
	`, ruleID, accountID, appID, actionJSON); err != nil {
		t.Fatalf("insert kind=cache row: %v (the widened CHECK must accept 'cache')", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from edge_rules where id = $1`, ruleID)
	})

	var gotKind string
	var gotAction []byte
	if err := pool.QueryRow(ctx, `
		select kind, action::text
		  from edge_rules
		 where id = $1
	`, ruleID).Scan(&gotKind, &gotAction); err != nil {
		t.Fatalf("read kind=cache row: %v", err)
	}
	if gotKind != "cache" {
		t.Errorf("kind round-trip: got %q, want 'cache'", gotKind)
	}
	if !strings.Contains(compactJSON(t, gotAction), `"max_age_seconds":60`) {
		t.Errorf("action jsonb round-trip: got %s, want action.cache.max_age_seconds=60", string(gotAction))
	}
	if !strings.Contains(compactJSON(t, gotAction), `"stale_if_error_seconds":300`) {
		t.Errorf("action jsonb round-trip: got %s, want action.cache.stale_if_error_seconds=300", string(gotAction))
	}
	if !strings.Contains(compactJSON(t, gotAction), `"Accept-Language"`) {
		t.Errorf("action jsonb round-trip: got %s, want action.cache.vary_on to preserve Accept-Language", string(gotAction))
	}

	// (5) Positive round-trip for kind='budget'. This is the
	// behavioural half of the regression fix: pin (3) proves the
	// literal is in the constraint def, this proves a row actually
	// inserts. Before 00345 this INSERT failed with 23514 on main.
	budgetRuleID := "00000000-0000-0000-0000-000000033210"
	budgetAction, err := json.Marshal(map[string]any{"budget": map[string]any{
		"budget_ms":             3000,
		"allow_override_header": "x-faas-budget-ms",
	}})
	if err != nil {
		t.Fatalf("marshal budget action: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path,
		                        priority, enabled, kind, action)
		values ($1, $2, $3, 'shop.example.com', '/v1/payment', 110, true, 'budget', $4)
	`, budgetRuleID, accountID, appID, budgetAction); err != nil {
		t.Fatalf("insert kind=budget row: %v (this is the 00265 regression — 'budget' was dropped from the CHECK by a later widening and every kind=budget create 23514'd)", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from edge_rules where id = $1`, budgetRuleID)
	})

	// (6) Every pre-existing kind still accepts. The load-bearing
	// regression pin for the CHECK-rewrite race: a future widening
	// that narrows the list to just its own value would 23514
	// every other kind's create on this code path. Each iteration
	// varies match_path so rows don't collide on the
	// (account_id, app_id, match_host, match_path) uniqueness.
	preExisting := []string{
		"route", "rewrite", "redirect", "headers", "cors", "jwt",
		"ip", "validate", "limit", "geo", "maintenance", "throttle",
	}
	for i, k := range preExisting {
		t.Run("vocab_still_accepts_"+k, func(t *testing.T) {
			probeID := probeUUID(i)
			if _, err := pool.Exec(ctx, `
				insert into edge_rules (id, account_id, app_id, match_host, match_path,
				                        priority, enabled, kind, action)
				values ($1, $2, $3, 'probe.example.com', $4, 200, true, $5, '{}'::jsonb)
			`, probeID, accountID, appID, "/probe/"+k, k); err != nil {
				t.Errorf("insert kind=%s: %v (00345 must not narrow the pre-existing vocabulary)", k, err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(ctx, `delete from edge_rules where id = $1`, probeID)
			})
		})
	}

	// (7) A typo is rejected. Proves the CHECK is a closed
	// vocabulary and not an open text column.
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path,
		                        priority, enabled, kind, action)
		values ('00000000-0000-0000-0000-000000093210', $1, $2, 'typo.example.com', '/x', 300, true, 'cache_typo', '{}'::jsonb)
	`, accountID, appID); err == nil {
		t.Error("insert kind='cache_typo' succeeded; want 23514 (the closed vocabulary must reject near-miss typos, otherwise a misspelled kind silently becomes an inert rule)")
	} else if !strings.Contains(err.Error(), "23514") && !strings.Contains(err.Error(), "edge_rules_kind_check") {
		t.Errorf("insert kind='cache_typo': got %v, want a 23514 check_violation on edge_rules_kind_check", err)
	}

	// (8) Replay safety: re-running the migration set is a no-op.
	// The DROP CONSTRAINT IF EXISTS + ADD pair must be idempotent
	// on a local re-run or a hot-fix path that bypasses goose's
	// version table.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp (replay): %v (00345 must be replay-safe — DROP ... IF EXISTS then ADD)", err)
	}
}

// probeUUID returns a distinct, deterministic UUID per vocabulary
// probe so parallel subtests never collide on the primary key.
func probeUUID(i int) string {
	const hex = "0123456789abcdef"
	return "00000000-0000-0000-0000-0000000432" + string([]byte{hex[(i/16)%16], hex[i%16]})
}
