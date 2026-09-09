//go:build !no_pg

// Migration-apply test for 00219_edge_rules_kind_limit.sql
// (ADR-091 D24 / new ADR-0NN-edge-rule-limit).
//
// Pins:
//
//  1. Migration set applies cleanly through 00219 (no goose
//     duplicate-version panic). The kind=limit slot landed at
//     00219 after fences at 00217 (PR #849 app_secrets_scope)
//     and 00218 (preview environments) — see
//     cross-pr-slot-fence-reservation-fence-pattern. Future
//     renumbering must re-verify `git ls-tree origin/main
//     migrations/` after every rebase, per
//     migration-test-uuid-sed-residual and
//     pr-845-edge-rules-geo-slot-chase-2026-08-11.
//  2. edge_rules_kind_check CHECK exists, with the closed
//     vocabulary of 9 values: route, rewrite, redirect, headers,
//     cors, jwt, ip, validate, limit. pg_get_constraintdef emits
//     the IN-list form per pg-get-constraintdef-shapes.md; assert
//     every value appears as a substring. The regression pin for
//     the CHECK-rewrite race: a future migration that widens or
//     narrows this CHECK must not silently drop any of these 9
//     values — this assertion catches it here, before production.
//  3. The constraint name is exactly `edge_rules_kind_check`
//     (Postgres-assigned default for an inline CHECK on `kind`).
//     A future regression that ADD CONSTRAINTs a differently-named
//     CHECK (e.g. via the explicit-name form) would create a
//     duplicate constraint and leave the inline CHECK in place;
//     the customer's kind='limit' row would still be validated
//     against the inline CHECK, but a future DROP CONSTRAINT
//     edge_rules_kind_check would silently leave the
//     explicitly-named CHECK in place and break the next
//     migration. Pin the name verbatim.
//  4. Positive round-trip: insert a row with kind='limit' +
//     action={limit:{max_body_bytes:5242880}} → read it back →
//     assert kind='limit'. Pins that the jsonb action column
//     accepts the new shape (pgstore's edgeRuleSelectCols is
//     kind-agnostic and stores action verbatim).
//  5. All 8 pre-existing kinds still accept (load-bearing
//     regression pin for the CHECK-rewrite race between this
//     migration and PR #845's kind=geo widening — when #845
//     lands, its migration must widen to 10 values, including
//     'limit'; this pin catches a regression in either
//     direction).
//  6. A typo kind='limit_typo' is rejected with 23514 (check
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

// limitMigrationVocab is the closed vocabulary edge_rules_kind_check
// must carry after this migration. The slice doubles as the pin
// set the test walks — adding a new value here without also
// widening the migration's IN list is a load-bearing failure mode.
var limitMigrationVocab = []string{
	"route", "rewrite", "redirect", "headers",
	"cors", "jwt", "ip", "validate", "limit",
}

func TestMigrations_00219_EdgeRulesKindLimit(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00219 should land last.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR follow-up failure mode: missing migration slot between 00218 fence and 00219 limit)", err)
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
	for _, v := range limitMigrationVocab {
		needle := "'" + v + "'"
		if !strings.Contains(def, needle) {
			t.Errorf("edge_rules_kind_check: missing %s in def %q (closed vocabulary must include all 9 values; a regression here means the CHECK was narrowed)", needle, def)
		}
	}
	// Belt-and-braces: assert the narrower pre-00219 vocabulary
	// (without 'limit') is NOT the active def. Catches a
	// regression where the migration was authored but the
	// ALTER TABLE ADD CONSTRAINT silently failed (e.g. a
	// transaction rollback that left the new constraint named
	// differently).
	if !strings.Contains(def, "'limit'") {
		t.Errorf("edge_rules_kind_check: 'limit' missing from def %q (the migration's ADD CONSTRAINT must include 'limit')", def)
	}

	// (4) Positive round-trip: kind='limit' row inserts and
	// reads back. Seeds an account + app + edge_rule with the
	// kind=limit action jsonb shape (a single
	// max_body_bytes integer). pgstore.MigrateUp has already
	// applied 00219 — the row goes through the active CHECK.
	accountID := "00000000-0000-0000-0000-000000002219"
	appID := "00000000-0000-0000-0000-000000012219"
	ruleID := "00000000-0000-0000-0000-000000022219"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ($1, 'scale', 'limit-kind-test@example.com')
		on conflict (id) do nothing
	`, accountID); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ($1, $2, 'limit-kind-test', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`, appID, accountID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	action := map[string]any{"limit": map[string]any{"max_body_bytes": 5_242_880}}
	actionJSON, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path,
		                        priority, enabled, kind, action)
		values ($1, $2, $3, 'api.example.com', '/upload', 100, true, 'limit', $4)
	`, ruleID, accountID, appID, actionJSON); err != nil {
		t.Fatalf("insert kind=limit row: %v", err)
	}
	var gotKind string
	var gotAction []byte
	if err := pool.QueryRow(ctx, `
		select kind, action::text
		  from edge_rules
		 where id = $1
	`, ruleID).Scan(&gotKind, &gotAction); err != nil {
		t.Fatalf("read kind=limit row: %v", err)
	}
	if gotKind != "limit" {
		t.Errorf("kind round-trip: got %q, want 'limit' (the closed-vocabulary CHECK accepted it on insert + read; pgstore's kind-agnostic jsonb path round-tripped)", gotKind)
	}
	if !strings.Contains(compactJSON(t, gotAction), `"max_body_bytes":5242880`) {
		t.Errorf("action jsonb round-trip: got %s, want action.max_body_bytes=5242880 (jsonb must preserve the limit action shape verbatim)", string(gotAction))
	}

	// (5) All pre-existing kinds still accept. Walk the
	// pre-00219 vocabulary (route..validate) and assert each
	// inserts successfully. This is the load-bearing regression
	// pin for the CHECK-rewrite race between this migration and
	// PR #845's kind=geo widening — a future regression that
	// narrows the CHECK to just 'limit' (e.g. by silently
	// overwriting the ADD CONSTRAINT with a one-value list)
	// would let kind='limit' rows in but fail every other kind,
	// and this assertion catches it.
	preExistingKinds := []string{
		"route", "rewrite", "redirect", "headers",
		"cors", "jwt", "ip", "validate",
	}
	for i, k := range preExistingKinds {
		// Each pre-existing kind uses a slot-unique rule id so
		// the inserts don't collide on PK. The action jsonb is
		// the kind's minimal valid shape — the gateway hot path
		// is what round-trips action verbosely; this test only
		// pins the CHECK vocabulary.
		var actionShape string
		switch k {
		case "route":
			actionShape = `{"route":{"target_app_slug":"self"}}`
		case "rewrite":
			actionShape = `{"rewrite":{"from":"/v1","to":"/api"}}`
		case "redirect":
			actionShape = `{"redirect":{"status_code":302,"to":"https://x"}}`
		case "headers":
			actionShape = `{"headers":{"request_headers":[],"response_headers":[]}}`
		case "cors":
			actionShape = `{"cors":{"allow_origins":["https://x"],"allow_methods":["GET"],"allow_headers":[],"expose_headers":[],"allow_credentials":false,"max_age_seconds":3600}}`
		case "jwt":
			actionShape = `{"jwt":{"issuer":"x","audience":[],"jwks_url":"https://x.test/jwks","algorithms":["RS256"],"required_claims":{}}}`
		case "ip":
			actionShape = `{"ip":{"allow":[],"deny":[]}}`
		case "validate":
			actionShape = `{"validate":{"schema":{}}}`
		}
		if _, err := pool.Exec(ctx, `
			insert into edge_rules (id, account_id, app_id, match_host, match_path,
			                        priority, enabled, kind, action)
			values (gen_random_uuid(), $1, $2, 'pre.example.com', '/p', 100, true, $3, $4::jsonb)
		`, accountID, appID, k, actionShape); err != nil {
			t.Errorf("pre-existing kind=%q insert: %v (closed-vocabulary CHECK must still accept all 8 pre-existing kinds after this migration)", k, err)
		}
		_ = i
	}

	// (6) Typo kind rejected with 23514. Insert with kind
	// 'limit_typo' and assert the SQLSTATE is exactly
	// check_violation (23514). A future regression that lets
	// unknown kinds through (e.g. by dropping the CHECK
	// entirely) would let production deploy an action shape
	// the gateway doesn't know how to enforce — the same
	// silent-no-op problem kind=limit is designed to surface
	// at create-time rather than silently pass at request-time.
	_, err = pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path,
		                        priority, enabled, kind, action)
		values (gen_random_uuid(), $1, $2, 'typo.example.com', '/typo',
		        100, true, 'limit_typo', '{"limit":{"max_body_bytes":1024}}'::jsonb)
	`, accountID, appID)
	if err == nil {
		t.Fatal("insert kind='limit_typo' succeeded; want 23514 CHECK violation (closed vocabulary must reject unknown kinds)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("limit_typo insert: got non-Postgres error: %v", err)
	}
	if pgErr.Code != "23514" {
		t.Errorf("limit_typo insert: got SQLSTATE=%s, want 23514 (check_violation from edge_rules_kind_check)", pgErr.Code)
	}
	if !strings.Contains(pgErr.ConstraintName, "edge_rules_kind_check") {
		t.Errorf("limit_typo insert: got constraint=%q, want edge_rules_kind_check", pgErr.ConstraintName)
	}

	// (7) Replay safety. MigrateUp twice must be a no-op
	// (mirrors 00160_traffic_percent_test.go:140). The DROP
	// CONSTRAINT IF EXISTS guards handle the second pass; if a
	// future refactor drops them, this assertion catches it
	// before the migration ships.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (DROP CONSTRAINT IF EXISTS guard must keep the second pass a no-op)", err)
	}
}
