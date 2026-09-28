package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreOrgAccessMutationsAreAtomicWithActivity(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	owner, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: "timeline-owner-" + uuid.NewString() + "@example.com", Plan: api.PlanPro,
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	member, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: "timeline-member-" + uuid.NewString() + "@example.com", Plan: api.PlanPro,
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	orgID := owner.PersonalOrg.ID
	activity := func(kind, sourceID string, data []byte) state.OrgActivity {
		actorID := uuid.MustParse(owner.Account.ID)
		return state.OrgActivity{
			OrgID: uuid.MustParse(orgID), Kind: kind, ActorType: state.OrgActivityActorUser,
			ActorAccountID: &actorID, ActorLabel: owner.Account.Email,
			ResourceType: "member", ResourceLabel: "member", SourceType: kind,
			SourceID: sourceID, Data: data,
		}
	}
	deliver := func(id int64) {
		t.Helper()
		if delivered, err := s.DeliverOrgActivityOutbox(ctx, id); err != nil || !delivered {
			t.Fatalf("deliver outbox %d = (%v, %v)", id, delivered, err)
		}
	}

	const tokenMaterial = "plaintext-invitation-token-must-never-appear"
	if _, _, err := s.CreateOrgInvitationWithActivity(ctx, state.OrgInvitation{
		OrgID: orgID, Email: member.Account.Email, Role: state.OrgRoleDeveloper,
		TokenHash: []byte("bad-activity-token-hash"), ExpiresAt: time.Now().Add(time.Hour),
	}, activity("org.invitation.created", "bad-create", []byte(`[]`))); err == nil {
		t.Fatal("invitation create with invalid activity succeeded")
	}
	if invitations, err := s.ListOrgInvitationsForOrg(ctx, orgID); err != nil || len(invitations) != 0 {
		t.Fatalf("invitations after rejected create = (%#v, %v), want none", invitations, err)
	}

	first, createID, err := s.CreateOrgInvitationWithActivity(ctx, state.OrgInvitation{
		OrgID: orgID, Email: member.Account.Email, Role: state.OrgRoleDeveloper,
		TokenHash: []byte(tokenMaterial), ExpiresAt: time.Now().Add(time.Hour),
	}, activity("org.invitation.created", "invite-create-1", []byte(`{"role":"developer"}`)))
	if err != nil {
		t.Fatalf("create invitation with activity: %v", err)
	}
	deliver(createID)
	joined, consumed, acceptedID, memberID, err := s.ConsumeOrgInvitationWithActivity(ctx, first.TokenHash, member.Account,
		activity("org.invitation.accepted", "invite-accepted-1", []byte(`{}`)),
		activity("org.member.added", "member-added-1", []byte(`{}`)))
	if err != nil || joined.AccountID != member.Account.ID || consumed.ConsumedAt == nil {
		t.Fatalf("accept invitation with activity = (%+v, %+v, %v)", joined, consumed, err)
	}
	deliver(acceptedID)
	deliver(memberID)

	updated, roleID, err := s.UpdateOrgMemberRoleWithActivity(ctx, orgID, member.Account.ID, state.OrgRoleAdmin,
		activity("org.member.role_changed", "member-role-1", []byte(`{}`)))
	if err != nil || updated.Role != state.OrgRoleAdmin {
		t.Fatalf("change member role with activity = (%+v, %v)", updated, err)
	}
	deliver(roleID)
	removed, removeID, err := s.RemoveOrgMemberWithActivity(ctx, orgID, member.Account.ID,
		activity("org.member.removed", "member-remove-1", []byte(`{}`)))
	if err != nil || removed.RemovedAt == nil {
		t.Fatalf("remove member with activity = (%+v, %v)", removed, err)
	}
	deliver(removeID)

	second, secondCreateID, err := s.CreateOrgInvitationWithActivity(ctx, state.OrgInvitation{
		OrgID: orgID, Email: "pending-" + uuid.NewString() + "@example.com", Role: state.OrgRoleViewer,
		TokenHash: []byte("stored-token-hash-2"), ExpiresAt: time.Now().Add(time.Hour),
	}, activity("org.invitation.created", "invite-create-2", []byte(`{"role":"viewer"}`)))
	if err != nil {
		t.Fatalf("create second invitation: %v", err)
	}
	deliver(secondCreateID)
	_, revokeID, err := s.RevokeOrgInvitationWithActivity(ctx, orgID, second.ID, owner.Account.ID,
		activity("org.invitation.revoked", "invite-revoked-1", []byte(`{}`)))
	if err != nil {
		t.Fatalf("revoke invitation with activity: %v", err)
	}
	deliver(revokeID)

	rows, err := s.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: uuid.MustParse(orgID), Limit: 20})
	if err != nil || len(rows) != 7 {
		t.Fatalf("workspace access activity = (%#v, %v), want seven events", rows, err)
	}
	for _, row := range rows {
		if strings.Contains(string(row.Data), tokenMaterial) || strings.Contains(row.ResourceLabel, tokenMaterial) || strings.Contains(string(row.Data), "stored-token-hash") {
			t.Errorf("invitation credential material leaked into activity: %+v", row)
		}
	}
}

func TestPgStoreOwnershipTransferIsAtomicWithActivity(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	owner, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: "transfer-owner-" + uuid.NewString() + "@example.com", Plan: api.PlanPro,
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	newOwner, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: "transfer-target-" + uuid.NewString() + "@example.com", Plan: api.PlanPro,
	})
	if err != nil {
		t.Fatalf("create new owner: %v", err)
	}
	orgID := owner.PersonalOrg.ID
	if err := s.AddOrgMember(ctx, orgID, newOwner.Account.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatalf("add target member: %v", err)
	}
	actorID := uuid.MustParse(owner.Account.ID)
	activity := state.OrgActivity{
		OrgID: uuid.MustParse(orgID), Kind: "org.ownership_transferred", ActorType: state.OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: owner.Account.Email, ResourceType: "member",
		ResourceLabel: "member", SourceType: "org.ownership_transferred", SourceID: "ownership-transfer-1",
		Data: []byte(`{}`),
	}
	invalid := activity
	invalid.Data = []byte(`[]`)
	if _, err := s.TransferOrgOwnershipWithActivity(ctx, orgID, owner.Account.ID, newOwner.Account.ID, invalid); err == nil {
		t.Fatal("transfer with invalid activity succeeded")
	}
	if from, err := s.OrgMemberByAccount(ctx, orgID, owner.Account.ID); err != nil || from.Role != state.OrgRoleOwner {
		t.Fatalf("owner after rejected transfer = (%+v, %v), want owner", from, err)
	}

	outboxID, err := s.TransferOrgOwnershipWithActivity(ctx, orgID, owner.Account.ID, newOwner.Account.ID, activity)
	if err != nil || outboxID <= 0 {
		t.Fatalf("transfer with activity = (%d, %v)", outboxID, err)
	}
	if from, err := s.OrgMemberByAccount(ctx, orgID, owner.Account.ID); err != nil || from.Role != state.OrgRoleAdmin {
		t.Fatalf("former owner after transfer = (%+v, %v), want admin", from, err)
	}
	if to, err := s.OrgMemberByAccount(ctx, orgID, newOwner.Account.ID); err != nil || to.Role != state.OrgRoleOwner {
		t.Fatalf("new owner after transfer = (%+v, %v), want owner", to, err)
	}
	if rows, err := s.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: uuid.MustParse(orgID), Limit: 10}); err != nil || len(rows) != 0 {
		t.Fatalf("activity before outbox delivery = (%#v, %v), want none", rows, err)
	}
	if delivered, err := s.DeliverOrgActivityOutbox(ctx, outboxID); err != nil || !delivered {
		t.Fatalf("deliver ownership transfer outbox = (%v, %v)", delivered, err)
	}
	rows, err := s.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: uuid.MustParse(orgID), Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ownership activity = (%#v, %v), want one row", rows, err)
	}
	if rows[0].Kind != "org.ownership_transferred" || rows[0].ResourceLabel != newOwner.Account.Email ||
		!strings.Contains(string(rows[0].Data), owner.Account.ID) || !strings.Contains(string(rows[0].Data), newOwner.Account.ID) {
		t.Fatalf("ownership activity payload = %+v", rows[0])
	}
}
