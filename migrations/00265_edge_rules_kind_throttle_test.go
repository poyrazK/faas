//go:build !no_pg

// Migration-apply test for 00265_edge_rules_kind_throttle.sql
// (ADR-091 D20.5 amendment, issue #881 PR-A, kind=throttle).
//
// Pins:
//
//  1. Migration set applies cleanly through 00265 (no goose
//     duplicate-version panic). The kind=throttle slot lands at
//     00265 after the kind=maintenance widening at 00236 (PR #867)
//     and PR #884's reservation fences at 00238-00243; see
//     cross-pr-slot-fence-reservation-fence-pattern. Future
//     renumbering must re-verify `git ls-tree origin/main
//     migrations/` AND enumerate open PR fence claims.
//  2. edge_rules_kind_check CHECK exists with the closed
//     vocabulary of 12 values: route, rewrite, redirect, headers,
//     cors, jwt, ip, validate, limit, geo, maintenance, throttle.
//     pg_get_constraintdef emits the IN-list form per
//     pg-get-constraintdef-shapes.md; assert every value appears
//     as a substring. The regression pin for the CHECK-rewrite
//     race: a future migration that widens or narrows this CHECK
//     must not silently drop any of these 12 values.
//  3. The constraint name is exactly `edge_rules_kind_check`
//     (Postgres-assigned default for an inline CHECK on `kind`).
//     Same posture as 00219 / 00214 / 00236.
//  4. Positive round-trip: insert a row with kind='throttle'
//     + action={throttle:{requests_per_second:10.5, burst:20}}
//     → read it back → assert kind='throttle' and the action
//     jsonb payload round-trips. Pins that the jsonb action
//     column accepts the new shape; the cmd-apid layer performs
//     semantic validation (≥1, ≤plan ceiling) so a direct-DB
//     seed can carry a value the API would otherwise reject —
//     this is intentional (defence-in-depth, not enforcement).
//  5. All 11 pre-existing kinds still accept (load-bearing
//     regression pin for the CHECK-rewrite race between this
//     migration and the kind=geo widening at 00229 / kind=
//     maintenance at 00236 — the 00265 vocabulary must still
//     round-trip after this migration).
//  6. A typo kind='throttel' is rejected with 23514
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

// throttleMigrationVocab is the closed vocabulary
// edge_rules_kind_check must carry after this migration. The slice
// doubles as the pin set the test walks — adding a new value here
// without also widening the migration's IN list is a load-bearing
// failure mode.
var throttleMigrationVocab = []string{
	"route", "rewrite", "redirect", "headers",
	"cors", "jwt", "ip", "validate", "limit", "geo",
	"maintenance", "throttle",
}

func TestMigrations_00265_EdgeRulesKindThrottle(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00265 should land last
	// (alongside 00245 if a PR-A trailing fence was needed; for
	// this PR 00265 carries the real DDL and there is no
	// 00245 from this branch). PR #884's 00238-00243 fences
	// must not interfere — they're pure fences with no DDL.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR follow-up failure mode: missing migration slot between 00236 maintenance and 00265 throttle)", err)
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
	for _, v := range throttleMigrationVocab {
		needle := "'" + v + "'"
		if !strings.Contains(def, needle) {
			t.Errorf("edge_rules_kind_check: missing %s in def %q (closed vocabulary must include all 12 values; a regression here means the CHECK was narrowed)", needle, def)
		}
	}
	// Belt-and-braces: 'throttle' must be present.
	if !strings.Contains(def, "'throttle'") {
		t.Errorf("edge_rules_kind_check: 'throttle' missing from def %q (the migration's ADD CONSTRAINT must include 'throttle')", def)
	}

	// (4) Positive round-trip: kind='throttle' row inserts and
	// reads back. Seeds an account + app + edge_rule with the
	// kind=throttle action jsonb shape (requests_per_second:float
	// + burst:int). pgstore.MigrateUp has already applied
	// 00265 — the row goes through the active CHECK.
	accountID := "00000000-0000-0000-0000-00000002244a"
	appID := "00000000-0000-0000-0000-00000022441b"
	ruleID := "00000000-0000-0000-0000-00000022244c"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ($1, 'scale', 'throttle-kind-test@example.com')
		on conflict (id) do nothing
	`, accountID); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ($1, $2, 'throttle-kind-test', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`, appID, accountID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	action := map[string]any{
		"throttle": map[string]any{
			"requests_per_second": 10.5,
			"burst":               20,
		},
	}
	actionJSON, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path,
		                        priority, enabled, kind, action)
		values ($1, $2, $3, 'api.example.com', '/payments', 100, true, 'throttle', $4)
	`, ruleID, accountID, appID, actionJSON); err != nil {
		t.Fatalf("insert kind=throttle row: %v", err)
	}
	var gotKind string
	var gotAction []byte
	if err := pool.QueryRow(ctx, `
		select kind, action::text
		  from edge_rules
		 where id = $1
	`, ruleID).Scan(&gotKind, &gotAction); err != nil {
		t.Fatalf("read kind=throttle row: %v", err)
	}
	if gotKind != "throttle" {
		t.Errorf("kind round-trip: got %q, want 'throttle'", gotKind)
	}
	if !strings.Contains(string(gotAction), `"requests_per_second":10.5`) {
		t.Errorf("action jsonb round-trip: got %s, want action.throttle.requests_per_second=10.5", string(gotAction))
	}
	if !strings.Contains(string(gotAction), `"burst":20`) {
		t.Errorf("action jsonb round-trip: got %s, want action.throttle.burst=20", string(gotAction))
	}

	// (5) All 11 pre-existing kinds still accept. Walk the
	// 00265 vocabulary (route..maintenance) and assert each
	// inserts successfully. The 00265 widening must not have
	// narrowed the CHECK.
	preExistingKinds := []string{
		"route", "rewrite", "redirect", "headers",
		"cors", "jwt", "ip", "validate", "limit", "geo",
		"maintenance",
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
			actionShape = `{"geo":{"allow":[],"deny":[]}}`
		case "maintenance":
			actionShape = `{"maintenance":{"retry_after_seconds":60,"message":"rot"}}`
		}
		if _, err := pool.Exec(ctx, `
			insert into edge_rules (id, account_id, app_id, match_host, match_path,
			                        priority, enabled, kind, action)
			values (gen_random_uuid(), $1, $2, 'pre.example.com', '/p', 100, true, $3, $4::jsonb)
		`, accountID, appID, k, actionShape); err != nil {
			t.Errorf("pre-existing kind=%q insert: %v (closed-vocabulary CHECK must still accept all 11 pre-existing kinds after this migration)", k, err)
		}
	}

	// (6) Typo kind rejected with 23514.
	_, err = pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path,
		                        priority, enabled, kind, action)
		values (gen_random_uuid(), $1, $2, 'typo.example.com', '/typo',
		        100, true, 'throttel', '{"throttle":{"requests_per_second":1,"burst":1}}'::jsonb)
	`, accountID, appID)
	if err == nil {
		t.Fatal("insert kind='throttel' succeeded; want 23514 CHECK violation")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("throttel insert: got non-Postgres error: %v", err)
	}
	if pgErr.Code != "23514" {
		t.Errorf("throttel insert: got SQLSTATE=%s, want 23514", pgErr.Code)
	}
	if !strings.Contains(pgErr.ConstraintName, "edge_rules_kind_check") {
		t.Errorf("throttel insert: got constraint=%q, want edge_rules_kind_check", pgErr.ConstraintName)
	}

	// (7) Replay safety.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (DROP CONSTRAINT IF EXISTS guard must keep the second pass a no-op)", err)
	}
}
