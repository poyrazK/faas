// adr: 100 — tenant-surface certificates are minted and renewed per host.
package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
)

// fakeLEIssuer stands in for the ACME CA. It signs whatever CSR certmagic
// sends with a fixed validity window and stores under Let's Encrypt's issuer
// key, so leafPath finds the result.
type fakeLEIssuer struct {
	mu        sync.Mutex
	calls     int
	notBefore time.Time
	notAfter  time.Time
}

func (f *fakeLEIssuer) IssuerKey() string { return "acme-v02.api.letsencrypt.org-directory" }

func (f *fakeLEIssuer) Issue(_ context.Context, csr *x509.CertificateRequest) (*certmagic.IssuedCertificate, error) {
	f.mu.Lock()
	f.calls++
	notBefore, notAfter := f.notBefore, f.notAfter
	f.mu.Unlock()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: csr.DNSNames[0]},
		DNSNames:     csr.DNSNames,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake CA"}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, csr.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	return &certmagic.IssuedCertificate{Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}, nil
}

func (f *fakeLEIssuer) setWindow(notBefore, notAfter time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notBefore, f.notAfter = notBefore, notAfter
}

func (f *fakeLEIssuer) issued() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// The first issuance used to panic (certmagic.New with a nil cache), and a
// stored certificate was never renewed: ObtainCertSync is a no-op whenever
// storage holds one, so the renewer restamped the old NotAfter until it
// lapsed.
func TestLetsEncryptCertIssuerRenewsStoredCertificateInsideItsWindow(t *testing.T) {
	l, err := NewLetsEncryptCertIssuer(t.TempDir(), "ops@example.com", true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.initConfig(); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLEIssuer{}
	l.cfg.Issuers = []certmagic.Issuer{fake}
	ctx := context.Background()
	now := time.Now()

	// An earlier run stored a certificate with 10 of its 90 days left:
	// inside certmagic's renewal window (the last third).
	fake.setWindow(now.Add(-80*24*time.Hour), now.Add(10*24*time.Hour))
	if err := l.cfg.ObtainCertSync(ctx, "renew.example.com"); err != nil {
		t.Fatal(err)
	}
	first := now.Add(10 * 24 * time.Hour)
	if fake.issued() != 1 {
		t.Fatalf("seeding made %d CA orders, want 1", fake.issued())
	}

	fake.setWindow(now.Add(-time.Hour), now.Add(90*24*time.Hour))
	renewed, err := l.Issue(ctx, "renew.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if fake.issued() != 2 || !renewed.After(first.Add(30*24*time.Hour)) {
		t.Fatalf("expiring certificate: orders=%d NotAfter %v → %v; want a renewal", fake.issued(), first, renewed)
	}

	// Outside the window the stored certificate is reused, without an order.
	again, err := l.Issue(ctx, "renew.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if fake.issued() != 2 || !again.Equal(renewed) {
		t.Fatalf("fresh certificate: orders=%d NotAfter %v → %v; want it reused", fake.issued(), renewed, again)
	}
}
