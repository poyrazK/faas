package state_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/domainhosts"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainPublicWildcardRejectsTenantReservation(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			appID, otherID := tlsHostApp(t, ctx, raw, ""), tlsHostApp(t, ctx, raw, "")
			other, err := store.AppByID(ctx, otherID)
			if err != nil {
				t.Fatal(err)
			}
			zone := "domain-" + uuid.NewString()[:8] + ".example.com"
			limits, _ := api.LimitsFor(api.PlanPro)
			surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: other.AccountID, AppID: otherID, Name: "concurrent-tenant"}, limits)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: "tenant." + zone, ChallengeToken: "tenant-proof"}, limits); err != nil {
				t.Fatal(err)
			}
			creator := raw.(interface {
				CreateCustomDomainIfUnderQuota(context.Context, string, string, string, int, int) (state.CustomDomain, error)
			})
			d, err := creator.CreateCustomDomainIfUnderQuota(state.WithPublicCustomDomainClaim(ctx), "*."+zone, appID, "wildcard-proof", 100, 500)
			var overlap *state.CustomDomainTenantOverlapError
			if !errors.As(err, &overlap) {
				t.Fatalf("public wildcard claim overlaps another account's tenant reservation: domain=%s app=%s tenant_app=%s", d.Domain, d.AppID, otherID)
			}
		})
	}
}

func TestCustomDomainAllowlistReadChecksCapturedClaim(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			first, next := tlsHostApp(t, ctx, raw, ""), tlsHostApp(t, ctx, raw, "")
			wildcard := "*.domain-" + uuid.NewString()[:8] + ".example.com"
			verifiedTLSWildcard(t, ctx, raw, wildcard, first)
			captured, err := store.DomainByName(ctx, wildcard)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.DeleteCustomDomain(ctx, wildcard); err != nil {
				t.Fatal(err)
			}
			verifiedTLSWildcard(t, ctx, raw, wildcard, next)
			if err = raw.(state.CustomDomainTLSAllowedHostStore).SetCustomDomainTLSAllowedHosts(ctx, wildcard, []string{"private-replacement-host"}); err != nil {
				t.Fatal(err)
			}
			out, problem := domainhosts.Read(ctx, raw, captured)
			if problem == nil || problem.Status != 404 || slices.Contains(out.Names, "private-replacement-host") {
				t.Fatalf("captured old claim disclosed replacement allowlist: %+v", out)
			}
		})
	}
}

func TestCustomDomainOldObservationPreservesCertificate(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			app := tlsHostApp(t, ctx, raw, "")
			host := "domain-" + uuid.NewString()[:8] + ".example.com"
			d, err := store.CreateCustomDomain(ctx, host, app, "proof")
			if err != nil {
				t.Fatal(err)
			}
			checked := time.Now().Add(-time.Minute)
			if err = store.MarkDomainVerified(ctx, host); err != nil {
				t.Fatal(err)
			}
			bound := state.WithCustomDomainProbe(ctx, d)
			if err = store.UpdateCustomDomainCertStatus(bound, host, state.CustomDomainCertIssued, time.Now().Add(30*24*time.Hour), "", time.Now()); err != nil {
				t.Fatal(err)
			}
			if err = store.UpdateCustomDomainCertStatus(bound, host, state.CustomDomainCertPending, time.Time{}, "", checked); err != nil {
				t.Fatal(err)
			}
			current, err := store.DomainByName(ctx, host)
			if err != nil {
				t.Fatal(err)
			}
			if current.CertStatus != state.CustomDomainCertIssued || current.CertExpiresAt.IsZero() {
				t.Fatalf("older in-flight result overwrote certificate: status=%s expires=%v", current.CertStatus, current.CertExpiresAt)
			}
		})
	}
}
