// adr: 520 — certificate reloads retain admission and current claim checks.
package gateway_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"go.uber.org/zap"
)

type noIssue struct{ calls *int }

func (n noIssue) IssuerKey() string { return "audit-issuer" }
func (n noIssue) Issue(context.Context, *x509.CertificateRequest) (*certmagic.IssuedCertificate, error) {
	(*n.calls)++
	return nil, errors.New("unexpected issuance")
}

type helloConn struct{ net.Conn }

func (helloConn) RemoteAddr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50000} }
func (helloConn) LocalAddr() net.Addr  { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443} }

func TestOnDemandTLSStoredCertificateSurvivesRepeatedEdgeRestarts(t *testing.T) {
	ctx := t.Context()
	const host = "reload.example.com"
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "reload-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "reload-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateCustomDomain(ctx, host, app.ID, "tok"); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDomainVerified(ctx, host); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expiry := now.Add(90 * 24 * time.Hour)
	if err = store.UpdateCustomDomainCertStatus(ctx, host, state.CustomDomainCertIssued, expiry, "", now); err != nil {
		t.Fatal(err)
	}
	policy := gateway.NewOnDemandTLSPolicy(store, "gregale.dev", nil, nil)
	policy.SetNow(func() time.Time { return now })
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: now.Add(-time.Hour), NotAfter: expiry, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	storage := &certmagic.FileStorage{Path: t.TempDir()}
	calls := 0
	issuer := noIssue{&calls}
	meta, err := json.Marshal(certmagic.CertificateResource{SANs: []string{host}})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		certmagic.StorageKeys.SiteCert(issuer.IssuerKey(), host):       pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		certmagic.StorageKeys.SitePrivateKey(issuer.IssuerKey(), host): pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		certmagic.StorageKeys.SiteMeta(issuer.IssuerKey(), host):       meta,
	} {
		if err = storage.Store(ctx, name, data); err != nil {
			t.Fatal(err)
		}
	}
	hello := &tls.ClientHelloInfo{
		ServerName: host, CipherSuites: []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, tls.TLS_AES_128_GCM_SHA256},
		SupportedCurves: []tls.CurveID{tls.CurveP256, tls.X25519}, SupportedPoints: []uint8{0},
		SupportedVersions: []uint16{tls.VersionTLS13, tls.VersionTLS12},
		SignatureSchemes:  []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256}, Conn: helloConn{},
	}
	load := func() error {
		var cfg *certmagic.Config
		cache := certmagic.NewCache(certmagic.CacheOptions{
			GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return cfg, nil },
			Logger:           zap.NewNop(),
		})
		defer cache.Stop()
		cfg = certmagic.New(cache, certmagic.Config{Storage: storage, Issuers: []certmagic.Issuer{issuer}, Logger: zap.NewNop(), OnDemand: &certmagic.OnDemandConfig{DecisionFunc: func(ctx context.Context, name string) error {
			ok, decision := policy.Decide(ctx, name)
			if !ok {
				return errors.New(decision)
			}
			return nil
		}}})
		cert, err := cfg.GetCertificateWithContext(ctx, hello)
		if err == nil && (cert == nil || len(cert.Certificate) == 0) {
			return errors.New("empty certificate")
		}
		return err
	}
	for restart := 1; restart <= 4; restart++ {
		err = load()
		if err != nil {
			t.Errorf("restart %d denied a valid stored certificate after %d issuer calls: %v", restart, calls, err)
		} else {
			t.Logf("restart %d successfully loaded existing certificate; issuer calls=%d", restart, calls)
		}
		now = now.Add(time.Minute)
	}
	if calls != 0 {
		t.Fatalf("issuance unexpectedly reached: %d", calls)
	}
	now = now.Add(time.Hour)
	if err = load(); err != nil {
		t.Fatalf("stored certificate still failed after backoff elapsed: %v", err)
	}
}
