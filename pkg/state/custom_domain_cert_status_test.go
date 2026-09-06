package state

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreCustomDomainCertStatus(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "cert-status@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: acct.ID, Slug: "cert-status-app", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := m.CreateCustomDomain(ctx, "status.example.com", app.ID, "token")
	if err != nil {
		t.Fatal(err)
	}
	if domain.CertStatus != CustomDomainCertPending {
		t.Fatalf("new domain cert status = %q, want pending", domain.CertStatus)
	}
	expires := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	checked := expires.Add(-time.Hour)
	if err := m.UpdateCustomDomainCertStatus(ctx, domain.Domain, CustomDomainCertIssued, expires, "", checked); err != nil {
		t.Fatal(err)
	}
	got, err := m.DomainByName(ctx, domain.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if got.CertStatus != CustomDomainCertIssued || !got.CertExpiresAt.Equal(expires) || !got.DNSLastCheckedAt.Equal(checked) {
		t.Fatalf("durable cert state = %#v", got)
	}
}
