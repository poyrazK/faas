package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 226 — account-scoped tenant links preserve ownership and suspension.
func TestPgPlatformTenantCrossAppLifecycle(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appA := seedConsumerKeyAccountApp(t, ctx, store)
	app, err := store.CreateApp(ctx, state.App{AccountID: accountID, Slug: "platform-tenant-b-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	appB := app.ID
	tenant, created, err := store.CreatePlatformTenant(ctx, accountID, "customer-42", "Customer 42", 250)
	if err != nil || !created {
		t.Fatalf("create tenant = %+v, %v, %v", tenant, created, err)
	}
	if replay, created, err := store.CreatePlatformTenant(ctx, accountID, "customer-42", "Customer 42", 250); err != nil || created || replay.ID != tenant.ID {
		t.Fatalf("replay tenant = %+v, %v, %v", replay, created, err)
	}
	if _, _, err := store.CreatePlatformTenant(ctx, accountID, "customer-42", "Other", 250); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("conflicting external ref = %v", err)
	}
	consumerA, err := store.CreateAPIConsumer(ctx, accountID, appA, "customer-42", "Customer 42")
	if err != nil {
		t.Fatal(err)
	}
	consumerB, err := store.CreateAPIConsumer(ctx, accountID, appB, "customer-42", "Customer 42")
	if err != nil {
		t.Fatal(err)
	}
	for _, consumer := range []state.APIConsumer{consumerA, consumerB} {
		if _, err := store.LinkPlatformTenantConsumer(ctx, accountID, tenant.ID, consumer.ID); err != nil {
			t.Fatalf("link consumer %s: %v", consumer.ID, err)
		}
	}
	_, prefix, hash, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateConsumerKeyForConsumer(ctx, accountID, consumerA.ID, "primary", prefix, hash, []string{"read"}, nil); err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanHobby)
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: accountID, AppID: appA, Name: "customer-42-surface",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantSurface(ctx, accountID, tenant.ID, surface.ID); err != nil {
		t.Fatalf("link surface: %v", err)
	}
	minute := time.Now().UTC().Add(-24 * time.Hour).Truncate(24 * time.Hour).Add(12 * time.Hour)
	for _, event := range []state.APIConsumerUsageEvent{
		{EventID: uuid.NewString(), AccountID: accountID, AppID: appA, ConsumerKey: consumerA.ID,
			WindowStart: minute, RequestCount: 10, ErrorCount: 2, BillableUnits: 8},
		{EventID: uuid.NewString(), AccountID: accountID, AppID: appA, ConsumerKey: consumerA.ID,
			WindowStart: minute.Add(time.Minute), RequestCount: 4, ErrorCount: 0, BillableUnits: 3},
		{EventID: uuid.NewString(), AccountID: accountID, AppID: appB, ConsumerKey: consumerB.ID,
			WindowStart: minute, RequestCount: 7, ErrorCount: 1, BillableUnits: 6},
	} {
		if _, err := store.RecordAPIConsumerUsage(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.ListPlatformTenantUsage(ctx, accountID, tenant.ID, minute, minute.Add(2*time.Minute))
	if err != nil || len(rows) != 2 || rows[0].RequestCount+rows[1].RequestCount != 21 {
		t.Fatalf("usage = %+v, %v", rows, err)
	}
	var dailyA state.APIConsumerUsageBucket
	for _, row := range rows {
		if row.ConsumerKey == consumerA.ID {
			dailyA = row
		}
	}
	if dailyA.RequestCount != 14 || !dailyA.WindowStart.Equal(minute.Truncate(24*time.Hour)) {
		t.Fatalf("daily consumer aggregation = %+v", dailyA)
	}
	if _, err := store.SetPlatformTenantStatus(ctx, accountID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumerKeyByAppAndPrefix(ctx, accountID, appA, prefix); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("suspended key = %v", err)
	}
	if suspended, err := store.PlatformTenantSurfaceSuspended(ctx, surface.ID); err != nil || !suspended {
		t.Fatalf("suspended surface = %v, %v", suspended, err)
	}
	_, anotherPrefix, anotherHash, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateConsumerKeyForConsumer(ctx, accountID, consumerB.ID, "blocked", anotherPrefix, anotherHash, []string{"read"}, nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("new key on suspended tenant = %v", err)
	}
	if _, err := store.SetPlatformTenantStatus(ctx, accountID, tenant.ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumerKeyByAppAndPrefix(ctx, accountID, appA, prefix); err != nil {
		t.Fatalf("resumed key: %v", err)
	}
	if suspended, err := store.PlatformTenantSurfaceSuspended(ctx, surface.ID); err != nil || suspended {
		t.Fatalf("resumed surface = %v, %v", suspended, err)
	}
	if _, err := store.GetPlatformTenant(ctx, uuid.NewString(), tenant.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account read = %v", err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, uuid.NewString(), tenant.ID, consumerA.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account link = %v", err)
	}
}
