package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestValidatePlatformTenantAppBinding(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "tenant-binding-"+uuid.NewString()+"@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "tenant-binding-" + uuid.NewString()[:8], Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "tenant-other-" + uuid.NewString()[:8], Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "tenant-binding", "Tenant binding", 10)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, "tenant-binding", "Tenant binding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	tenants := state.PlatformTenantStore(store)
	if err := state.ValidatePlatformTenantAppBinding(ctx, tenants, account.ID, tenant.ID, app.ID); err != nil {
		t.Fatalf("active linked tenant rejected: %v", err)
	}
	if err := state.ValidatePlatformTenantAppBinding(ctx, tenants, account.ID, tenant.ID, otherApp.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unlinked app error=%v, want not found", err)
	}
	if _, err := store.SetPlatformTenantStatus(ctx, account.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	if err := state.ValidatePlatformTenantAppBinding(ctx, tenants, account.ID, tenant.ID, app.ID); !errors.Is(err, state.ErrPlatformTenantSuspended) {
		t.Fatalf("suspended tenant error=%v", err)
	}
}
