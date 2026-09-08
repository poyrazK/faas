//go:build !no_pg

// Migration-apply test for 00293_validate_mode.sql
// (issue #975 item #3 / ADR-128). Pins the schema shape that
// ADR-128's top-level-authority story relies on:
//
//  1. Migration set applies cleanly through 00293.
//  2. edge_rules.validate_mode column exists, type TEXT,
//     NOT NULL, default 'block'.
//  3. edge_rules_validate_mode_check accepts the closed
//     vocabulary {observe, warn, block}.
//  4. A typo value ('observe_typo') is rejected with 23514 —
//     proves the column is closed, not open text.
//  5. NOT NULL is enforced (an INSERT without the column falls
//     back to the default 'block', not NULL).
//  6. Positive round-trip for all three modes reads back
//     verbatim.
//  7. Replay safety: re-running db.MigrateUp is a no-op.
//
// The runtime layer (cmd/gatewayd-internal/edge_rules.go:1551
// and pkg/gateway/handler.go:2692) reads this column today as a
// legacy path; ADR-128 promotes it to the single source of truth
// in a follow-up commit. This test pins the schema side so a
// future migration cannot silently narrow the closed vocabulary
// (the 00265 / 00345 'budget' regression pattern).

package migrations_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// validateModeVocab is the closed vocabulary the
// edge_rules_validate_mode_check must carry after 00293. Future
// migrations MAY add new modes (a future widening would change
// the ADR-128 follow-up shape, not this test), but the three
// shipped values are pinned here so a narrowing is loud, not
// silent.
var validateModeVocab = []string{"observe", "warn", "block"}

func TestMigrations_00293_ValidateMode(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00293 should land last
	// (or near-last — main reserves slots above 00293 for
	// unrelated work, but 00293 itself must apply cleanly).
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// (2) Column shape pin. NOT NULL + default 'block' is the
	// load-bearing behavior: pre-existing rows from before 00293
	// ship forward at 'block' (the strictest mode), so this
	// migration cannot change behavior for any rule created
	// before it applied. A regression here means a future
	// migration dropped the default — every pre-existing rule
	// would now violate NOT NULL.
	var (
		dataType      string
		isNullable    string
		columnDefault *string
	)
	if err := pool.QueryRow(ctx, `
		select data_type, is_nullable, column_default
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'edge_rules'
		   and column_name  = 'validate_mode'
	`).Scan(&dataType, &isNullable, &columnDefault); err != nil {
		t.Fatalf("query information_schema.columns for edge_rules.validate_mode: %v (the migration must add the column)", err)
	}
	if dataType != "text" {
		t.Errorf("validate_mode data_type: got %q, want 'text'", dataType)
	}
	if isNullable != "NO" {
		t.Errorf("validate_mode is_nullable: got %q, want 'NO' (NOT NULL is load-bearing — the column must reject NULL even before the default is applied)", isNullable)
	}
	if columnDefault == nil || !strings.Contains(*columnDefault, "'block'") {
		// Postgres renders the default as e.g.
		// "'block'::text" on PG15+ — substring match is the
		// load-bearing pin, exact-form match is over-tight.
		t.Errorf("validate_mode column_default: got %v, want substring 'block' (every pre-existing edge rule ships forward at the strictest mode)", columnDefault)
	}

	// (3) CHECK shape pin. Same pg_get_constraintdef wrapper-or-not
	// caveat as the 00345 companion test: PG15 emits
	// "CHECK (... IN (...))", PG16+ may strip the wrapper. Substring
	// match against the closed vocabulary is the load-bearing pin.
	var def string
	if err := pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'edge_rules_validate_mode_check'
		   and n.nspname = current_schema()
	`).Scan(&def); err != nil {
		t.Fatalf("query edge_rules_validate_mode_check: %v (the migration must add the named CHECK)", err)
	}
	for _, v := range validateModeVocab {
		needle := "'" + v + "'"
		if !strings.Contains(def, needle) {
			t.Errorf("edge_rules_validate_mode_check: missing %s in def %q (all three closed values must be present; a narrowing here would silently break every validate rule on the affected app)", needle, def)
		}
	}

	// Seed an account + app for the round-trip + negative
	// inserts. The companion test runs against the live pgtest
	// schema, so the rows land in current_schema() — same
	// scoping rule as the rest of the migration test suite.
	accountID := "00000000-0000-0000-0000-000000002931"
	appID := "00000000-0000-0000-0000-000000002932"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ($1, 'scale', 'validate-mode-test@example.com')
		on conflict (id) do nothing
	`, accountID); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ($1, $2, 'validate-mode-test', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`, appID, accountID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}

	// (5) NOT NULL fallback: insert without the column, expect
	// the default 'block' to fire. Proves NOT NULL is wired (a
	// future migration that drops the default would 23502
	// "not-null violation" here — the column doesn't tolerate
	// NULL even with the explicit omit).
	defaultRuleID := "00000000-0000-0000-0000-000000002933"
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path,
		                        priority, enabled, kind, action)
		values ($1, $2, $3, 'default.example.com', '/v1/default', 100, true, 'validate', '{"schema":{}}'::jsonb)
	`, defaultRuleID, accountID, appID); err != nil {
		t.Fatalf("insert validate rule without validate_mode: %v (the column default must fire — NOT NULL fallback)", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from edge_rules where id = $1`, defaultRuleID)
	})
	var defaultMode string
	if err := pool.QueryRow(ctx,
		`select validate_mode from edge_rules where id = $1`, defaultRuleID,
	).Scan(&defaultMode); err != nil {
		t.Fatalf("read default-mode row: %v", err)
	}
	if defaultMode != "block" {
		t.Errorf("validate_mode default: got %q, want 'block' (every rule created without an explicit mode ships forward at the strictest enforcement)", defaultMode)
	}

	// (6) Positive round-trip for all three closed-vocab values.
	// The runtime path (cmd/gatewayd-internal/edge_rules.go:1551)
	// reads this column verbatim; a typo in the column writer
	// (e.g. 'observe ' with a trailing space) would silently
	// re-route every validate rule on the affected app.
	for i, mode := range validateModeVocab {
		mode := mode
		t.Run("round_trip_"+mode, func(t *testing.T) {
			ruleID := validateModeProbeUUID(i)
			if _, err := pool.Exec(ctx, `
				insert into edge_rules (id, account_id, app_id, match_host, match_path,
				                        priority, enabled, kind, action, validate_mode)
				values ($1, $2, $3, 'rt.example.com', $4, 100, true, 'validate', '{"schema":{}}'::jsonb, $5)
			`, ruleID, accountID, appID, "/rt/"+mode, mode); err != nil {
				t.Fatalf("insert validate rule with mode=%q: %v", mode, err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(ctx, `delete from edge_rules where id = $1`, ruleID)
			})
			var gotMode string
			if err := pool.QueryRow(ctx,
				`select validate_mode from edge_rules where id = $1`, ruleID,
			).Scan(&gotMode); err != nil {
				t.Fatalf("read mode=%q row: %v", mode, err)
			}
			if gotMode != mode {
				t.Errorf("validate_mode round-trip: got %q, want %q (the column writer must not normalize, lowercase, or trim — the runtime reads it verbatim)", gotMode, mode)
			}
		})
	}

	// (4) Negative: a typo mode is rejected with 23514. Proves
	// the CHECK is the closed vocabulary, not an open text column.
	// Without this, a misspelled 'observee' mode would silently
	// become an inert rule (the runtime's empty-string → 'block'
	// coerce at handler.go:2694 wouldn't fire because the column
	// wouldn't be empty, just invalid).
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path,
		                        priority, enabled, kind, action, validate_mode)
		values ('00000000-0000-0000-0000-000000002939', $1, $2, 'typo.example.com', '/x', 300, true, 'validate', '{}'::jsonb, 'observe_typo')
	`, accountID, appID); err == nil {
		t.Error("insert validate_mode='observe_typo' succeeded; want 23514 (the closed vocabulary must reject near-miss typos)")
	} else if !strings.Contains(err.Error(), "23514") && !strings.Contains(err.Error(), "edge_rules_validate_mode_check") {
		t.Errorf("insert validate_mode='observe_typo': got %v, want a 23514 check_violation on edge_rules_validate_mode_check", err)
	}

	// (8) Replay safety: re-running the migration set is a
	// no-op. The DROP CONSTRAINT IF EXISTS + ADD pair is the
	// load-bearing replay-safety recipe (the migration is
	// idempotent on a hot-fix path that bypasses goose's version
	// table).
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp (replay): %v (00293 must be replay-safe — DROP CONSTRAINT IF EXISTS then ADD)", err)
	}
}

// validateModeProbeUUID returns a distinct, deterministic UUID
// per round-trip probe so parallel subtests never collide on the
// primary key. Pattern mirrors probeUUID in the 00345 companion
// test.
func validateModeProbeUUID(i int) string {
	// The final UUID group is exactly 12 hex digits. The previous
	// hand-counted literal carried an 11-digit prefix and appended two
	// more, producing a 13-digit group that Postgres rejects with
	// 22P02. %012x keeps the width structural instead of hand-counted.
	// 0x2900 keeps the "29" (00293) marker and stays clear of the
	// fixed probe ids used above (…2933, …2939).
	return fmt.Sprintf("00000000-0000-0000-0000-%012x", 0x2900+i)
}
