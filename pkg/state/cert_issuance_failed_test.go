package state

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreCertIssuanceFailureAgeAndCooldown(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "cert-failure@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: acct.ID, Slug: "cert-failure-app", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := m.CreateCustomDomain(ctx, "failed.example.com", app.ID, "token")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	failedAt := now.Add(-16 * time.Minute)
	if err := m.UpdateCustomDomainCertStatus(ctx, domain.Domain, CustomDomainCertFailed, time.Time{}, "dns-01 failed", failedAt); err != nil {
		t.Fatal(err)
	}
	if got, err := m.CountFailedCertIssuancesSince(ctx, acct.ID, app.ID, now.Add(-15*time.Minute)); err != nil || got != 1 {
		t.Fatalf("CountFailedCertIssuancesSince = (%d, %v), want (1, nil)", got, err)
	}
	if claimed, err := m.ClaimCustomDomainCertFailureEmail(ctx, domain.Domain, now); err != nil || !claimed {
		t.Fatalf("first claim = (%v, %v), want (true, nil)", claimed, err)
	}
	if claimed, err := m.ClaimCustomDomainCertFailureEmail(ctx, domain.Domain, now.Add(23*time.Hour)); err != nil || claimed {
		t.Fatalf("cooldown claim = (%v, %v), want (false, nil)", claimed, err)
	}
	if claimed, err := m.ClaimCustomDomainCertFailureEmail(ctx, domain.Domain, now.Add(25*time.Hour)); err != nil || !claimed {
		t.Fatalf("post-cooldown claim = (%v, %v), want (true, nil)", claimed, err)
	}
}
