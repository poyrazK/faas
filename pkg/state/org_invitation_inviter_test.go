package state_test

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// An invitation stops working when its inviter leaves the organization or
// loses a role that may invite (ADR 20261111130000).
func invitationFollowsInviter(t *testing.T, store state.Store) {
	t.Helper()
	f := setupOffboarding(t, store)
	if err := store.UpdateOrgMemberRole(t.Context(), f.org.ID, f.actor.ID, state.OrgRoleAdmin); err != nil {
		t.Fatal(err)
	}
	invite := func(label, email string) [32]byte {
		hash := sha256.Sum256([]byte(label))
		if _, err := store.CreateOrgInvitation(t.Context(), state.OrgInvitation{
			OrgID: f.org.ID, Email: email, Role: state.OrgRoleAdmin, TokenHash: hash[:],
			InvitedByAccountID: &f.actor.ID, ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	accept := func(hash [32]byte, email string) error {
		res, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: email, Plan: api.PlanFree})
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = store.ConsumeOrgInvitation(t.Context(), hash[:], res.Account)
		return err
	}

	live := invite("while-admin", "alt-one@example.com")
	if err := accept(live, "alt-one@example.com"); err != nil {
		t.Fatalf("invitation from a current admin: %v", err)
	}
	afterDemotion := invite("before-demotion", "alt-two@example.com")
	if err := store.UpdateOrgMemberRole(t.Context(), f.org.ID, f.actor.ID, state.OrgRoleDeveloper); err != nil {
		t.Fatal(err)
	}
	if err := accept(afterDemotion, "alt-two@example.com"); !errors.Is(err, state.ErrOrgInvitationInvalid) {
		t.Fatalf("invitation from a demoted admin = %v, want invalid", err)
	}
	if err := store.UpdateOrgMemberRole(t.Context(), f.org.ID, f.actor.ID, state.OrgRoleAdmin); err != nil {
		t.Fatal(err)
	}
	afterRemoval := invite("before-removal", "alt-three@example.com")
	if err := store.RemoveOrgMember(t.Context(), f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := accept(afterRemoval, "alt-three@example.com"); !errors.Is(err, state.ErrOrgInvitationInvalid) {
		t.Fatalf("invitation from a removed admin = %v, want invalid", err)
	}
}

func TestMemStoreInvitationFollowsInviter(t *testing.T) {
	invitationFollowsInviter(t, state.NewMemStore())
}
