package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type platformTenantSelfConsumerApplyFixture interface {
	state.PlatformTenantSelfConsumerApplyStore
	state.PlatformTenantConsumerProvisioningPolicyStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreatePlatformTenant(context.Context, string, string, string, int) (state.PlatformTenant, bool, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreateTenantSurfaceIfUnderQuota(context.Context, state.CreateTenantSurfaceParams, api.Limits) (state.TenantSurface, error)
	LinkPlatformTenantSurface(context.Context, string, string, string) (state.TenantSurface, error)
	UpdateTenantSurfaceStatus(context.Context, string, state.SurfaceStatus) error
	CreateAPIConsumer(context.Context, string, string, string, string) (state.APIConsumer, error)
	ListAPIConsumersForApp(context.Context, string, string) ([]state.APIConsumer, error)
}

func TestMemPlatformTenantSelfConsumerApply(t *testing.T) {
	testPlatformTenantSelfConsumerApply(t, state.NewMemStore(), context.Background())
}

func TestPgPlatformTenantSelfConsumerApply(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	testPlatformTenantSelfConsumerApply(t, store, ctx)
}

func testPlatformTenantSelfConsumerApply(t *testing.T, store platformTenantSelfConsumerApplyFixture, ctx context.Context) {
	t.Helper()
	account, err := store.CreateAccount(ctx, "self-consumer-apply-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "self-consumer-apply", "Self consumer apply", 100)
	if err != nil {
		t.Fatal(err)
	}
	makeSurface := func(label string) (state.App, state.TenantSurface) {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "self-consumer-apply-" + uuid.NewString()[:8],
			Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
			AccountID: account.ID, AppID: app.ID, Name: label + "-" + uuid.NewString()[:8],
		}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
			t.Fatal(err)
		}
		if _, err := store.LinkPlatformTenantSurface(ctx, account.ID, tenant.ID, surface.ID); err != nil {
			t.Fatal(err)
		}
		return app, surface
	}
	appA, surfaceA := makeSurface("surface-a")
	appB, surfaceB := makeSurface("surface-b")
	base := state.ApplyPlatformTenantSelfConsumersParams{AccountID: account.ID, TenantID: tenant.ID,
		ExternalRef: "customer-apply-42", Name: "Customer 42", SurfaceIDs: []string{surfaceB.ID, surfaceA.ID}}

	if _, err := store.ApplyPlatformTenantSelfConsumers(ctx, base); !errors.Is(err, state.ErrPlatformTenantConsumerProvisioningDisabled) {
		t.Fatalf("default-disabled policy error=%v", err)
	}
	if _, err := store.SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID, true, 2); err != nil {
		t.Fatal(err)
	}
	plan, err := store.ApplyPlatformTenantSelfConsumers(ctx, state.ApplyPlatformTenantSelfConsumersParams{
		AccountID: account.ID, TenantID: tenant.ID, ExternalRef: base.ExternalRef, Name: base.Name,
		SurfaceIDs: base.SurfaceIDs, DryRun: true,
	})
	if err != nil || plan.Changed || len(plan.Consumers) != 2 {
		t.Fatalf("dry-run result=%+v err=%v", plan, err)
	}
	for _, item := range plan.Consumers {
		if !item.Created || item.Consumer.ID != "" || item.Consumer.ExternalRef != base.ExternalRef {
			t.Errorf("dry-run item exposes or omits unexpected identity: %+v", item)
		}
	}
	for _, app := range []state.App{appA, appB} {
		consumers, err := store.ListAPIConsumersForApp(ctx, account.ID, app.ID)
		if err != nil || len(consumers) != 0 {
			t.Fatalf("dry run mutated app %s consumers=%+v err=%v", app.ID, consumers, err)
		}
	}

	created, err := store.ApplyPlatformTenantSelfConsumers(ctx, base)
	if err != nil || !created.Changed || len(created.Consumers) != 2 {
		t.Fatalf("apply result=%+v err=%v", created, err)
	}
	bySurface := make(map[string]state.APIConsumer, len(created.Consumers))
	for _, item := range created.Consumers {
		if !item.Created || item.Consumer.ID == "" || item.Consumer.PlatformTenantID != tenant.ID || !item.Consumer.Active() {
			t.Errorf("created item=%+v", item)
		}
		bySurface[item.SurfaceID] = item.Consumer
	}
	if bySurface[surfaceA.ID].AppID != appA.ID || bySurface[surfaceB.ID].AppID != appB.ID {
		t.Fatalf("surface-to-app binding was not preserved: %+v", bySurface)
	}

	if _, err := store.SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID, false, 0); err != nil {
		t.Fatal(err)
	}
	replay, err := store.ApplyPlatformTenantSelfConsumers(ctx, base)
	if err != nil || replay.Changed || len(replay.Consumers) != 2 {
		t.Fatalf("replay after policy disable=%+v err=%v", replay, err)
	}
	for _, item := range replay.Consumers {
		if item.Created || item.Consumer.ID != bySurface[item.SurfaceID].ID {
			t.Errorf("replay item=%+v, expected existing identity for surface %s", item, item.SurfaceID)
		}
	}

	// A batch that would need one new identity is gated as a whole. Existing
	// identities remain replayable, but the missing app identity is not added.
	if _, err := store.ApplyPlatformTenantSelfConsumers(ctx, state.ApplyPlatformTenantSelfConsumersParams{
		AccountID: account.ID, TenantID: tenant.ID, ExternalRef: "new-customer", Name: "New customer",
		SurfaceIDs: []string{surfaceA.ID, surfaceB.ID},
	}); !errors.Is(err, state.ErrPlatformTenantConsumerProvisioningDisabled) {
		t.Fatalf("partially gated batch error=%v", err)
	}
	for _, app := range []state.App{appA, appB} {
		consumers, err := store.ListAPIConsumersForApp(ctx, account.ID, app.ID)
		if err != nil || len(consumers) != 1 {
			t.Fatalf("disabled batch partially changed app %s consumers=%+v err=%v", app.ID, consumers, err)
		}
	}
}

func TestMemPlatformTenantSelfConsumerApplyValidatesEntireBatch(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "self-consumer-apply-validation-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "self-consumer-apply-validation", "Validation", 100)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "self-consumer-apply-validation-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	makeSurface := func(label string) state.TenantSurface {
		t.Helper()
		surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
			AccountID: account.ID, AppID: app.ID, Name: label + "-" + uuid.NewString()[:8],
		}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
			t.Fatal(err)
		}
		if _, err := store.LinkPlatformTenantSurface(ctx, account.ID, tenant.ID, surface.ID); err != nil {
			t.Fatal(err)
		}
		return surface
	}
	first, second := makeSurface("first"), makeSurface("second")
	if _, err := store.ApplyPlatformTenantSelfConsumers(ctx, state.ApplyPlatformTenantSelfConsumersParams{
		AccountID: account.ID, TenantID: tenant.ID, ExternalRef: "conflict", Name: "Expected", SurfaceIDs: []string{first.ID, second.ID},
	}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("two surfaces for same app error=%v", err)
	}
	if _, err := store.SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID, true, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyPlatformTenantSelfConsumers(ctx, state.ApplyPlatformTenantSelfConsumersParams{
		AccountID: account.ID, TenantID: tenant.ID, ExternalRef: "conflict", Name: "Expected", SurfaceIDs: []string{first.ID, first.ID},
	}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("duplicate surface error=%v", err)
	}
	tooMany := make([]string, state.MaxPlatformTenantSelfConsumerApplySurfaces+1)
	for i := range tooMany {
		tooMany[i] = uuid.NewString()
	}
	if _, err := store.ApplyPlatformTenantSelfConsumers(ctx, state.ApplyPlatformTenantSelfConsumersParams{
		AccountID: account.ID, TenantID: tenant.ID, ExternalRef: "conflict", Name: "Expected", SurfaceIDs: tooMany,
	}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("oversized batch error=%v", err)
	}
	appB, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "self-consumer-apply-conflict-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	surfaceB, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: account.ID, AppID: appB.ID, Name: "conflict-" + uuid.NewString()[:8],
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTenantSurfaceStatus(ctx, surfaceB.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantSurface(ctx, account.ID, tenant.ID, surfaceB.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIConsumer(ctx, account.ID, appB.ID, "conflict", "Different owner identity"); err != nil {
		t.Fatal(err)
	}
	input := state.ApplyPlatformTenantSelfConsumersParams{AccountID: account.ID, TenantID: tenant.ID,
		ExternalRef: "conflict", Name: "Expected", SurfaceIDs: []string{first.ID, surfaceB.ID}}
	if _, err := store.ApplyPlatformTenantSelfConsumers(ctx, input); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("conflicting existing app identity error=%v", err)
	}
	if consumers, err := store.ListAPIConsumersForApp(ctx, account.ID, app.ID); err != nil || len(consumers) != 0 {
		t.Fatalf("conflicting batch partially created identity: %+v err=%v", consumers, err)
	}
	quotaInput := input
	quotaInput.ExternalRef, quotaInput.Name = "two-new", "Two new"
	var quota *state.PlatformTenantConsumerProvisioningQuotaError
	if _, err := store.ApplyPlatformTenantSelfConsumers(ctx, quotaInput); !errors.As(err, &quota) || quota.Limit != 1 || quota.Observed != 2 {
		t.Fatalf("batch quota error=%v, want limit 1 and observed 2", err)
	}
	if consumers, err := store.ListAPIConsumersForApp(ctx, account.ID, app.ID); err != nil || len(consumers) != 0 {
		t.Fatalf("quota-limited batch partially created identity: %+v err=%v", consumers, err)
	}
}
