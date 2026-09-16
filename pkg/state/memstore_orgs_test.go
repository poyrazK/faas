package state

// MemStore coverage tests for the org schema surface (issue #190 /
// ADR-061, PR 2). Mirrors pkg/state/pgstore_orgs_test.go so each
// method on the new org cluster has at least one direct MemStore
// test, raising pkg/state coverage above the 70% gate. The two
// sister files MUST stay in sync; rotate coverage when one side
// gains a method.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// timeNow is a local seam that returns UTC clock. Used by invitation
// ExpiryAt values so the test fixtures don't drift when the host tz
// flips. Mirrors the same name in pgstore_orgs_test.go.
func timeNow() time.Time { return time.Now().UTC() }

// memstoreOrgTestAccountID is a deterministic non-personal account id
// used as the actor across the org surface. Mirrors the
// memstoreTrustedSignerKey fixture pattern.
const memstoreOrgTestAccountID = "00000000-0000-0000-0000-0000000000a1"

// memstoreOrgTestSecondAccountID is a sibling account for cross-account
// membership / FK-cascade assertions.
const memstoreOrgTestSecondAccountID = "00000000-0000-0000-0000-0000000000a2"

// newTestOrg returns a non-personal org fixture (no personal_owner).
func newTestOrg(slug string) Org {
	return Org{
		Slug:   slug,
		Name:   "Test Org " + slug,
		Plan:   api.PlanFree,
		Status: OrgStatusActive,
	}
}

// newTestPersonalOrg returns a personal org fixture bound to the
// supplied account.
func newTestPersonalOrg(accountID string) Org {
	id := accountID
	return Org{
		Slug:                   "personal-" + accountID,
		Name:                   "Personal",
		Personal:               true,
		PersonalOwnerAccountID: &id,
		Plan:                   api.PlanFree,
		Status:                 OrgStatusActive,
	}
}

func TestMemStore_CreateOrg_InsertAndSlugConflict(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	o1 := newTestOrg("acme-co")
	if _, err := m.CreateOrg(ctx, o1); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Same slug uppercase should collide (case-insensitive unique).
	o2 := newTestOrg("Acme-Co")
	if _, err := m.CreateOrg(ctx, o2); !errors.Is(err, ErrConflict) {
		t.Errorf("case-fold slug collision: err = %v, want ErrConflict", err)
	}
}

func TestMemStore_CreateOrg_PersonalOrgUniquePerAccount(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	o1 := newTestPersonalOrg(memstoreOrgTestAccountID)
	if _, err := m.CreateOrg(ctx, o1); err != nil {
		t.Fatalf("first personal org: %v", err)
	}

	o2 := newTestPersonalOrg(memstoreOrgTestAccountID)
	if _, err := m.CreateOrg(ctx, o2); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate personal org for same account: err = %v, want ErrConflict", err)
	}

	// A different account is allowed.
	o3 := newTestPersonalOrg(memstoreOrgTestSecondAccountID)
	if _, err := m.CreateOrg(ctx, o3); err != nil {
		t.Errorf("personal org for second account should succeed: %v", err)
	}
}

func TestMemStore_CreateOrg_PersonalRequiresOwnerPointer(t *testing.T) {
	// A Personal=true row must carry PersonalOwnerAccountID. The SQL
	// CHECK orgs_personal_owner_link rejects the insert, and the
	// MemStore path mirrors that with an ErrConflict short-circuit so it
	// does not dereference a nil pointer before surfacing the constraint.
	m := NewMemStore()
	ctx := context.Background()

	bad := Org{
		Slug:                   "personal-nil-owner",
		Name:                   "Personal No Owner",
		Personal:               true,
		PersonalOwnerAccountID: nil,
		Plan:                   api.PlanFree,
		Status:                 OrgStatusActive,
	}
	if _, err := m.CreateOrg(ctx, bad); !errors.Is(err, ErrConflict) {
		t.Errorf("Personal=true with nil owner pointer: err = %v, want ErrConflict (no panic)", err)
	}
}

func TestMemStore_OrgByID_OrgBySlug_OrgByPersonalAccount(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	shared := newTestOrg("shared-xyz")
	created, err := m.CreateOrg(ctx, shared)
	if err != nil {
		t.Fatalf("create shared: %v", err)
	}
	personal := newTestPersonalOrg(memstoreOrgTestAccountID)
	if _, err := m.CreateOrg(ctx, personal); err != nil {
		t.Fatalf("create personal: %v", err)
	}

	got, err := m.OrgByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("OrgByID: %v", err)
	}
	if got.Slug != "shared-xyz" {
		t.Errorf("OrgByID slug = %q, want shared-xyz", got.Slug)
	}

	got, err = m.OrgBySlug(ctx, "SHARED-XYZ") // case-insensitive
	if err != nil {
		t.Fatalf("OrgBySlug case-folder: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("OrgBySlug round-trip id = %s, want %s", got.ID, created.ID)
	}

	got, err = m.OrgByPersonalAccount(ctx, memstoreOrgTestAccountID)
	if err != nil {
		t.Fatalf("OrgByPersonalAccount: %v", err)
	}
	if !got.Personal {
		t.Errorf("OrgByPersonalAccount returned non-personal row")
	}

	// Missing cases.
	if _, err := m.OrgByID(ctx, "missing-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("OrgByID missing: err = %v, want ErrNotFound", err)
	}
	if _, err := m.OrgBySlug(ctx, "missing-slug"); !errors.Is(err, ErrNotFound) {
		t.Errorf("OrgBySlug missing: err = %v, want ErrNotFound", err)
	}
	if _, err := m.OrgByPersonalAccount(ctx, "no-personal-yet"); !errors.Is(err, ErrNotFound) {
		t.Errorf("OrgByPersonalAccount missing: err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_AddOrgMember_ExactlyOneOwner(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	org, err := m.CreateOrg(ctx, newTestOrg("one-owner"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	// Hobby plan (OrgMembersMax=10) so the second-add test isn't
	// blocked by Free's fail-closed 0/0 cap.
	if err := m.UpdateOrgPlan(ctx, org.ID, api.PlanHobby); err != nil {
		t.Fatalf("plan update: %v", err)
	}

	if err := m.AddOrgMember(ctx, org.ID, memstoreOrgTestAccountID, OrgRoleOwner, nil); err != nil {
		t.Fatalf("first owner: %v", err)
	}
	// Second owner must be rejected (partial unique surface).
	if err := m.AddOrgMember(ctx, org.ID, memstoreOrgTestSecondAccountID, OrgRoleOwner, nil); !errors.Is(err, ErrOrgLastOwner) {
		t.Errorf("second owner: err = %v, want ErrOrgLastOwner", err)
	}
	// Non-owner role is fine.
	if err := m.AddOrgMember(ctx, org.ID, memstoreOrgTestSecondAccountID, OrgRoleAdmin, nil); err != nil {
		t.Errorf("second member as admin: %v", err)
	}
	// Duplicate (org_id, account_id) is rejected as ErrConflict.
	if err := m.AddOrgMember(ctx, org.ID, memstoreOrgTestSecondAccountID, OrgRoleViewer, nil); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate membership: err = %v, want ErrConflict", err)
	}
	// Missing org → ErrNotFound.
	if err := m.AddOrgMember(ctx, "missing", "anything", OrgRoleViewer, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing org: err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_RemoveOrgMember_LastOwnerGuard(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	org, err := m.CreateOrg(ctx, newTestOrg("rm-test"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	// Hobby plan (OrgMembersMax=10) so the second-add seed isn't
	// blocked by Free's fail-closed 0/0 cap.
	if err := m.UpdateOrgPlan(ctx, org.ID, api.PlanHobby); err != nil {
		t.Fatalf("plan update: %v", err)
	}
	if err := m.AddOrgMember(ctx, org.ID, memstoreOrgTestAccountID, OrgRoleOwner, nil); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	if err := m.AddOrgMember(ctx, org.ID, memstoreOrgTestSecondAccountID, OrgRoleViewer, nil); err != nil {
		t.Fatalf("seed viewer: %v", err)
	}

	// Removing the only owner → ErrOrgLastOwner.
	if err := m.RemoveOrgMember(ctx, org.ID, memstoreOrgTestAccountID); !errors.Is(err, ErrOrgLastOwner) {
		t.Errorf("remove sole owner: err = %v, want ErrOrgLastOwner", err)
	}
	// Removing the viewer is fine and idempotent on rerun.
	if err := m.RemoveOrgMember(ctx, org.ID, memstoreOrgTestSecondAccountID); err != nil {
		t.Errorf("remove viewer: %v", err)
	}
	if err := m.RemoveOrgMember(ctx, org.ID, memstoreOrgTestSecondAccountID); err != nil {
		t.Errorf("remove viewer rerun: %v", err)
	}
	// Missing member → ErrNotFound.
	if err := m.RemoveOrgMember(ctx, org.ID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("remove missing: err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_UpdateOrgMemberRole_LastOwnerGuard(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	org, err := m.CreateOrg(ctx, newTestOrg("role-test"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	// Hobby plan (OrgMembersMax=10) so the second-add seed isn't
	// blocked by Free's fail-closed 0/0 cap.
	if err := m.UpdateOrgPlan(ctx, org.ID, api.PlanHobby); err != nil {
		t.Fatalf("plan update: %v", err)
	}
	if err := m.AddOrgMember(ctx, org.ID, memstoreOrgTestAccountID, OrgRoleOwner, nil); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	if err := m.AddOrgMember(ctx, org.ID, memstoreOrgTestSecondAccountID, OrgRoleAdmin, nil); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	// Demoting the only owner → ErrOrgLastOwner.
	if err := m.UpdateOrgMemberRole(ctx, org.ID, memstoreOrgTestAccountID, OrgRoleAdmin); !errors.Is(err, ErrOrgLastOwner) {
		t.Errorf("demote sole owner: err = %v, want ErrOrgLastOwner", err)
	}
	// Promote admin to developer is fine.
	if err := m.UpdateOrgMemberRole(ctx, org.ID, memstoreOrgTestSecondAccountID, OrgRoleDeveloper); err != nil {
		t.Errorf("promote admin → developer: %v", err)
	}
	// Missing → ErrNotFound.
	if err := m.UpdateOrgMemberRole(ctx, org.ID, "missing", OrgRoleViewer); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing: err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_ListOrgsForAccount_FiltersRemoved(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// Two orgs, second account belongs only to shared.
	shared, err := m.CreateOrg(ctx, newTestOrg("shared-list"))
	if err != nil {
		t.Fatalf("create shared: %v", err)
	}
	personal, err := m.CreateOrg(ctx, newTestPersonalOrg(memstoreOrgTestAccountID))
	if err != nil {
		t.Fatalf("create personal: %v", err)
	}
	if err := m.AddOrgMember(ctx, shared.ID, memstoreOrgTestAccountID, OrgRoleViewer, nil); err != nil {
		t.Fatalf("seed shared-membership: %v", err)
	}
	if err := m.AddOrgMember(ctx, personal.ID, memstoreOrgTestAccountID, OrgRoleOwner, nil); err != nil {
		t.Fatalf("seed personal-membership: %v", err)
	}

	got, err := m.ListOrgsForAccount(ctx, memstoreOrgTestAccountID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("list len = %d, want 2 (both orgs)", len(got))
	}
	// After removing from shared, only personal remains.
	if err := m.RemoveOrgMember(ctx, shared.ID, memstoreOrgTestAccountID); err != nil {
		t.Fatalf("remove viewer: %v", err)
	}
	got, err = m.ListOrgsForAccount(ctx, memstoreOrgTestAccountID)
	if err != nil {
		t.Fatalf("list after remove: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("list after remove len = %d, want 1", len(got))
	}
	if !got[0].Personal {
		t.Errorf("post-remove list should contain personal org only")
	}
}

func TestMemStore_CreateAndConsumeOrgInvitation_HappyAndInvalid(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	org, err := m.CreateOrg(ctx, newTestOrg("invite-test"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acceptor := Account{ID: memstoreOrgTestAccountID, Email: "accept@example.com"}

	tokenHash := sha256.New().Sum([]byte("invite-token-1"))
	inv := OrgInvitation{
		OrgID:     org.ID,
		Email:     "accept@example.com",
		Role:      OrgRoleDeveloper,
		TokenHash: tokenHash[:],
		ExpiresAt: timeNow().Add(24 * time.Hour),
	}
	created, err := m.CreateOrgInvitation(ctx, inv)
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	if created.ID == "" {
		t.Errorf("created invitation id is empty")
	}

	// Round-trip lookup.
	got, err := m.OrgInvitationByTokenHash(ctx, tokenHash[:])
	if err != nil {
		t.Fatalf("lookup by token: %v", err)
	}
	if got.OrgID != org.ID {
		t.Errorf("lookup round-trip org id = %s, want %s", got.OrgID, org.ID)
	}

	// Wrong-email acceptor → ErrOrgInvitationInvalid.
	mem, returned, err := m.ConsumeOrgInvitation(ctx, tokenHash[:], Account{ID: memstoreOrgTestSecondAccountID, Email: "other@example.com"})
	if !errors.Is(err, ErrOrgInvitationInvalid) {
		t.Errorf("wrong-email consume: err = %v, want ErrOrgInvitationInvalid", err)
	}
	if mem.OrgID != "" || mem.AccountID != "" {
		t.Errorf("consume error path should zero the membership: %+v", mem)
	}
	if returned.OrgID != "" || returned.ID != "" {
		t.Errorf("consume error path should zero the invitation: %+v", returned)
	}

	// Happy path: consume with matching email.
	mem, returned, err = m.ConsumeOrgInvitation(ctx, tokenHash[:], acceptor)
	if err != nil {
		t.Fatalf("happy consume: %v", err)
	}
	if mem.Role != OrgRoleDeveloper {
		t.Errorf("consume membership role = %s, want developer", mem.Role)
	}
	if returned.ConsumedAt == nil {
		t.Errorf("consumed invitation has nil consumed_at")
	}

	// Idempotency: second consume → ErrOrgInvitationInvalid.
	if _, _, err := m.ConsumeOrgInvitation(ctx, tokenHash[:], acceptor); !errors.Is(err, ErrOrgInvitationInvalid) {
		t.Errorf("re-consume: err = %v, want ErrOrgInvitationInvalid", err)
	}

	// Missing token.
	if _, err := m.OrgInvitationByTokenHash(ctx, []byte("nope")); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing lookup: err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_RevokeOrgInvitation(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	org, err := m.CreateOrg(ctx, newTestOrg("revoke-test"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	tokenHash := sha256.New().Sum([]byte("revoke-token"))
	inv := OrgInvitation{
		OrgID:     org.ID,
		Email:     "x@example.com",
		Role:      OrgRoleViewer,
		TokenHash: tokenHash[:],
		ExpiresAt: timeNow().Add(time.Hour),
	}
	created, err := m.CreateOrgInvitation(ctx, inv)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := m.RevokeOrgInvitation(ctx, "wrong-org", created.ID, "irrelevant"); !errors.Is(err, ErrOrgInvitationInvalid) {
		t.Fatalf("wrong-org revoke: err = %v, want ErrOrgInvitationInvalid", err)
	}
	if err := m.RevokeOrgInvitation(ctx, org.ID, created.ID, "irrelevant"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Revoking a revoked/expired invitation is ErrOrgInvitationInvalid
	// (matches the PgStore contract where the UPDATE matches 0 rows).
	if err := m.RevokeOrgInvitation(ctx, org.ID, created.ID, "irrelevant"); !errors.Is(err, ErrOrgInvitationInvalid) {
		t.Errorf("re-revoke: err = %v, want ErrOrgInvitationInvalid", err)
	}
	if err := m.RevokeOrgInvitation(ctx, org.ID, "never-existed", "x"); !errors.Is(err, ErrOrgInvitationInvalid) {
		t.Errorf("missing invitation revoke: err = %v, want ErrOrgInvitationInvalid", err)
	}
}

func TestMemStore_ExpireOrgInvitations(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	org, err := m.CreateOrg(ctx, newTestOrg("expire-test"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Past-expired + future.
	past := sha256.New().Sum([]byte("past-token"))
	future := sha256.New().Sum([]byte("future-token"))
	if _, err := m.CreateOrgInvitation(ctx, OrgInvitation{
		OrgID: org.ID, Email: "past@x.com", Role: OrgRoleViewer, TokenHash: past[:],
		ExpiresAt: timeNow().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed past: %v", err)
	}
	if _, err := m.CreateOrgInvitation(ctx, OrgInvitation{
		OrgID: org.ID, Email: "future@x.com", Role: OrgRoleViewer, TokenHash: future[:],
		ExpiresAt: timeNow().Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed future: %v", err)
	}

	now := timeNow()
	n, err := m.ExpireOrgInvitations(ctx, now)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 1 {
		t.Errorf("expired count = %d, want 1", n)
	}
	// The past one must now be revoked; the future one must still be
	// pending. Probe via token lookups.
	got, err := m.OrgInvitationByTokenHash(ctx, past[:])
	if err != nil {
		t.Fatalf("lookup past: %v", err)
	}
	if got.RevokedAt == nil {
		t.Errorf("past invitation not stamped revoked")
	}
	got, err = m.OrgInvitationByTokenHash(ctx, future[:])
	if err != nil {
		t.Fatalf("lookup future: %v", err)
	}
	if got.RevokedAt != nil {
		t.Errorf("future invitation stamped revoked prematurely")
	}
}

func TestMemStore_SoftDeleteOrg_And_StatusUpdates(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	org, err := m.CreateOrg(ctx, newTestOrg("lifecycle"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.UpdateOrgPlan(ctx, org.ID, api.PlanHobby); err != nil {
		t.Fatalf("plan update: %v", err)
	}
	if err := m.UpdateOrgStatus(ctx, org.ID, OrgStatusPastDue); err != nil {
		t.Fatalf("status update: %v", err)
	}
	if err := m.SoftDeleteOrg(ctx, org.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	got, err := m.OrgByID(ctx, org.ID)
	if err != nil {
		t.Fatalf("OrgByID after delete: %v", err)
	}
	if !got.DeletedPending {
		t.Errorf("DeletedPending flag missing after soft delete")
	}
	if got.Status != OrgStatusDeletedPending {
		t.Errorf("status = %s, want deleted_pending", got.Status)
	}
}

// PR 3 — CreateAccountWithPersonalOrg (issue #190 / ADR-061).
// MemStore mirrors of the PgStore sister-file tests above. The
// mutex serialises the three inserts so the partial-unique
// invariant the SQL partial index enforces is preserved.

func TestMemStore_CreateAccountWithPersonalOrg_Happy(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	res, err := m.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "happy@x.com",
		Plan:  api.PlanFree,
	})
	if err != nil {
		t.Fatalf("CreateAccountWithPersonalOrg: %v", err)
	}
	if res.Account.Email != "happy@x.com" {
		t.Errorf("email = %q", res.Account.Email)
	}
	if !res.PersonalOrg.Personal {
		t.Errorf("personal = false, want true")
	}
	if res.PersonalOrg.PersonalOwnerAccountID == nil ||
		*res.PersonalOrg.PersonalOwnerAccountID != res.Account.ID {
		t.Errorf("personal_owner_account_id mismatch: got %+v want %s",
			res.PersonalOrg.PersonalOwnerAccountID, res.Account.ID)
	}
	// Owner membership row exists.
	if _, err := m.OrgMemberByAccount(ctx, res.PersonalOrg.ID, res.Account.ID); err != nil {
		t.Errorf("OrgMemberByAccount: %v", err)
	}
}

func TestMemStore_CreateAccountWithPersonalOrg_DuplicateEmailReturnsErrConflict(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if _, err := m.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "dup@x.com",
		Plan:  api.PlanFree,
	}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, err := m.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "dup@x.com",
		Plan:  api.PlanFree,
	})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

func TestMemStore_CreateAccountWithPersonalOrg_SlugDeterministic(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	res, err := m.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
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

// TestMemStore_CountActiveOrgMembers_RemovedFiltered pins that the
// in-memory counter ignores soft-deleted memberships (mirrors the
// partial unique index on the SQL side). Hobby plan (OrgMembersMax=10)
// is used so the test can add 3 members without tripping the cap.
func TestMemStore_CountActiveOrgMembers_RemovedFiltered(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	o, err := m.CreateOrg(ctx, Org{
		Slug: "count-mem", Name: "Count Mem", Plan: api.PlanHobby, Status: OrgStatusActive,
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	owner := "00000000-0000-0000-0000-000000dc0001"
	kept := "00000000-0000-0000-0000-000000dc0002"
	gone := "00000000-0000-0000-0000-000000dc0003"
	for _, id := range []string{owner, kept, gone} {
		var role OrgRole
		switch id {
		case owner:
			role = OrgRoleOwner
		default:
			role = OrgRoleDeveloper
		}
		if err := m.AddOrgMember(ctx, o.ID, id, role, nil); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}
	if err := m.RemoveOrgMember(ctx, o.ID, gone); err != nil {
		t.Fatalf("remove gone: %v", err)
	}
	n, err := m.CountActiveOrgMembers(ctx, o.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("CountActiveOrgMembers = %d, want 2 (owner + kept, gone filtered)", n)
	}
}

// TestMemStore_CountPendingOrgInvitations_FiltersExpired pins that
// the pending-invitation counter ignores consumed / revoked /
// expired rows. Mirrors the SQL filter inside
// CountPendingOrgInvitations (PgStore twin).
func TestMemStore_CountPendingOrgInvitations_FiltersExpired(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	o, err := m.CreateOrg(ctx, newTestOrg("inv-mem"))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	hash := func(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }
	now := timeNow()

	// Pending row.
	if _, err := m.CreateOrgInvitation(ctx, OrgInvitation{
		OrgID: o.ID, Email: "a@x", Role: OrgRoleDeveloper, TokenHash: hash("a"),
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}
	// Already-consumed row.
	if _, err := m.CreateOrgInvitation(ctx, OrgInvitation{
		OrgID: o.ID, Email: "b@x", Role: OrgRoleDeveloper, TokenHash: hash("b"),
		ExpiresAt: now.Add(time.Hour), ConsumedAt: &now,
	}); err != nil {
		t.Fatalf("create consumed: %v", err)
	}
	// Revoked row.
	if _, err := m.CreateOrgInvitation(ctx, OrgInvitation{
		OrgID: o.ID, Email: "c@x", Role: OrgRoleDeveloper, TokenHash: hash("c"),
		ExpiresAt: now.Add(time.Hour), RevokedAt: &now,
	}); err != nil {
		t.Fatalf("create revoked: %v", err)
	}
	// Expired row (past).
	if _, err := m.CreateOrgInvitation(ctx, OrgInvitation{
		OrgID: o.ID, Email: "d@x", Role: OrgRoleDeveloper, TokenHash: hash("d"),
		ExpiresAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create expired: %v", err)
	}

	n, err := m.CountPendingOrgInvitations(ctx, o.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("CountPendingOrgInvitations = %d, want 1 (only the pending row)", n)
	}
}

// TestMemStore_ConsumeOrgInvitation_MemberCap is the in-memory
// parity twin of TestPgStore_ConsumeOrgInvitation_MemberCap (IAM-6
// / ADR-061 PR-2). Pins that the memstore cap check refuses an
// invitation accept that would push active members past
// Plan.OrgMembersMax(). Plan query is from the in-memory org
// struct (no SQL); the equality / membership insert logic is
// equivalent to PgStore's tx-scoped version.
func TestMemStore_ConsumeOrgInvitation_MemberCap(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	o, err := m.CreateOrg(ctx, Org{
		Slug: "consume-cap-mem", Name: "Consume Cap Mem",
		Plan: api.PlanHobby, Status: OrgStatusActive,
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Seed owner + 9 developers via direct AddOrgMember (which has
	// no cap check — internal owner-seed path).
	ownerID := "00000000-0000-0000-0000-000000cf0001"
	devIDs := make([]string, 9)
	for i := range devIDs {
		devIDs[i] = fmt.Sprintf("00000000-0000-0000-0000-000000cf%04x", 0x0002+i)
	}
	seedAccountsForMem(t, ctx, m, append([]string{ownerID}, devIDs...)...)
	if err := m.AddOrgMember(ctx, o.ID, ownerID, OrgRoleOwner, nil); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	for _, id := range devIDs {
		if err := m.AddOrgMember(ctx, o.ID, id, OrgRoleDeveloper, nil); err != nil {
			t.Fatalf("seed dev: %v", err)
		}
	}

	// Mint an invitation whose accept would make active = 11.
	overAcct := Account{ID: "00000000-0000-0000-0000-000000cf0010", Email: "over-cf@x.com"}
	seedAccountsForMem(t, ctx, m, overAcct.ID)
	plaintext := make([]byte, 32)
	for i := range plaintext {
		plaintext[i] = byte(i + 1) // deterministic but unique per-run
	}
	hash := sha256.Sum256(plaintext)
	if _, err := m.CreateOrgInvitation(ctx, OrgInvitation{
		OrgID:     o.ID,
		Email:     overAcct.Email,
		Role:      OrgRoleDeveloper,
		TokenHash: hash[:],
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateOrgInvitation: %v", err)
	}
	if _, _, err := m.ConsumeOrgInvitation(ctx, hash[:], overAcct); !errors.Is(err, ErrOrgMemberCapExceeded) {
		t.Fatalf("over-cap ConsumeOrgInvitation: err = %v, want ErrOrgMemberCapExceeded", err)
	}
	if _, err := m.OrgMemberByAccount(ctx, o.ID, overAcct.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("OrgMemberByAccount over-cap: err = %v, want ErrNotFound", err)
	}
}

// seedAccountsForMem inserts N accounts into the in-memory store so
// the (org_id, account_id) FK on org_memberships has a target. The
// memstore's AccountByEmail looks up by exact email, so we also
// stamp a deterministic email per id. Mirrors pgStoreSeedAccounts.
func seedAccountsForMem(t *testing.T, ctx context.Context, m *MemStore, ids ...string) {
	t.Helper()
	for _, id := range ids {
		// No-op if already present; the memstore has no insert
		// conflict semantics on a primary key, so we only add when
		// missing.
		if _, err := m.AccountByID(ctx, id); err == nil {
			continue
		}
		m.accounts[id] = Account{
			ID:        id,
			Email:     id + "@x.com",
			CreatedAt: time.Now().UTC(),
		}
	}
}
