package state_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type platformTenantAccessTestStore interface {
	state.PlatformTenantAccessStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreatePlatformTenant(context.Context, string, string, string, int) (state.PlatformTenant, bool, error)
}

func TestMemPlatformTenantAccessTokenLifecycle(t *testing.T) {
	testPlatformTenantAccessTokenLifecycle(t, state.NewMemStore())
}

func TestPgPlatformTenantAccessTokenLifecycle(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testPlatformTenantAccessTokenLifecycle(t, store)
}

func testPlatformTenantAccessTokenLifecycle(t *testing.T, store platformTenantAccessTestStore) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "tenant-access-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "access-test", "Access test", 250)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, prefix, hash, err := api.GeneratePlatformTenantAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	input := state.PlatformTenantAccessTokenInput{AccountID: account.ID, TenantID: tenant.ID,
		Name: "billing reader", Prefix: prefix, TokenHash: hash,
		Scopes: []string{api.ScopePlatformTenantStatementsRead}, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	token, err := store.CreatePlatformTenantAccessToken(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if token.ID == "" || token.Prefix != prefix || !bytes.Equal(token.TokenHash, hash) {
		t.Fatalf("unexpected persisted token metadata: %+v", token)
	}
	if _, err := store.CreatePlatformTenantAccessToken(ctx, input); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate active name err=%v, want conflict", err)
	}
	listed, err := store.ListPlatformTenantAccessTokens(ctx, account.ID, tenant.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != token.ID {
		t.Fatalf("listed tokens = %+v, %v", listed, err)
	}
	gotAccount, authenticated, err := store.AuthenticatePlatformTenantAccessToken(ctx, api.HashAPIKey(plaintext))
	if err != nil || gotAccount.ID != account.ID || authenticated.TenantID != tenant.ID || authenticated.LastUsedAt == nil {
		t.Fatalf("authenticate = %+v, %+v, %v", gotAccount, authenticated, err)
	}
	if _, changed, err := store.RevokePlatformTenantAccessToken(ctx, account.ID, tenant.ID, token.ID); err != nil || !changed {
		t.Fatalf("revoke changed=%v err=%v", changed, err)
	}
	if _, _, err := store.AuthenticatePlatformTenantAccessToken(ctx, api.HashAPIKey(plaintext)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("revoked token authentication err=%v, want not found", err)
	}
}
