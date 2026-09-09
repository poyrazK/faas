//go:build !no_pg

// Migration-apply test for 00487_edge_rules_cors_preset_fk.sql
// (ADR-129 D1). Pins four concerns because the migration carries
// four:
//
//  1. Migration set applies cleanly through 00487. Slot history:
//     00428 (initial) → 00472 (1st rebase, main's 00428 fence)
//     → 00475 (2nd rebase, PR #1064 merge's 00472/00473/00474/00476
//     fences) → 00479 (3rd renumber, PR #1111 claimed 00475)
//     → 00481 (4th renumber, PR #1126 merged MFA at 00479 + PR
//     #1127 claimed 00480) → 00484 (5th renumber, PR #1133
//     merged 00481/00482/00483) → **00487** (6th renumber, PR
//     #1111 finally merged to main at 00486, plus main gained
//     00484_reserve_slot.sql + 00485_deployments_canary_state.sql
//     from PR #1124 after the 5th renumber landed). 00487 is the
//     next free slot above mainline 00486.
//     Verify no goose duplicate-version panic against any of
//     main's or any open PR's slots.
//  2. edge_rules.cors_preset_id column exists under that exact
//     name, is nullable, and has no DEFAULT (NULL is the default).
//     ADR-129 D1 mandates NULL-on-default because gen_random_uuid()'s
//     UUIDs are never valid cors_presets.id by construction.
//  3. FK constraint edge_rules_cors_preset_fk exists, points at
//     cors_presets(id), and uses ON DELETE SET NULL — the cascade
//     decision is the load-bearing behavioural pin (cascade would
//     silently delete customer rules; restrict would refuse
//     legitimate preset cleanup; SET NULL keeps the rule in place
//     while the compile path fails closed).
//  4. Partial index edge_rules_cors_preset_id_idx exists with
//     predicate cors_preset_id IS NOT NULL — covers the per-rule
//     preset lookup in compileCORSRules.

package migrations_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00487_EdgeRulesCorsPresetFK(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00475 should land last.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (failure mode: a slot collision between this migration's 00487 and an open-PR fence — re-run the open-PR slot precheck including refs/pull/<N>/head)", err)
	}

	// (2) Column shape pin. information_schema.columns exposes the
	// (is_nullable, column_default) pair directly; the load-bearing
	// check is column_default IS NULL, mirroring the "NULL is the
	// default, no DEFAULT clause" pattern. Any DEFAULT clause here
	// would silently land invalid preset IDs on legacy rules.
	// column_default is SQL NULL for a column with no DEFAULT clause —
	// which is exactly the state this test pins — so the scan target
	// must be nullable (*string), not string.
	var isNullable string
	var columnDefault *string
	if err := pool.QueryRow(ctx, `
		select is_nullable, column_default
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name = 'edge_rules'
		   and column_name = 'cors_preset_id'
	`).Scan(&isNullable, &columnDefault); err != nil {
		t.Fatalf("query edge_rules.cors_preset_id column: %v (the column must have landed)", err)
	}
	if isNullable != "YES" {
		t.Errorf("edge_rules.cors_preset_id: is_nullable=%q, want YES (the column must be nullable — inline-only rules have NULL)", isNullable)
	}
	if columnDefault != nil {
		t.Errorf("edge_rules.cors_preset_id: column_default=%q, want NULL — the column must NOT carry a DEFAULT (any DEFAULT would silently land invalid cors_presets.id on legacy rules; the gen_random_uuid() default would 23503 on INSERT)", *columnDefault)
	}

	// (3) FK constraint shape. pg_get_constraintdef emits the
	// FOREIGN KEY (...) REFERENCES ... ON DELETE SET NULL clause;
	// string-pin is the load-bearing pattern (the pg_get_constraintdef
	// shapes memory entry documents the version-dependent wrapping —
	// 15 wraps in `FOREIGN KEY (...) ...`, 16+ strips the wrapper).
	var fkDef string
	if err := pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'edge_rules_cors_preset_fk'
		   and n.nspname = current_schema()
		   and c.contype = 'f'
	`).Scan(&fkDef); err != nil {
		t.Fatalf("query edge_rules_cors_preset_fk constraint: %v (the FK constraint must have landed with this exact conname so the Down block can DROP it)", err)
	}
	for _, want := range []string{
		"cors_presets",
		"ON DELETE SET NULL",
	} {
		if !strings.Contains(fkDef, want) {
			t.Errorf("edge_rules_cors_preset_fk: def=%q missing %q — ADR-129 D1 mandates cors_presets(id) target with ON DELETE SET NULL (any other shape — CASCADE, RESTRICT, NO ACTION — silently breaks customer data on preset delete)", fkDef, want)
		}
	}

	// (4) Partial index predicate. The compile-side
	// cmd/gatewayd-internal/edge_rules.go::compileCORSRules looks up
	// the preset by FK; a non-partial index would be the size of
	// edge_rules itself (most rules have NULL) and defeat the
	// purpose. Pin the WHERE clause substring directly.
	var indexDef string
	if err := pool.QueryRow(ctx, `
		select indexdef
		  from pg_indexes
		 where schemaname = current_schema()
		   and tablename = 'edge_rules'
		   and indexname = 'edge_rules_cors_preset_id_idx'
	`).Scan(&indexDef); err != nil {
		t.Fatalf("query edge_rules_cors_preset_id_idx: %v (the partial index must have landed — it covers the per-rule lookup in compileCORSRules)", err)
	}
	if !strings.Contains(indexDef, "WHERE") || !strings.Contains(indexDef, "cors_preset_id IS NOT NULL") {
		t.Errorf("edge_rules_cors_preset_id_idx: def=%q missing WHERE cors_preset_id IS NOT NULL — the partial predicate is load-bearing (most rules have NULL, so a non-partial index bloats the index for no query benefit)", indexDef)
	}

	// Round-trip: create a preset, create an edge rule referencing
	// it, then DELETE the preset and verify the rule's FK is now
	// NULL via ON DELETE SET NULL. This is the behavioural pin for
	// D1's "delete preset does not delete rule" guarantee.
	accountID := "00000000-0000-0000-0000-000000004281"
	appID := "00000000-0000-0000-0000-000000014281"
	presetID := "00000000-0000-0000-0000-000000024281"
	ruleID := "00000000-0000-0000-0000-000000034281"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ($1, 'scale', 'cors-preset-fk-test@example.com')
		on conflict (id) do nothing
	`, accountID); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ($1, $2, 'cors-preset-fk-test', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`, appID, accountID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}

	// Seed the preset directly. The api-side cors_presets surface
	// lives in cmd/apid/handlers_cors_presets.go and is tested
	// elsewhere; this test exercises the migration's FK contract
	// in isolation.
	if _, err := pool.Exec(ctx, `
		insert into cors_presets (id, account_id, name, allow_origins, allow_methods)
		values ($1, $2, 'public-https', '{"https://app.example.com"}', '{"GET","POST"}')
	`, presetID, accountID); err != nil {
		t.Fatalf("seed cors_presets: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from cors_presets where id = $1`, presetID)
	})

	actionJSON, err := json.Marshal(map[string]any{"cors": map[string]any{
		"cors_preset_id": presetID,
	}})
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path,
		                        priority, enabled, kind, action, cors_preset_id)
		values ($1, $2, $3, 'fk.example.com', '/api/*', 100, true, 'cors', $4, $5)
	`, ruleID, accountID, appID, actionJSON, presetID); err != nil {
		t.Fatalf("insert edge rule with cors_preset_id FK: %v (the FK must accept the seeded preset)", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from edge_rules where id = $1`, ruleID)
	})

	// Delete the preset. ON DELETE SET NULL must fire and clear the
	// rule's cors_preset_id without deleting the rule.
	if _, err := pool.Exec(ctx, `delete from cors_presets where id = $1`, presetID); err != nil {
		t.Fatalf("delete preset: %v", err)
	}

	var ruleFK *string
	if err := pool.QueryRow(ctx, `
		select cors_preset_id from edge_rules where id = $1
	`, ruleID).Scan(&ruleFK); err != nil {
		t.Fatalf("read rule after preset delete: %v", err)
	}
	if ruleFK != nil {
		t.Errorf("after preset delete: edge_rules.cors_preset_id = %v, want NULL (ON DELETE SET NULL is the load-bearing decision — anything else — CASCADE, RESTRICT, NO ACTION — silently breaks customer data)", *ruleFK)
	}

	// Replay safety: re-running the migration set is a no-op.
	// The DROP CONSTRAINT IF EXISTS + ADD pair must be idempotent.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp (replay): %v (00487 must be replay-safe — ADD COLUMN IF NOT EXISTS + DROP CONSTRAINT IF EXISTS + ADD CONSTRAINT)", err)
	}
}
