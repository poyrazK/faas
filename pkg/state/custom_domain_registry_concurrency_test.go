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

func domainRegistryStores() map[string]func(*testing.T) (tlsHostTestStore, context.Context) {
	stores := tlsHostStores()
	stores["pg_scoped"] = func(t *testing.T) (tlsHostTestStore, context.Context) {
		_, pool, ctx := pgStoreWithPool(t)
		return state.NewPgStore(pool, state.WithTrafficScopedBindingGuard(true)), ctx
	}
	return stores
}

func TestCustomDomainPublicRegistriesSerializeOverlappingReservations(t *testing.T) {
	for name, open := range domainRegistryStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			store := raw.(state.Store)
			first, other := tlsHostApp(t, ctx, raw, ""), tlsHostApp(t, ctx, raw, "")
			app, err := store.AppByID(ctx, other)
			if err != nil {
				t.Fatal(err)
			}
			limits, _ := api.LimitsFor(api.PlanPro)
			surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: app.AccountID, AppID: other, Name: "registry-race"}, limits)
			if err != nil {
				t.Fatal(err)
			}
			creator := raw.(interface {
				CreateCustomDomainIfUnderQuota(context.Context, string, string, string, int, int) (state.CustomDomain, error)
			})
			for i := range 4 {
				zone := "registry-" + uuid.NewString()[:8] + ".example.com"
				start := make(chan struct{})
				results := make(chan error, 2)
				go func() {
					<-start
					_, err := creator.CreateCustomDomainIfUnderQuota(state.WithPublicCustomDomainClaim(ctx), "*."+zone, first, "proof", 100, 500)
					results <- err
				}()
				go func() {
					<-start
					_, err := store.CreateTenantHostnameIfUnderQuota(state.WithPublicTenantHostnameClaim(ctx), state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: "tenant." + zone, ChallengeToken: "tenant-proof"}, limits)
					results <- err
				}()
				close(start)
				a, b := <-results, <-results
				var overlap *state.CustomDomainTenantOverlapError
				if (a == nil) == (b == nil) || a != nil && !errors.As(a, &overlap) || b != nil && !errors.As(b, &overlap) {
					t.Fatalf("iteration %d: wanted one publication and one overlap refusal, got %v / %v", i, a, b)
				}
			}
		})
	}
}

func TestCustomDomainTenantPublicationRejectsExistingWildcard(t *testing.T) {
	for name, open := range domainRegistryStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			appID := tlsHostApp(t, ctx, raw, "")
			app, err := store.AppByID(ctx, appID)
			if err != nil {
				t.Fatal(err)
			}
			limits, _ := api.LimitsFor(api.PlanPro)
			surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: app.AccountID, AppID: app.ID, Name: "tenant-after-wildcard"}, limits)
			if err != nil {
				t.Fatal(err)
			}
			for _, verified := range []bool{false, true} {
				zone := "existing-" + uuid.NewString()[:8] + ".example.com"
				if _, err := store.CreateCustomDomain(ctx, "*."+zone, appID, "proof"); err != nil {
					t.Fatal(err)
				}
				if verified {
					if err := store.MarkDomainVerified(ctx, "*."+zone); err != nil {
						t.Fatal(err)
					}
				}
				_, err := store.CreateTenantHostnameIfUnderQuota(state.WithPublicTenantHostnameClaim(ctx), state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: "nested.tenant." + zone, ChallengeToken: "proof"}, limits)
				var overlap *state.CustomDomainTenantOverlapError
				if !errors.As(err, &overlap) || overlap.Wildcard != "*."+zone {
					t.Fatalf("verified=%v: %v", verified, err)
				}
			}
		})
	}
}

func TestCustomDomainIntentRejectsRecreatedClaim(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			intents := raw.(interface {
				SetDefaultCustomDomain(context.Context, string, string) error
				RetryCustomDomainVerification(context.Context, string) error
				DeleteCustomDomainForApp(context.Context, string, string) error
			})
			app := tlsHostApp(t, ctx, raw, "")
			host := "intent-" + uuid.NewString()[:8] + ".example.com"
			old, err := store.CreateCustomDomain(ctx, host, app, "old-proof")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.DeleteCustomDomain(ctx, host); err != nil {
				t.Fatal(err)
			}
			current, err := store.CreateCustomDomain(ctx, host, app, "new-proof")
			if err != nil {
				t.Fatal(err)
			}
			stale := state.WithCustomDomainProbe(ctx, old)
			if err := intents.RetryCustomDomainVerification(stale, host); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("stale retry: %v", err)
			}
			if err := store.MarkDomainVerified(ctx, host); err != nil {
				t.Fatal(err)
			}
			if err := intents.SetDefaultCustomDomain(stale, app, host); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("stale default: %v", err)
			}
			if err := intents.DeleteCustomDomainForApp(stale, host, app); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("stale delete: %v", err)
			}
			if err := raw.(state.CustomDomainVerificationCheckStore).RecordCustomDomainVerificationCheck(ctx, old, time.Now()); !errors.Is(err, state.ErrStaleCustomDomainProbe) {
				t.Fatalf("stale verification check: %v", err)
			}
			bound := state.WithCustomDomainProbe(ctx, current)
			if err := intents.SetDefaultCustomDomain(bound, app, host); err != nil {
				t.Fatalf("current claim default: %v", err)
			}
			if err := intents.DeleteCustomDomainForApp(bound, host, app); err != nil {
				t.Fatal(err)
			}
		})
	}
}
