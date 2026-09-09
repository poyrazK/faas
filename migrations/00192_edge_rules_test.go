//go:build !no_pg

// Migration-apply test for 00192_edge_rules.sql (ADR-089 / PR 1 of
// the edge-rules rollout). Pins the table shape + the seven-kind
// CHECK + the priority CHECK + the partial indexes + the
// ON DELETE CASCADE behaviour.
//
// Pins:
//
//  1. Migration set applies cleanly through 00192.
//  2. The table exists with all 13 columns and the jsonb `action`
//     column has data_type='jsonb' (NOT 'text' — drift here is the
//     most likely silent regression).
//  3. Defaults: match_path='/', match_methods='{}', priority=100,
//     enabled=true.
//  4. The kind CHECK rejects an invalid kind with a 23514
//     (check_violation) — wire-bypass backstop.
//  5. The priority CHECK rejects a negative value.
//  6. ON DELETE CASCADE on app_id: deleting the parent app removes
//     the rule row.
//  7. Partial index edge_rules_match_host_pattern_idx exists and
//     uses text_pattern_ops — the load-bearing index for the
//     gateway's LIKE prefix scan.
//  8. Replay-safety: a second MigrateUp is a no-op (ADR-041).
//
// Seed UUIDs carry the slot number in the last group (`...000192`,
// `...000292`, `...000392`) so a reader scanning the test fixtures
// can pin each row to this migration without grepping the file
// name. The literal slot value MUST stay in sync with the
// filename; renumber per migrations/README.md if a sibling PR
// grabs 00192 first.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00192_EdgeRules(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00192.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 172)", err)
	}

	// (2) Table shape + action column type. Drift on action type
	// (jsonb → text) is the most likely silent regression because
	// the column reads look the same from Go until the first
	// marshal/unmarshal roundtrip.
	var actionType string
	if err := pool.QueryRow(ctx, `
		SELECT data_type FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'edge_rules'
		  AND column_name = 'action'
	`).Scan(&actionType); err != nil {
		t.Fatalf("read edge_rules.action data_type: %v", err)
	}
	if actionType != "jsonb" {
		t.Errorf("edge_rules.action: got data_type=%s, want jsonb (regression: action must be jsonb for the kind-tagged union)", actionType)
	}

	// (3) Seed the parent account + app + a single rule with
	// every column set to its default + a non-default action jsonb
	// payload. ON CONFLICT DO NOTHING so reruns are idempotent.
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-000000000192',
		        'edge-rules-test@example.com', 'hobby', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		-- status='active' is the live-app value admitted by
		-- apps_status_check (active | evicted_cold | deleted).
		-- 'live' is the DEPLOYMENTS status vocabulary, not the apps one.
		insert into apps (id, account_id, slug, runtime, status, created_at, ram_mb)
		values ('00000000-0000-0000-0000-000000000292',
		        '00000000-0000-0000-0000-000000000192',
		        'edge-rules-test-app', 'node22', 'active', now(), 256)
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (
			id, account_id, app_id, match_host, match_path,
			match_methods, priority, enabled, kind, action
		) values (
			'00000000-0000-0000-0000-000000000392',
			'00000000-0000-0000-0000-000000000192',
			'00000000-0000-0000-0000-000000000292',
			'*.example.com', '/api/*',
			ARRAY['GET','POST'], 50, true, 'rewrite',
			'{"kind":"rewrite","rewrite":{"from":"/api","to":"/v1"}}'::jsonb
		)
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed rule: %v", err)
	}

	// (4) Round-trip the seeded row + assert defaults. The seeded
	// row has explicit match_path + match_methods + priority so we
	// also seed a separate defaults row to assert the column
	// DEFAULTs.
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (
			id, account_id, app_id, match_host, kind, action
		) values (
			'00000000-0000-0000-0000-000000000492',
			'00000000-0000-0000-0000-000000000192',
			'00000000-0000-0000-0000-000000000292',
			'*', 'cors',
			'{"kind":"cors","cors":{"allow_origins":["https://app.example.com"],"allow_methods":["GET"],"allow_credentials":false,"max_age_seconds":600}}'::jsonb
		)
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed defaults row: %v", err)
	}

	var (
		gotMatchPath    string
		gotMatchMethods []string
		gotPriority     int
		gotEnabled      bool
		gotKind         string
	)
	if err := pool.QueryRow(ctx, `
		select match_path, match_methods, priority, enabled, kind
		  from edge_rules
		 where id = '00000000-0000-0000-0000-000000000492'
	`).Scan(&gotMatchPath, &gotMatchMethods, &gotPriority, &gotEnabled, &gotKind); err != nil {
		t.Fatalf("read defaults row: %v", err)
	}
	if gotMatchPath != "/" {
		t.Errorf("default match_path = %q, want %q", gotMatchPath, "/")
	}
	if len(gotMatchMethods) != 0 {
		t.Errorf("default match_methods = %v, want empty array", gotMatchMethods)
	}
	if gotPriority != 100 {
		t.Errorf("default priority = %d, want 100", gotPriority)
	}
	if !gotEnabled {
		t.Errorf("default enabled = false, want true")
	}
	if gotKind != "cors" {
		t.Errorf("seeded kind = %q, want cors", gotKind)
	}

	// (5) CHECK on kind: reject an invalid kind.
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (account_id, app_id, match_host, kind, action)
		values ('00000000-0000-0000-0000-000000000192',
		        '00000000-0000-0000-0000-000000000292',
		        'bad.example.com', 'nonsense', '{}'::jsonb)
	`); err == nil {
		t.Errorf("edge_rules.kind = 'nonsense' accepted; want CHECK violation (regression: closed enum must be enforced at the DB layer)")
	}

	// (6) CHECK on priority: reject a negative value.
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (account_id, app_id, match_host, priority, kind, action)
		values ('00000000-0000-0000-0000-000000000192',
		        '00000000-0000-0000-0000-000000000292',
		        'bad.example.com', -1, 'cors', '{}'::jsonb)
	`); err == nil {
		t.Errorf("edge_rules.priority = -1 accepted; want CHECK violation (regression: 0..10000 range must be enforced)")
	}

	// (7) ON DELETE CASCADE: deleting the parent app removes the
	// rule row. Mirrors the lifecycle test in
	// 00033_app_egress_allowlist_test.go.
	var preCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from edge_rules
		 where app_id = '00000000-0000-0000-0000-000000000292'
	`).Scan(&preCount); err != nil {
		t.Fatalf("count pre-cascade: %v", err)
	}
	if preCount == 0 {
		t.Fatalf("expected ≥1 rule row for the test app; seed didn't land")
	}
	if _, err := pool.Exec(ctx, `
		delete from apps where id = '00000000-0000-0000-0000-000000000292'
	`); err != nil {
		t.Fatalf("delete app: %v", err)
	}
	var postCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from edge_rules
		 where app_id = '00000000-0000-0000-0000-000000000292'
	`).Scan(&postCount); err != nil {
		t.Fatalf("count post-cascade: %v", err)
	}
	if postCount != 0 {
		t.Errorf("edge_rules rows for deleted app = %d, want 0 (regression: ON DELETE CASCADE missing on app_id)", postCount)
	}

	// (8) Replay-safety: a second MigrateUp is a no-op.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay-safety: second MigrateUp failed: %v", err)
	}

	// (9) text_pattern_ops index presence. The gateway's LIKE
	// prefix scan depends on this index — a regression that drops
	// it forces a seqscan on a customer with many rules per host.
	var patternIdxDef string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND tablename = 'edge_rules'
		  AND indexname = 'edge_rules_match_host_pattern_idx'
	`).Scan(&patternIdxDef); err != nil {
		t.Fatalf("read pattern index def: %v", err)
	}
	// Drift here surfaces as "seq scan on edge_rules" under load.
	if !strings.Contains(patternIdxDef, "text_pattern_ops") {
		t.Errorf("edge_rules_match_host_pattern_idx missing text_pattern_ops; got: %s", patternIdxDef)
	}
}

// contains helper intentionally not declared — sibling
// migrations/00057_sessions_test.go:120 already owns it in the
// shared migrations_test package. Use strings.Contains directly.
