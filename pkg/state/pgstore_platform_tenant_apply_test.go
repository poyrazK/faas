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

func TestPgPlatformTenantApplyCreatesHostnameIntentAtomically(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	hostname := "tenant-" + uuid.NewString()[:8] + ".example.test"
	in := state.ApplyPlatformTenantParams{AccountID: accountID, ExternalRef: "domain-" + uuid.NewString(),
		Name: "Domain Customer", TenantLimit: 250, Limits: api.MustLimitsFor(api.PlanPro), DryRun: true,
		Surfaces: []state.ApplyPlatformTenantSurface{{AppID: appID, Name: "domain-" + uuid.NewString()[:8],
			CertKind: state.CertKindPerHostSAN, Hostnames: []state.ApplyPlatformTenantHostname{{Hostname: hostname, ChallengeToken: "challenge-token"}}}}}
	preview, err := store.ApplyPlatformTenant(ctx, in)
	if err != nil || preview.Surfaces[0].Action != "create" || preview.Surfaces[0].Surface.ID != "" {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if count, err := store.CountTenantSurfacesForAccount(ctx, accountID); err != nil || count != 0 {
		t.Fatalf("dry-run surface count = %d, %v", count, err)
	}
	in.DryRun = false
	applied, err := store.ApplyPlatformTenant(ctx, in)
	if err != nil || applied.Surfaces[0].Surface.ID == "" || applied.Surfaces[0].Hostnames[0].Hostname.ChallengeToken != "challenge-token" {
		t.Fatalf("apply = %+v, %v", applied, err)
	}
	replay, err := store.ApplyPlatformTenant(ctx, in)
	if err != nil || replay.Surfaces[0].Action != "unchanged" || replay.Surfaces[0].Hostnames[0].Action != "unchanged" ||
		replay.Surfaces[0].Surface.ID != applied.Surfaces[0].Surface.ID {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	if replay.Surfaces[0].Hostnames[0].Hostname.Verified() {
		t.Fatal("unverified SQL hostname scanned as verified")
	}
	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, `listen tenant_surface_changed`); err != nil {
		t.Fatal(err)
	}
	in.Surfaces[0].Hostnames = append(in.Surfaces[0].Hostnames,
		state.ApplyPlatformTenantHostname{Hostname: "extra-" + uuid.NewString()[:8] + ".example.test", ChallengeToken: "extra-token"})
	if _, err := store.ApplyPlatformTenant(ctx, in); err != nil {
		t.Fatal(err)
	}
	listenCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	notice, err := listener.Conn().WaitForNotification(listenCtx)
	if err != nil || notice == nil || notice.Payload != applied.Surfaces[0].Surface.ID {
		t.Fatalf("hostname notify = %+v, %v; want surface ID", notice, err)
	}
	// A global hostname collision must leave neither the new tenant nor its surface.
	failed := in
	failed.ExternalRef = "domain-" + uuid.NewString()
	failed.Surfaces = []state.ApplyPlatformTenantSurface{{AppID: appID, Name: "other-" + uuid.NewString()[:8],
		CertKind: state.CertKindPerHostSAN, Hostnames: in.Surfaces[0].Hostnames}}
	if _, err := store.ApplyPlatformTenant(ctx, failed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("hostname collision = %v", err)
	}
	if count, err := store.CountTenantSurfacesForAccount(ctx, accountID); err != nil || count != 1 {
		t.Fatalf("collision surface count = %d, %v", count, err)
	}
	if tenants, err := store.ListPlatformTenants(ctx, accountID, 100, 0); err != nil || len(tenants) != 1 {
		t.Fatalf("collision tenants = %+v, %v", tenants, err)
	}
}
