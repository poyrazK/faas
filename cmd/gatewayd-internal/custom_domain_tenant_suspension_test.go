package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainUnprovedTenantSuspensionPreservesRoute(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	for _, mode := range []string{"memory", "postgres-cached", "postgres-snapshot"} {
		t.Run(mode, func(t *testing.T) {
			for _, wild := range []bool{false, true} {
				t.Run(map[bool]string{false: "exact", true: "wildcard"}[wild], func(t *testing.T) {
					var store state.Store = state.NewMemStore()
					if mode != "memory" {
						store = newPublicRoutingPGFixture(t).store
					}
					owner := seedApp(t, store, "victim", api.PlanPro)
					attacker := seedApp(t, store, "claimant", api.PlanPro)
					const host = "live.victim.example.com"
					domain := host
					if wild {
						domain = "*.victim.example.com"
					}
					if _, err := store.CreateCustomDomain(t.Context(), domain, owner.ID, "owner-proof"); err != nil {
						t.Fatal(err)
					}
					if err := store.MarkDomainVerified(t.Context(), domain); err != nil {
						t.Fatal(err)
					}
					router := pgRouter{store: store, appsSuffix: ".apps.gregale.dev", cachedHostPolicy: mode != "postgres-snapshot", tenantSurfacesEnabled: func() bool { return true }}
					before, found, err := router.ResolveHost(t.Context(), host)
					if err != nil || !found || before.ID != owner.ID {
						t.Fatalf("invalid control: found=%v app=%s err=%v", found, before.ID, err)
					}
					limits, _ := api.LimitsFor(api.PlanPro)
					applied, err := store.(state.PlatformTenantApplyStore).ApplyPlatformTenant(t.Context(), state.ApplyPlatformTenantParams{AccountID: attacker.AccountID, ExternalRef: "blocked", Name: "Blocked", TenantLimit: 100, Limits: limits, Surfaces: []state.ApplyPlatformTenantSurface{{AppID: attacker.ID, Name: "unproved-host", CertKind: state.CertKindPerHostSAN, Hostnames: []state.ApplyPlatformTenantHostname{{Hostname: host, ChallengeToken: "never-proved"}}}}})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := store.(state.PlatformTenantStore).SetPlatformTenantStatus(t.Context(), attacker.AccountID, applied.Tenant.ID, state.PlatformTenantSuspended); err != nil {
						t.Fatal(err)
					}
					binding, _, err := router.ResolvePlatformTenantHost(t.Context(), host)
					if err != nil || binding.Verified || !binding.Suspended {
						t.Fatalf("not exercised: %+v %v", binding, err)
					}
					after, found, err := router.ResolveHost(t.Context(), host)
					if err != nil || !found || after.ID != owner.ID {
						t.Fatalf("unverified foreign tenant blocks victim: before=%s after=%s found=%v err=%v binding=%+v", before.ID, after.ID, found, err, binding)
					}
				})
			}
		})
	}
}
