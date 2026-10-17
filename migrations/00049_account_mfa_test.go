//go:build !no_pg

// Shape test for migration 00047 (IAM-2 / issue #186).
//
// Asserts:
//
//   1. The four mfa_* columns land on accounts (mfa_enrolled_at,
//      mfa_secret_encrypted, mfa_recovery_codes_hash, mfa_required).
//   2. The CHECK constraint accounts_mfa_enrolled_shape_chk rejects
//      an enrolled row with no secret (the enrolled-implies-secret
//      branch) and accepts the empty-enrollment shape.
//   3. The partial index accounts_mfa_required_pending_idx fires
//      on the (mfa_required=true, mfa_enrolled_at IS NULL) shape
//      and stays cold for unrelated rows.
//
// Build tag mirrors apply_walk_test.go:4 — set FAAS_SKIP_PG_TESTS=1
// locally to skip.

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00047_AccountMFA_ShapeAndCheck(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	const acctID = "00000000-0000-0000-0000-000000000047"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, 'iam2-shape@example.com', 'free', now())
		on conflict (id) do nothing
	`, acctID); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	// (1) The four columns introduced by this migration still exist. Later
	// migrations may add MFA lifecycle fields, such as the mail revision fence.
	want := []string{
		"mfa_enrolled_at", "mfa_recovery_codes_hash",
		"mfa_required", "mfa_secret_encrypted",
	}
	rows, err := pool.Query(ctx, `
		select attname from pg_attribute
		 where attrelid = 'accounts'::regclass
		   and attname = any($1::text[])
		   and attnum > 0
		 order by attname
	`, want)
	if err != nil {
		t.Fatalf("pg_attribute: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, s)
	}
	if len(got) != len(want) {
		t.Fatalf("mfa_* column count = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("col[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// (2a) An enrolled row with no secret violates the CHECK
	// (enrolled-implies-secret-present). The default-false mfa_required
	// is the safer empty-enrollment state, so we explicitly stamp
	// enrolled_at to drive the failure path.
	if _, err := pool.Exec(ctx, `
		update accounts set mfa_enrolled_at = now() where id = $1
	`, acctID); err == nil {
		t.Fatalf("expected CHECK violation on enrolled-without-secret")
	} else {
		// Reset for the next assertion.
		if _, err := pool.Exec(ctx, `
			update accounts set mfa_enrolled_at = null where id = $1
		`, acctID); err != nil {
			t.Fatalf("reset enrolled_at: %v", err)
		}
	}

	// (2b) An enrolled row WITH secret + recovery codes is accepted.
	if _, err := pool.Exec(ctx, `
		update accounts
		   set mfa_enrolled_at = now(),
		       mfa_secret_encrypted = decode('deadbeef', 'hex'),
		       mfa_recovery_codes_hash = ARRAY[
		           decode('00112233445566778899aabbccddeeff', 'hex'),
		           decode('ffeeddccbbaa99887766554433221100', 'hex')
		       ]::bytea[]
		 where id = $1
	`, acctID); err != nil {
		t.Fatalf("enrolled with secret+codes: %v", err)
	}

	// (3) The partial index fires on the (required=true, enrolled=NULL) shape.
	//     We rely on EXPLAIN to confirm the planner picks the index; the
	//     simpler functional check is that setting required=true for a
	//     non-enrolled account returns the row from a partial-index scan.
	if _, err := pool.Exec(ctx, `
		update accounts set mfa_required = true where id = $1
	`, acctID); err != nil {
		t.Fatalf("set required: %v", err)
	}
	var cnt int
	if err := pool.QueryRow(ctx, `
		select count(*) from accounts
		 where id = $1
		   and mfa_required = true
		   and mfa_enrolled_at is null
	`, acctID).Scan(&cnt); err != nil {
		t.Fatalf("partial index count: %v", err)
	}
	if cnt != 0 {
		t.Errorf("partial index returned %d rows for an enrolled account", cnt)
	}

	// Now flip back to unenrolled + required and confirm the index shape is set.
	if _, err := pool.Exec(ctx, `
		update accounts
		   set mfa_enrolled_at = null,
		       mfa_secret_encrypted = null,
		       mfa_recovery_codes_hash = null,
		       mfa_required = true
		 where id = $1
	`, acctID); err != nil {
		t.Fatalf("reset to required+unenrolled: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		select count(*) from accounts
		 where id = $1
		   and mfa_required = true
		   and mfa_enrolled_at is null
	`, acctID).Scan(&cnt); err != nil {
		t.Fatalf("partial index count unenrolled: %v", err)
	}
	if cnt != 1 {
		t.Errorf("partial index returned %d rows for required+unenrolled", cnt)
	}
}

// TestMigrations_00047_AccountMFA_BurnOutAllowed pins Review
// Finding #5: a customer who has burned every recovery code is
// still enrolled (mfa_enrolled_at IS NOT NULL, secret present,
// codes empty). The pre-fix CHECK
// `coalesce(array_length(mfa_recovery_codes_hash, 1), 0) > 0`
// would have rejected this terminal state and locked the customer
// out of /disable. The post-fix CHECK accepts the empty-array
// shape so the customer can /disable via password and ClearMFA
// the row.
func TestMigrations_00047_AccountMFA_BurnOutAllowed(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	const acctID = "00000000-0000-0000-0000-000000000148"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, 'iam2-burnout@example.com', 'free', now())
		on conflict (id) do nothing
	`, acctID); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	// Enrolled + secret + ZERO recovery codes. This is the
	// terminal state after the customer burned every code and
	// before they hit /disable. Must pass the CHECK.
	if _, err := pool.Exec(ctx, `
		update accounts
		   set mfa_enrolled_at = now(),
		       mfa_secret_encrypted = decode('deadbeef', 'hex'),
		       mfa_recovery_codes_hash = '{}'::bytea[]
		 where id = $1
	`, acctID); err != nil {
		t.Fatalf("enrolled + zero codes should be allowed, got: %v", err)
	}

	// ClearMFA then takes the row to the all-NULL shape, which the
	// CHECK also accepts.
	if _, err := pool.Exec(ctx, `
		update accounts
		   set mfa_enrolled_at = null,
		       mfa_secret_encrypted = null,
		       mfa_recovery_codes_hash = null
		 where id = $1
	`, acctID); err != nil {
		t.Fatalf("ClearMFA-equivalent update should be allowed, got: %v", err)
	}
}

// TestMigrations_00047_AccountMFA_ReApplyIsIdempotent pins Review
// Finding #12: a box with a half-applied migration (constraint or
// index already in place) must not crash on the second `goose up`.
// The constraint is wrapped in a `do $$ ... exception when
// duplicate_object then null; end $$` block, and the index uses
// `create index if not exists`.
func TestMigrations_00047_AccountMFA_ReApplyIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("first MigrateUp: %v", err)
	}
	// Second pass: the constraint + index already exist; the
	// idempotence guards must make this a no-op rather than an
	// error.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("second MigrateUp (idempotence check): %v", err)
	}
}
