// adr: 375
package state

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
)

var trafficTenantActivationModes = []string{"plain", "challenge", "surface", "tenant", "link", "namespace", "uppercase", "unchanged", "stale", "canceled", "foreign"}

func trafficTenantClaim(t *testing.T, store Store, account Account, app App, hostname string) (TenantSurface, TenantHostname) {
	t.Helper()
	limits := api.MustLimitsFor(account.Plan)
	surface, err := store.CreateTenantSurfaceIfUnderQuota(t.Context(), CreateTenantSurfaceParams{AccountID: account.ID, AppID: app.ID, Name: "traffic-tenant"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	host, err := store.CreateTenantHostnameIfUnderQuota(t.Context(), CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: hostname, ChallengeToken: "current-private-tenant-token"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	return surface, host
}

func testTrafficTenantActivation(t *testing.T, store Store, account Account, app App, mode string, seed func(EdgeRule)) {
	t.Helper()
	hostname := "tenant-publication.example.test"
	if mode == "namespace" {
		hostname = hostidentity.BuildEnvironmentHost(hostidentity.DeployWildcardSuffix, uuid.NewString(), uuid.NewString())
	} else if mode == "uppercase" {
		hostname = strings.ToUpper(hostname)
	}
	surface, host := trafficTenantClaim(t, store, account, app, hostname)
	platform := store.(PlatformTenantStore)
	var tenant PlatformTenant
	if mode == "tenant" || mode == "link" {
		var err error
		tenant, _, err = platform.CreatePlatformTenant(t.Context(), account.ID, "traffic-tenant", "Traffic tenant", account.Plan.ConsumerKeysPerAccount())
		if err != nil {
			t.Fatal(err)
		}
		if mode == "tenant" {
			if _, err := platform.LinkPlatformTenantSurface(t.Context(), account.ID, tenant.ID, surface.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := platform.SetPlatformTenantStatus(t.Context(), account.ID, tenant.ID, PlatformTenantSuspended); err != nil {
				t.Fatal(err)
			}
		}
	}
	if mode != "surface" {
		if err := store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, SurfaceStatusActive); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "surface" || mode == "tenant" || mode == "link" || mode == "unchanged" {
		if err := store.MarkTenantHostnameVerified(t.Context(), hostname); err != nil {
			t.Fatal(err)
		}
	}
	policyAccount, policyApp := account, app
	if mode == "foreign" {
		var err error
		policyAccount, err = store.CreateAccount(t.Context(), "foreign-tenant-policy@example.test", api.PlanScale)
		if err != nil {
			t.Fatal(err)
		}
		policyApp, err = store.CreateApp(t.Context(), App{AccountID: policyAccount.ID, Slug: "foreign-tenant-policy", Status: AppActive})
		if err != nil {
			t.Fatal(err)
		}
	}
	legacy := publicationLegacyRule(policyAccount, policyApp, strings.ToLower(hostname))
	seed(legacy)
	beforeSurface, err := store.GetTenantSurfaceByID(t.Context(), surface.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeHost, err := store.GetTenantHostnameByName(t.Context(), hostname)
	if err != nil {
		t.Fatal(err)
	}
	bindingStore := store.(interface {
		PlatformTenantHostBinding(context.Context, string) (PlatformTenantHostBinding, error)
	})
	beforeBinding, err := bindingStore.PlatformTenantHostBinding(t.Context(), hostname)
	if err != nil {
		t.Fatal(err)
	}
	apply := func() error {
		switch mode {
		case "surface":
			return store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, SurfaceStatusActive)
		case "tenant":
			_, err := platform.SetPlatformTenantStatus(t.Context(), account.ID, tenant.ID, PlatformTenantActive)
			return err
		case "link":
			_, err := platform.LinkPlatformTenantSurface(t.Context(), account.ID, tenant.ID, surface.ID)
			return err
		case "plain", "unchanged":
			return store.MarkTenantHostnameVerified(t.Context(), hostname)
		default:
			ctx, token := t.Context(), host.ChallengeToken
			if mode == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			} else if mode == "stale" {
				token = "old-private-tenant-token"
			}
			matched, err := store.(TenantHostnameChallengeVerifier).MarkTenantHostnameVerifiedIfChallenge(ctx, hostname, token)
			if err == nil && matched == (mode == "stale") {
				return errors.New("challenge verification returned an unexpected match")
			}
			return err
		}
	}
	err = apply()
	refused := mode == "plain" || mode == "challenge" || mode == "surface" || mode == "tenant" || mode == "link" || mode == "uppercase"
	if refused {
		var aggregate *TrafficPolicyAggregateError
		if !errors.As(err, &aggregate) || aggregate.Observed <= aggregate.Limit || aggregate.Host != strings.ToLower(hostname) {
			t.Fatalf("new tenant binding inherited prior allowance: %v", err)
		}
	} else if mode == "canceled" {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled tenant publication: %v", err)
		}
	} else if err != nil {
		t.Fatalf("safe tenant publication refused: %v", err)
	}
	if refused || mode == "stale" || mode == "canceled" {
		afterSurface, surfaceErr := store.GetTenantSurfaceByID(t.Context(), surface.ID)
		afterHost, hostErr := store.GetTenantHostnameByName(t.Context(), hostname)
		afterBinding, bindingErr := bindingStore.PlatformTenantHostBinding(t.Context(), hostname)
		if surfaceErr != nil || hostErr != nil || bindingErr != nil || !reflect.DeepEqual(beforeSurface, afterSurface) || !reflect.DeepEqual(beforeHost, afterHost) || !reflect.DeepEqual(beforeBinding, afterBinding) {
			t.Fatalf("refused tenant publication changed intent: surface=%v host=%v binding=%v", surfaceErr, hostErr, bindingErr)
		}
		if err := store.DeleteEdgeRule(t.Context(), legacy.ID); err != nil {
			t.Fatal(err)
		}
		if refused {
			if err := apply(); err != nil {
				t.Fatalf("repaired tenant publication refused: %v", err)
			}
		}
	}
}

func TestMemTrafficTenantActivationRefusalAndRepair(t *testing.T) {
	for _, mode := range trafficTenantActivationModes {
		t.Run(mode, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			testTrafficTenantActivation(t, m, account, app, mode, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule })
		})
	}
}

func TestMemTrafficTenantMetadataCaseAliasesAndVisibility(t *testing.T) {
	m, account, _, app, _ := memTrafficFixture(t)
	surface, host := trafficTenantClaim(t, m, account, app, "UPPER.TENANT.EXAMPLE.TEST")
	if err := m.UpdateTenantSurfaceStatus(t.Context(), surface.ID, SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if matched, err := m.MarkTenantHostnameVerifiedIfChallenge(t.Context(), strings.ToLower(host.Hostname), host.ChallengeToken); err != nil || !matched {
		t.Fatalf("case-insensitive challenge publication: matched=%v err=%v", matched, err)
	}
	view, err := m.readMemTrafficHostAnalysisLocked(t.Context(), account.ID, memTrafficPolicyChange{})
	if err != nil || len(view.Tenants) != 1 || view.Tenants[0].Host != strings.ToLower(host.Hostname) {
		t.Fatalf("case aliases changed tenant metadata: tenants=%+v err=%v", view.Tenants, err)
	}
	for _, alias := range []string{host.Hostname, strings.ToLower(host.Hostname)} {
		if !m.tenantHostnames[alias].Verified() {
			t.Fatal("verification left a stale hostname case alias")
		}
	}
	visibility := api.AppVisibilityInternal
	if _, err := m.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &visibility}); err != nil {
		t.Fatal(err)
	}
	legacy := publicationLegacyRule(account, app, strings.ToLower(host.Hostname))
	m.edgeRules[legacy.ID] = legacy
	visibility = api.AppVisibilityPublic
	_, err = m.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &visibility})
	requireMemTrafficAggregate(t, err, "host_rule_projection")
	if m.apps[app.ID].Visibility != api.AppVisibilityInternal {
		t.Fatal("refused app publication exposed an active tenant hostname")
	}
	delete(m.edgeRules, legacy.ID)
	if _, err := m.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &visibility}); err != nil {
		t.Fatalf("repaired app publication refused: %v", err)
	}
}
