// adr: 520 — issuer observations remain bound to the verified claim.
package gateway

import (
	"context"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type transferringWildcardCA struct {
	delegate *fakeLEIssuer
	transfer func()
}

func (c transferringWildcardCA) IssuerKey() string { return c.delegate.IssuerKey() }
func (c transferringWildcardCA) Issue(ctx context.Context, csr *x509.CertificateRequest) (*certmagic.IssuedCertificate, error) {
	c.transfer()
	return c.delegate.Issue(ctx, csr)
}

func TestWildcardCertIssuerCannotWriteReplacementClaim(t *testing.T) {
	store := state.NewMemStore()
	makeApp := func(email, slug string) state.App {
		account, err := store.CreateAccount(t.Context(), email, api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	oldApp, newApp := makeApp("old-wildcard@example.com", "old-wildcard"), makeApp("new-wildcard@example.com", "new-wildcard")
	const domain = "*.issuer-claim.example.com"
	if _, err := store.CreateCustomDomain(t.Context(), domain, oldApp.ID, "old-proof"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(t.Context(), domain); err != nil {
		t.Fatal(err)
	}
	le, err := NewLetsEncryptCertIssuer(t.TempDir(), "ops@example.com", true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = le.initConfig(); err != nil {
		t.Fatal(err)
	}
	ca := &fakeLEIssuer{}
	ca.setWindow(time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
	newExpiry := time.Now().Add(20 * 24 * time.Hour)
	le.cfg.Issuers = []certmagic.Issuer{transferringWildcardCA{delegate: ca, transfer: func() {
		if err := store.DeleteCustomDomain(t.Context(), domain); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateCustomDomain(t.Context(), domain, newApp.ID, "new-proof"); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDomainVerified(t.Context(), domain); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateCustomDomainCertStatus(t.Context(), domain, state.CustomDomainCertIssued, newExpiry, "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}}}
	issuer := NewTenantSurfaceCertIssuer(store, nil, le, nil)
	if err = issuer.RequestCertForWildcardDomain(t.Context(), domain); !errors.Is(err, state.ErrStaleCustomDomainProbe) {
		t.Fatalf("obsolete issuer result=%v", err)
	}
	current, err := store.DomainByName(t.Context(), domain)
	if err != nil {
		t.Fatal(err)
	}
	if current.AppID != newApp.ID || current.CertStatus != state.CustomDomainCertIssued || !current.CertExpiresAt.Equal(newExpiry) {
		t.Fatalf("replacement certificate overwritten: %+v", current)
	}
}
