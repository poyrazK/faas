package main

import (
	"context"
	"crypto/x509"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDNSPoller_CustomDomainDriftRevokesVerificationOnce(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "drift@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "drift", RAMMB: 256, Status: state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := store.CreateCustomDomain(ctx, "api.example.com", app.ID, "txt-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, domain.Domain); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateCustomDomainCertStatus(ctx, domain.Domain, state.CustomDomainCertIssued, time.Now().Add(24*time.Hour), "", time.Now()); err != nil {
		t.Fatal(err)
	}

	withSeams(t,
		func(_ context.Context, _ string) ([]string, error) { return []string{"203.0.113.10"}, nil },
		func(_ context.Context, _ string) ([]string, error) {
			return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
		},
		func(_ context.Context, _ string) ([]string, error) {
			return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
		}, "edge.gregale.dev")
	prevCNAME := cnameLookupFunc
	cnameLookupFunc = func(_ context.Context, _ string) (string, error) {
		return "elsewhere.example.", nil
	}
	t.Cleanup(func() { cnameLookupFunc = prevCNAME })
	prevDial := dialCertFunc
	dialCertFunc = func(_ context.Context, _ string) (*x509.Certificate, error) {
		return &x509.Certificate{NotAfter: time.Now().Add(24 * time.Hour)}, nil
	}
	t.Cleanup(func() { dialCertFunc = prevDial })

	log := slog.Default()
	srv := &server{store: store, audit: newAuditor(store, log, nil), notif: noopNotifier{}}
	srv.runDoctorForDomain(ctx, log, domain.Domain)
	got, err := store.DomainByName(ctx, domain.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if got.Verified() {
		t.Fatal("domain remains verified after CNAME drift")
	}
	if got.CertStatus != state.CustomDomainCertDNSDrifted {
		t.Fatalf("cert status = %q, want dns_drifted", got.CertStatus)
	}
	if got.CertLastError == "" {
		t.Fatal("drift reason is empty")
	}

	// A missing TXT record on later verifier ticks must not erase the
	// durable drift warning before the customer fixes the CNAME.
	prevTXT := txtLookupFunc
	txtLookupFunc = func(_ context.Context, _ string) ([]string, error) { return nil, nil }
	t.Cleanup(func() { txtLookupFunc = prevTXT })
	srv.runVerifyOnce(ctx, log)
	got, err = store.DomainByName(ctx, domain.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if got.CertStatus != state.CustomDomainCertDNSDrifted || got.Verified() {
		t.Fatalf("unmatched TXT downgraded drift: verified=%v cert_status=%q", got.Verified(), got.CertStatus)
	}

	srv.runDoctorForDomain(ctx, log, domain.Domain)
	events, err := store.ListEvents(ctx, acct.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	driftEvents := 0
	for _, event := range events {
		if event.Kind == "domain.drifted" {
			driftEvents++
		}
	}
	if driftEvents != 1 {
		t.Fatalf("domain.drifted events = %d, want 1", driftEvents)
	}

	// A matching TXT record alone is not enough while the CNAME is still
	// wrong; the verifier must see both records repaired.
	txtLookupFunc = func(_ context.Context, _ string) ([]string, error) { return []string{"txt-token"}, nil }
	srv.runVerifyOnce(ctx, log)
	got, err = store.DomainByName(ctx, domain.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if got.Verified() || got.CertStatus != state.CustomDomainCertDNSDrifted {
		t.Fatalf("matching TXT with wrong CNAME restored domain: verified=%v cert_status=%q", got.Verified(), got.CertStatus)
	}
	cnameLookupFunc = func(_ context.Context, _ string) (string, error) { return "edge.gregale.dev.", nil }
	srv.runVerifyOnce(ctx, log)
	got, err = store.DomainByName(ctx, domain.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Verified() || got.CertStatus != state.CustomDomainCertPending {
		t.Fatalf("after TXT verification: verified=%v cert_status=%q, want true/pending", got.Verified(), got.CertStatus)
	}
}
