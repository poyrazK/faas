package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type platformTenantConsumerPolicyFixture interface {
	state.PlatformTenantConsumerProvisioningPolicyStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreatePlatformTenant(context.Context, string, string, string, int) (state.PlatformTenant, bool, error)
}

func TestMemPlatformTenantConsumerProvisioningPolicy(t *testing.T) {
	testPlatformTenantConsumerProvisioningPolicy(t, state.NewMemStore())
}

func TestPgPlatformTenantConsumerProvisioningPolicy(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testPlatformTenantConsumerProvisioningPolicy(t, store)
}

func testPlatformTenantConsumerProvisioningPolicy(t *testing.T, store platformTenantConsumerPolicyFixture) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "consumer-policy-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "consumer-policy", "Consumer policy", 100)
	if err != nil {
		t.Fatal(err)
	}

	policy, err := store.GetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID)
	if err != nil || policy.Enabled || policy.MaxConsumers != 0 {
		t.Fatalf("default policy=%+v err=%v", policy, err)
	}
	if _, err := store.SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID, true, 0); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("enabled zero-cap policy error=%v", err)
	}
	if _, err := store.SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID, false, 1); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("disabled nonzero-cap policy error=%v", err)
	}
	policy, err = store.SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID, true, 12)
	if err != nil || !policy.Enabled || policy.MaxConsumers != 12 || policy.UpdatedAt.IsZero() {
		t.Fatalf("enabled policy=%+v err=%v", policy, err)
	}
	unchanged, err := store.SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID, true, 12)
	if err != nil || !unchanged.UpdatedAt.Equal(policy.UpdatedAt) {
		t.Fatalf("idempotent policy update=%+v err=%v", unchanged, err)
	}
	policy, err = store.SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID, false, 0)
	if err != nil || policy.Enabled || policy.MaxConsumers != 0 {
		t.Fatalf("disabled policy=%+v err=%v", policy, err)
	}
	if _, err := store.GetPlatformTenantConsumerProvisioningPolicy(ctx, uuid.NewString(), tenant.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account policy read error=%v", err)
	}
}
