package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainTenantReservationsCheckExactAndBulkClaims(t *testing.T) {
	for backend, open := range tlsHostStores() {
		t.Run(backend, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			ownerID, otherID := tlsHostApp(t, ctx, raw, ""), tlsHostApp(t, ctx, raw, "")
			owner, err := store.AppByID(ctx, ownerID)
			if err != nil {
				t.Fatal(err)
			}
			other, err := store.AppByID(ctx, otherID)
			if err != nil {
				t.Fatal(err)
			}
			host := "exact-" + uuid.NewString()[:8] + ".example.com"
			if _, err = store.CreateCustomDomain(ctx, host, ownerID, "owner-proof"); err != nil {
				t.Fatal(err)
			}
			if err = store.MarkDomainVerified(ctx, host); err != nil {
				t.Fatal(err)
			}
			limits, _ := api.LimitsFor(api.PlanPro)
			surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: other.AccountID, AppID: otherID, Name: "foreign-exact"}, limits)
			if err != nil {
				t.Fatal(err)
			}
			public := state.WithPublicTenantHostnameClaim(ctx)
			if _, err = store.CreateTenantHostnameIfUnderQuota(public, state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: host, ChallengeToken: "unproved"}, limits); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("foreign exact reservation: %v", err)
			}
			bundle := state.ApplyPlatformTenantParams{AccountID: other.AccountID, ExternalRef: "foreign-bulk", Name: "Foreign bulk", TenantLimit: 100, Limits: limits, Surfaces: []state.ApplyPlatformTenantSurface{{AppID: otherID, Name: "bulk-host", CertKind: state.CertKindPerHostSAN, Hostnames: []state.ApplyPlatformTenantHostname{{Hostname: host, ChallengeToken: "unproved"}}}}}
			for _, dryRun := range []bool{true, false} {
				bundle.DryRun = dryRun
				if _, err = raw.(state.PlatformTenantApplyStore).ApplyPlatformTenant(public, bundle); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("foreign bulk exact reservation dryRun=%v: %v", dryRun, err)
				}
			}
			if _, err = store.GetTenantHostnameByName(ctx, host); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("rejected bundle published hostname: %v", err)
			}
			// A same-tenant migration may move the hostname to another app's surface.
			next, err := store.CreateApp(ctx, state.App{AccountID: owner.AccountID, Slug: "migrate-" + uuid.NewString()[:8], Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			own, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: owner.AccountID, AppID: next.ID, Name: "same-owner"}, limits)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.CreateTenantHostnameIfUnderQuota(public, state.CreateTenantHostnameParams{SurfaceID: own.ID, Hostname: host, ChallengeToken: "new-proof"}, limits); err != nil {
				t.Fatalf("same-tenant exact handoff: %v", err)
			}
		})
	}
}

func TestCustomDomainOlderDriftCannotOverwriteNewerState(t *testing.T) {
	for backend, open := range tlsHostStores() {
		t.Run(backend, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			appID := tlsHostApp(t, ctx, raw, "")
			host := "drift-" + uuid.NewString()[:8] + ".example.com"
			claim, err := store.CreateCustomDomain(ctx, host, appID, "proof")
			if err != nil {
				t.Fatal(err)
			}
			if err = store.MarkDomainVerified(ctx, host); err != nil {
				t.Fatal(err)
			}
			newer := time.Now().UTC().Truncate(time.Microsecond)
			expiry := newer.Add(time.Hour)
			if err = store.UpdateCustomDomainCertStatus(ctx, host, state.CustomDomainCertIssued, expiry, "", newer); err != nil {
				t.Fatal(err)
			}
			drift := raw.(state.CustomDomainDNSDriftStore)
			for _, bound := range []bool{false, true} {
				probe := ctx
				if bound {
					probe = state.WithCustomDomainProbe(ctx, claim)
				}
				changed, err := drift.MarkCustomDomainDNSDrifted(probe, host, newer.Add(-time.Minute), "obsolete error")
				if changed || !errors.Is(err, state.ErrStaleCustomDomainObservation) {
					t.Fatalf("bound=%v changed=%v err=%v", bound, changed, err)
				}
				current, err := store.DomainByName(ctx, host)
				if err != nil {
					t.Fatal(err)
				}
				if !current.Verified() || current.CertStatus != state.CustomDomainCertIssued || !current.DNSLastCheckedAt.Equal(newer) || !current.CertExpiresAt.Equal(expiry) || current.CertLastError != "" {
					t.Fatalf("newer healthy state changed: %+v", current)
				}
			}
			if changed, err := drift.MarkCustomDomainDNSDrifted(ctx, host, newer.Add(time.Minute), "current mismatch"); err != nil || !changed {
				t.Fatalf("current drift: %v %v", changed, err)
			}
			if changed, err := drift.MarkCustomDomainDNSDrifted(ctx, host, newer, "older mismatch"); changed || !errors.Is(err, state.ErrStaleCustomDomainObservation) {
				t.Fatalf("old repeated drift: %v %v", changed, err)
			}
			current, err := store.DomainByName(ctx, host)
			if err != nil {
				t.Fatal(err)
			}
			if current.CertLastError != "current mismatch" || !current.DNSLastCheckedAt.Equal(newer.Add(time.Minute)) {
				t.Fatalf("newer drift replaced: %+v", current)
			}
		})
	}
}
