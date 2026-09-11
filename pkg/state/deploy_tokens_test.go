package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedDeployTokenFixture(t *testing.T) (*state.MemStore, state.Account, state.App) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "deploy-token@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{AccountID: acct.ID, Slug: "deploy-token-app", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return store, acct, app
}

func TestMemStoreDeployTokenRoundTripAndAppScope(t *testing.T) {
	store, acct, app := seedDeployTokenFixture(t)
	ctx := context.Background()
	plain, hash, err := api.GenerateDeployToken()
	if err != nil {
		t.Fatal(err)
	}
	if !api.ValidDeployTokenFormat(plain) {
		t.Fatalf("invalid generated token: %q", plain)
	}
	expires := time.Now().Add(24 * time.Hour)
	token, err := store.CreateDeployToken(ctx, acct.ID, app.ID, hash, "ci", []string{api.ScopeDeployWrite}, expires)
	if err != nil {
		t.Fatalf("CreateDeployToken: %v", err)
	}
	if token.AppID != app.ID || token.Status != "active" {
		t.Fatalf("created token = %+v", token)
	}
	gotAcct, gotKey, err := store.AuthenticateDeployToken(ctx, api.HashAPIKey(plain))
	if err != nil {
		t.Fatalf("AuthenticateDeployToken: %v", err)
	}
	if gotAcct.ID != acct.ID || gotKey.AppID != app.ID || gotKey.Scopes[0] != api.ScopeDeployWrite {
		t.Fatalf("authenticated principal = %+v / %+v", gotAcct, gotKey)
	}
	if _, err := store.GetDeployToken(ctx, acct.ID, "other-app", token.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-app lookup error = %v, want ErrNotFound", err)
	}
}

func TestMemStoreDeployTokenRotateAndRevoke(t *testing.T) {
	store, acct, app := seedDeployTokenFixture(t)
	ctx := context.Background()
	oldPlain, oldHash, err := api.GenerateDeployToken()
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.CreateDeployToken(ctx, acct.ID, app.ID, oldHash, "ci", []string{api.ScopeDeployWrite}, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	newPlain, newHash, _ := api.GenerateDeployToken()
	newToken, oldToken, err := store.RotateDeployToken(ctx, acct.ID, app.ID, old.ID, newHash, "", time.Now().Add(48*time.Hour), 0)
	if err != nil {
		t.Fatalf("RotateDeployToken: %v", err)
	}
	if newToken.RotatedFromID == nil || *newToken.RotatedFromID != old.ID || oldToken.Status != "revoked" {
		t.Fatalf("rotation = new %+v old %+v", newToken, oldToken)
	}
	if _, _, err := store.AuthenticateDeployToken(ctx, api.HashAPIKey(oldPlain)); !errors.Is(err, state.ErrAPIKeyRevoked) {
		t.Fatalf("old token auth error = %v, want ErrAPIKeyRevoked", err)
	}
	if _, _, err := store.AuthenticateDeployToken(ctx, api.HashAPIKey(newPlain)); err != nil {
		t.Fatalf("new token auth: %v", err)
	}
	if _, err := store.RevokeDeployToken(ctx, acct.ID, app.ID, newToken.ID); err != nil {
		t.Fatalf("RevokeDeployToken: %v", err)
	}
	if _, _, err := store.AuthenticateDeployToken(ctx, api.HashAPIKey(newPlain)); !errors.Is(err, state.ErrAPIKeyRevoked) {
		t.Fatalf("revoked token auth error = %v, want ErrAPIKeyRevoked", err)
	}
}
