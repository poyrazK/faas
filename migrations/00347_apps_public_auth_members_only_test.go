//go:build !no_pg

// Migration-apply test for 00347 (ADR-123 — per-app ingress
// 'members_only' mode, extends apps.public_auth_mode with
// 'members_only'). Pins the contract:
//
//   1. The migration set applies cleanly through 00347.
//   2. Setting public_auth_mode='members_only' is accepted
//      (CHECK widening applied).
//   3. The closed public_auth_mode enum rejects unknown values
//      (`'unknown'` fails the widened CHECK).
//   4. Down-migrate narrows the CHECK back to the pre-ADR-123
//      vocabulary (and the row we seeded in members_only
//      would 23514 against the narrower CHECK — pin that the
//      Down section does NOT silently destroy customer rows;
//      the operator is responsible for clearing rows before
//      running the Down).
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
//
// The migration is a CHECK widening only — no new column, no
// trigger. So the test surface is much smaller than the
// ip_allowlist sibling (00326) and matches the internal_only
// sibling (00333). The contract that matters is the
// closed-enum vocabulary + the down-migrate ordering.

package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrations_00347_AppPublicAuthMembersOnly pins the
// members_only contract from ADR-123. Mirrors the 00333
// (internal_only) test verbatim with the new mode swapped
// in; same SQLSTATE + ConstraintName assertions.
func TestMigrations_00347_AppPublicAuthMembersOnly(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00347.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 346)", err)
	}

	// (2) Seed an account + apps row to carry the column. The
	// literal UUIDs are fixed across reruns so the seed is
	// idempotent; mirrors the 00326 + 00333 test styles for
	// grep-ability.
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-000000000347',
		        'public-auth-members-only-test@example.com', 'hobby', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ('00000000-0000-0000-0000-000000000347',
		        '00000000-0000-0000-0000-000000000347',
		        'public-auth-members-only-test', 256, 1, 60, 'active', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	// (3) EnumAcceptsMembersOnly.
	if _, err := pool.Exec(ctx, `
		update apps set public_auth_mode = 'members_only'
		 where id = '00000000-0000-0000-0000-000000000347'
	`); err != nil {
		t.Fatalf("set public_auth_mode='members_only': %v (CHECK widening not applied?)", err)
	}

	// (4) RoundTrip: read back the value to confirm the row
	// actually stored it.
	var gotMode string
	if err := pool.QueryRow(ctx, `
		select public_auth_mode from apps
		 where id = '00000000-0000-0000-0000-000000000347'
	`).Scan(&gotMode); err != nil {
		t.Fatalf("read back public_auth_mode: %v", err)
	}
	if gotMode != "members_only" {
		t.Fatalf("public_auth_mode round-trip: got %q, want %q", gotMode, "members_only")
	}

	// (5) EnumRejectsUnknown: setting an unknown mode value
	// fails with SQLSTATE 23514 and the widened CHECK
	// constraint name.
	_, err := pool.Exec(ctx, `
		update apps set public_auth_mode = 'unknown_mode_xyz'
		 where id = '00000000-0000-0000-0000-000000000347'
	`)
	if err == nil {
		t.Fatalf("set public_auth_mode='unknown_mode_xyz': expected SQLSTATE 23514, got nil")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error is not *pgconn.PgError: %v (typed assertion failure)", err)
	}
	if pgErr.Code != "23514" {
		t.Errorf("SQLSTATE: got %q, want %q", pgErr.Code, "23514")
	}
	if pgErr.ConstraintName != "apps_public_auth_mode_chk" {
		t.Errorf("constraint name: got %q, want %q", pgErr.ConstraintName, "apps_public_auth_mode_chk")
	}

	// (6) EnumStillAcceptsExistingModes: regression — open,
	// bearer, basic, ip_allowlist, internal_only must still
	// pass after the widening (the Down-narrow only removes
	// members_only, not the pre-existing modes).
	for _, mode := range []string{"open", "bearer", "basic", "ip_allowlist", "internal_only"} {
		if _, err := pool.Exec(ctx, `
			update apps set public_auth_mode = $1
			 where id = '00000000-0000-0000-0000-000000000347'
		`, mode); err != nil {
			t.Errorf("set public_auth_mode=%q: %v (regression: pre-existing mode should still be accepted)", mode, err)
		}
	}

	// (7) DownGrade_NarrowsBackToPreADR120: set the row back to
	// members_only so the Down attempt below exercises the
	// "row present + narrower CHECK" failure mode.
	if _, err := pool.Exec(ctx, `
		update apps set public_auth_mode = 'members_only'
		 where id = '00000000-0000-0000-0000-000000000347'
	`); err != nil {
		t.Fatalf("re-set members_only for Down test: %v", err)
	}

	// Attempting the Down must surface SQLSTATE 23514 with
	// ConstraintName `apps_public_auth_mode_chk` because the
	// narrower CHECK rejects the row's mode='members_only'.
	// Pin this contract: the Down section does NOT silently
	// destroy customer rows; the operator must clear them
	// before running the Down.
	//
	// The migration test package does not expose a
	// `MigrateDown` helper (the package only ships Up
	// because Down is operator-driven in this repo — see
	// ADR-041 carve-out). We invoke the Down SQL shape
	// directly via pool.Exec to validate the SQLSTATE
	// contract. The fenced shape is the same SQL the
	// migration embeds verbatim (-- +goose Down section).
	_, downErr := pool.Exec(ctx, `
		ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_public_auth_mode_chk;
		ALTER TABLE apps ADD CONSTRAINT apps_public_auth_mode_chk
		  CHECK (public_auth_mode IN ('open','bearer','basic','ip_allowlist','internal_only'));
	`)
	if downErr == nil {
		t.Fatalf("Down with row in members_only: expected SQLSTATE 23514, got nil (the narrower CHECK silently destroyed a customer row?)")
	}
	var pgErrDown *pgconn.PgError
	if !errors.As(downErr, &pgErrDown) {
		t.Fatalf("Down error is not *pgconn.PgError: %v (typed assertion failure)", downErr)
	}
	if pgErrDown.Code != "23514" {
		t.Errorf("Down SQLSTATE: got %q, want %q", pgErrDown.Code, "23514")
	}
	if pgErrDown.ConstraintName != "apps_public_auth_mode_chk" {
		t.Errorf("Down constraint name: got %q, want %q", pgErrDown.ConstraintName, "apps_public_auth_mode_chk")
	}

	// Operator clears the row (simulating the documented
	// pre-Down procedure), then the Down succeeds.
	if _, err := pool.Exec(ctx, `
		update apps set public_auth_mode = 'open'
		 where id = '00000000-0000-0000-0000-000000000347'
	`); err != nil {
		t.Fatalf("operator-clear: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_public_auth_mode_chk;
		ALTER TABLE apps ADD CONSTRAINT apps_public_auth_mode_chk
		  CHECK (public_auth_mode IN ('open','bearer','basic','ip_allowlist','internal_only'));
	`); err != nil {
		t.Fatalf("Down after operator-clear: %v", err)
	}

	// (8) After Down, the CHECK must reject members_only.
	// Re-attempt to set the row to members_only (it should
	// fail because the CHECK has been narrowed).
	if _, err := pool.Exec(ctx, `
		update apps set public_auth_mode = 'members_only'
		 where id = '00000000-0000-0000-0000-000000000347'
	`); err == nil {
		t.Fatalf("set members_only after Down: expected SQLSTATE 23514 (CHECK should have been narrowed), got nil")
	}
}
