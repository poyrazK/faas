package state

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreConnectGrants(t *testing.T) {
	ctx := t.Context()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "grant@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, err := m.CreateAPIKey(ctx, acct.ID, []byte("connect-grant-hash"), api.MCPLoginKeyLabel, []string{api.ScopeAppsRead, api.ScopeDeployWrite})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := m.APIKeyConnectGrant(ctx, key.ID); err != nil || ok {
		t.Fatalf("grant before one was recorded = %v, %v", ok, err)
	}
	if err := m.CreateAPIKeyConnectGrant(ctx, "00000000-0000-0000-0000-000000000000", acct.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("grant for a missing key = %v, want ErrNotFound", err)
	}
	if err := m.CreateAPIKeyConnectGrant(ctx, key.ID, acct.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateAPIKeyConnectGrant(ctx, key.ID, acct.ID); err == nil {
		t.Fatal("a second grant for the key was recorded")
	}
	grant, ok, err := m.APIKeyConnectGrant(ctx, key.ID)
	if err != nil || !ok || grant.KeyID != key.ID || grant.GrantedBy != acct.ID || grant.GrantedAt.IsZero() {
		t.Fatalf("grant = %+v, %v, %v", grant, ok, err)
	}
	if err := m.DeleteAPIKey(ctx, acct.ID, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := m.APIKeyConnectGrant(ctx, key.ID); err != nil || ok {
		t.Fatalf("grant after the key was deleted = %v, %v", ok, err)
	}
}
