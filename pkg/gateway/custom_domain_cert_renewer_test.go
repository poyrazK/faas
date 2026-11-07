// adr: 520
package gateway

import (
	"context"
	"crypto/x509"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type recoveringCustomDomainCA struct {
	delegate *fakeLEIssuer
	fail     atomic.Bool
	attempts atomic.Int32
}

func (c *recoveringCustomDomainCA) IssuerKey() string { return c.delegate.IssuerKey() }
func (c *recoveringCustomDomainCA) Issue(ctx context.Context, csr *x509.CertificateRequest) (*certmagic.IssuedCertificate, error) {
	c.attempts.Add(1)
	if c.fail.Load() {
		return nil, errors.New("temporary CA outage")
	}
	return c.delegate.Issue(ctx, csr)
}

func customDomainIssuerFixture(t *testing.T) (*state.MemStore, *TenantSurfaceCertIssuer, *recoveringCustomDomainCA, state.CustomDomain) {
	t.Helper()
	t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", "")
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "wildcard-retry@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "wildcard-retry", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	const host = "*.retry.example.com"
	if _, err = store.CreateCustomDomain(t.Context(), host, app.ID, "proof"); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDomainVerified(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	claim, err := store.DomainByName(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	le, err := NewLetsEncryptCertIssuer(t.TempDir(), "ops@example.com", true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = le.initConfig(); err != nil {
		t.Fatal(err)
	}
	ca := &recoveringCustomDomainCA{delegate: &fakeLEIssuer{}}
	ca.delegate.setWindow(time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
	le.cfg.Issuers = []certmagic.Issuer{ca}
	return store, NewTenantSurfaceCertIssuer(store, nil, le, nil), ca, claim
}

func TestWildcardCertIssuerRecoversAfterDurableCooldown(t *testing.T) {
	store, issuer, ca, claim := customDomainIssuerFixture(t)
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "false")
	now := time.Now().UTC()
	issuer.SetNow(func() time.Time { return now })
	ca.fail.Store(true)
	if err := issuer.RequestCertForWildcardDomain(t.Context(), claim.Domain); err == nil {
		t.Fatal("CA outage was not reported")
	}
	ca.fail.Store(false)
	// A new worker observes the existing lease rather than resetting it.
	restarted := NewTenantSurfaceCertIssuer(store, nil, issuer.le, nil).SetNow(func() time.Time { return now })
	renewer := NewCustomDomainCertRenewer(store, restarted, nil)
	renewer.clock = func() time.Time { return now }
	if err := renewer.tickOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ca.attempts.Load() != 1 {
		t.Fatal("restart bypassed durable retry cooldown")
	}
	now = now.Add(api.CustomDomainCertRetryDelay)
	if err := renewer.tickOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	d, err := store.DomainByName(t.Context(), claim.Domain)
	if err != nil || d.CertStatus != state.CustomDomainCertIssued || !d.CertExpiresAt.After(now.Add(80*24*time.Hour)) {
		t.Fatalf("wildcard did not recover: %+v %v", d, err)
	}
	if ca.attempts.Load() != 2 {
		t.Fatalf("CA attempts=%d, want 2", ca.attempts.Load())
	}
	if err = renewer.tickOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ca.attempts.Load() != 2 {
		t.Fatal("healthy wildcard was repeatedly issued")
	}
}

func TestWildcardCertRenewerRepairsMissedNotificationAndRenews(t *testing.T) {
	store, issuer, ca, claim := customDomainIssuerFixture(t)
	now := time.Now().UTC()
	issuer.SetNow(func() time.Time { return now })
	ca.delegate.setWindow(now.Add(-80*24*time.Hour), now.Add(10*24*time.Hour))
	renewer := NewCustomDomainCertRenewer(store, issuer, nil)
	renewer.clock = func() time.Time { return now }
	if err := renewer.tickOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, err := store.DomainByName(t.Context(), claim.Domain)
	if err != nil || first.CertStatus != state.CustomDomainCertIssued {
		t.Fatalf("missed event did not converge: %+v %v", first, err)
	}
	ca.delegate.setWindow(now.Add(-time.Hour), now.Add(90*24*time.Hour))
	now = now.Add(api.CustomDomainCertRetryDelay)
	if err = renewer.tickOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	renewed, err := store.DomainByName(t.Context(), claim.Domain)
	if err != nil || !renewed.CertExpiresAt.After(first.CertExpiresAt.Add(30*24*time.Hour)) {
		t.Fatalf("expiring wildcard was not renewed: before=%v after=%+v err=%v", first.CertExpiresAt, renewed, err)
	}
}

func TestWildcardCertIssuerRejectsStaleQueuedClaim(t *testing.T) {
	store, issuer, ca, claim := customDomainIssuerFixture(t)
	if err := store.DeleteCustomDomain(t.Context(), claim.Domain); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(t.Context(), claim.Domain, claim.AppID, "replacement-proof"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(t.Context(), claim.Domain); err != nil {
		t.Fatal(err)
	}
	if err := issuer.RequestCertForWildcardDomain(state.WithCustomDomainProbe(t.Context(), claim), claim.Domain); !errors.Is(err, state.ErrStaleCustomDomainProbe) {
		t.Fatalf("old queue entry accepted: %v", err)
	}
	if ca.attempts.Load() != 0 {
		t.Fatal("obsolete claim contacted the CA")
	}
	if err := issuer.RequestCertForWildcardDomain(t.Context(), claim.Domain); err != nil {
		t.Fatal(err)
	}
	if ca.attempts.Load() != 1 {
		t.Fatal("obsolete queue entry consumed replacement lease")
	}
}

func TestWildcardCertIssuerHonorsCurrentPlanAndOnDemandMode(t *testing.T) {
	for _, mode := range []string{"downgraded", "on-demand"} {
		t.Run(mode, func(t *testing.T) {
			store, issuer, ca, claim := customDomainIssuerFixture(t)
			if mode == "on-demand" {
				t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", "on_demand")
			} else {
				app, err := store.AppByID(t.Context(), claim.AppID)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.UpdateAccountPlan(t.Context(), app.AccountID, api.PlanFree); err != nil {
					t.Fatal(err)
				}
			}
			if err := NewCustomDomainCertRenewer(store, issuer, nil).tickOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := issuer.RequestCertForWildcardDomain(t.Context(), claim.Domain); err != nil {
				t.Fatal(err)
			}
			if ca.attempts.Load() != 0 {
				t.Fatalf("%s triggered legacy issuance", mode)
			}
		})
	}
}

func TestWildcardCertIssuerUsesCompanyBillingPlan(t *testing.T) {
	store, issuer, ca, claim := customDomainIssuerFixture(t)
	ctx := t.Context()
	creatorApp, err := store.AppByID(ctx, claim.AppID)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "wildcard-company", Name: "Company", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateOrgPlan(ctx, org.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: creatorApp.AccountID, OrgID: org.ID, Slug: "company-certificate", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateAccountPlan(ctx, app.AccountID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteCustomDomain(ctx, claim.Domain); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateCustomDomain(ctx, claim.Domain, app.ID, "company-proof"); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDomainVerified(ctx, claim.Domain); err != nil {
		t.Fatal(err)
	}
	if err = issuer.RequestCertForWildcardDomain(ctx, claim.Domain); err != nil {
		t.Fatal(err)
	}
	if ca.attempts.Load() != 1 {
		t.Fatal("the creator's Free plan suppressed a Pro company certificate")
	}
}

type customDomainRetryReaderFunc func(context.Context, time.Time, time.Time, int) ([]state.CustomDomain, error)

func (f customDomainRetryReaderFunc) ListCustomDomainCertificateRetries(ctx context.Context, now, cutoff time.Time, limit int) ([]state.CustomDomain, error) {
	return f(ctx, now, cutoff, limit)
}

type wildcardRetryIssuerFunc func(context.Context, string) error

func (f wildcardRetryIssuerFunc) RequestCertForWildcardDomain(ctx context.Context, domain string) error {
	return f(ctx, domain)
}

func TestCustomDomainCertificateRetryPassBoundsWorkersAndCancellation(t *testing.T) {
	t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", "")
	reader := customDomainRetryReaderFunc(func(_ context.Context, _, _ time.Time, limit int) ([]state.CustomDomain, error) {
		if limit != api.CustomDomainCertRetryBatchLimit {
			t.Errorf("unbounded retry limit: %d", limit)
		}
		claims := make([]state.CustomDomain, limit)
		for n := range claims {
			claims[n] = state.CustomDomain{Domain: "*.retry.example.com", AppID: "captured-app", ChallengeToken: "captured-proof"}
		}
		return claims, nil
	})
	started := make(chan struct{}, api.CustomDomainCertRetryWorkers)
	var calls atomic.Int32
	issuer := wildcardRetryIssuerFunc(func(ctx context.Context, _ string) error {
		calls.Add(1)
		claim, bound := state.CustomDomainProbe(ctx)
		if !bound || claim.AppID != "captured-app" || claim.ChallengeToken != "captured-proof" {
			t.Error("retry lost captured claim")
		}
		end, ok := ctx.Deadline()
		if !ok || time.Until(end) > api.CustomDomainCertRetryTimeout {
			t.Error("retry has no bounded deadline")
		}
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- NewCustomDomainCertRenewer(reader, issuer, nil).tickOnce(ctx) }()
	for range api.CustomDomainCertRetryWorkers {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("retry workers did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop retry pass")
	}
	if calls.Load() != int32(api.CustomDomainCertRetryWorkers) {
		t.Fatalf("canceled pass started extra work: %d", calls.Load())
	}
}

func TestCustomDomainCertificateRetryPassSkipsReadsInOnDemandMode(t *testing.T) {
	t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", "on_demand")
	reader := customDomainRetryReaderFunc(func(context.Context, time.Time, time.Time, int) ([]state.CustomDomain, error) {
		t.Error("on-demand mode read legacy retry queue")
		return nil, nil
	})
	issuer := wildcardRetryIssuerFunc(func(context.Context, string) error { t.Error("on-demand mode invoked legacy issuer"); return nil })
	if err := NewCustomDomainCertRenewer(reader, issuer, nil).tickOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
}
