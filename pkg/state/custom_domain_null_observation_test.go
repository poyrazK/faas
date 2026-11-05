// adr: 520
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainCertificateWithoutDNSTimeCannotEraseRevocation(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			app := tlsHostApp(t, ctx, raw, "")
			host := "legacy-clock-" + uuid.NewString()[:8] + ".example.com"
			if _, err := store.CreateCustomDomain(ctx, host, app, "proof"); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDomainVerified(ctx, host); err != nil {
				t.Fatal(err)
			}
			claim, err := store.DomainByName(ctx, host)
			if err != nil || !claim.DNSLastCheckedAt.IsZero() {
				t.Fatalf("legacy claim: %+v %v", claim, err)
			}
			bound := state.WithCustomDomainProbe(ctx, claim)
			expiry := time.Now().UTC().Add(24 * time.Hour)
			if err := store.UpdateCustomDomainCertStatus(bound, host, state.CustomDomainCertIssued, expiry, "", time.Time{}); err != nil {
				t.Fatalf("legacy certificate before any DNS observation: %v", err)
			}
			at := time.Now().UTC().Truncate(time.Microsecond).Add(time.Minute)
			if changed, err := raw.(state.CustomDomainDNSDriftStore).MarkCustomDomainDNSDrifted(bound, host, at, "wrong CNAME"); err != nil || !changed {
				t.Fatalf("revoke: %v %v", changed, err)
			}
			for _, status := range []state.CustomDomainCertStatus{state.CustomDomainCertPending, state.CustomDomainCertIssued, state.CustomDomainCertFailed} {
				if err := store.UpdateCustomDomainCertStatus(bound, host, status, expiry, "late certificate result", time.Time{}); !errors.Is(err, state.ErrStaleCustomDomainObservation) {
					t.Fatalf("late %s observation: %v", status, err)
				}
			}
			current, err := store.DomainByName(ctx, host)
			if err != nil || current.Verified() || current.CertStatus != state.CustomDomainCertDNSDrifted || !current.DNSLastCheckedAt.Equal(at) || !current.CertExpiresAt.IsZero() || current.CertLastError != "wrong CNAME" {
				t.Fatalf("DNS revocation overwritten: %+v %v", current, err)
			}
		})
	}
}

func TestCustomDomainTLSKnownHostsArePagedAndRemovedWithClaim(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			app := tlsHostApp(t, ctx, raw, "")
			zone := "hints-" + uuid.NewString()[:8] + ".example.com"
			wildcard := "*." + zone
			verifiedTLSWildcard(t, ctx, raw, wildcard, app)
			for _, prefix := range []string{"a", "b", "c"} {
				a := roomyTLSAdmission(wildcard, prefix+"."+zone, time.Now())
				if verdict, err := raw.AdmitCustomDomainTLSHost(ctx, a); err != nil || verdict != state.TLSHostAdmitted {
					t.Fatalf("admit: %v %v", verdict, err)
				}
			}
			reader := raw.(state.CustomDomainTLSKnownHostReader)
			var found []string
			for after := ""; ; {
				page, err := reader.ListCustomDomainTLSKnownHosts(ctx, after, 2)
				if err != nil {
					t.Fatal(err)
				}
				for _, host := range page {
					if state.WildcardMatchesHost(wildcard, host) {
						found = append(found, host)
					}
				}
				if len(page) < 2 {
					break
				}
				after = page[len(page)-1]
			}
			if len(found) != 3 || found[0] != "a."+zone || found[2] != "c."+zone {
				t.Fatalf("paged hints lost or duplicated a host: %v", found)
			}
			if err := raw.DeleteCustomDomain(ctx, wildcard); err != nil {
				t.Fatal(err)
			}
			page, err := reader.ListCustomDomainTLSKnownHosts(ctx, "", 10000)
			if err != nil {
				t.Fatal(err)
			}
			for _, host := range page {
				if state.WildcardMatchesHost(wildcard, host) {
					t.Fatalf("deleted claim retains durable hint %s", host)
				}
			}
		})
	}
}
