package state_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type platformTenantSelfConsumerFixture interface {
	state.PlatformTenantSelfConsumerProvisioningStore
	state.PlatformTenantConsumerProvisioningPolicyStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreatePlatformTenant(context.Context, string, string, string, int) (state.PlatformTenant, bool, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreateTenantSurfaceIfUnderQuota(context.Context, state.CreateTenantSurfaceParams, api.Limits) (state.TenantSurface, error)
	LinkPlatformTenantSurface(context.Context, string, string, string) (state.TenantSurface, error)
	UpdateTenantSurfaceStatus(context.Context, string, state.SurfaceStatus) error
	SetPlatformTenantStatus(context.Context, string, string, string) (state.PlatformTenant, error)
}

func TestMemPlatformTenantSelfConsumerProvisioning(t *testing.T) {
	testPlatformTenantSelfConsumerProvisioning(t, state.NewMemStore(), context.Background())
}

func TestPgPlatformTenantSelfConsumerProvisioning(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	testPlatformTenantSelfConsumerProvisioning(t, store, ctx)
}

func testPlatformTenantSelfConsumerProvisioning(t *testing.T, store platformTenantSelfConsumerFixture, ctx context.Context) {
	t.Helper()
	account, err := store.CreateAccount(ctx, "self-consumer-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "self-consumer", "Self consumer", 100)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "self-consumer-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	makeSurface := func(name string, linked bool) state.TenantSurface {
		t.Helper()
		surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
			AccountID: account.ID, AppID: app.ID, Name: name,
		}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
			t.Fatal(err)
		}
		if linked {
			if _, err := store.LinkPlatformTenantSurface(ctx, account.ID, tenant.ID, surface.ID); err != nil {
				t.Fatal(err)
			}
		}
		return surface
	}
	surfaceA := makeSurface("surface-a-"+uuid.NewString()[:8], true)
	surfaceB := makeSurface("surface-b-"+uuid.NewString()[:8], true)
	foreignSurface := makeSurface("surface-unlinked-"+uuid.NewString()[:8], false)
	input := state.CreatePlatformTenantSelfConsumerParams{AccountID: account.ID, TenantID: tenant.ID,
		SurfaceID: surfaceA.ID, ExternalRef: "customer-1", Name: "Customer 1"}

	if _, _, err := store.CreatePlatformTenantSelfConsumer(ctx, input); !errors.Is(err, state.ErrPlatformTenantConsumerProvisioningDisabled) {
		t.Fatalf("default-disabled policy error=%v", err)
	}
	if _, err := store.SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID, true, 1); err != nil {
		t.Fatal(err)
	}
	created, wasCreated, err := store.CreatePlatformTenantSelfConsumer(ctx, input)
	if err != nil || !wasCreated || created.PlatformTenantID != tenant.ID || created.AppID != app.ID || !created.Active() {
		t.Fatalf("create consumer=%+v created=%v err=%v", created, wasCreated, err)
	}
	replay, wasCreated, err := store.CreatePlatformTenantSelfConsumer(ctx, input)
	if err != nil || wasCreated || replay.ID != created.ID {
		t.Fatalf("replay consumer=%+v created=%v err=%v", replay, wasCreated, err)
	}
	var quota *state.PlatformTenantConsumerProvisioningQuotaError
	second := input
	second.SurfaceID, second.ExternalRef, second.Name = surfaceB.ID, "customer-2", "Customer 2"
	if _, _, err := store.CreatePlatformTenantSelfConsumer(ctx, second); !errors.As(err, &quota) || quota.Limit != 1 || quota.Observed != 1 {
		t.Fatalf("second customer quota error=%v", err)
	}
	foreign := input
	foreign.SurfaceID, foreign.ExternalRef = foreignSurface.ID, "foreign-surface-customer"
	if _, _, err := store.CreatePlatformTenantSelfConsumer(ctx, foreign); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unlinked surface error=%v", err)
	}
	if _, err := store.SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID, false, 0); err != nil {
		t.Fatal(err)
	}
	if replay, wasCreated, err := store.CreatePlatformTenantSelfConsumer(ctx, input); err != nil || wasCreated || replay.ID != created.ID {
		t.Fatalf("replay after disable=%+v created=%v err=%v", replay, wasCreated, err)
	}
	if _, _, err := store.CreatePlatformTenantSelfConsumer(ctx, second); !errors.Is(err, state.ErrPlatformTenantConsumerProvisioningDisabled) {
		t.Fatalf("disabled policy error=%v", err)
	}
	if _, err := store.SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID, true, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPlatformTenantStatus(ctx, account.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreatePlatformTenantSelfConsumer(ctx, second); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("suspended tenant error=%v", err)
	}
	invalid := input
	invalid.Name = " padded "
	if _, _, err := store.CreatePlatformTenantSelfConsumer(ctx, invalid); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid name error=%v", err)
	}
}

func TestMemPlatformTenantSelfConsumerProvisioningSerializesCap(t *testing.T) {
	testPlatformTenantSelfConsumerProvisioningSerializesCap(t, state.NewMemStore(), context.Background())
}

func TestPgPlatformTenantSelfConsumerProvisioningSerializesCap(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	testPlatformTenantSelfConsumerProvisioningSerializesCap(t, store, ctx)
}

func testPlatformTenantSelfConsumerProvisioningSerializesCap(t *testing.T, store platformTenantSelfConsumerFixture, ctx context.Context) {
	t.Helper()
	account, err := store.CreateAccount(ctx, "self-consumer-race-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "self-consumer-race", "Race", 100)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "self-consumer-race-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: account.ID, AppID: app.ID, Name: "surface-" + uuid.NewString()[:8],
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
	if _, err := store.SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID, true, 1); err != nil {
		t.Fatal(err)
	}
	const attempts = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	created, limited := 0, 0
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			input := state.CreatePlatformTenantSelfConsumerParams{AccountID: account.ID, TenantID: tenant.ID,
				SurfaceID: surface.ID, ExternalRef: fmt.Sprintf("customer-%d", i), Name: fmt.Sprintf("Customer %d", i)}
			_, wasCreated, err := store.CreatePlatformTenantSelfConsumer(ctx, input)
			mu.Lock()
			defer mu.Unlock()
			if err == nil && wasCreated {
				created++
			} else if errors.As(err, new(*state.PlatformTenantConsumerProvisioningQuotaError)) {
				limited++
			} else {
				t.Errorf("create attempt %d: created=%v err=%v", i, wasCreated, err)
			}
		}(i)
	}
	wg.Wait()
	if created != 1 || limited != attempts-1 {
		t.Fatalf("created=%d quota-limited=%d, want 1 and %d", created, limited, attempts-1)
	}
}
