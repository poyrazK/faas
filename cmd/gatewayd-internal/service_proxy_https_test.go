package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testServiceProxyCertificate(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, serial int64, name, certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
		DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testServiceProxyPKI(t *testing.T) (Config, *x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "service-only-test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		ServiceProxyListen: "10.100.0.1:10080", ServiceProxyHTTPSListen: "10.100.0.1:443",
		ServiceProxyTLSCAPath:   filepath.Join(dir, "ca.crt"),
		ServiceProxyTLSCertPath: filepath.Join(dir, "service.crt"),
		ServiceProxyTLSKeyPath:  filepath.Join(dir, "service.key"),
	}
	if err := os.WriteFile(cfg.ServiceProxyTLSCAPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	testServiceProxyCertificate(t, parsed, key, 2, "*.internal", cfg.ServiceProxyTLSCertPath, cfg.ServiceProxyTLSKeyPath)
	return cfg, parsed, key
}

func TestServiceProxyHTTPSConfigAndLeafRotation(t *testing.T) {
	cfg, ca, caKey := testServiceProxyPKI(t)
	tlsConfig, err := serviceProxyHTTPSConfig(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if tlsConfig.MinVersion != tls.VersionTLS13 || strings.Join(tlsConfig.NextProtos, ",") != "h2,http/1.1" {
		t.Fatalf("TLS posture = %+v", tlsConfig)
	}
	first, err := tlsConfig.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	testServiceProxyCertificate(t, ca, caKey, 3, "*.internal", cfg.ServiceProxyTLSCertPath, cfg.ServiceProxyTLSKeyPath)
	second, err := tlsConfig.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Certificate[0]) == string(second.Certificate[0]) {
		t.Fatal("rotated leaf was not reloaded")
	}
	clientRoots := x509.NewCertPool()
	caPEM, _ := os.ReadFile(cfg.ServiceProxyTLSCAPath)
	clientRoots.AppendCertsFromPEM(caPEM)
	server := httptest.NewUnstartedServer(serviceProxyHTTPSHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	server.TLS = tlsConfig
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: clientRoots, ServerName: "billing.internal"}
	client.Transport.(*http.Transport).ForceAttemptHTTP2 = true
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "billing.internal"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("HTTPS status = %d", resp.StatusCode)
	}
	if resp.ProtoMajor != 2 {
		t.Fatalf("HTTPS protocol = %s, want HTTP/2 for gRPC", resp.Proto)
	}
}

func TestServiceProxyHTTPSRejectsUnscopedNamesAndPartialConfig(t *testing.T) {
	cfg, ca, caKey := testServiceProxyPKI(t)
	for _, addr := range []string{"0.0.0.0:443", "10.100.0.2:443", "10.100.0.1:8443"} {
		cfg.ServiceProxyHTTPSListen = addr
		if _, err := serviceProxyHTTPSConfig(&cfg); err == nil {
			t.Errorf("accepted HTTPS listener %q", addr)
		}
	}
	cfg.ServiceProxyHTTPSListen = "10.100.0.1:443"
	testServiceProxyCertificate(t, ca, caKey, 4, "billing.internal", cfg.ServiceProxyTLSCertPath, cfg.ServiceProxyTLSKeyPath)
	if _, err := serviceProxyHTTPSConfig(&cfg); err == nil {
		t.Fatal("accepted leaf without dedicated wildcard SAN")
	}
	testServiceProxyCertificate(t, ca, caKey, 5, "*.internal", cfg.ServiceProxyTLSCertPath, cfg.ServiceProxyTLSKeyPath)
	if err := os.Chmod(cfg.ServiceProxyTLSKeyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := serviceProxyHTTPSConfig(&cfg); err == nil {
		t.Fatal("accepted world-readable private key")
	}
	cfg.ServiceProxyTLSKeyPath = ""
	if _, err := serviceProxyHTTPSConfig(&cfg); err == nil {
		t.Fatal("accepted partial TLS configuration")
	}

	called := 0
	handler := serviceProxyHTTPSHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called++ }))
	for _, tc := range []struct {
		host, sni string
		status    int
	}{
		{"billing.internal", "billing.internal", http.StatusOK},
		{"billing.svc.gregale", "billing.svc.gregale", http.StatusMisdirectedRequest},
		{"billing.internal", "identity.internal", http.StatusMisdirectedRequest},
		{"billing.other.internal", "billing.other.internal", http.StatusMisdirectedRequest},
		{"billing.internal:10080", "billing.internal", http.StatusMisdirectedRequest},
	} {
		req := httptest.NewRequest(http.MethodGet, "https://"+tc.host+"/", nil)
		req.Host = tc.host
		req.TLS = &tls.ConnectionState{ServerName: tc.sni}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Errorf("Host %q/SNI %q = %d, want %d", tc.host, tc.sni, rec.Code, tc.status)
		}
	}
	if called != 1 {
		t.Fatalf("proxy called %d times, want one", called)
	}
}
