package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 226 — account-level quota is checked after the idempotent replay path.
func TestMemPlatformTenantQuotaAndPaging(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "platform-tenant-quota@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	first, created, err := store.CreatePlatformTenant(ctx, account.ID, "customer-1", "Customer 1", 1)
	if err != nil || !created {
		t.Fatalf("first = %+v, %v, %v", first, created, err)
	}
	if replay, created, err := store.CreatePlatformTenant(ctx, account.ID, "customer-1", "Customer 1", 1); err != nil || created || replay.ID != first.ID {
		t.Fatalf("replay at quota = %+v, %v, %v", replay, created, err)
	}
	var quota *state.PlatformTenantQuotaError
	if _, _, err := store.CreatePlatformTenant(ctx, account.ID, "customer-2", "Customer 2", 1); !errors.As(err, &quota) || quota.Limit != 1 {
		t.Fatalf("second at quota = %v", err)
	}
	page, err := store.ListPlatformTenants(ctx, account.ID, 1, 0)
	if err != nil || len(page) != 1 || page[0].ID != first.ID {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	page, err = store.ListPlatformTenants(ctx, account.ID, 1, 1)
	if err != nil || len(page) != 0 {
		t.Fatalf("empty second page = %+v, %v", page, err)
	}
}

func TestMemPlatformTenantLinkHydratesConsumerIdentity(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "platform-tenant-link@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "platform-link", Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, "customer-a", "Customer A")
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "customer-a", "Customer A", 250)
	if err != nil {
		t.Fatal(err)
	}
	if linked, err := store.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil || linked.PlatformTenantID != tenant.ID {
		t.Fatalf("link = %+v, %v", linked, err)
	}
	loaded, err := store.GetAPIConsumerByID(ctx, account.ID, consumer.ID)
	if err != nil || loaded.PlatformTenantID != tenant.ID {
		t.Fatalf("loaded consumer = %+v, %v", loaded, err)
	}
}
