package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// The certificate-expiry alerts read these rows (bug hunt 32 M6): verified
// domains with an observed certificate, soonest first, capped.
func TestMemStoreSoonestCustomDomainCertExpiries(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	acct, err := m.CreateAccount(ctx, "certs@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "certs", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	seed := func(domain string, verified bool, expires time.Time) {
		t.Helper()
		if _, err := m.CreateCustomDomain(ctx, domain, app.ID, "token"); err != nil {
			t.Fatal(err)
		}
		if verified {
			if err := m.MarkDomainVerified(ctx, domain); err != nil {
				t.Fatal(err)
			}
		}
		if !expires.IsZero() {
			if err := m.UpdateCustomDomainCertStatus(ctx, domain, state.CustomDomainCertIssued, expires, "", now); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed("late.example.com", true, now.Add(60*24*time.Hour))
	seed("soon.example.com", true, now.Add(5*24*time.Hour))
	seed("mid.example.com", true, now.Add(20*24*time.Hour))
	seed("pending.example.com", false, now.Add(time.Hour))
	seed("unobserved.example.com", true, time.Time{})

	got, err := m.SoonestCustomDomainCertExpiries(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Domain != "soon.example.com" || got[1].Domain != "mid.example.com" {
		t.Fatalf("expiries = %+v, want soon then mid (verified, observed, capped at 2)", got)
	}
	if !got[0].NotAfter.Equal(now.Add(5 * 24 * time.Hour)) {
		t.Fatalf("NotAfter = %s", got[0].NotAfter)
	}
}
