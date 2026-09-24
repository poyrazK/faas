package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
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
	minute := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	preLink := state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: account.ID,
		AppID: app.ID, ConsumerKey: consumer.ID, WindowStart: minute,
		RequestCount: 5, BillableUnits: 5}
	if _, err := store.RecordAPIConsumerUsage(ctx, preLink); err != nil {
		t.Fatal(err)
	}
	if linked, err := store.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil || linked.PlatformTenantID != tenant.ID {
		t.Fatalf("link = %+v, %v", linked, err)
	}
	loaded, err := store.GetAPIConsumerByID(ctx, account.ID, consumer.ID)
	if err != nil || loaded.PlatformTenantID != tenant.ID {
		t.Fatalf("loaded consumer = %+v, %v", loaded, err)
	}
	if rows, err := store.ListPlatformTenantUsage(ctx, account.ID, tenant.ID, minute, minute.Add(time.Minute)); err != nil || len(rows) != 0 {
		t.Fatalf("pre-link traffic was retroactively attributed: %+v, %v", rows, err)
	}
	linked := preLink
	linked.EventID = uuid.NewString()
	linked.PlatformTenantID = tenant.ID
	linked.RequestCount, linked.BillableUnits = 2, 2
	if applied, err := store.RecordAPIConsumerUsage(ctx, linked); err != nil || !applied {
		t.Fatalf("linked usage = %v, %v", applied, err)
	}
	if applied, err := store.RecordAPIConsumerUsage(ctx, linked); err != nil || applied {
		t.Fatalf("replayed usage = %v, %v", applied, err)
	}
	if rows, err := store.ListPlatformTenantUsage(ctx, account.ID, tenant.ID, minute, minute.Add(time.Minute)); err != nil || len(rows) != 1 || rows[0].RequestCount != 2 {
		t.Fatalf("immutable tenant usage = %+v, %v", rows, err)
	}
	foreignAccount, err := store.CreateAccount(ctx, "platform-tenant-foreign@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignTenant, _, err := store.CreatePlatformTenant(ctx, foreignAccount.ID, "foreign", "Foreign", 250)
	if err != nil {
		t.Fatal(err)
	}
	foreign := linked
	foreign.EventID = uuid.NewString()
	foreign.PlatformTenantID = foreignTenant.ID
	if applied, err := store.RecordAPIConsumerUsage(ctx, foreign); !errors.Is(err, state.ErrNotFound) || applied {
		t.Fatalf("cross-account attribution = %v, %v", applied, err)
	}
	if rows, err := store.ListPlatformTenantUsage(ctx, account.ID, tenant.ID, minute, minute.Add(time.Minute)); err != nil || len(rows) != 1 || rows[0].RequestCount != 2 {
		t.Fatalf("cross-account event changed usage: %+v, %v", rows, err)
	}
}
