// adr: 520
package apidserver_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainDoctorRevokesDriftDespiteOverlappingSurface(t *testing.T) {
	for _, tc := range []struct {
		name, flag string
		active     bool
	}{
		{"pending-disabled", "false", false}, {"pending-enabled", "true", false},
		{"active-enabled", "true", true}, {"active-disabled", "false", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_TENANT_SURFACES_ENABLED", tc.flag)
			e := apidserver.Setup(t, api.PlanPro)
			store, ctx := *e.Store(), t.Context()
			appID := apidserver.MustSeedApp(t, e, "doctor-registry")
			const host = "doctor-registry.example.com"
			domainRecoveryVerified(t, store, host, appID, "proof")
			limits := api.MustLimitsFor(api.PlanPro)
			surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: (*e.Acct()).ID, AppID: appID, Name: "overlap"}, limits)
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.CreateTenantHostnameIfUnderQuota(state.WithPublicTenantHostnameClaim(ctx), state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: host, ChallengeToken: "tenant-proof"}, limits)
			if err != nil {
				t.Fatal(err)
			}
			if tc.active {
				if err = store.MarkTenantHostnameVerified(ctx, host); err != nil {
					t.Fatal(err)
				}
				if err = store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
					t.Fatal(err)
				}
			}
			before := time.Now().Add(-time.Minute)
			if err = store.UpsertDoctorObservation(ctx, state.DomainDoctorObservation{Domain: host, SurfaceID: surface.ID, ObservedAt: before, DNSCheckedAt: before, CertState: "issued", CertNotAfter: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			configureDomainDNS(t)
			*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "away.invalid.", nil }
			*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"1.1.1.1"}, nil }
			*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, nil }
			if err = (*e.S()).RunDoctorForDomain(ctx, slog.New(slog.DiscardHandler), host); err != nil {
				t.Fatal(err)
			}
			d, err := store.DomainByName(ctx, host)
			if err != nil || d.Verified() || d.CertStatus != state.CustomDomainCertDNSDrifted {
				t.Fatalf("custom claim not revoked: %+v %v", d, err)
			}
			obs, err := store.GetDoctorObservation(ctx, host)
			if err != nil || obs.SurfaceID != "" || obs.PointsToGregale {
				t.Fatalf("custom observation remained tied to surface: %+v %v", obs, err)
			}
			if allowed, decision := gateway.NewOnDemandTLSPolicy(store, "gregale.dev", nil, nil).Decide(ctx, host); allowed {
				t.Fatalf("drifted custom domain retained TLS permission: %s", decision)
			}
		})
	}
}
