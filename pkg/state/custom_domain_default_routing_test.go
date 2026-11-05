// adr: 167
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type defaultDomainSelectionStore interface {
	state.DefaultCustomDomainStore
	state.DefaultCustomDomainBatchReader
	SetDefaultCustomDomain(context.Context, string, string) error
	IsDefaultCustomDomain(context.Context, string, string) (bool, error)
}

func assertDefaultDomainSelection(t *testing.T, ctx context.Context, store defaultDomainSelectionStore, appID, host string, usable bool) {
	t.Helper()
	domain, err := store.DefaultCustomDomain(ctx, appID)
	if usable && (err != nil || domain != host) || !usable && !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("single default = %q, %v; usable=%v", domain, err, usable)
	}
	domains, err := store.DefaultCustomDomains(ctx, []string{appID})
	if err != nil || usable && domains[appID] != host || !usable && domains[appID] != "" {
		t.Fatalf("batch defaults = %v, %v; usable=%v", domains, err, usable)
	}
	selected, err := store.IsDefaultCustomDomain(ctx, appID, host)
	if err != nil || selected != usable {
		t.Fatalf("default flag = %v, %v; want %v", selected, err, usable)
	}
}

func TestCustomDomainDefaultFollowsTenantRouting(t *testing.T) {
	for name, open := range tlsHostStores() {
		for _, sameApp := range []bool{false, true} {
			t.Run(name+map[bool]string{true: "/same-app", false: "/other-app"}[sameApp], func(t *testing.T) {
				raw, baseCtx := open(t)
				ctx := state.WithCustomDomainRouting(baseCtx, true)
				legacy := state.WithCustomDomainRouting(baseCtx, false)
				store := raw.(state.Store)
				defaults := raw.(defaultDomainSelectionStore)
				tenants := raw.(state.PlatformTenantStore)
				appID := tlsHostApp(t, ctx, raw, "")
				app, err := store.AppByID(ctx, appID)
				if err != nil {
					t.Fatal(err)
				}
				target := app
				if !sameApp {
					target, err = store.CreateApp(ctx, state.App{AccountID: app.AccountID, Slug: "target-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128})
					if err != nil {
						t.Fatal(err)
					}
				}
				host := "canonical-" + uuid.NewString()[:8] + ".example.com"
				fallback := "fallback-" + uuid.NewString()[:8] + ".example.com"
				verifiedTLSWildcard(t, ctx, raw, host, appID)
				verifiedTLSWildcard(t, ctx, raw, fallback, appID)
				if err = defaults.SetDefaultCustomDomain(ctx, appID, host); err != nil {
					t.Fatal(err)
				}
				limits := api.MustLimitsFor(api.PlanPro)
				surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: app.AccountID, AppID: target.ID, Name: "canonical"}, limits)
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.CreateTenantHostnameIfUnderQuota(state.WithPublicTenantHostnameClaim(ctx), state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: host, ChallengeToken: "tenant-proof"}, limits)
				if err != nil {
					t.Fatal(err)
				}
				tenant, _, err := tenants.CreatePlatformTenant(ctx, app.AccountID, "canonical", "Canonical tenant", 100)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = tenants.LinkPlatformTenantSurface(ctx, app.AccountID, tenant.ID, surface.ID); err != nil {
					t.Fatal(err)
				}
				if err = store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
					t.Fatal(err)
				}
				if _, err = tenants.SetPlatformTenantStatus(ctx, app.AccountID, tenant.ID, state.PlatformTenantSuspended); err != nil {
					t.Fatal(err)
				}
				// Unproved tenant reservations never suppress an existing domain.
				assertDefaultDomainSelection(t, ctx, defaults, appID, host, true)
				if err = store.MarkTenantHostnameVerified(ctx, host); err != nil {
					t.Fatal(err)
				}
				assertDefaultDomainSelection(t, ctx, defaults, appID, host, false)
				assertDefaultDomainSelection(t, legacy, defaults, appID, host, true)
				// A rejected selection must not overwrite an unrelated usable default.
				if err = defaults.SetDefaultCustomDomain(ctx, appID, fallback); err != nil {
					t.Fatal(err)
				}
				if err = defaults.SetDefaultCustomDomain(ctx, appID, host); !errors.Is(err, state.ErrCustomDomainNotRoutable) {
					t.Fatalf("suspended tenant default accepted: %v", err)
				}
				assertDefaultDomainSelection(t, ctx, defaults, appID, fallback, true)
				if err = defaults.SetDefaultCustomDomain(legacy, appID, host); err != nil {
					t.Fatal(err)
				}
				if _, err = tenants.SetPlatformTenantStatus(ctx, app.AccountID, tenant.ID, state.PlatformTenantActive); err != nil {
					t.Fatal(err)
				}
				assertDefaultDomainSelection(t, ctx, defaults, appID, host, sameApp)
				err = defaults.SetDefaultCustomDomain(ctx, appID, host)
				if sameApp && err != nil || !sameApp && !errors.Is(err, state.ErrCustomDomainNotRoutable) {
					t.Fatalf("active tenant default (same app=%v): %v", sameApp, err)
				}
				// Inactive surfaces keep legacy fallback; tenant suspension still wins.
				if err = store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusSuspended); err != nil {
					t.Fatal(err)
				}
				assertDefaultDomainSelection(t, ctx, defaults, appID, host, true)
				if _, err = tenants.SetPlatformTenantStatus(ctx, app.AccountID, tenant.ID, state.PlatformTenantSuspended); err != nil {
					t.Fatal(err)
				}
				assertDefaultDomainSelection(t, ctx, defaults, appID, host, false)
				if err = store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusDeleted); err != nil {
					t.Fatal(err)
				}
				assertDefaultDomainSelection(t, ctx, defaults, appID, host, true)
			})
		}
	}
}
