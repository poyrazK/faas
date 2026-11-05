// adr: 520
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainObservationOrdering(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			app := tlsHostApp(t, ctx, raw, "")
			host := "ordering-" + uuid.NewString()[:8] + ".example.com"
			claim, err := store.CreateCustomDomain(ctx, host, app, "proof")
			if err != nil {
				t.Fatal(err)
			}
			verifier := raw.(state.CustomDomainChallengeVerifier)
			if matched, err := verifier.MarkDomainVerifiedIfChallenge(ctx, host, "proof"); err != nil || !matched {
				t.Fatalf("verify: %v %v", matched, err)
			}
			bound := state.WithCustomDomainProbe(ctx, claim)
			at := time.Now().UTC().Truncate(time.Microsecond).Add(time.Minute)
			expiry := at.Add(-time.Hour)
			if err := store.UpdateCustomDomainCertStatus(bound, host, state.CustomDomainCertIssued, expiry, "", at); err != nil {
				t.Fatal(err)
			}
			healthy := state.DomainDoctorObservation{Domain: host, ObservedAt: at, DNSCheckedAt: at, CertCheckedAt: at, CertState: "issued", CertNotAfter: expiry, PointsToGregale: true}
			if err := store.UpsertDoctorObservation(bound, healthy); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateCustomDomainCertStatus(bound, host, state.CustomDomainCertFailed, time.Time{}, "old failure", at.Add(-time.Second)); !errors.Is(err, state.ErrStaleCustomDomainObservation) {
				t.Fatalf("stale certificate result: %v", err)
			}
			old := healthy
			old.CertState, old.CertNotAfter = "dial_failed", time.Time{}
			old.ObservedAt, old.DNSCheckedAt = at.Add(-time.Second), at.Add(-time.Second)
			for _, probeCtx := range []bool{false, true} {
				writeCtx := ctx
				if probeCtx {
					writeCtx = bound
				}
				if err := store.UpsertDoctorObservation(writeCtx, old); !errors.Is(err, state.ErrStaleCustomDomainObservation) {
					t.Fatalf("stale observation (bound=%v): %v", probeCtx, err)
				}
			}
			old.ObservedAt = at.Add(time.Second)
			if err := store.UpsertDoctorObservation(bound, old); !errors.Is(err, state.ErrStaleCustomDomainObservation) {
				t.Fatalf("new completion with old DNS results: %v", err)
			}
			obs, err := store.GetDoctorObservation(bound, host)
			if err != nil || obs.CertState != "issued" || !obs.DNSCheckedAt.Equal(at) {
				t.Fatalf("new observation overwritten: %+v %v", obs, err)
			}
			if err := store.UpdateCustomDomainCertStatus(bound, host, state.CustomDomainCertFailed, time.Time{}, "new timeout", at.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			current, err := store.DomainByName(ctx, host)
			if err != nil || !current.CertExpiresAt.Equal(expiry) {
				t.Fatalf("failed probe erased expiry: %+v %v", current, err)
			}
			expiries, err := raw.(state.CustomDomainCertExpiryStore).SoonestCustomDomainCertExpiries(ctx, 100)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, x := range expiries {
				found = found || x.Domain == host && x.NotAfter.Equal(expiry)
			}
			if !found {
				t.Fatal("failed probe removed expired certificate from expiry monitoring")
			}
		})
	}
}

func TestCustomDomainVerificationRejectsNewerDNSObservation(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			app := tlsHostApp(t, ctx, raw, "")
			host := "verify-order-" + uuid.NewString()[:8] + ".example.com"
			if _, err := store.CreateCustomDomain(ctx, host, app, "proof"); err != nil {
				t.Fatal(err)
			}
			verifier := raw.(state.CustomDomainChallengeVerifier)
			if matched, err := verifier.MarkDomainVerifiedIfChallenge(ctx, host, "proof"); err != nil || !matched {
				t.Fatalf("verify: %v %v", matched, err)
			}
			drift := raw.(state.CustomDomainDNSDriftStore)
			at := time.Now().UTC().Truncate(time.Microsecond).Add(time.Minute)
			if changed, err := drift.MarkCustomDomainDNSDrifted(ctx, host, at, "wrong target"); err != nil || !changed {
				t.Fatalf("initial drift: %v %v", changed, err)
			}
			claim, err := store.DomainByName(ctx, host)
			if err != nil {
				t.Fatal(err)
			}
			bound := state.WithCustomDomainProbe(ctx, claim)
			if _, err := drift.MarkCustomDomainDNSDrifted(bound, host, at.Add(time.Second), "newer wrong target"); err != nil {
				t.Fatal(err)
			}
			if matched, err := verifier.MarkDomainVerifiedIfChallenge(bound, host, "proof"); err != nil || matched {
				t.Fatalf("old DNS check restored verification: %v %v", matched, err)
			}
			current, err := store.DomainByName(ctx, host)
			if err != nil || current.Verified() || current.CertStatus != state.CustomDomainCertDNSDrifted {
				t.Fatalf("newer drift overwritten: %+v %v", current, err)
			}
			if matched, err := verifier.MarkDomainVerifiedIfChallenge(state.WithCustomDomainProbe(ctx, current), host, "proof"); err != nil || !matched {
				t.Fatalf("fresh DNS check could not verify: %v %v", matched, err)
			}
		})
	}
}

func TestCustomDomainVerificationReleasesUnfinishedLeases(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			app := tlsHostApp(t, ctx, raw, "")
			host := "lease-" + uuid.NewString()[:8] + ".example.com"
			created, err := store.CreateCustomDomain(ctx, host, app, "proof")
			if err != nil {
				t.Fatal(err)
			}
			claimer := raw.(interface {
				ClaimCustomDomainsForVerification(context.Context, int) ([]state.CustomDomain, error)
			})
			claim := func() state.CustomDomain {
				t.Helper()
				rows, err := claimer.ClaimCustomDomainsForVerification(ctx, 64)
				if err != nil {
					t.Fatal(err)
				}
				for _, d := range rows {
					if d.Domain == host {
						return d
					}
				}
				t.Fatal("unfinished domain was not available for verification")
				return state.CustomDomain{}
			}
			first := claim()
			leases := raw.(state.CustomDomainVerificationLeaseStore)
			if err := leases.ReleaseCustomDomainVerificationLeases(ctx, []state.CustomDomain{first}); err != nil {
				t.Fatal(err)
			}
			current, err := store.DomainByName(ctx, host)
			if err != nil || current.VerificationAttempts != 0 || !current.VerificationExpiresAt.Equal(created.VerificationExpiresAt) {
				t.Fatalf("unfinished check accrued backoff or extended ownership: %+v %v", current, err)
			}
			next := claim()
			if err := leases.ReleaseCustomDomainVerificationLeases(ctx, []state.CustomDomain{first}); err != nil {
				t.Fatal(err)
			}
			current, err = store.DomainByName(ctx, host)
			if err != nil || current.VerificationAttempts != next.VerificationAttempts || !current.VerificationNextCheckAt.Equal(next.VerificationNextCheckAt) {
				t.Fatalf("old cleanup changed a newer lease: %+v %v", current, err)
			}
		})
	}
}

func TestCustomDomainInitialVerificationIgnoresHealthyMonitoring(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			app := tlsHostApp(t, ctx, raw, "")
			host := "pending-order-" + uuid.NewString()[:8] + ".example.com"
			claim, err := store.CreateCustomDomain(ctx, host, app, "proof")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateCustomDomainCertStatus(ctx, host, state.CustomDomainCertPending, time.Time{}, "", time.Now()); err != nil {
				t.Fatal(err)
			}
			verifier := raw.(state.CustomDomainChallengeVerifier)
			if matched, err := verifier.MarkDomainVerifiedIfChallenge(state.WithCustomDomainProbe(ctx, claim), host, "proof"); err != nil || !matched {
				t.Fatalf("healthy pending observation blocked proof: %v %v", matched, err)
			}
			if _, err := raw.(state.CustomDomainDNSDriftStore).MarkCustomDomainDNSDrifted(ctx, host, time.Now().Add(time.Second), "new mismatch"); err != nil {
				t.Fatal(err)
			}
			if matched, err := verifier.MarkDomainVerifiedIfChallenge(state.WithCustomDomainProbe(ctx, claim), host, "proof"); err != nil || matched {
				t.Fatalf("initial proof ignored later drift: %v %v", matched, err)
			}
		})
	}
}
