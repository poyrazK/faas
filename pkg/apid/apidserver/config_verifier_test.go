// Tests for the PR-B Load*TLSWithVerifier siblings on apid's Config
// (issue #678 / ADR-056). Pins the contract that a non-nil verifier
// surfaces as a tls.Config.VerifyPeerCertificate hook on every dial
// site (githubd client, advisory server, githubd-bridge server), and
// that a nil verifier degrades to the stdlib trust path.
//
// Sister file: config_test.go (LoadConfig + Load*TLS without verifier).
package apidserver

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
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// recordingVerifier is the test-side stub that records every CN
// LookupCN is asked about. Used by the NonNilVerifierInstallsHook
// tests to assert the installed hook actually delegates to the
// stub verifier on every dial-site wire factory.
type recordingVerifier struct {
	mu  sync.Mutex
	CNs []string
	// accept, when true, makes LookupCN return nil (= accept the
	// CN). When false, LookupCN returns ErrNodeVerifierCNMismatch
	// (the same sentinel wire.Load*TLSConfigWithPrefixAndVerifier
	// consults).
	accept bool
}

func (v *recordingVerifier) LookupCN(cn string) error {
	v.mu.Lock()
	v.CNs = append(v.CNs, cn)
	v.mu.Unlock()
	if v.accept {
		return nil
	}
	return wire.ErrNodeVerifierCNMismatch
}

func (v *recordingVerifier) seen() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]string, len(v.CNs))
	copy(out, v.CNs)
	return out
}

// writePKIMaterial writes a self-signed CA + leaf cert + key + CA
// PEM to disk under dir and returns (caPath, certPath, keyPath).
// The leaf CN is "test-apid-leaf" so the LookupCN pin tests can
// assert the verifier receives that exact CN.
func writePKIMaterial(t *testing.T, dir string) (caPath, certPath, keyPath string) {
	t.Helper()
	// Self-signed CA.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey (ca): %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate (ca): %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	// Leaf signed by the CA.
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey (leaf): %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("ParseCertificate (ca): %v", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-apid-leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate (leaf): %v", err)
	}
	leafCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey (leaf): %v", err)
	}
	leafKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyDER})

	caPath = filepath.Join(dir, "ca.pem")
	certPath = filepath.Join(dir, "leaf.crt")
	keyPath = filepath.Join(dir, "leaf.key")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	if err := os.WriteFile(certPath, leafCertPEM, 0o600); err != nil {
		t.Fatalf("write leaf.crt: %v", err)
	}
	if err := os.WriteFile(keyPath, leafKeyPEM, 0o600); err != nil {
		t.Fatalf("write leaf.key: %v", err)
	}
	return caPath, certPath, keyPath
}

// configureWithPKI fills the three TLS-path fields on c from a fresh
// PKI material write. Used by NonNilVerifierInstallsHook tests.
func configureWithPKI(t *testing.T, c *Config, caPath, certPath, keyPath string, prefix string) {
	t.Helper()
	switch prefix {
	case "advisory_":
		c.AdvisoryTLSCAPath = caPath
		c.AdvisoryTLSCertPath = certPath
		c.AdvisoryTLSKeyPath = keyPath
	case "githubd_bridge_":
		c.GithubdBridgeTLSCAPath = caPath
		c.GithubdBridgeTLSCertPath = certPath
		c.GithubdBridgeTLSKeyPath = keyPath
	case "githubd_":
		c.GithubdClientTLSCAPath = caPath
		c.GithubdClientTLSCertPath = certPath
		c.GithubdClientTLSKeyPath = keyPath
	default:
		t.Fatalf("unknown prefix %q", prefix)
	}
}

// --- LoadAdvisoryTLSWithVerifier ---

func TestConfig_LoadAdvisoryTLSWithVerifier_NilVerifierReturnsAllowAll(t *testing.T) {
	// Empty cluster → (nil, nil) is the single-box back-compat path.
	c := &Config{}
	tls, err := c.LoadAdvisoryTLSWithVerifier(nil)
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	// Non-empty cluster + nil verifier → *tls.Config is built (the
	// stdlib trust path is the production behaviour) but no
	// VerifyPeerCertificate hook is installed. setVerifyHook in
	// pkg/wire/grpc.go:532-540 is the canonical no-op on nil
	// verifier.
	dir := t.TempDir()
	ca, cert, key := writePKIMaterial(t, dir)
	configureWithPKI(t, c, ca, cert, key, "advisory_")
	tls, err = c.LoadAdvisoryTLSWithVerifier(nil)
	if err != nil {
		t.Fatalf("non-empty cluster + nil verifier: err=%v, want nil", err)
	}
	if tls == nil {
		t.Fatal("non-empty cluster: tls = nil, want non-nil")
	}
	if tls.VerifyPeerCertificate != nil {
		t.Errorf("nil verifier: VerifyPeerCertificate = %p, want nil", tls.VerifyPeerCertificate)
	}
}

func TestConfig_LoadAdvisoryTLSWithVerifier_NonNilVerifierInstallsHook(t *testing.T) {
	dir := t.TempDir()
	ca, cert, key := writePKIMaterial(t, dir)
	c := &Config{}
	configureWithPKI(t, c, ca, cert, key, "advisory_")

	stub := &recordingVerifier{accept: true}
	tls, err := c.LoadAdvisoryTLSWithVerifier(stub)
	if err != nil {
		t.Fatalf("LoadAdvisoryTLSWithVerifier: %v", err)
	}
	if tls == nil {
		t.Fatal("tls = nil, want non-nil")
	}
	if tls.VerifyPeerCertificate == nil {
		t.Fatal("VerifyPeerCertificate = nil, want non-nil (stub installed)")
	}

	// Invoke the hook with a synthetic verified chain carrying the
	// "test-apid-leaf" CN. The recording verifier must see exactly
	// that CN.
	leafCert, err := x509.ParseCertificate(mustLeafDER(t, dir))
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	chains := [][]*x509.Certificate{{leafCert}}
	if err := tls.VerifyPeerCertificate(nil, chains); err != nil {
		t.Fatalf("hook invoke: %v", err)
	}
	got := stub.seen()
	if len(got) != 1 || got[0] != "test-apid-leaf" {
		t.Errorf("recording verifier saw CNs %v, want [test-apid-leaf]", got)
	}
}

func TestConfig_LoadAdvisoryTLSWithVerifier_PartialClusterStillRejected(t *testing.T) {
	c := &Config{AdvisoryTLSCertPath: "/some/cert"}
	if _, err := c.LoadAdvisoryTLSWithVerifier(&recordingVerifier{}); err == nil {
		t.Errorf("partial (cert only): expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "advisory_tls_key_path") || !strings.Contains(err.Error(), "advisory_tls_ca_path") {
		t.Errorf("err = %q, want both advisory_tls_key_path and advisory_tls_ca_path named", err.Error())
	}
}

// --- LoadGithubdBridgeTLSWithVerifier ---

func TestConfig_LoadGithubdBridgeTLSWithVerifier_NilVerifierReturnsAllowAll(t *testing.T) {
	c := &Config{}
	tls, err := c.LoadGithubdBridgeTLSWithVerifier(nil)
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	dir := t.TempDir()
	ca, cert, key := writePKIMaterial(t, dir)
	configureWithPKI(t, c, ca, cert, key, "githubd_bridge_")
	tls, err = c.LoadGithubdBridgeTLSWithVerifier(nil)
	if err != nil {
		t.Fatalf("non-empty cluster + nil verifier: err=%v, want nil", err)
	}
	if tls == nil {
		t.Fatal("non-empty cluster: tls = nil, want non-nil")
	}
	if tls.VerifyPeerCertificate != nil {
		t.Errorf("nil verifier: VerifyPeerCertificate = %p, want nil", tls.VerifyPeerCertificate)
	}
}

func TestConfig_LoadGithubdBridgeTLSWithVerifier_NonNilVerifierInstallsHook(t *testing.T) {
	dir := t.TempDir()
	ca, cert, key := writePKIMaterial(t, dir)
	c := &Config{}
	configureWithPKI(t, c, ca, cert, key, "githubd_bridge_")

	stub := &recordingVerifier{accept: true}
	tls, err := c.LoadGithubdBridgeTLSWithVerifier(stub)
	if err != nil {
		t.Fatalf("LoadGithubdBridgeTLSWithVerifier: %v", err)
	}
	if tls == nil || tls.VerifyPeerCertificate == nil {
		t.Fatal("tls or hook = nil, want both non-nil")
	}
	leafCert, err := x509.ParseCertificate(mustLeafDER(t, dir))
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if err := tls.VerifyPeerCertificate(nil, [][]*x509.Certificate{{leafCert}}); err != nil {
		t.Fatalf("hook invoke: %v", err)
	}
	if got := stub.seen(); len(got) != 1 || got[0] != "test-apid-leaf" {
		t.Errorf("CNs %v, want [test-apid-leaf]", got)
	}
}

func TestConfig_LoadGithubdBridgeTLSWithVerifier_PartialClusterStillRejected(t *testing.T) {
	c := &Config{GithubdBridgeTLSCertPath: "/some/cert"}
	if _, err := c.LoadGithubdBridgeTLSWithVerifier(&recordingVerifier{}); err == nil {
		t.Errorf("partial (cert only): expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "githubd_bridge_tls_key_path") || !strings.Contains(err.Error(), "githubd_bridge_tls_ca_path") {
		t.Errorf("err = %q, want both githubd_bridge_tls_key_path and githubd_bridge_tls_ca_path named", err.Error())
	}
}

// --- LoadGithubdTLSWithVerifier ---

func TestConfig_LoadGithubdTLSWithVerifier_NilVerifierReturnsAllowAll(t *testing.T) {
	c := &Config{}
	tls, err := c.LoadGithubdTLSWithVerifier(nil)
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	dir := t.TempDir()
	ca, cert, key := writePKIMaterial(t, dir)
	configureWithPKI(t, c, ca, cert, key, "githubd_")
	tls, err = c.LoadGithubdTLSWithVerifier(nil)
	if err != nil {
		t.Fatalf("non-empty cluster + nil verifier: err=%v, want nil", err)
	}
	if tls == nil {
		t.Fatal("non-empty cluster: tls = nil, want non-nil")
	}
	if tls.VerifyPeerCertificate != nil {
		t.Errorf("nil verifier: VerifyPeerCertificate = %p, want nil", tls.VerifyPeerCertificate)
	}
}

func TestConfig_LoadGithubdTLSWithVerifier_NonNilVerifierInstallsHook(t *testing.T) {
	dir := t.TempDir()
	ca, cert, key := writePKIMaterial(t, dir)
	c := &Config{}
	configureWithPKI(t, c, ca, cert, key, "githubd_")

	stub := &recordingVerifier{accept: true}
	tls, err := c.LoadGithubdTLSWithVerifier(stub)
	if err != nil {
		t.Fatalf("LoadGithubdTLSWithVerifier: %v", err)
	}
	if tls == nil || tls.VerifyPeerCertificate == nil {
		t.Fatal("tls or hook = nil, want both non-nil")
	}
	leafCert, err := x509.ParseCertificate(mustLeafDER(t, dir))
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if err := tls.VerifyPeerCertificate(nil, [][]*x509.Certificate{{leafCert}}); err != nil {
		t.Fatalf("hook invoke: %v", err)
	}
	if got := stub.seen(); len(got) != 1 || got[0] != "test-apid-leaf" {
		t.Errorf("CNs %v, want [test-apid-leaf]", got)
	}
}

func TestConfig_LoadGithubdTLSWithVerifier_PartialClusterStillRejected(t *testing.T) {
	c := &Config{GithubdClientTLSCertPath: "/some/cert"}
	if _, err := c.LoadGithubdTLSWithVerifier(&recordingVerifier{}); err == nil {
		t.Errorf("partial (cert only): expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "githubd_tls_key_path") || !strings.Contains(err.Error(), "githubd_tls_ca_path") {
		t.Errorf("err = %q, want both githubd_tls_key_path and githubd_tls_ca_path named", err.Error())
	}
}

// mustLeafDER re-reads the leaf cert PEM and returns the raw DER
// bytes. Used by the NonNilVerifierInstallsHook tests to construct
// a synthetic verified chain for the hook invocation. Lives next
// to writePKIMaterial because it shares the same temp dir layout.
func mustLeafDER(t *testing.T, dir string) []byte {
	t.Helper()
	pemBytes, err := os.ReadFile(filepath.Join(dir, "leaf.crt"))
	if err != nil {
		t.Fatalf("read leaf.crt: %v", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatalf("leaf.crt is not PEM")
	}
	return block.Bytes
}
