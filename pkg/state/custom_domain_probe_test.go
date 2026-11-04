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

func TestCustomDomainProbeCannotModifyOrReadReplacementClaim(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			oldApp, nextApp := tlsHostApp(t, ctx, raw, ""), tlsHostApp(t, ctx, raw, "")
			domain := "probe-" + uuid.NewString()[:8] + ".example.com"
			old, err := store.CreateCustomDomain(ctx, domain, oldApp, "old-proof")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.GetDoctorObservation(state.WithCustomDomainProbe(ctx, old), domain); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("fresh claim has an observation: %v", err)
			}
			if err = store.UpsertDoctorObservation(ctx, state.DomainDoctorObservation{Domain: domain, ObservedAt: time.Now(), CertState: "pending", LastError: "old owner private observation"}); err != nil {
				t.Fatal(err)
			}
			next, err := raw.(state.CustomDomainClaimTakeover).TakeOverCustomDomainClaim(ctx, domain, nextApp, "", "new-proof", 100, 500)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.GetDoctorObservation(ctx, domain); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("takeover retained old observation: %v", err)
			}
			if err = store.MarkDomainVerified(ctx, domain); err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC()
			if err = store.UpdateCustomDomainCertStatus(ctx, domain, state.CustomDomainCertFailed, time.Time{}, "new owner failure", at.Add(-20*time.Minute)); err != nil {
				t.Fatal(err)
			}
			obs := state.DomainDoctorObservation{Domain: domain, ObservedAt: at, CertState: "failed", LastError: "new owner private observation"}
			if err = store.UpsertDoctorObservation(ctx, obs); err != nil {
				t.Fatal(err)
			}
			changedApp, changedToken := next, next
			changedApp.AppID = old.AppID
			changedToken.ChallengeToken = old.ChallengeToken
			for _, claim := range []state.CustomDomain{old, changedApp, changedToken} {
				stale := state.WithCustomDomainProbe(ctx, claim)
				if err = store.UpdateCustomDomainCertStatus(stale, domain, state.CustomDomainCertIssued, at.Add(time.Hour), "old result", at); !errors.Is(err, state.ErrStaleCustomDomainProbe) {
					t.Fatalf("stale certificate write: %v", err)
				}
				if _, err = raw.(state.CustomDomainDNSDriftStore).MarkCustomDomainDNSDrifted(stale, domain, at, "old DNS"); !errors.Is(err, state.ErrStaleCustomDomainProbe) {
					t.Fatalf("stale revocation: %v", err)
				}
				if err = store.UpsertDoctorObservation(stale, state.DomainDoctorObservation{Domain: domain, ObservedAt: at, CertState: "pending"}); !errors.Is(err, state.ErrStaleCustomDomainProbe) {
					t.Fatalf("stale observation write: %v", err)
				}
				if _, err = store.GetDoctorObservation(stale, domain); !errors.Is(err, state.ErrStaleCustomDomainProbe) {
					t.Fatalf("stale observation read: %v", err)
				}
				if claimed, err := raw.(state.CustomDomainCertFailureEmailCooldownStore).ClaimCustomDomainCertFailureEmail(stale, domain, at); claimed || err != nil && !errors.Is(err, state.ErrStaleCustomDomainProbe) {
					t.Fatalf("stale email claim=%v err=%v", claimed, err)
				}
			}
			current, err := store.DomainByName(ctx, domain)
			if err != nil {
				t.Fatal(err)
			}
			if !current.Verified() || current.CertStatus != state.CustomDomainCertFailed || current.CertLastError != "new owner failure" {
				t.Fatalf("replacement mutated: %+v", current)
			}
			bound := state.WithCustomDomainProbe(ctx, current)
			got, err := store.GetDoctorObservation(bound, domain)
			if err != nil || got.LastError != obs.LastError {
				t.Fatalf("replacement observation: %+v %v", got, err)
			}
			if claimed, err := raw.(state.CustomDomainCertFailureEmailCooldownStore).ClaimCustomDomainCertFailureEmail(bound, domain, at); err != nil || !claimed {
				t.Fatalf("current email claim=%v err=%v", claimed, err)
			}
			// A current probe still updates the certificate and its cached observation.
			if err = store.UpdateCustomDomainCertStatus(bound, domain, state.CustomDomainCertIssued, at.Add(time.Hour), "", at); err != nil {
				t.Fatal(err)
			}
			obs.CertState, obs.LastError = "issued", ""
			if err = store.UpsertDoctorObservation(bound, obs); err != nil {
				t.Fatal(err)
			}
			if got, err = store.GetDoctorObservation(bound, domain); err != nil || got.CertState != "issued" {
				t.Fatalf("current probe observation: %+v %v", got, err)
			}
			drift := raw.(state.CustomDomainDNSDriftStore)
			if transitioned, err := drift.MarkCustomDomainDNSDrifted(bound, domain, at, "routing changed"); err != nil || !transitioned {
				t.Fatalf("current probe drift transition=%v err=%v", transitioned, err)
			}
			first, err := store.DomainByName(ctx, domain)
			if err != nil {
				t.Fatal(err)
			}
			if transitioned, err := drift.MarkCustomDomainDNSDrifted(bound, domain, at.Add(time.Minute), "still drifted"); err != nil || transitioned {
				t.Fatalf("repeat drift transition=%v err=%v", transitioned, err)
			}
			repeated, err := store.DomainByName(ctx, domain)
			if err != nil || repeated.Verified() || !repeated.VerificationExpiresAt.Equal(first.VerificationExpiresAt) {
				t.Fatalf("repeat drift changed verification deadline: first=%+v repeated=%+v err=%v", first, repeated, err)
			}
		})
	}
}

func TestPublicCustomDomainClaimRechecksVerifiedWildcard(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			ownerApp, otherApp := tlsHostApp(t, ctx, raw, ""), tlsHostApp(t, ctx, raw, "")
			zone := "public-" + uuid.NewString()[:8] + ".example.com"
			wildcard := "*." + zone
			if _, err := store.CreateCustomDomain(ctx, wildcard, ownerApp, "proof"); err != nil {
				t.Fatal(err)
			}
			other, err := store.AppByID(ctx, otherApp)
			if err != nil {
				t.Fatal(err)
			}
			if parent, err := state.ForeignVerifiedWildcard(ctx, store, "live."+zone, other); err != nil || parent != "" {
				t.Fatalf("preflight=%s %v", parent, err)
			}
			if err = store.MarkDomainVerified(ctx, wildcard); err != nil {
				t.Fatal(err)
			}
			public := state.WithPublicCustomDomainClaim(ctx)
			creator := raw.(interface {
				CreateCustomDomainIfUnderQuota(context.Context, string, string, string, int, int) (state.CustomDomain, error)
			})
			var conflict *state.CustomDomainWildcardConflictError
			if _, err = creator.CreateCustomDomainIfUnderQuota(public, "live."+zone, otherApp, "new-proof", 100, 500); !errors.As(err, &conflict) || conflict.Wildcard != wildcard {
				t.Fatalf("public reservation bypassed verified wildcard: %v", err)
			}
			if _, err = creator.CreateCustomDomainIfUnderQuota(public, "*.nested."+zone, otherApp, "new-proof", 100, 500); !errors.As(err, &conflict) {
				t.Fatalf("nested reservation bypassed verified wildcard: %v", err)
			}
			if _, err = creator.CreateCustomDomainIfUnderQuota(public, "own."+zone, ownerApp, "own-proof", 100, 500); err != nil {
				t.Fatalf("same tenant refused: %v", err)
			}
			// Internal repair remains distinct from a public claim, as ADR-570 requires.
			if _, err = store.CreateCustomDomain(ctx, "repair."+zone, otherApp, "repair-proof"); err != nil {
				t.Fatalf("internal repair refused: %v", err)
			}
			if _, err = raw.(state.CustomDomainClaimTakeover).TakeOverCustomDomainClaim(public, "repair."+zone, otherApp, "", "takeover-proof", 100, 500); !errors.As(err, &conflict) {
				t.Fatalf("public takeover bypassed wildcard: %v", err)
			}
		})
	}
}

func TestPublicCustomDomainClaimUsesOrganizationOwnership(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			company, err := raw.CreateOrg(ctx, state.Org{Slug: "wildcard-" + uuid.NewString()[:8], Name: "Company", Plan: api.PlanPro})
			if err != nil {
				t.Fatal(err)
			}
			rival, err := raw.CreateOrg(ctx, state.Org{Slug: "rival-" + uuid.NewString()[:8], Name: "Rival", Plan: api.PlanPro})
			if err != nil {
				t.Fatal(err)
			}
			ownerApp := tlsHostApp(t, ctx, raw, company.ID)
			memberApp := tlsHostApp(t, ctx, raw, company.ID)
			owner, err := store.AppByID(ctx, ownerApp)
			if err != nil {
				t.Fatal(err)
			}
			zone := "company-" + uuid.NewString()[:8] + ".example.com"
			verifiedTLSWildcard(t, ctx, raw, "*."+zone, ownerApp)
			public := state.WithPublicCustomDomainClaim(ctx)
			creator := raw.(interface {
				CreateCustomDomainIfUnderQuota(context.Context, string, string, string, int, int) (state.CustomDomain, error)
			})
			if _, err = creator.CreateCustomDomainIfUnderQuota(public, "member."+zone, memberApp, "proof", 100, 500); err != nil {
				t.Fatalf("another member's company app refused: %v", err)
			}
			for _, orgID := range []string{rival.ID, ""} {
				app, err := raw.CreateApp(ctx, state.App{AccountID: owner.AccountID, OrgID: orgID, Slug: "foreign-" + uuid.NewString()[:8]})
				if err != nil {
					t.Fatal(err)
				}
				var conflict *state.CustomDomainWildcardConflictError
				if _, err = creator.CreateCustomDomainIfUnderQuota(public, "foreign."+zone, app.ID, "proof", 100, 500); !errors.As(err, &conflict) {
					t.Fatalf("creator's other tenant bypassed wildcard ownership: %v", err)
				}
			}
		})
	}
}
