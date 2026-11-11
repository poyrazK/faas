package state_test

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A deploy token minted by one member on another member's company app
// authenticates as the minter and dies with the minter's membership; a token
// minted before the minter column existed keeps authenticating as the app's
// creator (ADR 20261110190000).
func deployTokenMinter(t *testing.T, store state.Store) {
	t.Helper()
	f := setupOffboarding(t, store)
	adminResult, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: "minter-admin@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	admin := adminResult.Account
	if err := store.AddOrgMember(t.Context(), f.org.ID, admin.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	tokens := store.(state.DeployTokenStore)
	minted := sha256.Sum256([]byte("minted-by-admin"))
	legacy := sha256.Sum256([]byte("minted-without-minter"))
	expires := time.Now().Add(time.Hour)
	if _, err := tokens.CreateDeployToken(state.WithDeployTokenMinter(t.Context(), admin.ID), f.actor.ID, f.app.ID, minted[:], "admin CI", nil, expires); err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.CreateDeployToken(t.Context(), f.actor.ID, f.app.ID, legacy[:], "legacy CI", nil, expires); err != nil {
		t.Fatal(err)
	}

	acct, key, err := tokens.AuthenticateDeployToken(t.Context(), minted[:])
	if err != nil || acct.ID != admin.ID || key.AccountID != admin.ID || key.OrgID != f.org.ID || key.AppID != f.app.ID {
		t.Fatalf("minted token = %s %+v %v, want the admin bound to the company app", acct.ID, key, err)
	}
	snapshot, err := store.OrgAccessSnapshot(t.Context(), f.org.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := state.WithOrgAccessFence(t.Context(), state.OrgAccessFence{OrgID: f.org.ID, AccountID: admin.ID, Revision: snapshot.Revision, CredentialID: key.ID, AppID: f.app.ID})
	defer cancel(nil)
	if err := store.CheckOrgAccess(ctx); err != nil {
		t.Fatalf("fence rejected the minter's live token: %v", err)
	}
	if acct, _, err := tokens.AuthenticateDeployToken(t.Context(), legacy[:]); err != nil || acct.ID != f.actor.ID {
		t.Fatalf("legacy token = %s %v, want the app's creator", acct.ID, err)
	}

	if err := store.RemoveOrgMember(t.Context(), f.org.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tokens.AuthenticateDeployToken(t.Context(), minted[:]); !errors.Is(err, state.ErrAPIKeyRevoked) {
		t.Errorf("removed minter's token: %v, want revoked", err)
	}
	if _, _, err := tokens.AuthenticateDeployToken(t.Context(), legacy[:]); err != nil {
		t.Errorf("creator's token revoked with the minter: %v", err)
	}
	again := sha256.Sum256([]byte("minted-after-removal"))
	if _, err := tokens.CreateDeployToken(state.WithDeployTokenMinter(t.Context(), admin.ID), f.actor.ID, f.app.ID, again[:], "after removal", nil, expires); err == nil {
		t.Error("removed member minted a token on a company app")
	}
}

func TestMemStoreDeployTokenMinter(t *testing.T) {
	deployTokenMinter(t, state.NewMemStore())
}
