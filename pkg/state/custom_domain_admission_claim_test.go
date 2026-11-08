package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainTLSAdmissionChecksCurrentClaimAndAllowlist(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			oldApp, nextApp := tlsHostApp(t, ctx, raw, ""), tlsHostApp(t, ctx, raw, "")
			zone := "admission-" + uuid.NewString()[:8] + ".example.com"
			wildcard, host := "*."+zone, "live."+zone
			verifiedTLSWildcard(t, ctx, raw, wildcard, oldApp)
			old, err := store.DomainByName(ctx, wildcard)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.DeleteCustomDomain(ctx, wildcard); err != nil {
				t.Fatal(err)
			}
			verifiedTLSWildcard(t, ctx, raw, wildcard, nextApp)
			current, err := store.DomainByName(ctx, wildcard)
			if err != nil {
				t.Fatal(err)
			}
			a := roomyTLSAdmission(wildcard, host, time.Now().UTC())
			// Make the replacement's host known, exercising the indexed fast
			// path as well as new-host admission under the row lock.
			if got, err := raw.AdmitCustomDomainTLSHost(ctx, a); err != nil || got != state.TLSHostAdmitted {
				t.Fatalf("seed known host=%v %v", got, err)
			}
			a.ExpectedAppID, a.ExpectedToken = old.AppID, old.ChallengeToken
			if got, err := raw.AdmitCustomDomainTLSHost(ctx, a); err != nil || got != state.TLSHostClaimChanged {
				t.Fatalf("stale known host=%v %v", got, err)
			}
			a.Host = "fresh." + zone
			if got, err := raw.AdmitCustomDomainTLSHost(ctx, a); err != nil || got != state.TLSHostClaimChanged {
				t.Fatalf("stale new host=%v %v", got, err)
			}
			a.ExpectedAppID, a.ExpectedToken = current.AppID, "stale-token"
			if got, err := raw.AdmitCustomDomainTLSHost(ctx, a); err != nil || got != state.TLSHostClaimChanged {
				t.Fatalf("stale token=%v %v", got, err)
			}
			a.ExpectedAppID, a.ExpectedToken = current.AppID, current.ChallengeToken
			lists := raw.(state.CustomDomainTLSAllowedHostStore)
			if err = lists.SetCustomDomainTLSAllowedHostsForClaim(ctx, current, []string{"new-only"}); err != nil {
				t.Fatal(err)
			}
			for _, denied := range []string{host, "fresh." + zone} {
				a.Host = denied
				if got, err := raw.AdmitCustomDomainTLSHost(ctx, a); err != nil || got != state.TLSHostNotAllowlisted {
					t.Fatalf("revoked allowlist %s=%v %v", denied, got, err)
				}
			}
			probe, _ := state.WildcardProbeHost(wildcard)
			for _, allowed := range []string{"new-only." + zone, probe} {
				a.Host = allowed
				if got, err := raw.AdmitCustomDomainTLSHost(ctx, a); err != nil || got != state.TLSHostAdmitted {
					t.Fatalf("current allowlist %s=%v %v", allowed, got, err)
				}
			}
		})
	}
}

func TestCertificateFailureEmailReleaseIsClaimAndAttemptBound(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			attempts := raw.(state.CustomDomainCertFailureEmailAttemptStore)
			oldApp, nextApp := tlsHostApp(t, ctx, raw, ""), tlsHostApp(t, ctx, raw, "")
			host := "email-" + uuid.NewString()[:8] + ".example.com"
			verifiedTLSWildcard(t, ctx, raw, host, oldApp)
			old, err := store.DomainByName(ctx, host)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC().Truncate(time.Microsecond)
			fail := func() {
				if err := store.UpdateCustomDomainCertStatus(ctx, host, state.CustomDomainCertFailed, time.Time{}, "failed", at.Add(-20*time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			claim := func(when time.Time, want bool) {
				t.Helper()
				ok, err := attempts.ClaimCustomDomainCertFailureEmail(ctx, host, when)
				if err != nil || ok != want {
					t.Fatalf("claim=%v err=%v want=%v", ok, err, want)
				}
			}
			fail()
			claim(at, true)
			if err = attempts.ReleaseCustomDomainCertFailureEmail(ctx, old, at.Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
			claim(at.Add(time.Second), false)
			if err = attempts.ReleaseCustomDomainCertFailureEmail(ctx, old, at); err != nil {
				t.Fatal(err)
			}
			claim(at.Add(time.Second), true)
			if err = attempts.ReleaseCustomDomainCertFailureEmail(ctx, old, at); err != nil {
				t.Fatal(err)
			}
			claim(at.Add(2*time.Second), false)
			if err = store.DeleteCustomDomain(ctx, host); err != nil {
				t.Fatal(err)
			}
			verifiedTLSWildcard(t, ctx, raw, host, nextApp)
			fail()
			claim(at.Add(3*time.Second), true)
			if err = attempts.ReleaseCustomDomainCertFailureEmail(ctx, old, at.Add(3*time.Second)); err != nil {
				t.Fatal(err)
			}
			claim(at.Add(4*time.Second), false)
		})
	}
}
