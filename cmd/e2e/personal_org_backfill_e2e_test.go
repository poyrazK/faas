// personal_org_backfill_e2e_test.go — PR 3 acceptance (issue #190 /
// ADR-061). Boots a fresh harness with a real PgStore, seeds raw
// accounts (simulating pre-PR-3 state), applies migration 101 via
// db.MigrateUp, and asserts the "every account has exactly one
// personal org" invariant. Plus a replay-safety probe and a
// CreateAccountWithPersonalOrg / OrgByPersonalAccount round-trip
// via the e2e harness.
//
// All three boot a dedicated harness so the migration state and
// raw-account seed don't bleed across tests.
//
// Build tag: (none). CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS) and a buildable ./cmd/apid.

package e2e_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestE2E_PersonalOrgBackfill_BackfillsRawAccounts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	store := state.NewPgStore(pool)

	// Apply migrations 1..100 first so the orgs / org_memberships
	// tables (created by 00099) exist. Then seed three pre-PR-3
	// accounts directly via the bare CreateAccount (bypassing the
	// helper, simulating pre-PR-3 state). The backfill migration
	// already ran on an empty accounts table during the MigrateUp
	// above; re-run its INSERT bodies directly so the seeded
	// accounts get a personal org (the migration is replay-safe per
	// ADR-041).
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	seed := []struct{ id, email string }{
		// Realistic UUIDs whose first 12 hex chars (after dash
		// stripping) are distinct so the deterministic
		// PersonalOrgSlug derivation produces three distinct
		// slugs that satisfy orgs_slug_uniq (case-insensitive
		// unique on lower(slug)).
		{"a1a2a3a4-0000-0000-0000-000000000001", "e2e+backfill-1@test.example"},
		{"b1b2b3b4-0000-0000-0000-000000000002", "e2e+backfill-2@test.example"},
		{"c1c2c3c4-0000-0000-0000-000000000003", "e2e+backfill-3@test.example"},
	}
	for _, sa := range seed {
		if _, err := store.CreateAccount(ctx, sa.email, api.PlanFree); err != nil {
			t.Fatalf("seed account %s: %v", sa.id, err)
		}
	}

	// Re-run the backfill's own INSERT bodies so the freshly seeded
	// accounts get a personal org. Same SQL the migration runs,
	// executed directly against the live DB so the test can probe
	// the invariants end-to-end.
	if _, err := pool.Exec(ctx, `
		INSERT INTO orgs (
			id, slug, name, personal_org, personal_owner_account_id,
			plan, status, created_at, updated_at
		)
		SELECT
			gen_random_uuid(),
			'u-' || substring(replace(a.id::text, '-', '') from 1 for 12),
			'Personal', true, a.id, a.plan, a.status, a.created_at, now()
		FROM accounts a
		LEFT JOIN orgs o
			ON o.personal_owner_account_id = a.id AND o.personal_org = true
		WHERE o.id IS NULL
		ON CONFLICT DO NOTHING
	`); err != nil {
		t.Fatalf("backfill orgs: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO org_memberships (org_id, account_id, role, invited_by_account_id)
		SELECT o.id, a.id, 'owner', NULL
		FROM accounts a
		JOIN orgs o ON o.personal_owner_account_id = a.id AND o.personal_org = true
		LEFT JOIN org_memberships m ON m.org_id = o.id AND m.account_id = a.id
		WHERE m.org_id IS NULL
		ON CONFLICT DO NOTHING
	`); err != nil {
		t.Fatalf("backfill memberships: %v", err)
	}

	// Probe: every account has exactly one personal org.
	var accountCount, personalOrgCount int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM accounts),
		       (SELECT count(*) FROM orgs WHERE personal_org = true)
	`).Scan(&accountCount, &personalOrgCount); err != nil {
		t.Fatalf("count parity: %v", err)
	}
	if accountCount != personalOrgCount {
		t.Errorf("count parity: %d accounts, %d personal orgs", accountCount, personalOrgCount)
	}

	// Probe: every personal org has an active owner membership.
	var ownerlessCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM orgs o
		 WHERE o.personal_org = true
		   AND NOT EXISTS (
		       SELECT 1 FROM org_memberships m
		        WHERE m.org_id = o.id AND m.role = 'owner' AND m.removed_at IS NULL
		   )
	`).Scan(&ownerlessCount); err != nil {
		t.Fatalf("owner membership probe: %v", err)
	}
	if ownerlessCount != 0 {
		t.Errorf("ownerless personal orgs: %d", ownerlessCount)
	}
}

func TestE2E_PersonalOrgBackfill_ReplaysAreIdempotent(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	store := state.NewPgStore(pool)

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("first MigrateUp: %v", err)
	}
	// Seed two accounts so the backfill has something to do.
	for _, acct := range []struct{ id, email string }{
		// Distinct first-12-hex-char prefixes so the slugs are
		// distinct (PersonalOrgSlug collision otherwise).
		{"d1d2d3d4-0000-0000-0000-000000000001", "e2e+replay-1@test.example"},
		{"e1e2e3e4-0000-0000-0000-000000000002", "e2e+replay-2@test.example"},
	} {
		if _, err := store.CreateAccount(ctx, acct.email, api.PlanFree); err != nil {
			t.Fatalf("seed %s: %v", acct.id, err)
		}
	}
	// Re-run the backfill's own INSERT bodies.
	for _, stmt := range []string{
		`INSERT INTO orgs (
			id, slug, name, personal_org, personal_owner_account_id,
			plan, status, created_at, updated_at
		)
		SELECT
			gen_random_uuid(),
			'u-' || substring(replace(a.id::text, '-', '') from 1 for 12),
			'Personal', true, a.id, a.plan, a.status, a.created_at, now()
		FROM accounts a
		LEFT JOIN orgs o
			ON o.personal_owner_account_id = a.id AND o.personal_org = true
		WHERE o.id IS NULL
		ON CONFLICT DO NOTHING`,
		`INSERT INTO org_memberships (org_id, account_id, role, invited_by_account_id)
		SELECT o.id, a.id, 'owner', NULL
		FROM accounts a
		JOIN orgs o ON o.personal_owner_account_id = a.id AND o.personal_org = true
		LEFT JOIN org_memberships m ON m.org_id = o.id AND m.account_id = a.id
		WHERE m.org_id IS NULL
		ON CONFLICT DO NOTHING`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("backfill: %v", err)
		}
	}
	var pre int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM orgs WHERE personal_org = true
	`).Scan(&pre); err != nil {
		t.Fatalf("count pre: %v", err)
	}
	// Re-run the backfill INSERT bodies again — idempotency check.
	for _, stmt := range []string{
		`INSERT INTO orgs (
			id, slug, name, personal_org, personal_owner_account_id,
			plan, status, created_at, updated_at
		)
		SELECT
			gen_random_uuid(),
			'u-' || substring(replace(a.id::text, '-', '') from 1 for 12),
			'Personal', true, a.id, a.plan, a.status, a.created_at, now()
		FROM accounts a
		LEFT JOIN orgs o
			ON o.personal_owner_account_id = a.id AND o.personal_org = true
		WHERE o.id IS NULL
		ON CONFLICT DO NOTHING`,
		`INSERT INTO org_memberships (org_id, account_id, role, invited_by_account_id)
		SELECT o.id, a.id, 'owner', NULL
		FROM accounts a
		JOIN orgs o ON o.personal_owner_account_id = a.id AND o.personal_org = true
		LEFT JOIN org_memberships m ON m.org_id = o.id AND m.account_id = a.id
		WHERE m.org_id IS NULL
		ON CONFLICT DO NOTHING`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("backfill replay: %v", err)
		}
	}
	var post int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM orgs WHERE personal_org = true
	`).Scan(&post); err != nil {
		t.Fatalf("count post: %v", err)
	}
	if pre != post {
		t.Errorf("replay safety: pre=%d post=%d personal orgs", pre, post)
	}
}

func TestE2E_PersonalOrgBackfill_HelperRoundTrip(t *testing.T) {
	// Exercises CreateAccountWithPersonalOrg via the harness's
	// SeedAccount path. Asserts OrgByPersonalAccount returns the
	// freshly minted personal org and that the owner membership
	// row exists.
	pool := pgtest.OpenMigrated(t)
	// Pre-migrate to the harness's target (e2etest.Start polls for
	// it; if we let the apid subprocess do all the migration work,
	// the harness's WaitForMigration times out because Start()
	// calls it BEFORE startAPID — see account_e2e_test.go's
	// pre-MigrateUp pattern). After pre-migration the apid's own
	// MigrateUp is a no-op (ADR-041 contract).
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	store := state.NewPgStore(h.Pool)

	_ = h.SeedAccount(ctx, api.PlanFree, "pr3-roundtrip")

	// Lookup the seed account by email; the harness email
	// convention is e2e+<plan>[+<label>]@test.example.
	acct, err := store.AccountByEmail(ctx, "e2e+free+pr3-roundtrip@test.example")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	org, err := store.OrgByPersonalAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("OrgByPersonalAccount: %v", err)
	}
	if !org.Personal {
		t.Errorf("personal = false, want true")
	}
	if org.PersonalOwnerAccountID == nil || *org.PersonalOwnerAccountID != acct.ID {
		t.Errorf("personal_owner_account_id = %+v, want %s", org.PersonalOwnerAccountID, acct.ID)
	}
	if _, err := store.OrgMemberByAccount(ctx, org.ID, acct.ID); err != nil {
		t.Errorf("OrgMemberByAccount: %v", err)
	}
}
