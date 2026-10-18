package state_test

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type offboardingFixture struct {
	store        state.Store
	org          state.Org
	owner, actor state.Account
	app          state.App
}

func setupOffboarding(t *testing.T, store state.Store) offboardingFixture {
	t.Helper()
	ownerResult, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: "offboard-owner@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	actorResult, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: "offboard-actor@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	owner, actor := ownerResult.Account, actorResult.Account
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "offboard-team", Name: "Offboard Team", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []struct {
		id   string
		role state.OrgRole
	}{{owner.ID, state.OrgRoleOwner}, {actor.ID, state.OrgRoleDeveloper}} {
		if err := store.AddOrgMember(t.Context(), org.ID, member.id, member.role, nil); err != nil {
			t.Fatal(err)
		}
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: actor.ID, OrgID: org.ID, Slug: "offboard-api"})
	if err != nil {
		t.Fatal(err)
	}
	return offboardingFixture{store: store, org: org, owner: owner, actor: actor, app: app}
}

func offboardingKey(t *testing.T, store state.Store, orgID, accountID, label string) state.APIKey {
	t.Helper()
	hash := sha256.Sum256([]byte(label))
	key, err := store.CreateOrgAPIKey(t.Context(), orgID, accountID, hash[:], label, api.ScopesAdminOnly, nil)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func offboardingCredentials(t *testing.T, store state.Store) {
	t.Helper()
	f := setupOffboarding(t, store)
	key := offboardingKey(t, store, f.org.ID, f.actor.ID, "team")
	newHash := sha256.Sum256([]byte("team-rotated"))
	newKey, oldKey, err := store.RotateOrgAPIKey(t.Context(), f.org.ID, key.ID, newHash[:], "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	personalOrg, err := store.OrgByPersonalAccount(t.Context(), f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	personal := offboardingKey(t, store, personalOrg.ID, f.actor.ID, "personal")
	teammate := offboardingKey(t, store, f.org.ID, f.owner.ID, "teammate")
	otherOrg, err := store.CreateOrg(t.Context(), state.Org{Slug: "other-company", Name: "Other Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), otherOrg.ID, f.actor.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	other := offboardingKey(t, store, otherOrg.ID, f.actor.ID, "other")
	personalApp, err := store.CreateApp(t.Context(), state.App{AccountID: f.actor.ID, OrgID: personalOrg.ID, Slug: "personal-api"})
	if err != nil {
		t.Fatal(err)
	}
	tokens := store.(state.DeployTokenStore)
	teamTokenHash := sha256.Sum256([]byte("team-token"))
	personalTokenHash := sha256.Sum256([]byte("personal-token"))
	for appID, hash := range map[string][32]byte{f.app.ID: teamTokenHash, personalApp.ID: personalTokenHash} {
		if _, err := tokens.CreateDeployToken(t.Context(), f.actor.ID, appID, hash[:], "CI", []string{api.ScopeDeployWrite}, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := store.OrgAccessSnapshot(t.Context(), f.org.ID, f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	invitationHash := sha256.Sum256([]byte("outstanding-invitation"))
	if _, err := store.CreateOrgInvitation(t.Context(), state.OrgInvitation{OrgID: f.org.ID, Email: f.actor.Email, Role: state.OrgRoleDeveloper, TokenHash: invitationHash[:], ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := state.WithOrgAccessFence(t.Context(), state.OrgAccessFence{OrgID: f.org.ID, AccountID: f.actor.ID, Revision: snapshot.Revision})
	defer cancel(nil)
	if err := store.RemoveOrgMember(t.Context(), f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if invitation, err := store.OrgInvitationByTokenHash(t.Context(), invitationHash[:]); err != nil || invitation.RevokedAt == nil {
		t.Errorf("outstanding invitation survives removal: %+v %v", invitation, err)
	}
	for _, revoked := range []state.APIKey{oldKey, newKey} {
		if _, _, err := store.AuthenticateKey(t.Context(), revoked.Hash); !errors.Is(err, state.ErrAPIKeyRevoked) {
			t.Errorf("removed member credential: %v", err)
		}
	}
	for _, retained := range []state.APIKey{personal, teammate, other} {
		if _, _, err := store.AuthenticateKey(t.Context(), retained.Hash); err != nil {
			t.Errorf("unrelated credential revoked: %v", err)
		}
	}
	if _, _, err := tokens.AuthenticateDeployToken(t.Context(), teamTokenHash[:]); !errors.Is(err, state.ErrAPIKeyRevoked) {
		t.Errorf("removed member deploy token: %v", err)
	}
	if _, _, err := tokens.AuthenticateDeployToken(t.Context(), personalTokenHash[:]); err != nil {
		t.Errorf("personal deploy token revoked: %v", err)
	}
	if err := store.UpsertAppEnvInScope(ctx, f.actor.ID, f.app.ID, "default", "AFTER_REMOVAL", "forbidden"); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Errorf("stale mutation: %v", err)
	}
	rows, err := store.ListAppEnvInScope(t.Context(), f.actor.ID, f.app.ID, "default")
	if err != nil || len(rows) != 0 {
		t.Fatalf("stale mutation persisted: %+v, %v", rows, err)
	}
	freshHash := sha256.Sum256([]byte("after-removal"))
	if _, err := store.CreateOrgAPIKey(t.Context(), f.org.ID, f.actor.ID, freshHash[:], "after removal", api.ScopesAdminOnly, nil); err == nil {
		t.Error("removed member minted an org key")
	}
	if app, err := store.AppByID(t.Context(), f.app.ID); err != nil || app.Status != f.app.Status {
		t.Errorf("removal changed application: %+v %v", app, err)
	}
}

func TestMemStoreOrgOffboardingCredentialsAndFence(t *testing.T) {
	offboardingCredentials(t, state.NewMemStore())
}

func offboardingOIDCCredential(t *testing.T, store state.Store) {
	t.Helper()
	f := setupOffboarding(t, store)
	hash := sha256.Sum256([]byte("oidc-bearer"))
	token := state.OIDCExchangedToken{AccountID: f.actor.ID, TokenHash: hash[:], ExpiresAt: time.Now().Add(time.Minute), IssuerURL: "https://token.actions.githubusercontent.com", Subject: "repo:acme/team:ref:refs/heads/main", Audience: []string{"gregale"}}
	if _, err := store.InsertOIDCExchangedToken(t.Context(), &token); err != nil {
		t.Fatal(err)
	}
	_, key, err := store.AuthenticateOIDCBearer(t.Context(), hash[:])
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.OrgAccessSnapshot(t.Context(), f.org.ID, f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence := state.OrgAccessFence{OrgID: f.org.ID, AccountID: f.actor.ID, Revision: snapshot.Revision, CredentialID: key.ID}
	ctx, cancel := state.WithOrgAccessFence(t.Context(), fence)
	defer cancel(nil)
	if err := store.CheckOrgAccess(ctx); err != nil {
		t.Fatalf("active CI bearer rejected as an API key: %v", err)
	}
	if err := store.DeleteOIDCExchangedToken(t.Context(), key.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckOrgAccess(ctx); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Fatalf("removed CI bearer retained company authority: %v", err)
	}
}

func TestMemStoreOrgOffboardingOIDCCredential(t *testing.T) {
	offboardingOIDCCredential(t, state.NewMemStore())
}
