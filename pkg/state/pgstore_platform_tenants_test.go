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
	minute := time.Now().UTC().Add(-24 * time.Hour).Truncate(24 * time.Hour).Add(12 * time.Hour)
	preLink := state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: accountID, AppID: appA,
		ConsumerKey: consumerA.ID, WindowStart: minute, RequestCount: 5, BillableUnits: 5}
	if _, err := store.RecordAPIConsumerUsage(ctx, preLink); err != nil {
		t.Fatal(err)
	}
	for _, consumer := range []state.APIConsumer{consumerA, consumerB} {
		if linked, err := store.LinkPlatformTenantConsumer(ctx, accountID, tenant.ID, consumer.ID); err != nil || linked.PlatformTenantID != tenant.ID {
			t.Fatalf("link consumer %s: %v", consumer.ID, err)
		}
		if loaded, err := store.GetAPIConsumerByID(ctx, accountID, consumer.ID); err != nil || loaded.PlatformTenantID != tenant.ID {
			t.Fatalf("hydrate linked consumer %s: %+v, %v", consumer.ID, loaded, err)
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
	for _, event := range []state.APIConsumerUsageEvent{
		{EventID: uuid.NewString(), AccountID: accountID, AppID: appA, ConsumerKey: consumerA.ID,
			PlatformTenantID: tenant.ID, WindowStart: minute, RequestCount: 10, ErrorCount: 2, BillableUnits: 8},
		{EventID: uuid.NewString(), AccountID: accountID, AppID: appA, ConsumerKey: consumerA.ID,
			PlatformTenantID: tenant.ID, WindowStart: minute.Add(time.Minute), RequestCount: 4, ErrorCount: 0, BillableUnits: 3},
		{EventID: uuid.NewString(), AccountID: accountID, AppID: appB, ConsumerKey: consumerB.ID,
			PlatformTenantID: tenant.ID, WindowStart: minute, RequestCount: 7, ErrorCount: 1, BillableUnits: 6},
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
	appUsage, err := store.ListAPIConsumerUsage(ctx, accountID, appA, consumerA.ID, minute, minute.Add(time.Minute))
	if err != nil || len(appUsage) != 1 || appUsage[0].RequestCount != 15 {
		t.Fatalf("app-local ledger must retain pre-link traffic: %+v, %v", appUsage, err)
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

func TestPgPlatformTenantCursorPaginationSurvivesInsert(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, _ := seedConsumerKeyAccountApp(t, ctx, store)
	originalIDs := make(map[string]struct{}, 3)
	for _, externalRef := range []string{"page-a-" + uuid.NewString(), "page-b-" + uuid.NewString(), "page-c-" + uuid.NewString()} {
		tenant, _, err := store.CreatePlatformTenant(ctx, accountID, externalRef, externalRef, 10)
		if err != nil {
			t.Fatal(err)
		}
		originalIDs[tenant.ID] = struct{}{}
	}
	tiedAt := time.Now().UTC().Add(-time.Minute)
	if _, err := pool.Exec(ctx, `update platform_tenants set created_at = $2 where account_id = $1::uuid`, accountID, tiedAt); err != nil {
		t.Fatal(err)
	}

	first, token, err := store.ListPlatformTenantsPage(ctx, accountID, 1, 0, "")
	if err != nil || len(first) != 1 || token == "" {
		t.Fatalf("first page = %+v, token %q, err %v", first, token, err)
	}
	inserted, _, err := store.CreatePlatformTenant(ctx, accountID, "page-new-"+uuid.NewString(), "New customer", 10)
	if err != nil {
		t.Fatal(err)
	}

	second, nextToken, err := store.ListPlatformTenantsPage(ctx, accountID, 1, 0, token)
	if err != nil || len(second) != 1 {
		t.Fatalf("second page = %+v, token %q, err %v", second, nextToken, err)
	}
	if second[0].ID == first[0].ID || second[0].ID == inserted.ID {
		t.Fatalf("cursor page repeated or included post-page insert: first=%s second=%s inserted=%s", first[0].ID, second[0].ID, inserted.ID)
	}
	if _, ok := originalIDs[second[0].ID]; !ok {
		t.Fatalf("cursor returned tenant outside original list: %s", second[0].ID)
	}

	if _, _, err := store.ListPlatformTenantsPage(ctx, accountID, 1, 1, token); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("offset with cursor = %v, want invalid argument", err)
	}
	if _, _, err := store.ListPlatformTenantsPage(ctx, accountID, 1, 0, "not-a-cursor"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("malformed cursor = %v, want invalid argument", err)
	}
}
