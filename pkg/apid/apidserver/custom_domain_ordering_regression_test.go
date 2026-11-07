// adr: 520
package apidserver_test

import (
	"context"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainDoctorDiscardsSupersededTLSFailure(t *testing.T) {
	t.Setenv("FAAS_DOMAIN_DOCTOR_ENABLED", "true")
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "tls-observation-order")
	const host = "tls-order.example.com"
	domainRecoveryVerified(t, store, host, app, "proof")
	configureDomainDNS(t)
	*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "edge.gregale.dev.", nil }
	*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"8.8.8.8"}, nil }
	*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, nil }
	newer := time.Now().UTC().Add(time.Minute)
	healthy := state.DomainDoctorObservation{Domain: host, ObservedAt: newer, DNSCheckedAt: newer, CertCheckedAt: newer, CertState: "issued", CertNotAfter: newer.Add(time.Hour), PointsToGregale: true}
	*apidserver.DialCertFunc = func(ctx context.Context, _ string) (*x509.Certificate, error) {
		if err := store.UpdateCustomDomainCertStatus(ctx, host, state.CustomDomainCertIssued, healthy.CertNotAfter, "", newer); err != nil {
			return nil, err
		}
		if err := store.UpsertDoctorObservation(ctx, healthy); err != nil {
			return nil, err
		}
		return nil, context.DeadlineExceeded
	}
	response := e.Do(t, http.MethodGet, "/v1/domains/"+host+"/doctor", nil, nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("superseded TLS result: %d %s", response.Code, response.Body)
	}
	obs, err := store.GetDoctorObservation(t.Context(), host)
	if err != nil || obs.CertState != "issued" || !obs.DNSCheckedAt.Equal(newer) {
		t.Fatalf("old TLS failure overwrote newer success: %+v %v", obs, err)
	}
}

func TestCustomDomainVerifierRejectsConcurrentDriftObservation(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "false")
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "verify-observation-order")
	const host = "verify-order.example.com"
	domainRecoveryVerified(t, store, host, app, "proof")
	drift := store
	at := time.Now().UTC().Add(time.Minute)
	if _, err := drift.MarkCustomDomainDNSDrifted(t.Context(), host, at, "old wrong target"); err != nil {
		t.Fatal(err)
	}
	configureDomainDNS(t)
	beforeTXT := *apidserver.TxtLookupFunc
	t.Cleanup(func() { *apidserver.TxtLookupFunc = beforeTXT })
	*apidserver.TxtLookupFunc = func(context.Context, string) ([]string, error) { return []string{"proof"}, nil }
	*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"8.8.8.8"}, nil }
	*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, nil }
	*apidserver.CnameLookupFunc = func(ctx context.Context, _ string) (string, error) {
		_, err := drift.MarkCustomDomainDNSDrifted(ctx, host, at.Add(time.Second), "newer wrong target")
		return "edge.gregale.dev.", err
	}
	(*e.S()).RunVerifyOnce(t.Context(), slog.New(slog.DiscardHandler))
	current, err := store.DomainByName(t.Context(), host)
	if err != nil || current.Verified() || current.CertStatus != state.CustomDomainCertDNSDrifted || current.CertLastError != "newer wrong target" {
		t.Fatalf("old verifier restored a drifted domain: %+v %v", current, err)
	}
}

func TestCustomDomainVerificationCancellationReleasesQueuedClaims(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "false")
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	for i := range api.DomainVerificationWorkers + 1 {
		app := domainRecoveryApp(t, store, fmt.Sprintf("queue-%d@example.com", i), fmt.Sprintf("verify-queue-%d", i))
		if _, err := store.CreateCustomDomain(t.Context(), fmt.Sprintf("queue-%d.example.com", i), app.ID, "proof"); err != nil {
			t.Fatal(err)
		}
	}
	beforeTXT := *apidserver.TxtLookupFunc
	t.Cleanup(func() { *apidserver.TxtLookupFunc = beforeTXT })
	started := make(chan struct{}, api.DomainVerificationWorkers+1)
	*apidserver.TxtLookupFunc = func(ctx context.Context, _ string) ([]string, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { (*e.S()).RunVerifyOnce(ctx, slog.New(slog.DiscardHandler)); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("verification did not stop after cancellation")
		}
	}()
	for range api.DomainVerificationWorkers {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("verification checks did not run concurrently")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("verification did not release cancelled checks")
	}
	rows, err := store.ClaimCustomDomainsForVerification(t.Context(), 64)
	if err != nil || len(rows) != api.DomainVerificationWorkers+1 {
		t.Fatalf("cancelled work retained backoff: available=%d err=%v", len(rows), err)
	}
}

func TestCustomDomainHealthyDoctorDoesNotBlockDriftRecovery(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "false")
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "healthy-drift-recovery")
	const host = "healthy-drift.example.com"
	domainRecoveryVerified(t, store, host, app, "proof")
	if _, err := store.MarkCustomDomainDNSDrifted(t.Context(), host, time.Now(), "old target"); err != nil {
		t.Fatal(err)
	}
	configureDomainDNS(t)
	t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", "")
	*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "edge.gregale.dev.", nil }
	*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"8.8.8.8"}, nil }
	*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, nil }
	beforeTXT := *apidserver.TxtLookupFunc
	t.Cleanup(func() { *apidserver.TxtLookupFunc = beforeTXT })
	*apidserver.TxtLookupFunc = func(ctx context.Context, _ string) ([]string, error) {
		if err := (*e.S()).RunDoctorForDomain(ctx, slog.New(slog.DiscardHandler), host); err != nil {
			return nil, err
		}
		return []string{"proof"}, nil
	}
	(*e.S()).RunVerifyOnce(t.Context(), slog.New(slog.DiscardHandler))
	current, err := store.DomainByName(t.Context(), host)
	if err != nil || !current.Verified() || current.CertStatus != state.CustomDomainCertPending {
		t.Fatalf("healthy doctor blocked recovery: %+v %v", current, err)
	}
}
