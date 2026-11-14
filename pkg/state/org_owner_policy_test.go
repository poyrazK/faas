package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Only an active account can become an organization's owner, operators can
// give an organization a new owner, and an organization that requires
// two-factor authentication does not admit a member without it (ADR
// 20261113180000).
func orgOwnerAndPolicy(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	f := setupOffboarding(t, store)
	assigner, ok := store.(state.OrgOwnerAssigner)
	if !ok {
		t.Fatal("store cannot assign org owners")
	}
	policies, ok := store.(state.OrgSecurityPolicyStore)
	if !ok {
		t.Fatal("store has no org security policy")
	}
	snapshots, ok := store.(state.OrgAccessStore)
	if !ok {
		t.Fatal("store has no org access snapshot")
	}

	if err := store.UpdateAccountStatus(ctx, f.actor.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if err := store.TransferOrgOwnership(ctx, f.org.ID, f.owner.ID, f.actor.ID); !errors.Is(err, state.ErrOrgOwnerInactive) {
		t.Fatalf("transfer to a suspended member = %v, want ErrOrgOwnerInactive", err)
	}
	if _, err := assigner.AssignOrgOwner(ctx, f.org.ID, f.actor.ID); !errors.Is(err, state.ErrOrgOwnerInactive) {
		t.Fatalf("assign a suspended member = %v, want ErrOrgOwnerInactive", err)
	}
	if err := store.UpdateAccountStatus(ctx, f.actor.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	previous, err := assigner.AssignOrgOwner(ctx, f.org.ID, f.actor.ID)
	if err != nil || previous != f.owner.ID {
		t.Fatalf("assign owner = %q, %v; want previous %s", previous, err, f.owner.ID)
	}
	for id, want := range map[string]state.OrgRole{f.actor.ID: state.OrgRoleOwner, f.owner.ID: state.OrgRoleAdmin} {
		member, err := store.OrgMemberByAccount(ctx, f.org.ID, id)
		if err != nil || member.Role != want {
			t.Fatalf("member %s role = %q, %v; want %q", id, member.Role, err, want)
		}
	}

	if err := policies.SetOrgRequireMFA(ctx, f.org.ID, true, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if policy, err := policies.OrgSecurityPolicy(ctx, f.org.ID); err != nil || !policy.RequireMFA {
		t.Fatalf("policy = %+v, %v; want require_mfa", policy, err)
	}
	if _, err := snapshots.OrgAccessSnapshot(ctx, f.org.ID, f.owner.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("snapshot of a member without MFA = %v, want ErrNotFound", err)
	}
	if err := store.SetMFASecret(ctx, f.owner.ID, []byte("sealed-secret"), nil); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMFAEnrolled(ctx, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	if snap, err := snapshots.OrgAccessSnapshot(ctx, f.org.ID, f.owner.ID); err != nil || snap.Role != state.OrgRoleAdmin {
		t.Fatalf("snapshot after MFA = %+v, %v; want admin", snap, err)
	}
	if err := policies.SetOrgRequireMFA(ctx, f.org.ID, false, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshots.OrgAccessSnapshot(ctx, f.org.ID, f.actor.ID); err != nil {
		t.Fatalf("snapshot after the policy is off = %v", err)
	}
}

func TestMemOrgOwnerAndPolicy(t *testing.T) {
	orgOwnerAndPolicy(t, state.NewMemStore())
}
