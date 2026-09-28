package state

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreOrgAccessMutationsAreAtomicWithActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemStore()
	owner, err := store.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "timeline-owner-" + uuid.NewString() + "@example.com", Plan: api.PlanPro,
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	member, err := store.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "timeline-member-" + uuid.NewString() + "@example.com", Plan: api.PlanPro,
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	orgID := owner.PersonalOrg.ID
	activity := func(kind, sourceID string, data []byte) OrgActivity {
		actorID := uuid.MustParse(owner.Account.ID)
		return OrgActivity{
			OrgID: uuid.MustParse(orgID), Kind: kind, ActorType: OrgActivityActorUser,
			ActorAccountID: &actorID, ActorLabel: owner.Account.Email,
			ResourceType: "member", ResourceLabel: "member", SourceType: kind,
			SourceID: sourceID, Data: data,
		}
	}
	deliver := func(id int64) {
		t.Helper()
		if id <= 0 {
			t.Fatalf("outbox id = %d, want positive id", id)
		}
		if delivered, err := store.DeliverOrgActivityOutbox(ctx, id); err != nil || !delivered {
			t.Fatalf("deliver outbox %d = (%v, %v)", id, delivered, err)
		}
	}

	const tokenMaterial = "plaintext-invitation-token-must-never-appear"
	bad := activity("org.invitation.created", "bad-create", []byte(`[]`))
	if _, _, err := store.CreateOrgInvitationWithActivity(ctx, OrgInvitation{
		OrgID: orgID, Email: member.Account.Email, Role: OrgRoleDeveloper,
		TokenHash: []byte(tokenMaterial), ExpiresAt: time.Now().Add(time.Hour),
	}, bad); err == nil {
		t.Fatal("invitation create with invalid activity succeeded")
	}
	if invitations, err := store.ListOrgInvitationsForOrg(ctx, orgID); err != nil || len(invitations) != 0 {
		t.Fatalf("invitations after rejected create = (%#v, %v), want none", invitations, err)
	}

	first, createID, err := store.CreateOrgInvitationWithActivity(ctx, OrgInvitation{
		OrgID: orgID, Email: member.Account.Email, Role: OrgRoleDeveloper,
		TokenHash: []byte(tokenMaterial), ExpiresAt: time.Now().Add(time.Hour),
	}, activity("org.invitation.created", "invite-create-1", []byte(`{"role":"developer"}`)))
	if err != nil {
		t.Fatalf("create invitation with activity: %v", err)
	}
	deliver(createID)

	acceptedID, memberID := int64(0), int64(0)
	joined, consumed, acceptedID, memberID, err := store.ConsumeOrgInvitationWithActivity(ctx, first.TokenHash, member.Account,
		activity("org.invitation.accepted", "invite-accepted-1", []byte(`{}`)),
		activity("org.member.added", "member-added-1", []byte(`{}`)))
	if err != nil || joined.AccountID != member.Account.ID || consumed.ConsumedAt == nil {
		t.Fatalf("accept invitation with activity = (%+v, %+v, %v)", joined, consumed, err)
	}
	deliver(acceptedID)
	deliver(memberID)

	updated, roleID, err := store.UpdateOrgMemberRoleWithActivity(ctx, orgID, member.Account.ID, OrgRoleAdmin,
		activity("org.member.role_changed", "member-role-1", []byte(`{}`)))
	if err != nil || updated.Role != OrgRoleAdmin {
		t.Fatalf("change member role with activity = (%+v, %v)", updated, err)
	}
	deliver(roleID)
	if _, duplicateID, err := store.UpdateOrgMemberRoleWithActivity(ctx, orgID, member.Account.ID, OrgRoleAdmin,
		activity("org.member.role_changed", "member-role-noop", []byte(`{}`))); err != nil || duplicateID != 0 {
		t.Fatalf("unchanged role mutation = (%d, %v), want no duplicate activity", duplicateID, err)
	}

	removed, removeID, err := store.RemoveOrgMemberWithActivity(ctx, orgID, member.Account.ID,
		activity("org.member.removed", "member-remove-1", []byte(`{}`)))
	if err != nil || removed.RemovedAt == nil {
		t.Fatalf("remove member with activity = (%+v, %v)", removed, err)
	}
	deliver(removeID)
	if _, duplicateID, err := store.RemoveOrgMemberWithActivity(ctx, orgID, member.Account.ID,
		activity("org.member.removed", "member-remove-noop", []byte(`{}`))); err != nil || duplicateID != 0 {
		t.Fatalf("repeat member removal = (%d, %v), want no duplicate activity", duplicateID, err)
	}

	second, secondCreateID, err := store.CreateOrgInvitationWithActivity(ctx, OrgInvitation{
		OrgID: orgID, Email: "pending-" + uuid.NewString() + "@example.com", Role: OrgRoleViewer,
		TokenHash: []byte("stored-token-hash-2"), ExpiresAt: time.Now().Add(time.Hour),
	}, activity("org.invitation.created", "invite-create-2", []byte(`{"role":"viewer"}`)))
	if err != nil {
		t.Fatalf("create second invitation: %v", err)
	}
	deliver(secondCreateID)
	_, revokeID, err := store.RevokeOrgInvitationWithActivity(ctx, orgID, second.ID, owner.Account.ID,
		activity("org.invitation.revoked", "invite-revoked-1", []byte(`{}`)))
	if err != nil {
		t.Fatalf("revoke invitation with activity: %v", err)
	}
	deliver(revokeID)

	rows, err := store.ListOrgActivity(ctx, OrgActivityFilter{OrgID: uuid.MustParse(orgID), Limit: 20})
	if err != nil || len(rows) != 7 {
		t.Fatalf("workspace access activity = (%#v, %v), want seven events", rows, err)
	}
	wantKinds := map[string]int{
		"org.invitation.created": 2, "org.invitation.accepted": 1, "org.member.added": 1,
		"org.member.role_changed": 1, "org.member.removed": 1, "org.invitation.revoked": 1,
	}
	for _, row := range rows {
		if wantKinds[row.Kind] == 0 {
			t.Errorf("unexpected activity kind %q", row.Kind)
		}
		wantKinds[row.Kind]--
		if strings.Contains(string(row.Data), tokenMaterial) || strings.Contains(row.ResourceLabel, tokenMaterial) ||
			strings.Contains(string(row.Data), "stored-token-hash") {
			t.Errorf("invitation credential material leaked into activity: %+v", row)
		}
	}
	for kind, count := range wantKinds {
		if count != 0 {
			t.Errorf("activity kind %q count missing by %d", kind, count)
		}
	}
}

func TestMemStoreOwnershipTransferIsAtomicWithActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemStore()
	owner, err := store.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "transfer-owner-" + uuid.NewString() + "@example.com", Plan: api.PlanPro,
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	newOwner, err := store.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "transfer-target-" + uuid.NewString() + "@example.com", Plan: api.PlanPro,
	})
	if err != nil {
		t.Fatalf("create new owner: %v", err)
	}
	orgID := owner.PersonalOrg.ID
	if err := store.AddOrgMember(ctx, orgID, newOwner.Account.ID, OrgRoleDeveloper, nil); err != nil {
		t.Fatalf("add target member: %v", err)
	}
	actorID := uuid.MustParse(owner.Account.ID)
	activity := OrgActivity{
		OrgID: uuid.MustParse(orgID), Kind: "org.ownership_transferred", ActorType: OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: owner.Account.Email, ResourceType: "member",
		ResourceLabel: "member", SourceType: "org.ownership_transferred", SourceID: "ownership-transfer-1",
		Data: []byte(`{}`),
	}
	invalid := activity
	invalid.Data = []byte(`[]`)
	if _, err := store.TransferOrgOwnershipWithActivity(ctx, orgID, owner.Account.ID, newOwner.Account.ID, invalid); err == nil {
		t.Fatal("transfer with invalid activity succeeded")
	}
	if from, _ := store.OrgMemberByAccount(ctx, orgID, owner.Account.ID); from.Role != OrgRoleOwner {
		t.Fatalf("owner role after rejected transfer = %q, want owner", from.Role)
	}

	outboxID, err := store.TransferOrgOwnershipWithActivity(ctx, orgID, owner.Account.ID, newOwner.Account.ID, activity)
	if err != nil || outboxID <= 0 {
		t.Fatalf("transfer with activity = (%d, %v)", outboxID, err)
	}
	from, err := store.OrgMemberByAccount(ctx, orgID, owner.Account.ID)
	if err != nil || from.Role != OrgRoleAdmin {
		t.Fatalf("former owner membership = (%+v, %v), want admin", from, err)
	}
	to, err := store.OrgMemberByAccount(ctx, orgID, newOwner.Account.ID)
	if err != nil || to.Role != OrgRoleOwner {
		t.Fatalf("new owner membership = (%+v, %v), want owner", to, err)
	}
	if rows, err := store.ListOrgActivity(ctx, OrgActivityFilter{OrgID: uuid.MustParse(orgID), Limit: 10}); err != nil || len(rows) != 0 {
		t.Fatalf("activity before outbox delivery = (%#v, %v), want none", rows, err)
	}
	if delivered, err := store.DeliverOrgActivityOutbox(ctx, outboxID); err != nil || !delivered {
		t.Fatalf("deliver ownership transfer outbox = (%v, %v)", delivered, err)
	}
	rows, err := store.ListOrgActivity(ctx, OrgActivityFilter{OrgID: uuid.MustParse(orgID), Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ownership activity = (%#v, %v), want one row", rows, err)
	}
	if rows[0].Kind != "org.ownership_transferred" || rows[0].ResourceLabel != newOwner.Account.Email ||
		!strings.Contains(string(rows[0].Data), owner.Account.ID) || !strings.Contains(string(rows[0].Data), newOwner.Account.ID) {
		t.Fatalf("ownership activity payload = %+v", rows[0])
	}
}
