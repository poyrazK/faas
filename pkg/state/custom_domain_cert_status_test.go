package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreCustomDomainCertStatus(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	acct, err := m.CreateAccount(ctx, "cert-status@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: acct.ID, Slug: "cert-status-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := m.CreateCustomDomain(ctx, "status.example.com", app.ID, "token")
	if err != nil {
		t.Fatal(err)
	}
	if domain.CertStatus != state.CustomDomainCertPending {
		t.Fatalf("new domain cert status = %q, want pending", domain.CertStatus)
	}
	expires := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	checked := expires.Add(-time.Hour)
	if err := m.UpdateCustomDomainCertStatus(ctx, domain.Domain, state.CustomDomainCertIssued, expires, "", checked); err != nil {
		t.Fatal(err)
	}
	got, err := m.DomainByName(ctx, domain.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if got.CertStatus != state.CustomDomainCertIssued || !got.CertExpiresAt.Equal(expires) || !got.DNSLastCheckedAt.Equal(checked) {
		t.Fatalf("durable cert state = %#v", got)
	}
}
