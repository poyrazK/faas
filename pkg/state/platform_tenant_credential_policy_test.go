package state_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type platformTenantCredentialPolicyFixture interface {
	state.PlatformTenantCredentialPolicyStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreatePlatformTenant(context.Context, string, string, string, int) (state.PlatformTenant, bool, error)
}

func TestMemPlatformTenantCredentialPolicyRoundTrip(t *testing.T) {
	testPlatformTenantCredentialPolicyRoundTrip(t, state.NewMemStore())
}

func TestPgPlatformTenantCredentialPolicyRoundTrip(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testPlatformTenantCredentialPolicyRoundTrip(t, store)
}

func testPlatformTenantCredentialPolicyRoundTrip(t *testing.T, store platformTenantCredentialPolicyFixture) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "credential-policy-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "credential-policy", "Credential policy", 250)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := store.GetPlatformTenantCredentialPolicy(ctx, account.ID, tenant.ID)
	if err != nil || initial.TenantID != tenant.ID || len(initial.AllowedScopes) != 0 || initial.MaxKeysPerConsumer != 0 {
		t.Fatalf("default policy=%+v err=%v", initial, err)
	}
	wantScopes := []string{"read", "write"}
	configured, err := store.SetPlatformTenantCredentialPolicy(ctx, account.ID, tenant.ID, wantScopes, 5)
	if err != nil {
		t.Fatal(err)
	}
	if configured.MaxKeysPerConsumer != 5 || configured.UpdatedAt.IsZero() || !reflect.DeepEqual(configured.AllowedScopes, wantScopes) {
		t.Fatalf("configured policy=%+v", configured)
	}
	replay, err := store.SetPlatformTenantCredentialPolicy(ctx, account.ID, tenant.ID, wantScopes, 5)
	if err != nil || !replay.UpdatedAt.Equal(configured.UpdatedAt) {
		t.Fatalf("idempotent policy replay=%+v err=%v", replay, err)
	}
	if _, err := store.SetPlatformTenantCredentialPolicy(ctx, account.ID, tenant.ID, nil, 0); err != nil {
		t.Fatalf("disable policy: %v", err)
	}
	foreign, err := store.CreateAccount(ctx, "credential-policy-foreign-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPlatformTenantCredentialPolicy(ctx, foreign.ID, tenant.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account read error=%v", err)
	}
	if _, err := store.SetPlatformTenantCredentialPolicy(ctx, foreign.ID, tenant.ID, wantScopes, 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account write error=%v", err)
	}
}

func TestPlatformTenantCredentialPolicyRejectsInvalidInput(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "credential-policy-invalid-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "credential-policy-invalid", "Invalid credential policy", 250)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		scopes []string
		limit  int
	}{
		{name: "empty scopes require zero limit", scopes: nil, limit: 1},
		{name: "scopes require positive limit", scopes: []string{"read"}, limit: 0},
		{name: "unknown scope", scopes: []string{"secrets:write"}, limit: 1},
		{name: "duplicate scope", scopes: []string{"read", "read"}, limit: 1},
		{name: "too many scopes", scopes: []string{"read", "write", "admin", "extra"}, limit: 1},
		{name: "limit too large", scopes: []string{"read"}, limit: api.MaxPlatformTenantKeysPerConsumer + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.SetPlatformTenantCredentialPolicy(ctx, account.ID, tenant.ID, tc.scopes, tc.limit); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("SetPlatformTenantCredentialPolicy error=%v, want ErrInvalidArgument", err)
			}
		})
	}
}
