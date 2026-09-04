//go:build !no_pg

// is_org_member_test.go — pin the IsOrgMember helper
// (pkg/authz/is_org_member.go) end-to-end against the
// pgxpool-backed migration schema (migration 00099 ships the
// org_memberships table). The tests run under the
// `pkg/authz/` package so they can drive the unexported
// `IsOrgMember` helper directly. A separate pkg/gateway suite
// (pkg/gateway/public_auth_members_only_test.go) covers the
// gate-side wiring against a stub OrgMemberChecker — this
// file is the IsOrgMember semantics layer.
//
// Build tag matches the pgstore_*_test.go family (`!no_pg`);
// set FAAS_SKIP_PG_TESTS=1 to skip locally (see
// migrations/README.md). The companion
// pkg/gateway/public_auth_members_only_test.go suite runs
// under the !no_pg tag too because it spins up the gateway
// handler fixtures via pgtest.Open — these two suites share
// the same Postgres state lifecycle.
package authz

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// testIDs is the canonical UUID family used across these
// tests so the SQL fixtures don't collide with adjacent
// suites when the same DB pool is shared. Each test pins
// a distinct sub-range via the (orgID, accountID) pair so
// fixture teardown can truncate without touching
// sibling-suite rows.
const (
	testOrgA  = "00000000-0000-0000-0000-00000000a101"
	testOrgB  = "00000000-0000-0000-0000-00000000a102"
	testAcctX = "00000000-0000-0000-0000-00000000a201"
	testAcctY = "00000000-0000-0000-0000-00000000a202"
	testAcctZ = "00000000-0000-0000-0000-00000000a203"
)

// seedAccount inserts an account row by id and plan.
// org_memberships has an FK on accounts(id) so the test
// fixtures for the org-membership PK must seed both tables.
func seedAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, plan string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO accounts (id, email, plan, created_at) VALUES ($1, $2, $3, now()) ON CONFLICT (id) DO NOTHING`,
		id, id+"@test.local", plan,
	); err != nil {
		t.Fatalf("seedAccount(%s): %v", id, err)
	}
}

// TestIsOrgMember_ActiveMember pins the happy path: a
// freshly-created membership row returns (true, nil).
// Mirrors the (org_id, account_id, role, removed_at) shape
// migration 00099 ships; the inserted row matches the PK
// exactly so the PK lookup returns the row in one btree
// probe.
func TestIsOrgMember_ActiveMember(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	seedAccount(t, ctx, pool, testAcctX, "hobby")
	if _, err := pool.Exec(ctx,
		`INSERT INTO orgs (id, slug, created_at) VALUES ($1, $2, now()) ON CONFLICT (id) DO NOTHING`,
		testOrgA, "org-a-active",
	); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO org_memberships (org_id, account_id, role, joined_at) VALUES ($1, $2, 'admin', now()) ON CONFLICT DO NOTHING`,
		testOrgA, testAcctX,
	); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	ok, err := IsOrgMember(ctx, pool, testOrgA, testAcctX)
	if err != nil {
		t.Fatalf("IsOrgMember active: err = %v; want nil", err)
	}
	if !ok {
		t.Fatalf("IsOrgMember active: ok = false; want true (org_a + acct_x with active membership row)")
	}
}

// TestIsOrgMember_RemovedMember pins the removed-membership
// path: a row with removed_at IS NOT NULL must return
// (false, nil) — NOT ErrMembershipLookup (the row exists in
// the DB; the lookup succeeded). The audit code at the gate
// layer surfaces 'reason=removed_member' for this case
// (distinct from 'not_member' for no-row) so an operator
// can diagnose whether an account was kicked vs never
// joined.
func TestIsOrgMember_RemovedMember(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	seedAccount(t, ctx, pool, testAcctY, "hobby")
	if _, err := pool.Exec(ctx,
		`INSERT INTO orgs (id, slug, created_at) VALUES ($1, $2, now()) ON CONFLICT (id) DO NOTHING`,
		testOrgA, "org-a-removed",
	); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO org_memberships (org_id, account_id, role, joined_at, removed_at) VALUES ($1, $2, 'admin', now() - interval '1 day', now()) ON CONFLICT DO NOTHING`,
		testOrgA, testAcctY,
	); err != nil {
		t.Fatalf("seed removed membership: %v", err)
	}
	ok, err := IsOrgMember(ctx, pool, testOrgA, testAcctY)
	if err != nil {
		t.Fatalf("IsOrgMember removed: err = %v; want nil", err)
	}
	if ok {
		t.Fatalf("IsOrgMember removed: ok = true; want false (removed_at IS NOT NULL row)")
	}
}

// TestIsOrgMember_NoRow pins the no-membership path: an
// account that has never been added to the org returns
// (false, nil). The SELECT EXISTS returns false (not an
// error) for a missing PK combo. This is the most-common
// production path — a Hobby customer's members_only app
// receiving a request from a non-org account.
func TestIsOrgMember_NoRow(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	seedAccount(t, ctx, pool, testAcctZ, "hobby")
	if _, err := pool.Exec(ctx,
		`INSERT INTO orgs (id, slug, created_at) VALUES ($1, $2, now()) ON CONFLICT (id) DO NOTHING`,
		testOrgB, "org-b-norow",
	); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	// Note: NO insert into org_memberships for (org_b, acct_z).
	ok, err := IsOrgMember(ctx, pool, testOrgB, testAcctZ)
	if err != nil {
		t.Fatalf("IsOrgMember no-row: err = %v; want nil", err)
	}
	if ok {
		t.Fatalf("IsOrgMember no-row: ok = true; want false (no membership row)")
	}
}

// TestIsOrgMember_DBError pins the fail-closed posture:
// when the SQL itself errors (we simulate by passing a
// closed pool), IsOrgMember must return (false,
// ErrMembershipLookup). The gate at
// pkg/gateway/handler.go::applyIngressMembersOnly uses
// errors.Is(err, ErrMembershipLookup) to pivot on
// 'reason=lookup_error' audit code + a controlled 401/403
// rather than a 500 Go-panic.
//
// We force the error by closing the pool BEFORE the call —
// pgx surfaces a clear "closed pool" error on the QueryRow
// call which the helper wraps with ErrMembershipLookup.
// This is the cleanest reproducer for the gate's
// `lookup_error` audit emission without bringing in a
// connection-killer (FAAS_PG_DOWN) fixture.
func TestIsOrgMember_DBError(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	pool.Close() // Force the next query to fail.
	ok, err := IsOrgMember(ctx, pool, testOrgA, testAcctX)
	if err == nil {
		t.Fatalf("IsOrgMember closed-pool: err = nil; want ErrMembershipLookup")
	}
	if !errors.Is(err, ErrMembershipLookup) {
		t.Fatalf("IsOrgMember closed-pool: err = %v; want errors.Is(ErrMembershipLookup)", err)
	}
	if ok {
		t.Fatalf("IsOrgMember closed-pool: ok = true; want false (fail-closed)")
	}
	// Cross-check the IsMembership helper — same chain so a
	// caller at the gate layer can pivot via the same
	// predicate. (Avoids the load-org v1.0 drift the
	// substring-match bug ADR-119 round-2 fixed.)
	if !IsMembership(err) {
		t.Fatalf("IsMembership: returned false; want true for ErrMembershipLookup chain")
	}
	// Sanity: a pgx error trace (the "closed pool" detail)
	// must be present in the wrapped error body so the
	// operator's log pipeline retains the diagnostic. The
	// audit payload does NOT carry the wrapped message (the
	// helper passes only `reason="lookup_error"` to
	// pkg/audit) — the wrapped detail stays in the gate's
	// internal slog only.
	if msg := fmt.Sprint(err); msg == ErrMembershipLookup.Error() {
		t.Fatalf("IsOrgMember: wrapped err lost the pgx detail; got bare sentinel %q", msg)
	}
}

// TestIsOrgMember_EmptyInputs pins the defensive short-
// circuit: empty orgID or accountID returns (false, nil)
// without hitting the DB. This is NOT the fail-closed
// posture (a missing cookie is a 401 with
// 'reason=no_cookie', not a 'lookup_error' 401) — the gate
// layer is responsible for translating "no cookie" into
// the right reason code before ever calling IsOrgMember.
// An empty input here means a wiring bug, and the defensive
// nil/sentinel split (empty → (false, nil); DB-error →
// (false, ErrMembershipLookup)) lets the gate differentiate
// the two by `reason` mapping instead of thread-blocking
// bad inputs into a panic.
func TestIsOrgMember_EmptyInputs(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// Pool is healthy but never queried — calls must
	// short-circuit BEFORE any SQL fires. We assert via
	// (no-error, no-true) rather than via "pool is closed"
	// because pgtest's pool is healthy here.

	cases := []struct {
		name    string
		orgID   string
		acctID  string
		wantOk  bool
		wantErr bool
	}{
		{"empty_org", "", testAcctX, false, false},
		{"empty_account", testOrgA, "", false, false},
		{"both_empty", "", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := IsOrgMember(ctx, pool, tc.orgID, tc.acctID)
			if (err != nil) != tc.wantErr {
				t.Fatalf("IsOrgMember(%s): err = %v; wantErr = %v", tc.name, err, tc.wantErr)
			}
			if ok != tc.wantOk {
				t.Fatalf("IsOrgMember(%s): ok = %v; want %v", tc.name, ok, tc.wantOk)
			}
		})
	}
}
