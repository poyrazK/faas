package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreMarkCustomDomainDNSDrifted(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "dns-drift@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{
		ID:        uuid.NewString(),
		AccountID: account.ID,
		Slug:      "dns-drift",
		Status:    AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}

	if transitioned, err := m.MarkCustomDomainDNSDrifted(ctx, "missing.example.com", time.Now(), "missing"); !errors.Is(err, ErrNotFound) || transitioned {
		t.Fatalf("missing domain = (%v, %v), want (false, ErrNotFound)", transitioned, err)
	}

	pending, err := m.CreateCustomDomain(ctx, "pending.example.com", app.ID, "pending-token")
	if err != nil {
		t.Fatal(err)
	}
	checkedAt := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	if transitioned, err := m.MarkCustomDomainDNSDrifted(ctx, pending.Domain, checkedAt, "points at the wrong target"); err != nil || transitioned {
		t.Fatalf("pending domain = (%v, %v), want (false, nil)", transitioned, err)
	}
	unchanged, err := m.DomainByName(ctx, pending.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.CertStatus != CustomDomainCertPending || !unchanged.VerifiedAt.IsZero() {
		t.Fatalf("pending domain changed: %+v", unchanged)
	}

	verified, err := m.CreateCustomDomain(ctx, "verified.example.com", app.ID, "verified-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDomainVerified(ctx, verified.Domain); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateCustomDomainCertStatus(ctx, verified.Domain, CustomDomainCertIssued,
		checkedAt.Add(24*time.Hour), "stale", checkedAt); err != nil {
		t.Fatal(err)
	}

	if transitioned, err := m.MarkCustomDomainDNSDrifted(ctx, verified.Domain, checkedAt, "wrong CNAME"); err != nil || !transitioned {
		t.Fatalf("verified domain = (%v, %v), want (true, nil)", transitioned, err)
	}
	drifted, err := m.DomainByName(ctx, verified.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if drifted.Verified() || drifted.CertStatus != CustomDomainCertDNSDrifted || !drifted.CertExpiresAt.IsZero() || drifted.CertLastError != "wrong CNAME" || !drifted.DNSLastCheckedAt.Equal(checkedAt) {
		t.Fatalf("drift transition = %+v", drifted)
	}

	refreshedAt := checkedAt.Add(time.Minute)
	if transitioned, err := m.MarkCustomDomainDNSDrifted(ctx, verified.Domain, refreshedAt, "still wrong"); err != nil || transitioned {
		t.Fatalf("repeated drift = (%v, %v), want (false, nil)", transitioned, err)
	}
	drifted, err = m.DomainByName(ctx, verified.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if !drifted.DNSLastCheckedAt.Equal(refreshedAt) || drifted.CertLastError != "still wrong" {
		t.Fatalf("repeated drift did not refresh observation: %+v", drifted)
	}
}
