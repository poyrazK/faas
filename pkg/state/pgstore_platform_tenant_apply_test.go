package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgPlatformTenantApplyIsAtomicAndRetrySafe(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appA := seedConsumerKeyAccountApp(t, ctx, store)
	app, err := store.CreateApp(ctx, state.App{AccountID: accountID,
		Slug: "apply-b-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	appB := app.ID
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: accountID, AppID: appA, Name: "apply-" + uuid.NewString()[:8]}, api.MustLimitsFor(api.PlanHobby))
	if err != nil {
		t.Fatal(err)
	}
	in := state.ApplyPlatformTenantParams{AccountID: accountID, ExternalRef: "apply-" + uuid.NewString(),
		Name: "Customer", TenantLimit: 250,
		Consumers: []state.ApplyPlatformTenantConsumer{
			{AppID: appA, ExternalRef: "customer", Name: "Customer"},
			{AppID: appB, ExternalRef: "customer", Name: "Customer"}},
		SurfaceIDs: []string{surface.ID}, DryRun: true}
	preview, err := store.ApplyPlatformTenant(ctx, in)
	if err != nil || preview.Action != "create" || preview.Tenant.ID != "" {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if consumers, err := store.ListAPIConsumersForApp(ctx, accountID, appA); err != nil || len(consumers) != 0 {
		t.Fatalf("dry run wrote consumer: %+v, %v", consumers, err)
	}
	in.DryRun = false
	created, err := store.ApplyPlatformTenant(ctx, in)
	if err != nil || created.Tenant.ID == "" || created.Consumers[0].Consumer.ID == "" {
		t.Fatalf("apply = %+v, %v", created, err)
	}
	replay, err := store.ApplyPlatformTenant(ctx, in)
	if err != nil || replay.Tenant.ID != created.Tenant.ID || replay.Action != "unchanged" ||
		replay.Consumers[0].Action != "unchanged" || replay.Surfaces[0].Action != "unchanged" {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	other, _, err := store.CreatePlatformTenant(ctx, accountID, "other-"+uuid.NewString(), "Other", 250)
	if err != nil {
		t.Fatal(err)
	}
	otherConsumer, err := store.CreateAPIConsumer(ctx, accountID, appB, "other", "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, accountID, other.ID, otherConsumer.ID); err != nil {
		t.Fatal(err)
	}
	failed := in
	failed.ExternalRef = "failed-" + uuid.NewString()
	failed.Consumers = []state.ApplyPlatformTenantConsumer{
		{AppID: appA, ExternalRef: "new", Name: "New"},
		{AppID: appB, ExternalRef: "other", Name: "Other"}}
	failed.SurfaceIDs = nil
	if _, err := store.ApplyPlatformTenant(ctx, failed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("conflicting bundle = %v", err)
	}
	if consumers, err := store.ListAPIConsumersForApp(ctx, accountID, appA); err != nil || len(consumers) != 1 {
		t.Fatalf("conflict wrote first consumer: %+v, %v", consumers, err)
	}
	if tenants, err := store.ListPlatformTenants(ctx, accountID, 100, 0); err != nil || len(tenants) != 2 {
		t.Fatalf("conflict wrote tenant: %+v, %v", tenants, err)
	}
}
