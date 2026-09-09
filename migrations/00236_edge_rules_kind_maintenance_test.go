//go:build !no_pg

// Migration-apply test for 00236_edge_rules_kind_maintenance.sql
// (ADR-091 amendment — D18 per-kind extension, PR-A fence + PR-B
// runtime + PR-C rollout-closer).
//
// Pins:
//
//  1. Migration set applies cleanly through 00236 (no goose
//     duplicate-version panic). The kind=maintenance slot landed
//     at 00236 after the kind=geo widening at 00229 (PR #845) and
//     the 00227/00228 cross-PR fence stampede; see
//     cross-pr-slot-fence-reservation-fence-pattern. Future
//     renumbering must re-verify `git ls-tree origin/main
//     migrations/` AND enumerate open PR fence claims (PR #873
//     secretscan fences 00233; PR #864 ADR-093 reshuffled to
//     00234 after PR #872 landed).
//  2. edge_rules_kind_check CHECK exists, with the closed
//     vocabulary of 11 values: route, rewrite, redirect, headers,
//     cors, jwt, ip, validate, limit, geo, maintenance.
//     pg_get_constraintdef emits the IN-list form per
//     pg-get-constraintdef-shapes.md; assert every value appears
//     as a substring. The regression pin for the CHECK-rewrite
//     race: a future migration that widens or narrows this
//     CHECK must not silently drop any of these 11 values.
//  3. The constraint name is exactly `edge_rules_kind_check`
//     (Postgres-assigned default for an inline CHECK on `kind`).
//     Same posture as 00219 / 00214.
//  4. Positive round-trip: insert a row with kind='maintenance'
//     + action={maintenance:{retry_after_seconds:60,
//     message:"…"}} → read it back → assert kind='maintenance'.
//     Pins that the jsonb action column accepts the new shape.
//  5. All 10 pre-existing kinds still accept (load-bearing
//     regression pin for the CHECK-rewrite race between this
//     migration and the kind=limit widening at 00219 — the
//     00236/00237 vocabulary must still round-trip after this
//     migration).
//  6. A typo kind='maintenance_typo' is rejected with 23514
//     (check_violation). Pins the closed vocabulary contract.
//  7. Replay safety: re-running db.MigrateUp is a no-op.
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

// maintenanceMigrationVocab is the closed vocabulary
// edge_rules_kind_check must carry after this migration. The slice
// doubles as the pin set the test walks — adding a new value here
// without also widening the migration's IN list is a load-bearing
// failure mode.
var maintenanceMigrationVocab = []string{
	"route", "rewrite", "redirect", "headers",
	"cors", "jwt", "ip", "validate", "limit", "geo", "maintenance",
}

func TestMigrations_00236_EdgeRulesKindMaintenance(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00236 should land last (alongside 00237)
	// (which is the apps maintenance_mode
	// column + trigger).
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR follow-up failure mode: missing migration slot between 00229 geo and 00236 maintenance)", err)
	}

	// (2) CHECK constraint shape + (3) constraint name pin.
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
	for _, v := range maintenanceMigrationVocab {
		needle := "'" + v + "'"
		if !strings.Contains(def, needle) {
			t.Errorf("edge_rules_kind_check: missing %s in def %q (closed vocabulary must include all 11 values; a regression here means the CHECK was narrowed)", needle, def)
		}
	}
	// Belt-and-braces: 'maintenance' must be present.
	if !strings.Contains(def, "'maintenance'") {
		t.Errorf("edge_rules_kind_check: 'maintenance' missing from def %q (the migration's ADD CONSTRAINT must include 'maintenance')", def)
	}

	// (4) Positive round-trip: kind='maintenance' row inserts
	// and reads back. Seeds an account + app + edge_rule with
	// the kind=maintenance action jsonb shape (retry_after_seconds
	// integer + message string). pgstore.MigrateUp has already
	// applied 00236 — the row goes through the active CHECK.
	accountID := "00000000-0000-0000-0000-000000002292"
	appID := "00000000-0000-0000-0000-000000012224"
	ruleID := "00000000-0000-0000-0000-000000022922"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ($1, 'scale', 'maintenance-kind-test@example.com')
		on conflict (id) do nothing
	`, accountID); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ($1, $2, 'maintenance-kind-test', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`, appID, accountID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	action := map[string]any{
		"maintenance": map[string]any{
			"retry_after_seconds": 3600,
			"message":             "Scheduled payment rollout in progress",
		},
	}
	actionJSON, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path,
		                        priority, enabled, kind, action)
		values ($1, $2, $3, 'api.example.com', '/payments', 100, true, 'maintenance', $4)
	`, ruleID, accountID, appID, actionJSON); err != nil {
		t.Fatalf("insert kind=maintenance row: %v", err)
	}
	var gotKind string
	var gotAction []byte
	if err := pool.QueryRow(ctx, `
		select kind, action::text
		  from edge_rules
		 where id = $1
	`, ruleID).Scan(&gotKind, &gotAction); err != nil {
		t.Fatalf("read kind=maintenance row: %v", err)
	}
	if gotKind != "maintenance" {
		t.Errorf("kind round-trip: got %q, want 'maintenance'", gotKind)
	}
	if !strings.Contains(compactJSON(t, gotAction), `"retry_after_seconds":3600`) {
		t.Errorf("action jsonb round-trip: got %s, want action.maintenance.retry_after_seconds=3600", string(gotAction))
	}
	if !strings.Contains(compactJSON(t, gotAction), `Scheduled payment rollout`) {
		t.Errorf("action jsonb round-trip: got %s, want action.maintenance.message substring", string(gotAction))
	}

	// (5) All 10 pre-existing kinds still accept. Walk the
	// 00236/00237 vocabulary (route..geo) and assert each
	// inserts successfully. The 00236 widening must not have
	// narrowed the CHECK.
	preExistingKinds := []string{
		"route", "rewrite", "redirect", "headers",
		"cors", "jwt", "ip", "validate", "limit", "geo",
	}
	for _, k := range preExistingKinds {
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
		case "limit":
			actionShape = `{"limit":{"max_body_bytes":1048576}}`
		case "geo":
			actionShape = `{"geo":{"allow_countries":["DE"],"deny_countries":[]}}`
		}
		if actionShape == "" {
			t.Fatalf("no action shape for kind %q: extend the switch when the vocabulary grows", k)
		}
		if _, err := pool.Exec(ctx, `
			insert into edge_rules (id, account_id, app_id, match_host, match_path,
			                        priority, enabled, kind, action)
			values (gen_random_uuid(), $1, $2, 'pre.example.com', '/p', 100, true, $3, $4::jsonb)
		`, accountID, appID, k, actionShape); err != nil {
			t.Errorf("pre-existing kind=%q insert: %v (closed-vocabulary CHECK must still accept all 10 pre-existing kinds after this migration)", k, err)
		}
	}

	// (6) Typo kind rejected with 23514.
	_, err = pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path,
		                        priority, enabled, kind, action)
		values (gen_random_uuid(), $1, $2, 'typo.example.com', '/typo',
		        100, true, 'maintenance_typo', '{"maintenance":{"retry_after_seconds":60}}'::jsonb)
	`, accountID, appID)
	if err == nil {
		t.Fatal("insert kind='maintenance_typo' succeeded; want 23514 CHECK violation")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("maintenance_typo insert: got non-Postgres error: %v", err)
	}
	if pgErr.Code != "23514" {
		t.Errorf("maintenance_typo insert: got SQLSTATE=%s, want 23514", pgErr.Code)
	}
	if !strings.Contains(pgErr.ConstraintName, "edge_rules_kind_check") {
		t.Errorf("maintenance_typo insert: got constraint=%q, want edge_rules_kind_check", pgErr.ConstraintName)
	}

	// (7) Replay safety.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (DROP CONSTRAINT IF EXISTS guard must keep the second pass a no-op)", err)
	}
}
