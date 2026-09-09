//go:build !no_pg

// Migration-apply test for 00254_edge_rules_kind_budget.sql
// (ADR-093 §Decision / new ADR-0NN-kind-budget). The kind=budget
// slot lands at 00254 — fence-free after main's 00237
// (apps_maintenance_mode, PR-B post-272-preview) and PR #884's
// 00238-00243 (tenant_surfaces PR-0, ADR-099 cluster, issue #879).
// 00254 is unclaimed by any open PR at the time of PR #864
// force-push. Slots 00254+ are open. Future renumbering must
// re-verify `git ls-tree origin/main migrations/` AND enumerate
// open-PR fence claims
// (cross-pr-slot-gate-reservation-fence-pattern) after every
// rebase, per migration-test-uuid-sed-residual and
// pr-845-edge-rules-geo-slot-chase-2026-08-11.
//
// Pins:
//
//  1. Migration set applies cleanly through 00254 (no goose
//     duplicate-version panic).
//  2. edge_rules_kind_check CHECK exists, with the closed
//     vocabulary of 12 values: route, rewrite, redirect, headers,
//     cors, jwt, ip, validate, limit, geo, maintenance, budget.
//     pg_get_constraintdef emits the IN-list form per
//     pg-get-constraintdef-shapes.md; assert every value appears
//     as a substring. The regression pin for the CHECK-rewrite
//     race: a future migration that widens or narrows this CHECK
//     must not silently drop any of these 12 values — this
//     assertion catches it here, before production.
//  3. The constraint name is exactly `edge_rules_kind_check`
//     (Postgres-assigned default for an inline CHECK on `kind`).
//  4. Positive round-trip: insert a row with kind='budget' +
//     action={budget:{budget_ms:3000,allow_override_header:"x-faas-budget-ms"}}
//     → read it back → assert kind='budget' and the action
//     jsonb preserves the budget_ms + allow_override_header
//     fields verbatim. Pins that the jsonb action column accepts
//     the new shape (pgstore's edgeRuleSelectCols is
//     kind-agnostic and stores action verbatim).
//  5. All 11 pre-existing kinds still accept. Walk the
//     pre-00254 vocabulary and assert each inserts successfully.
//     This is the load-bearing regression pin for the
//     CHECK-rewrite race — a future regression that narrows the
//     CHECK to just 'budget' (e.g. by silently overwriting the
//     ADD CONSTRAINT with a one-value list) would 23514 every
//     kind=validate / kind=limit / kind=geo / kind=maintenance
//     create on this code path.
//  6. A typo kind='budget_typo' is rejected with 23514 (check
//     violation). Pins the closed vocabulary contract end-to-end.
//  7. Replay safety: re-running db.MigrateUp is a no-op (the
//     DROP CONSTRAINT IF EXISTS guards make the second pass
//     silent; the apply_walk_test harness pins this at the
//     directory level but per-migration shape is also asserted
//     here as defence in depth).
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// budgetMigrationVocab is the closed vocabulary edge_rules_kind_check
// must carry after this migration. The slice doubles as the pin
// set the test walks — adding a new value here without also
// widening the migration's IN list is a load-bearing failure mode.
// Includes 'geo' from migration 00229 (PR #845, kind=geo) and
// 'maintenance' from migration 00236 (PR-B post-272-preview,
// kind=maintenance) because PR #864's migration 00254 must rewrite
// the CHECK with the union of all post-00219 vocab — losing either
// would re-trigger the CHECK-rewrite race (PR #864 CI run
// 31705973056, PG shard 2 fail).
var budgetMigrationVocab = []string{
	"route", "rewrite", "redirect", "headers",
	"cors", "jwt", "ip", "validate", "limit",
	"geo", "maintenance", "budget",
}

func TestMigrations_00254_EdgeRulesKindBudget(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00254 should land last.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR follow-up failure mode: missing migration slot between 00237 maintenance_mode and 00254 budget — also ensure no other open PR has claimed 00254+)", err)
	}

	// (2) CHECK constraint shape + (3) constraint name pin. One
	// round-trip query: pg_constraint joined with pg_namespace,
	// scoped to current_schema() per
	// migrations-info-schema-scoping-pattern so a parallel
	// pgtest run on the same box doesn't bleed rows in. Assert
	// the name verbatim and every closed-vocabulary value
	// present in pg_get_constraintdef's IN-list form.
	var def string
	err := pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'edge_rules_kind_check'
		   and n.nspname = current_schema()`).Scan(&def)
	if err != nil {
		t.Fatalf("query edge_rules_kind_check constraint: %v (closed-vocabulary CHECK must have landed)", err)
	}
	// pg_get_constraintdef emits either `(... IN (...))` or
	// `CHECK (kind IN (...))` depending on Postgres version
	// (15 emits the `CHECK (...)` wrapper, 16+ strips it); per
	// pg-get-constraintdef-shapes.md, substring pin is the
	// load-bearing pattern. Assert each vocab value appears as
	// a quoted literal, not as a substring of another value
	// (e.g. 'ip' must not match 'pipe' or 'tip').
	for _, v := range budgetMigrationVocab {
		needle := "'" + v + "'"
		if !strings.Contains(def, needle) {
			t.Errorf("edge_rules_kind_check: missing %s in def %q (closed vocabulary must include all 12 values; a regression here means the CHECK was narrowed)", needle, def)
		}
	}
	// Belt-and-braces: assert the narrower pre-00254 vocabulary
	// (without 'budget') is NOT the active def. Catches a
	// regression where the migration was authored but the
	// ALTER TABLE ADD CONSTRAINT silently failed (e.g. a
	// transaction rollback that left the new constraint named
	// differently).
	if !strings.Contains(def, "'budget'") {
		t.Errorf("edge_rules_kind_check: 'budget' missing from def %q (the migration's ADD CONSTRAINT must include 'budget')", def)
	}

	// (4) Positive round-trip: kind='budget' row inserts and
	// reads back. Seeds an account + app + edge_rule with the
	// kind=budget action jsonb shape (budget_ms +
	// allow_override_header). pgstore.MigrateUp has already
	// applied 00254 — the row goes through the active CHECK.
	accountID := "00000000-0000-0000-0000-000000002254"
	appID := "00000000-0000-0000-0000-000000012254"
	ruleID := "00000000-0000-0000-0000-000000022254"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ($1, 'scale', 'budget-kind-test@example.com')
		on conflict (id) do nothing
	`, accountID); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ($1, $2, 'budget-kind-test', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`, appID, accountID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	action := map[string]any{"budget": map[string]any{
		"budget_ms":             3000,
		"allow_override_header": "x-faas-budget-ms",
	}}
	actionJSON, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path,
		                        priority, enabled, kind, action)
		values ($1, $2, $3, 'api.example.com', '/v1/payment', 100, true, 'budget', $4)
	`, ruleID, accountID, appID, actionJSON); err != nil {
		t.Fatalf("insert kind=budget row: %v", err)
	}
	var gotKind string
	var gotAction []byte
	if err := pool.QueryRow(ctx, `
		select kind, action::text
		  from edge_rules
		 where id = $1
	`, ruleID).Scan(&gotKind, &gotAction); err != nil {
		t.Fatalf("read kind=budget row: %v", err)
	}
	if gotKind != "budget" {
		t.Errorf("kind round-trip: got %q, want 'budget' (the closed-vocabulary CHECK accepted it on insert + read; pgstore's kind-agnostic jsonb path round-tripped)", gotKind)
	}
	if !strings.Contains(compactJSON(t, gotAction), `"budget_ms":3000`) {
		t.Errorf("action jsonb round-trip: got %s, want action.budget.budget_ms=3000 (jsonb must preserve the budget action shape verbatim)", string(gotAction))
	}
	if !strings.Contains(compactJSON(t, gotAction), `"allow_override_header":"x-faas-budget-ms"`) {
		t.Errorf("action jsonb round-trip: got %s, want action.budget.allow_override_header=\"x-faas-budget-ms\"", string(gotAction))
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from edge_rules where id = $1`, ruleID)
	})

	// (5) All 11 pre-existing kinds still accept. Walk the
	// pre-00254 vocabulary (route..maintenance) and assert each
	// inserts successfully. This is the load-bearing regression
	// pin for the CHECK-rewrite race between this migration and
	// every prior widening PR — a future regression that
	// narrows the CHECK to just 'budget' (e.g. by silently
	// overwriting the ADD CONSTRAINT with a one-value list)
	// would 23514 every kind=validate / kind=limit / kind=geo /
	// kind=maintenance create on this code path. Each iteration
	// seeds its own row so the inserts don't collide on the
	// (account_id, app_id, match_host, match_path) uniqueness.
	for i, k := range []string{"route", "rewrite", "redirect", "headers", "cors", "jwt", "ip", "validate", "limit", "geo", "maintenance"} {
		i, k := i, k
		t.Run("vocab_still_accepts_"+k, func(t *testing.T) {
			// Reuse the same account + app; vary match_path so
			// each kind row is distinct.
			probeID := "00000000-0000-0000-0000-0000000003" + hex2(i)
			probeAction := map[string]any{}
			switch k {
			case "route":
				probeAction["route"] = map[string]any{"target_app_slug": "x"}
			case "rewrite":
				probeAction["rewrite"] = map[string]any{"from": "/a", "to": "/b"}
			case "redirect":
				probeAction["redirect"] = map[string]any{"status_code": 301, "to": "/x"}
			case "headers":
				probeAction["headers"] = map[string]any{"request_headers": []any{}}
			case "cors":
				probeAction["cors"] = map[string]any{"allow_origins": []any{"*"}}
			case "jwt":
				probeAction["jwt"] = map[string]any{"issuer": "https://x", "audience": "y"}
			case "ip":
				probeAction["ip"] = map[string]any{"allow": []string{"10.0.0.0/8"}}
			case "validate":
				probeAction["validate"] = map[string]any{"schema": map[string]any{"type": "object"}}
			case "limit":
				probeAction["limit"] = map[string]any{"max_body_bytes": 1024}
			case "geo":
				probeAction["geo"] = map[string]any{"allow": []string{"DE"}}
			case "maintenance":
				probeAction["maintenance"] = map[string]any{"retry_after_seconds": 60}
			}
			aJSON, mErr := json.Marshal(probeAction)
			if mErr != nil {
				t.Fatalf("marshal action for %s: %v", k, mErr)
			}
			if _, err := pool.Exec(ctx, `
				insert into edge_rules (id, account_id, app_id, match_host, match_path,
				                        priority, enabled, kind, action)
				values ($1, $2, $3, 'api.example.com', $4, 100, true, $5, $6)
			`, probeID, accountID, appID, "/probe/"+k, k, aJSON); err != nil {
				t.Fatalf("insert kind=%s: %v (CHECK regression — pre-existing kind rejected after 00254 widening)", k, err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(ctx, `delete from edge_rules where id = $1`, probeID)
			})
		})
	}

	// (6) Typo kind is rejected with 23514.
	t.Run("typo_kind_rejected", func(t *testing.T) {
		typoID := "00000000-0000-0000-0000-000000022299"
		if _, err := pool.Exec(ctx, `delete from edge_rules where id = $1`, typoID); err != nil {
			t.Fatalf("DELETE pre: %v", err)
		}
		_, err := pool.Exec(ctx, `
			insert into edge_rules (id, account_id, app_id, match_host, match_path,
			                        priority, enabled, kind, action)
			values ($1, $2, $3, 'api.example.com', '/v1/payment', 100, true, 'budget_typo', '{}'::jsonb)
		`, typoID, accountID, appID)
		if err == nil {
			t.Fatal("INSERT kind='budget_typo': no error, want 23514 check_violation")
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			t.Fatalf("INSERT kind='budget_typo' error: %v, want pgconn.PgError", err)
		}
		if pgErr.Code != "23514" {
			t.Errorf("INSERT kind='budget_typo' pgErr.Code = %q, want 23514 (full: %v)", pgErr.Code, err)
		}
	})

	// (7) Replay safety: re-running db.MigrateUp is a no-op. The
	// apply_walk_test harness pins this at the directory level but
	// per-migration shape is also asserted here as defence in depth.
	t.Run("replay_safety", func(t *testing.T) {
		if err := db.MigrateUp(ctx, pool); err != nil {
			t.Fatalf("db.MigrateUp (replay): %v", err)
		}
	})
}

// hex2 renders i as exactly two hex digits so the probe row IDs stay valid
// uuids under the 00000000-0000-0000-0000-00000003XX pattern. The previous
// version sliced the kind NAME ("ro", "re", ...), which is not hex and made
// every probe insert fail with SQLSTATE 22P02.
func hex2(i int) string {
	const hex = "0123456789abcdef"
	return string([]byte{hex[(i/16)%16], hex[i%16]})
}
