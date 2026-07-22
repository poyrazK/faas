package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleConfig = `
public_addr = ":443"
control_addr = "127.0.0.1:9090"
apps_domain = "apps.example.com"
apid_loopback = "http://127.0.0.1:8081"
githubd_loopback = "http://127.0.0.1:8083"

[tls]
disabled = false
wildcard_cert_domain = "apps.example.com"
hetzner_dns_api_token_path = "/etc/faas/secrets/hetzner-dns.token"
hetzner_zone = "example.com"
storage_dir = "/var/lib/faas/certs"
contact_email = "ops@example.com"
use_staging_ca = true
`

func TestLoadConfig_DefaultsWhenMissing(t *testing.T) {
	c, err := LoadConfig("/no/such/path")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if c.PublicAddr != ":8080" {
		t.Errorf("default PublicAddr = %q, want :8080", c.PublicAddr)
	}
	if c.ControlAddr != "127.0.0.1:9090" {
		t.Errorf("default ControlAddr = %q, want 127.0.0.1:9090", c.ControlAddr)
	}
	if !c.TLS.Disabled {
		t.Error("default TLS.Disabled should be true (e2e harness path)")
	}
}

func TestLoadConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gatewayd.toml")
	if err := os.WriteFile(path, []byte(sampleConfig), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.PublicAddr != ":443" {
		t.Errorf("PublicAddr = %q, want :443", c.PublicAddr)
	}
	if c.AppsDomain != "apps.example.com" {
		t.Errorf("AppsDomain = %q, want apps.example.com", c.AppsDomain)
	}
	if c.TLS.Disabled {
		t.Error("TLS.Disabled should be false")
	}
	if c.TLS.WildcardCertDomain != "apps.example.com" {
		t.Errorf("WildcardCertDomain = %q", c.TLS.WildcardCertDomain)
	}
	if c.TLS.HetznerZone != "example.com" {
		t.Errorf("HetznerZone = %q", c.TLS.HetznerZone)
	}
	if c.TLS.StorageDir != "/var/lib/faas/certs" {
		t.Errorf("StorageDir = %q", c.TLS.StorageDir)
	}
	if c.TLS.ContactEmail != "ops@example.com" {
		t.Errorf("ContactEmail = %q, want ops@example.com", c.TLS.ContactEmail)
	}
	if !c.TLS.UseStagingCA {
		t.Error("UseStagingCA should be true from the fixture")
	}
	if got := c.resolveTLSConfig(func(string) bool { return true }); !got.UseStagingCA || got.ContactEmail != "ops@example.com" {
		t.Errorf("resolveTLSConfig lost TLS fields: %+v", got)
	}
}

// TestConfig_ResolveTLSConfig — the on-disk shape (TOMLTLSConfig) lacks the
// allowlist (function pointer). resolveTLSConfig injects it; the rest of the
// daemon consumes gateway.TLSConfig.
func TestConfig_ResolveTLSConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gatewayd.toml")
	if err := os.WriteFile(path, []byte(sampleConfig), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	called := false
	allowlist := func(string) bool { called = true; return true }
	got := c.resolveTLSConfig(allowlist)
	if got.Disabled {
		t.Error("resolved TLS.Disabled = true, want false")
	}
	if got.OnDemandHTTP01Allowlist == nil {
		t.Fatal("resolved allowlist is nil")
	}
	if !got.OnDemandHTTP01Allowlist("any.example.com") {
		t.Error("allowlist returned false on a permissive closure")
	}
	if !called {
		t.Error("allowlist was not invoked")
	}
}

func TestLoadConfig_GarbageTOMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gatewayd.toml")
	if err := os.WriteFile(path, []byte("this is not = toml = =\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Error("garbage TOML should produce an error, got nil")
	}
}

// TestConfig_LoadVMMDPingTLS covers the [vmmd_tls] cluster that the
// per-node dial closure consumes (issue #98 / ADR-028, plumbed via
// issue #120). Three branches mirror the cmd/schedd LoadVMMTLS
// contract (test there for the server-side reference):
//
//   - all-empty cluster → (nil, nil). Single-box default keeps
//     the legacy behaviour: gatewayd dials the unix socket with
//     no TLS, and wire.DialContext accepts nil TLS for unix
//     targets.
//   - partial cluster → non-nil error mentioning the missing
//     vmmd_tls_* fields by name (so an operator can map the
//     error straight to a TOML key, per the LoadVMMDPingTLS
//     contract documented in config.go).
//   - populated cluster → wire.LoadClientTLSConfigWithPrefix is
//     invoked; its success path falls through wire's helper to
//     the file fixture test. We use real fixture files written
//     to t.TempDir() so the test exercises the full load path,
//     not a stubbed one.
func TestConfig_LoadVMMDPingTLS(t *testing.T) {
	t.Run("all empty returns nil config", func(t *testing.T) {
		c := &Config{}
		tlsCfg, err := c.LoadVMMDPingTLS()
		if err != nil || tlsCfg != nil {
			t.Errorf("all-empty: tlsCfg=%v err=%v, want nil", tlsCfg, err)
		}
	})
	t.Run("partial cluster names missing fields", func(t *testing.T) {
		c := &Config{VMMDPingTLSCertPath: "/some/cert"}
		if _, err := c.LoadVMMDPingTLS(); err == nil {
			t.Fatal("partial cluster: expected error, got nil")
		} else if !strings.Contains(err.Error(), "vmmd_tls_key_path") || !strings.Contains(err.Error(), "vmmd_tls_ca_path") {
			t.Errorf("err = %q, want both vmmd_tls_key_path and vmmd_tls_ca_path named", err.Error())
		}
	})
	t.Run("populated cluster loads mTLS", func(t *testing.T) {
		// Real fixture files written under t.TempDir so the test
		// exercises wire.LoadClientTLSConfigWithPrefix end-to-end.
		// contents are deliberately minimal: a self-signed client
		// cert, matching key, and CA. The CA bundle can hold a
		// single PEM block; wire's helper tolerates extra whitespace.
		dir := t.TempDir()
		certPath, keyPath, caPath := writeTLSFixtures(t, dir)
		c := &Config{
			VMMDPingTLSCertPath: certPath,
			VMMDPingTLSKeyPath:  keyPath,
			VMMDPingTLSCAPath:   caPath,
		}
		tlsCfg, err := c.LoadVMMDPingTLS()
		if err != nil {
			t.Fatalf("LoadVMMDPingTLS: %v", err)
		}
		if tlsCfg == nil {
			t.Fatal("tlsCfg is nil for populated cluster")
		}
		// wire.LoadClientTLSConfigWithPrefix wires the resulting
		// *tls.Config for outbound mTLS: Certificates must include
		// the leaf we generated, and RootCAs must include the CA
		// we wrote. Pinned here so a regression that drops the
		// root or the leaf at load time surfaces in CI rather than
		// at first dial.
		if len(tlsCfg.Certificates) != 1 {
			t.Errorf("Certificates len = %d, want 1", len(tlsCfg.Certificates))
		}
		if tlsCfg.RootCAs == nil {
			t.Error("RootCAs is nil; CA bundle not loaded")
		}
	})
}

// writeTLSFixtures writes a self-signed client cert + key + CA into
// dir and returns the three paths. Used by
// TestConfig_LoadVMMDPingTLS to exercise wire.LoadClientTLSConfigWithPrefix
// end-to-end. The cert has no real chain — wire's helper does not
// verify the chain at load time (that happens at dial time via the
// stdlib default verifier) — so a self-signed root is sufficient for
// the load path. The big.Int serial is rand-only (not zero) because
// some parsers reject serial=0 as "no serial".
func writeTLSFixtures(t *testing.T, dir string) (certPath, keyPath, caPath string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen ca key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "faas-test-vmmd-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create ca cert: %v", err)
	}
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen cert key: %v", err)
	}
	clientTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano() + 1),
		Subject:      pkix.Name{CommonName: "faas-test-vmmd-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTmpl, caTmpl, &certKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create client cert: %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER})
	keyDER, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	caPath = filepath.Join(dir, "vmmd_ca.pem")
	certPath = filepath.Join(dir, "vmmd_cert.pem")
	keyPath = filepath.Join(dir, "vmmd_key.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath, caPath
}
