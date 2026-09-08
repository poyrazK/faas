package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreCertIssuanceFailureAgeAndCooldown(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "cert-failure-pg@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "cert-failure-pg-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := s.CreateCustomDomain(ctx, "failed-pg.example.com", app.ID, "token")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	failedAt := now.Add(-16 * time.Minute)
	if err := s.UpdateCustomDomainCertStatus(ctx, domain.Domain, state.CustomDomainCertFailed, time.Time{}, "dns-01 failed", failedAt); err != nil {
		t.Fatal(err)
	}
	if got, err := s.CountFailedCertIssuancesSince(ctx, acct.ID, app.ID, now.Add(-15*time.Minute)); err != nil || got != 1 {
		t.Fatalf("CountFailedCertIssuancesSince = (%d, %v), want (1, nil)", got, err)
	}
	if got, err := s.CountFailedCertIssuancesSince(ctx, acct.ID, "", now.Add(-15*time.Minute)); err != nil || got != 1 {
		t.Fatalf("account-scoped count = (%d, %v), want (1, nil)", got, err)
	}
	if claimed, err := s.ClaimCustomDomainCertFailureEmail(ctx, domain.Domain, now); err != nil || !claimed {
		t.Fatalf("first claim = (%v, %v), want (true, nil)", claimed, err)
	}
	if claimed, err := s.ClaimCustomDomainCertFailureEmail(ctx, domain.Domain, now.Add(23*time.Hour)); err != nil || claimed {
		t.Fatalf("cooldown claim = (%v, %v), want (false, nil)", claimed, err)
	}
	if claimed, err := s.ClaimCustomDomainCertFailureEmail(ctx, domain.Domain, now.Add(25*time.Hour)); err != nil || !claimed {
		t.Fatalf("post-cooldown claim = (%v, %v), want (true, nil)", claimed, err)
	}
}
