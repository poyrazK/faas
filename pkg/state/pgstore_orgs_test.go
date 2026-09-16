//go:build !no_pg

// PgStore coverage tests for the org schema surface (issue #190 /
// ADR-061, PR 2). Sister file to memstore_orgs_test.go so each method
// has parity coverage on both backends. The migration shape is
// pinned by 00095_orgs_memberships_invitations_test.go — this file
// proves the PgStore adapter wires the right SQL to those constraints.
//
// Build tag matches migrations/*_test.go — set FAAS_SKIP_PG_TESTS=1 to
// skip locally (see migrations/README.md).

package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func pgStoreTimeNow() time.Time { return time.Now().UTC() }

func newPgStore(t *testing.T) *PgStore {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	return NewPgStore(pool)
}

func TestPgStore_OrgSurface_RoundTripsAndLastOwnerGuard(t *testing.T) {
	s := newPgStore(t)
	pool := s.pool
	ctx := context.Background()

	// Seed an account (the slug / FK on org_memberships.account_id does
	// not require a pre-existing account to insert into orgs, but
	// AddOrgMember does).
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-0000000000b1', 'pg-org-1@x.com', 'free', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-0000000000b2', 'pg-org-2@x.com', 'free', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account 2: %v", err)
	}

	o, err := s.CreateOrg(ctx, newTestOrg("pg-org"))
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	// Case-insensitive unique: orgs_slug_uniq uses lower(slug). The
	// slug regex forces lowercase, so two shape-valid slugs that
	// lower-case the same value can only be inserted via raw SQL.
	// Probe the unique contract directly to pin what the sqlc surface
	// relies on.
	if _, err := pool.Exec(ctx, `
		insert into orgs (slug, name) values ('pg-org', 'Other Org')
	`); err == nil {
		t.Errorf("orgs_slug_uniq: duplicate slug should be rejected")
	}

	got, err := s.OrgBySlug(ctx, "pg-org")
	if err != nil {
		t.Fatalf("OrgBySlug: %v", err)
	}
	if got.ID != o.ID {
		t.Errorf("slug round-trip id mismatch")
	}

	if err := s.AddOrgMember(ctx, o.ID, "00000000-0000-0000-0000-0000000000b1", OrgRoleOwner, nil); err != nil {
		t.Fatalf("add owner: %v", err)
	}
	if err := s.AddOrgMember(ctx, o.ID, "00000000-0000-0000-0000-0000000000b2", OrgRoleOwner, nil); !errors.Is(err, ErrOrgLastOwner) {
		t.Errorf("second owner: err = %v, want ErrOrgLastOwner", err)
	}
	if err := s.AddOrgMember(ctx, o.ID, "00000000-0000-0000-0000-0000000000b2", OrgRoleAdmin, nil); err != nil {
		t.Errorf("add admin: %v", err)
	}
	if err := s.RemoveOrgMember(ctx, o.ID, "00000000-0000-0000-0000-0000000000b1"); !errors.Is(err, ErrOrgLastOwner) {
		t.Errorf("remove sole owner: err = %v, want ErrOrgLastOwner", err)
	}

	got2, err := s.ListOrgMembers(ctx, o.ID)
	if err != nil {
		t.Fatalf("ListOrgMembers: %v", err)
	}
	if len(got2) != 2 {
		t.Errorf("members = %d, want 2", len(got2))
	}

	if err := s.SoftDeleteOrg(ctx, o.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	final, err := s.OrgByID(ctx, o.ID)
	if err != nil {
		t.Fatalf("OrgByID after delete: %v", err)
	}
	if !final.DeletedPending {
		t.Errorf("DeletedPending false after soft delete")
	}
}

func TestPgStore_ConsumeOrgInvitation_HappyPath(t *testing.T) {
	// Smoke test for the tx-heavy acceptance path. The full cap / email /
	// race matrix is exercised by the migration test (which pins SQL
	// invariants) and the memstore parity test (which pins store semantics);
	// this file proves the PgStore wires both to the same tx semantics.
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	s := NewPgStore(pool)
	ctx := context.Background()

	for i, id := range []string{
		"00000000-0000-0000-0000-0000000000c1",
		"00000000-0000-0000-0000-0000000000c2",
	} {
		email := []string{"c1@x.com", "c2@x.com"}[i]
		if _, err := pool.Exec(ctx, `
			insert into accounts (id, email, plan, created_at)
			values ($1::uuid, $2, 'free', now())
			on conflict (id) do nothing
		`, id, email); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	o, err := s.CreateOrg(ctx, Org{
		Slug: "consume-test", Name: "Consume Test",
		Plan: api.PlanHobby, Status: OrgStatusActive,
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := s.AddOrgMember(ctx, o.ID, "00000000-0000-0000-0000-0000000000c1", OrgRoleOwner, nil); err != nil {
		t.Fatalf("add owner: %v", err)
	}
	tokenHash := sha256.New().Sum([]byte("cap-token"))
	inv := OrgInvitation{
		OrgID:     o.ID,
		Email:     "c2@x.com",
		Role:      OrgRoleDeveloper,
		TokenHash: tokenHash[:],
		ExpiresAt: pgStoreTimeNow().Add(time.Hour),
	}
	if _, err := s.CreateOrgInvitation(ctx, inv); err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	acceptor := Account{
		ID:    "00000000-0000-0000-0000-0000000000c2",
		Email: "c2@x.com",
	}
	mem, returned, err := s.ConsumeOrgInvitation(ctx, tokenHash[:], acceptor)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if mem.Role != OrgRoleDeveloper {
		t.Errorf("consume membership role = %s, want developer", mem.Role)
	}
	if returned.ConsumedAt == nil {
		t.Errorf("returned invitation has nil ConsumedAt")
	}
}

func TestPgStore_ExpireOrgInvitations_Counts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	s := NewPgStore(pool)
	ctx := context.Background()

	// Seed an account + org.
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-0000000000d1', 'expire@x.com', 'free', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	o, err := s.CreateOrg(ctx, newTestOrg("expire-pg"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	pastHash := sha256.New().Sum([]byte("past-pg"))
	futureHash := sha256.New().Sum([]byte("future-pg"))
	if _, err := s.CreateOrgInvitation(ctx, OrgInvitation{
		OrgID: o.ID, Email: "p@x.com", Role: OrgRoleViewer,
		TokenHash: pastHash[:], ExpiresAt: pgStoreTimeNow().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed past: %v", err)
	}
	if _, err := s.CreateOrgInvitation(ctx, OrgInvitation{
		OrgID: o.ID, Email: "f@x.com", Role: OrgRoleViewer,
		TokenHash: futureHash[:], ExpiresAt: pgStoreTimeNow().Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed future: %v", err)
	}
	n, err := s.ExpireOrgInvitations(ctx, pgStoreTimeNow())
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 1 {
		t.Errorf("expired count = %d, want 1", n)
	}
}

// pgStoreSeedAccounts inserts N test accounts (deterministic UUIDs +
// emails) so the rest of the coverage suite can reuse them. Idempotent
// (ON CONFLICT DO NOTHING).
func pgStoreSeedAccounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := pool.Exec(ctx, `
			insert into accounts (id, email, plan, created_at)
			values ($1::uuid, $2, 'free', now())
			on conflict (id) do nothing
		`, id, "pg-"+id+"@x.com"); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
}

// TestPgStore_OrgLookups_ByPersonalAndSlug exercises OrgByPersonalAccount
// and OrgBySlug happy + ErrNotFound paths that the roundtrip test does
// not cover.
func TestPgStore_OrgLookups_ByPersonalAndSlug(t *testing.T) {
	s := newPgStore(t)
	pool := s.pool
	ctx := context.Background()
	pgStoreSeedAccounts(t, ctx, pool, "00000000-0000-0000-0000-0000000000d2")

	// Personal orgs use a slug derived from the account id short-form so
	// it fits the orgs_slug_shape check (3-32 chars). MemStore does not
	// enforce this constraint, but PgStore does.
	shortAcct := "d2"
	personalFixture := newTestPersonalOrg("00000000-0000-0000-0000-0000000000d2")
	personalFixture.Slug = "p-" + shortAcct
	personal, err := s.CreateOrg(ctx, personalFixture)
	if err != nil {
		t.Fatalf("create personal: %v", err)
	}

	got, err := s.OrgByPersonalAccount(ctx, "00000000-0000-0000-0000-0000000000d2")
	if err != nil {
		t.Fatalf("OrgByPersonalAccount: %v", err)
	}
	if got.ID != personal.ID {
		t.Errorf("OrgByPersonalAccount id = %s, want %s", got.ID, personal.ID)
	}

	if _, err := s.OrgByPersonalAccount(ctx, "00000000-0000-0000-0000-000000000099"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing personal account: err = %v, want ErrNotFound", err)
	}

	// ListOrgsForAccount must JOIN through active memberships; adding
	// the personal account as the owner membership surfaces the personal
	// org in the returned slice.
	if err := s.AddOrgMember(ctx, personal.ID, "00000000-0000-0000-0000-0000000000d2", OrgRoleOwner, nil); err != nil {
		t.Fatalf("add owner membership: %v", err)
	}
	list, err := s.ListOrgsForAccount(ctx, "00000000-0000-0000-0000-0000000000d2")
	if err != nil {
		t.Fatalf("ListOrgsForAccount: %v", err)
	}
	if len(list) != 1 || list[0].ID != personal.ID {
		t.Errorf("ListOrgsForAccount = %+v, want exactly [%s]", list, personal.ID)
	}

	// OrgBySlug miss path.
	if _, err := s.OrgBySlug(ctx, "no-such-slug-zzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing slug: err = %v, want ErrNotFound", err)
	}
}

// TestPgStore_OrgUpdates_PlanStatusDelete exercises UpdateOrgPlan,
// UpdateOrgStatus, and SoftDeleteOrg happy + ErrNotFound paths.
func TestPgStore_OrgUpdates_PlanStatusDelete(t *testing.T) {
	s := newPgStore(t)
	ctx := context.Background()

	o, err := s.CreateOrg(ctx, newTestOrg("update-pg"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	if err := s.UpdateOrgPlan(ctx, o.ID, api.PlanPro); err != nil {
		t.Fatalf("UpdateOrgPlan: %v", err)
	}
	got, err := s.OrgByID(ctx, o.ID)
	if err != nil {
		t.Fatalf("OrgByID: %v", err)
	}
	if got.Plan != api.PlanPro {
		t.Errorf("after UpdateOrgPlan plan = %s, want pro", got.Plan)
	}

	if err := s.UpdateOrgStatus(ctx, o.ID, OrgStatusPastDue); err != nil {
		t.Fatalf("UpdateOrgStatus: %v", err)
	}
	got, err = s.OrgByID(ctx, o.ID)
	if err != nil {
		t.Fatalf("OrgByID: %v", err)
	}
	if got.Status != OrgStatusPastDue {
		t.Errorf("after UpdateOrgStatus status = %s, want past_due", got.Status)
	}

	// ErrNotFound on bogus org id.
	if err := s.UpdateOrgPlan(ctx, "00000000-0000-0000-0000-000000000099", api.PlanHobby); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateOrgPlan bogus id: err = %v, want ErrNotFound", err)
	}
	if err := s.UpdateOrgStatus(ctx, "00000000-0000-0000-0000-000000000099", OrgStatusActive); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateOrgStatus bogus id: err = %v, want ErrNotFound", err)
	}

	if err := s.SoftDeleteOrg(ctx, o.ID); err != nil {
		t.Fatalf("SoftDeleteOrg: %v", err)
	}
	got, err = s.OrgByID(ctx, o.ID)
	if err != nil {
		t.Fatalf("OrgByID: %v", err)
	}
	if !got.DeletedPending {
		t.Errorf("after SoftDeleteOrg DeletedPending = false")
	}

	if err := s.SoftDeleteOrg(ctx, "00000000-0000-0000-0000-000000000099"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SoftDeleteOrg bogus id: err = %v, want ErrNotFound", err)
	}
}

// TestPgStore_MembershipOps_AllBranches exercises AddOrgMember (success +
// dup), UpdateOrgMemberRole (success + ErrNotFound), OrgMemberByAccount
// (success + ErrNotFound), and ListOrgMembers happy path.
func TestPgStore_MembershipOps_AllBranches(t *testing.T) {
	s := newPgStore(t)
	pool := s.pool
	ctx := context.Background()
	pgStoreSeedAccounts(t, ctx, pool,
		"00000000-0000-0000-0000-0000000000e1",
		"00000000-0000-0000-0000-0000000000e2",
	)

	o, err := s.CreateOrg(ctx, newTestOrg("memops-pg"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := s.AddOrgMember(ctx, o.ID, "00000000-0000-0000-0000-0000000000e1", OrgRoleOwner, nil); err != nil {
		t.Fatalf("add owner: %v", err)
	}

	// AddOrgMember dup path: re-adding same (org,account) → ErrConflict.
	if err := s.AddOrgMember(ctx, o.ID, "00000000-0000-0000-0000-0000000000e1", OrgRoleAdmin, nil); !errors.Is(err, ErrConflict) {
		t.Errorf("AddOrgMember dup: err = %v, want ErrConflict", err)
	}

	if err := s.AddOrgMember(ctx, o.ID, "00000000-0000-0000-0000-0000000000e2", OrgRoleDeveloper, nil); err != nil {
		t.Fatalf("add dev: %v", err)
	}

	// OrgMemberByAccount happy + miss.
	mem, err := s.OrgMemberByAccount(ctx, o.ID, "00000000-0000-0000-0000-0000000000e2")
	if err != nil {
		t.Fatalf("OrgMemberByAccount: %v", err)
	}
	if mem.Role != OrgRoleDeveloper {
		t.Errorf("role = %s, want developer", mem.Role)
	}
	if _, err := s.OrgMemberByAccount(ctx, o.ID, "00000000-0000-0000-0000-0000000000ff"); !errors.Is(err, ErrNotFound) {
		t.Errorf("OrgMemberByAccount miss: err = %v, want ErrNotFound", err)
	}

	// UpdateOrgMemberRole happy + ErrNotFound.
	if err := s.UpdateOrgMemberRole(ctx, o.ID, "00000000-0000-0000-0000-0000000000e2", OrgRoleAdmin); err != nil {
		t.Fatalf("UpdateOrgMemberRole: %v", err)
	}
	mem, err = s.OrgMemberByAccount(ctx, o.ID, "00000000-0000-0000-0000-0000000000e2")
	if err != nil {
		t.Fatalf("OrgMemberByAccount: %v", err)
	}
	if mem.Role != OrgRoleAdmin {
		t.Errorf("role after update = %s, want admin", mem.Role)
	}
	if err := s.UpdateOrgMemberRole(ctx, o.ID, "00000000-0000-0000-0000-0000000000ff", OrgRoleViewer); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateOrgMemberRole miss: err = %v, want ErrNotFound", err)
	}

	// ListOrgMembers happy path: 2 active members.
	listed, err := s.ListOrgMembers(ctx, o.ID)
	if err != nil {
		t.Fatalf("ListOrgMembers: %v", err)
	}
	if len(listed) != 2 {
		t.Errorf("ListOrgMembers len = %d, want 2", len(listed))
	}
}

// TestPgStore_InvitationOps_AllBranches exercises CreateOrgInvitation +
// OrgInvitationByTokenHash + RevokeOrgInvitation + ListOrgInvitationsForOrg.
func TestPgStore_InvitationOps_AllBranches(t *testing.T) {
	s := newPgStore(t)
	ctx := context.Background()

	o, err := s.CreateOrg(ctx, newTestOrg("inviteops-pg"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// OrgInvitationByTokenHash miss path (the roundtrip test covers hit).
	if _, err := s.OrgInvitationByTokenHash(ctx, []byte("never-stored")); !errors.Is(err, ErrNotFound) {
		t.Errorf("OrgInvitationByTokenHash miss: err = %v, want ErrNotFound", err)
	}

	hash := sha256.New().Sum([]byte("inviteops-token"))
	inv := OrgInvitation{
		OrgID:     o.ID,
		Email:     "inv@x.com",
		Role:      OrgRoleDeveloper,
		TokenHash: hash[:],
		ExpiresAt: pgStoreTimeNow().Add(time.Hour),
	}
	created, err := s.CreateOrgInvitation(ctx, inv)
	if err != nil {
		t.Fatalf("CreateOrgInvitation: %v", err)
	}
	inv = created

	got, err := s.OrgInvitationByTokenHash(ctx, hash[:])
	if err != nil {
		t.Fatalf("OrgInvitationByTokenHash hit: %v", err)
	}
	if got.Email != "inv@x.com" {
		t.Errorf("invitation email = %s, want inv@x.com", got.Email)
	}

	// ListOrgInvitationsForOrg: one pending.
	listed, err := s.ListOrgInvitationsForOrg(ctx, o.ID)
	if err != nil {
		t.Fatalf("ListOrgInvitationsForOrg: %v", err)
	}
	if len(listed) != 1 {
		t.Errorf("ListOrgInvitationsForOrg len = %d, want 1", len(listed))
	}

	// The row ID is not sufficient on its own: revocation is scoped to the
	// organization resolved by the authenticated route.
	if err := s.RevokeOrgInvitation(ctx, "00000000-0000-0000-0000-000000000097", inv.ID, "00000000-0000-0000-0000-000000000099"); !errors.Is(err, ErrOrgInvitationInvalid) {
		t.Fatalf("RevokeOrgInvitation wrong org: err = %v, want ErrOrgInvitationInvalid", err)
	}

	// RevokeOrgInvitation happy path.
	if err := s.RevokeOrgInvitation(ctx, o.ID, inv.ID, "00000000-0000-0000-0000-000000000099"); err != nil {
		t.Fatalf("RevokeOrgInvitation: %v", err)
	}
	got, err = s.OrgInvitationByTokenHash(ctx, hash[:])
	if err != nil {
		t.Fatalf("OrgInvitationByTokenHash post-revoke: %v", err)
	}
	if got.RevokedAt == nil {
		t.Errorf("after revoke: RevokedAt is nil")
	}

	// RevokeOrgInvitation miss path: an invitation ID that never existed is
	// indistinguishable from one already revoked — both report zero
	// rows updated, which the implementation surfaces as
	// ErrOrgInvitationInvalid (parity with ConsumeOrgInvitation).
	if err := s.RevokeOrgInvitation(ctx, o.ID, "00000000-0000-0000-0000-000000000098", "00000000-0000-0000-0000-000000000099"); !errors.Is(err, ErrOrgInvitationInvalid) {
		t.Errorf("RevokeOrgInvitation miss: err = %v, want ErrOrgInvitationInvalid", err)
	}
}

// TestPgStore_ConsumeOrgInvitation_ErrorPaths exercises the email
// mismatch + already-consumed branches that the happy-path test does
// not hit. These flow through pgx error mapping → store sentinel errors.
func TestPgStore_ConsumeOrgInvitation_ErrorPaths(t *testing.T) {
	s := newPgStore(t)
	pool := s.pool
	ctx := context.Background()
	pgStoreSeedAccounts(t, ctx, pool,
		"00000000-0000-0000-0000-0000000000f1",
		"00000000-0000-0000-0000-0000000000f2",
	)

	o, err := s.CreateOrg(ctx, newTestOrg("consume-err-pg"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := s.AddOrgMember(ctx, o.ID, "00000000-0000-0000-0000-0000000000f1", OrgRoleOwner, nil); err != nil {
		t.Fatalf("add owner: %v", err)
	}

	hash := sha256.New().Sum([]byte("consume-err-token"))
	if _, err := s.CreateOrgInvitation(ctx, OrgInvitation{
		OrgID:     o.ID,
		Email:     "expected@x.com",
		Role:      OrgRoleViewer,
		TokenHash: hash[:],
		ExpiresAt: pgStoreTimeNow().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create invitation: %v", err)
	}

	// Email mismatch → ErrOrgInvitationInvalid.
	mismatchAcct := Account{ID: "00000000-0000-0000-0000-0000000000f2", Email: "different@x.com"}
	if _, _, err := s.ConsumeOrgInvitation(ctx, hash[:], mismatchAcct); !errors.Is(err, ErrOrgInvitationInvalid) {
		t.Errorf("email mismatch: err = %v, want ErrOrgInvitationInvalid", err)
	}

	// Consume once successfully to flip consumed_at, then re-consume → ErrOrgInvitationInvalid.
	correctAcct := Account{ID: "00000000-0000-0000-0000-0000000000f2", Email: "expected@x.com"}
	if _, _, err := s.ConsumeOrgInvitation(ctx, hash[:], correctAcct); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if _, _, err := s.ConsumeOrgInvitation(ctx, hash[:], correctAcct); !errors.Is(err, ErrOrgInvitationInvalid) {
		t.Errorf("re-consume: err = %v, want ErrOrgInvitationInvalid", err)
	}
}

// PR 3 — CreateAccountWithPersonalOrg (issue #190 / ADR-061).
// The PR 3 canonical account-creation entry point: account +
// personal org + owner membership in a single tx.

func TestPgStore_CreateAccountWithPersonalOrg_Happy(t *testing.T) {
	s := newPgStore(t)
	ctx := context.Background()
	res, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "happy@x.com",
		Plan:  api.PlanFree,
	})
	if err != nil {
		t.Fatalf("CreateAccountWithPersonalOrg: %v", err)
	}
	if res.Account.Email != "happy@x.com" {
		t.Errorf("email = %q", res.Account.Email)
	}
	if res.Account.Plan != api.PlanFree {
		t.Errorf("plan = %s, want free", res.Account.Plan)
	}
	if !res.PersonalOrg.Personal {
		t.Errorf("personal = false, want true")
	}
	if res.PersonalOrg.PersonalOwnerAccountID == nil ||
		*res.PersonalOrg.PersonalOwnerAccountID != res.Account.ID {
		t.Errorf("personal_owner_account_id mismatch: got %+v want %s",
			res.PersonalOrg.PersonalOwnerAccountID, res.Account.ID)
	}
	if res.PersonalOrg.Plan != api.PlanFree {
		t.Errorf("personal org plan = %s, want free (mirrors account)", res.PersonalOrg.Plan)
	}
	if res.PersonalOrg.Status != OrgStatusActive {
		t.Errorf("personal org status = %s, want active", res.PersonalOrg.Status)
	}
	// Owner membership row exists.
	if _, err := s.OrgMemberByAccount(ctx, res.PersonalOrg.ID, res.Account.ID); err != nil {
		t.Errorf("OrgMemberByAccount: %v", err)
	}
}

func TestPgStore_CreateAccountWithPersonalOrg_DuplicateEmailReturnsErrConflict(t *testing.T) {
	s := newPgStore(t)
	ctx := context.Background()
	if _, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "dup@x.com",
		Plan:  api.PlanFree,
	}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "dup@x.com",
		Plan:  api.PlanFree,
	})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

func TestPgStore_CreateAccountWithPersonalOrg_SlugDeterministic(t *testing.T) {
	s := newPgStore(t)
	ctx := context.Background()
	res, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "slug@x.com",
		Plan:  api.PlanFree,
	})
	if err != nil {
		t.Fatalf("CreateAccountWithPersonalOrg: %v", err)
	}
	want := PersonalOrgSlug(res.Account.ID)
	if res.PersonalOrg.Slug != want {
		t.Errorf("slug = %q, want %q", res.PersonalOrg.Slug, want)
	}
}

// TestPgStore_TransferOrgOwnership_AllBranches exercises the new
// TxOrgOwnership method (issue #190 / ADR-061, PR 5). Sister parity
// to TestTransferOrgOwnership_MemStorePin in
// cmd/apid/handlers_org_test.go. The PgStore path differs from the
// MemStore in three load-bearing ways the test pins:
//
//   - Tx-wrapped (s.pool.BeginTx) with FOR UPDATE row locks on both
//     sides of the swap.
//   - Demote-first ordering so the partial unique
//     org_memberships_one_owner_idx trips on the promote step, not
//     the demote step (23505 → ErrOrgLastOwner).
//   - pgconn.PgError code mapping for the partial-unique tripwire.
//
// Five sub-cases, one per sentinel the handler distinguishes:
//
//   - success → both rows updated, previous owner demoted to admin
//   - self-transfer (from == to) → ErrOrgLastOwner
//   - from is not the active owner → ErrOrgLastOwner
//   - to is not an active member → ErrNotFound
//   - to is removed → ErrNotFound
func TestPgStore_TransferOrgOwnership_AllBranches(t *testing.T) {
	s := newPgStore(t)
	pool := s.pool
	ctx := context.Background()

	ownerID := "00000000-0000-0000-0000-0000000000a1"
	memberID := "00000000-0000-0000-0000-0000000000a2"
	strangerID := "00000000-0000-0000-0000-0000000000a3"
	removedID := "00000000-0000-0000-0000-0000000000a4"
	pgStoreSeedAccounts(t, ctx, pool, ownerID, memberID, strangerID, removedID)

	o, err := s.CreateOrg(ctx, newTestOrg("transfer-pg"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := s.AddOrgMember(ctx, o.ID, ownerID, OrgRoleOwner, nil); err != nil {
		t.Fatalf("add owner: %v", err)
	}
	if err := s.AddOrgMember(ctx, o.ID, memberID, OrgRoleDeveloper, nil); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := s.AddOrgMember(ctx, o.ID, removedID, OrgRoleViewer, nil); err != nil {
		t.Fatalf("add removed: %v", err)
	}
	if err := s.RemoveOrgMember(ctx, o.ID, removedID); err != nil {
		t.Fatalf("remove removed: %v", err)
	}

	// Case 1: success. owner → admin, member → owner.
	if err := s.TransferOrgOwnership(ctx, o.ID, ownerID, memberID); err != nil {
		t.Fatalf("TransferOrgOwnership: %v", err)
	}
	row, err := s.OrgMemberByAccount(ctx, o.ID, memberID)
	if err != nil {
		t.Fatalf("OrgMemberByAccount(new owner): %v", err)
	}
	if row.Role != OrgRoleOwner {
		t.Errorf("new owner role = %q, want owner", row.Role)
	}
	row, err = s.OrgMemberByAccount(ctx, o.ID, ownerID)
	if err != nil {
		t.Fatalf("OrgMemberByAccount(prev owner): %v", err)
	}
	if row.Role != OrgRoleAdmin {
		t.Errorf("prev owner role = %q, want admin (demoted)", row.Role)
	}

	// Restore: hand ownership back so subsequent cases have the
	// canonical starting state (owner + developer member). The
	// restore promotes ownerID back to owner but the success-path
	// demote rule left memberID as admin; reset memberID to
	// developer explicitly so the post-failure sanity check
	// below matches the seeded state.
	if err := s.TransferOrgOwnership(ctx, o.ID, memberID, ownerID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := s.UpdateOrgMemberRole(ctx, o.ID, memberID, OrgRoleDeveloper); err != nil {
		t.Fatalf("reset member role: %v", err)
	}

	// Case 2: self-transfer (from == to). The handler front-loads
	// the check, but the Store is also defensive — a no-op would
	// silently skip the swap and the wire contract is to refuse.
	if err := s.TransferOrgOwnership(ctx, o.ID, ownerID, ownerID); !errors.Is(err, ErrOrgLastOwner) {
		t.Errorf("self-transfer: err = %v, want ErrOrgLastOwner", err)
	}

	// Case 3: from is not the active owner. Member is not the
	// owner right now (we just restored in Case 1), so a
	// transfer from member is the "caller is not the owner" path.
	if err := s.TransferOrgOwnership(ctx, o.ID, memberID, ownerID); !errors.Is(err, ErrOrgLastOwner) {
		t.Errorf("non-owner caller: err = %v, want ErrOrgLastOwner", err)
	}

	// Case 4: to is not an active member of the org. strangerID
	// was seeded but never added as a member — the FOR UPDATE
	// probe on toAccountID returns pgx.ErrNoRows → ErrNotFound.
	if err := s.TransferOrgOwnership(ctx, o.ID, ownerID, strangerID); !errors.Is(err, ErrNotFound) {
		t.Errorf("non-member target: err = %v, want ErrNotFound", err)
	}

	// Case 5: to is a removed member. removedID was added then
	// RemoveOrgMember'd; the row exists with removed_at != nil so
	// the Store must return ErrNotFound (a removed invitee cannot
	// become owner).
	if err := s.TransferOrgOwnership(ctx, o.ID, ownerID, removedID); !errors.Is(err, ErrNotFound) {
		t.Errorf("removed target: err = %v, want ErrNotFound", err)
	}

	// Sanity: owner is still owner, member is still member. The
	// failed cases must not have written anything.
	row, err = s.OrgMemberByAccount(ctx, o.ID, ownerID)
	if err != nil {
		t.Fatalf("sanity OrgMemberByAccount: %v", err)
	}
	if row.Role != OrgRoleOwner {
		t.Errorf("post-failure owner role = %q, want owner", row.Role)
	}
	row, err = s.OrgMemberByAccount(ctx, o.ID, memberID)
	if err != nil {
		t.Fatalf("sanity OrgMemberByAccount member: %v", err)
	}
	if row.Role != OrgRoleDeveloper {
		t.Errorf("post-failure member role = %q, want developer", row.Role)
	}
}

// TestPgStore_ConsumeOrgInvitation_MemberCap pins the IAM-6 / ADR-061
// PR-2 cap check on the load-bearing insert path. Hobby's
// OrgMembersMax == 10; an invitation accept that would push active
// members past 10 must refuse with ErrOrgMemberCapExceeded inside
// the same tx as the membership insert. Personal orgs never reach
// the invitation-accept path.
func TestPgStore_ConsumeOrgInvitation_MemberCap(t *testing.T) {
	s := newPgStore(t)
	pool := s.pool
	ctx := context.Background()

	o, err := s.CreateOrg(ctx, newTestOrg("consume-cap-pg"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := pool.Exec(ctx, `update orgs set plan = 'hobby' where id = $1`, o.ID); err != nil {
		t.Fatalf("update plan: %v", err)
	}
	o.Plan = api.PlanHobby

	// Seed owner + 9 developers (active = 10) via direct AddOrgMember
	// (which has no cap check; this is the internal owner-seed path).
	ownerID := "00000000-0000-0000-0000-000000ce0001"
	devIDs := make([]string, 9)
	for i := range devIDs {
		devIDs[i] = fmt.Sprintf("00000000-0000-0000-0000-000000ce%04x", 0x0002+i)
	}
	pgStoreSeedAccounts(t, ctx, pool, append([]string{ownerID}, devIDs...)...)
	if err := s.AddOrgMember(ctx, o.ID, ownerID, OrgRoleOwner, nil); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	for _, id := range devIDs {
		if err := s.AddOrgMember(ctx, o.ID, id, OrgRoleDeveloper, nil); err != nil {
			t.Fatalf("seed dev: %v", err)
		}
	}

	// Mint an invitation whose accept would make active = 11. The
	// ConsumeOrgInvitation call must refuse with
	// ErrOrgMemberCapExceeded inside the same tx — no partial-state
	// leak.
	overAcct := Account{
		ID:    "00000000-0000-0000-0000-000000ce0010",
		Email: "over-ce@x.com",
	}
	pgStoreSeedAccounts(t, ctx, pool, overAcct.ID)
	plaintext := make([]byte, 32)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	hash := sha256.Sum256(plaintext)
	if _, err := s.CreateOrgInvitation(ctx, OrgInvitation{
		OrgID:     o.ID,
		Email:     overAcct.Email,
		Role:      OrgRoleDeveloper,
		TokenHash: hash[:],
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateOrgInvitation: %v", err)
	}
	if _, _, err := s.ConsumeOrgInvitation(ctx, hash[:], overAcct); !errors.Is(err, ErrOrgMemberCapExceeded) {
		t.Fatalf("over-cap ConsumeOrgInvitation: err = %v, want ErrOrgMemberCapExceeded", err)
	}
	if _, err := s.OrgMemberByAccount(ctx, o.ID, overAcct.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("OrgMemberByAccount over-cap: err = %v, want ErrNotFound", err)
	}
}
