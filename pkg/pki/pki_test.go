package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEnsureCAGeneratesAndWrites validates the happy path of EnsureCA:
// a fresh CA appears at <rootDir>/ca/{ca.crt,ca.key} with the right
// modes and parseable content. The rootDir lives under t.TempDir() so
// it disappears with the test — no manual cleanup.
func TestEnsureCAGeneratesAndWrites(t *testing.T) {
	root := t.TempDir()
	cert, key, err := EnsureCA(root, false)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	if cert == nil || key == nil {
		t.Fatal("EnsureCA returned nil cert/key")
	}
	certPath, keyPath := CARoot(root)

	// Cert mode: 0444.
	certInfo, err := os.Stat(certPath)
	if err != nil {
		t.Fatalf("stat ca.crt: %v", err)
	}
	if got, want := certInfo.Mode().Perm(), os.FileMode(0o444); got != want {
		t.Errorf("ca.crt mode = %#o, want %#o", got, want)
	}
	// Key mode: 0400.
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat ca.key: %v", err)
	}
	if got, want := keyInfo.Mode().Perm(), os.FileMode(0o400); got != want {
		t.Errorf("ca.key mode = %#o, want %#o", got, want)
	}

	// Cert is a parseable self-signed CA.
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read ca.crt: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("ca.crt is not PEM-encoded")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse ca.crt: %v", err)
	}
	if !parsed.IsCA {
		t.Error("ca.crt IsCA = false, want true")
	}
	// NotAfter is roughly 5 years out (CertValidity). Allow a 1-hour
	// skew on either side to absorb clock drift between generation
	// and verification.
	expectedNotAfter := time.Now().Add(CertValidity)
	if delta := parsed.NotAfter.Sub(expectedNotAfter); delta > time.Hour || delta < -time.Hour {
		t.Errorf("ca.crt NotAfter drift = %v, want within ±1h", delta)
	}
}

// TestEnsureCAIdempotent checks that a second EnsureCA call with
// force=false returns the SAME cert/key (no re-issue churn). The
// serial number is the observable: a re-issued CA would carry a fresh
// random serial.
func TestEnsureCAIdempotent(t *testing.T) {
	root := t.TempDir()
	first, _, err := EnsureCA(root, false)
	if err != nil {
		t.Fatalf("first EnsureCA: %v", err)
	}
	second, _, err := EnsureCA(root, false)
	if err != nil {
		t.Fatalf("second EnsureCA: %v", err)
	}
	if first.SerialNumber.Cmp(second.SerialNumber) != 0 {
		t.Errorf("EnsureCA re-issued; first serial=%s, second=%s",
			first.SerialNumber, second.SerialNumber)
	}
}

// TestEnsureLeafSkipsFreshLeaf pins the idempotent path: a freshly
// issued leaf with NotAfter well past ReissueThreshold should be
// returned by EnsureLeaf only as ErrLeafNotExpiringSoon when force is
// false. A second EnsureLeaf call without force on the same leaf
// therefore returns the same sentinel without writing new material.
func TestEnsureLeafSkipsFreshLeaf(t *testing.T) {
	root := t.TempDir()
	caCert, caKey, err := EnsureCA(root, false)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	role := Role{
		CommonName: "schedd.faas",
		Kind:       KindServer,
		Directory:  "schedd",
		Filename:   "server",
		AltNames:   ProductionSANs("schedd.faas"),
	}
	if err := EnsureLeaf(root, role, caCert, caKey, false); err != nil {
		t.Fatalf("first EnsureLeaf: %v", err)
	}
	err = EnsureLeaf(root, role, caCert, caKey, false)
	if !errors.Is(err, ErrLeafNotExpiringSoon) {
		t.Errorf("second EnsureLeaf: got %v, want ErrLeafNotExpiringSoon", err)
	}
}

// TestEnsureLeafForceRotates verifies that force=true re-issues even
// when the existing leaf is fresh. The serial number must differ.
func TestEnsureLeafForceRotates(t *testing.T) {
	root := t.TempDir()
	caCert, caKey, err := EnsureCA(root, false)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	role := Role{
		CommonName: "schedd.faas",
		Kind:       KindServer,
		Directory:  "schedd",
		Filename:   "server",
		AltNames:   ProductionSANs("schedd.faas"),
	}
	if err := EnsureLeaf(root, role, caCert, caKey, false); err != nil {
		t.Fatalf("first EnsureLeaf: %v", err)
	}
	// Read serial before rotate.
	certPath, _ := LeafPaths(root, role)
	before, err := readSerial(certPath)
	if err != nil {
		t.Fatalf("readSerial before: %v", err)
	}
	if err := EnsureLeaf(root, role, caCert, caKey, true); err != nil {
		t.Fatalf("force EnsureLeaf: %v", err)
	}
	after, err := readSerial(certPath)
	if err != nil {
		t.Fatalf("readSerial after: %v", err)
	}
	if before.SerialNumber.Cmp(after.SerialNumber) == 0 {
		t.Errorf("force did not rotate leaf; serial stayed %s", before.SerialNumber)
	}
}

// TestEnsureLeafServerExtKeyUsage pins the EKU choice: a server leaf
// must carry ServerAuth and NOT ClientAuth. This is the defense-in-
// depth on top of the stdlib verifier (a stolen server leaf should not
// be usable as a client credential).
func TestEnsureLeafServerExtKeyUsage(t *testing.T) {
	root := t.TempDir()
	caCert, caKey, err := EnsureCA(root, false)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	role := Role{
		CommonName: "vmmd.faas",
		Kind:       KindServer,
		Directory:  "vmmd",
		Filename:   "server",
		AltNames:   ProductionSANs("vmmd.faas"),
	}
	if err := EnsureLeaf(root, role, caCert, caKey, false); err != nil {
		t.Fatalf("EnsureLeaf: %v", err)
	}
	certPath, _ := LeafPaths(root, role)
	cert := parseTestCert(t, certPath)

	if !hasEKU(cert, x509.ExtKeyUsageServerAuth) {
		t.Error("server leaf missing ServerAuth EKU")
	}
	if hasEKU(cert, x509.ExtKeyUsageClientAuth) {
		t.Error("server leaf carries ClientAuth EKU — defense-in-depth violation")
	}
}

// TestEnsureLeafClientExtKeyUsage is the symmetric test for client
// leaves: must carry ClientAuth, must NOT carry ServerAuth.
func TestEnsureLeafClientExtKeyUsage(t *testing.T) {
	root := t.TempDir()
	caCert, caKey, err := EnsureCA(root, false)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	role := Role{
		CommonName: "vmmd.faas",
		Kind:       KindClient,
		Directory:  "vmmd",
		Filename:   "schedd-client",
		AltNames:   ProductionSANs("vmmd.faas"),
	}
	if err := EnsureLeaf(root, role, caCert, caKey, false); err != nil {
		t.Fatalf("EnsureLeaf: %v", err)
	}
	certPath, _ := LeafPaths(root, role)
	cert := parseTestCert(t, certPath)

	if !hasEKU(cert, x509.ExtKeyUsageClientAuth) {
		t.Error("client leaf missing ClientAuth EKU")
	}
	if hasEKU(cert, x509.ExtKeyUsageServerAuth) {
		t.Error("client leaf carries ServerAuth EKU — defense-in-depth violation")
	}
}

// TestEnsureLeafIncludesLocalDevSANs pins the SAN list: every leaf
// must carry localhost + 127.0.0.1 + ::1 so single-box tests stay
// correct. The CommonName (e.g. schedd.faas) is added in addition.
func TestEnsureLeafIncludesLocalDevSANs(t *testing.T) {
	root := t.TempDir()
	caCert, caKey, err := EnsureCA(root, false)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	role := Role{
		CommonName: "schedd.faas",
		Kind:       KindServer,
		Directory:  "schedd",
		Filename:   "server",
		AltNames:   ProductionSANs("schedd.faas"),
	}
	if err := EnsureLeaf(root, role, caCert, caKey, false); err != nil {
		t.Fatalf("EnsureLeaf: %v", err)
	}
	certPath, _ := LeafPaths(root, role)
	cert := parseTestCert(t, certPath)

	wantDNS := map[string]bool{"localhost": false, "schedd.faas": false}
	for _, name := range cert.DNSNames {
		if _, ok := wantDNS[name]; ok {
			wantDNS[name] = true
		}
	}
	for name, seen := range wantDNS {
		if !seen {
			t.Errorf("missing DNS SAN %q in leaf (got %v)", name, cert.DNSNames)
		}
	}
	wantIPs := map[string]bool{"127.0.0.1": false, "::1": false}
	for _, ip := range cert.IPAddresses {
		if _, ok := wantIPs[ip.String()]; ok {
			wantIPs[ip.String()] = true
		}
	}
	for ip, seen := range wantIPs {
		if !seen {
			t.Errorf("missing IP SAN %q in leaf (got %v)", ip, cert.IPAddresses)
		}
	}
}

// TestEnsureLeafSignedByCA verifies the chain: the leaf's issuer DN
// must equal the CA's subject DN. This is the load-bearing chain
// trust that the stdlib verifier will check on every dial.
func TestEnsureLeafSignedByCA(t *testing.T) {
	root := t.TempDir()
	caCert, caKey, err := EnsureCA(root, false)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	role := Role{
		CommonName: "schedd.faas",
		Kind:       KindServer,
		Directory:  "schedd",
		Filename:   "server",
		AltNames:   ProductionSANs("schedd.faas"),
	}
	if err := EnsureLeaf(root, role, caCert, caKey, false); err != nil {
		t.Fatalf("EnsureLeaf: %v", err)
	}
	certPath, _ := LeafPaths(root, role)
	leaf := parseTestCert(t, certPath)

	if leaf.Issuer.CommonName != caCert.Subject.CommonName {
		t.Errorf("leaf Issuer CN = %q, want %q (CA's Subject CN)",
			leaf.Issuer.CommonName, caCert.Subject.CommonName)
	}
}

// TestRolesIncludesEveryDaemon pins the canonical role set: a future
// operator who adds a daemon (e.g. a new "auditd") must update Roles()
// AND every per-daemon TOML that references it. This test guards
// against silent drift — if the role list shrinks, something is wrong.
func TestRolesIncludesEveryDaemon(t *testing.T) {
	roles := Roles()
	wantDirs := []string{"schedd", "vmmd", "builderd", "gatewayd", "apid", "meterd", "githubd", "egress"}
	seen := map[string]bool{}
	for _, r := range roles {
		seen[r.Directory] = true
	}
	for _, d := range wantDirs {
		if !seen[d] {
			t.Errorf("Roles() missing directory %q (got %v)", d, keys(seen))
		}
	}
}

// TestLeafPathsRoundTrip pins the path helper: every Role's
// LeafPaths() must produce a stable suffix so per-daemon TOML
// references like `tls_cert_path = "/etc/faas/tls/<dir>/<file>.crt"`
// stay accurate.
func TestLeafPathsRoundTrip(t *testing.T) {
	role := Role{Directory: "vmmd", Filename: "schedd-client"}
	certPath, keyPath := LeafPaths("/etc/faas/tls", role)
	if got, want := certPath, "/etc/faas/tls/vmmd/schedd-client.crt"; got != want {
		t.Errorf("LeafPaths cert = %q, want %q", got, want)
	}
	if got, want := keyPath, "/etc/faas/tls/vmmd/schedd-client.key"; got != want {
		t.Errorf("LeafPaths key = %q, want %q", got, want)
	}
}

// TestInsecureModeRejected covers the LoadExistingLeaf path: a leaf
// written with mode 0644 must be refused on subsequent loads. This is
// the tamper-detection story — a misconfigured install is caught at
// the next `gregale pki status`, not at a TLS handshake failure weeks
// later.
func TestInsecureModeRejected(t *testing.T) {
	root := t.TempDir()
	caCert, caKey, err := EnsureCA(root, false)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	role := Role{
		CommonName: "schedd.faas",
		Kind:       KindServer,
		Directory:  "schedd",
		Filename:   "server",
		AltNames:   ProductionSANs("schedd.faas"),
	}
	if err := EnsureLeaf(root, role, caCert, caKey, false); err != nil {
		t.Fatalf("EnsureLeaf: %v", err)
	}
	certPath, _ := LeafPaths(root, role)
	// Loosen the cert mode — the public side allows 0644 in some
	// production scenarios; the test specifically wants to verify the
	// loader's rejection.
	if err := os.Chmod(certPath, 0o644); err != nil {
		t.Fatalf("chmod cert: %v", err)
	}
	// Next EnsureLeaf must fail with ErrInsecurePubKeyPerms.
	err = EnsureLeaf(root, role, caCert, caKey, false)
	if err == nil {
		t.Fatal("EnsureLeaf accepted insecure cert mode")
	}
	if !strings.Contains(err.Error(), ErrInsecurePubKeyPerms.Error()) {
		t.Errorf("EnsureLeaf error = %v, want to mention ErrInsecurePubKeyPerms", err)
	}
}

// --- helpers -------------------------------------------------------------

func parseTestCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("%q is not PEM", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse %q: %v", path, err)
	}
	return cert
}

func readSerial(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, os.ErrInvalid
	}
	return x509.ParseCertificate(block.Bytes)
}

func hasEKU(cert *x509.Certificate, want x509.ExtKeyUsage) bool {
	for _, e := range cert.ExtKeyUsage {
		if e == want {
			return true
		}
	}
	return false
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Sanity check the path layout matches what the per-daemon TOML
// references — if this drifts, every *.toml.example file needs updating.
func TestCARootPathStable(t *testing.T) {
	certPath, keyPath := CARoot("/etc/faas/tls")
	if got, want := certPath, filepath.Join("/etc/faas/tls", "ca", "ca.crt"); got != want {
		t.Errorf("CARoot cert = %q, want %q", got, want)
	}
	if got, want := keyPath, filepath.Join("/etc/faas/tls", "ca", "ca.key"); got != want {
		t.Errorf("CARoot key = %q, want %q", got, want)
	}
}

// TestEnsureLeafRejectsCertKeyMismatch guards the load-bearing pair
// validation in loadExistingLeaf. Without the pair check, a leaf
// whose cert was rotated but whose key was left dangling from a
// previous rotate would slip past the loader and fail at the first
// TLS handshake with an opaque "private key does not match public
// key" error. The pair check surfaces the mismatch at the next
// `gregale pki status` instead.
//
// The test installs a valid leaf, then replaces the key on disk with
// a freshly generated one (different private key, same cert on top)
// so the cert↔key pair no longer matches. The next EnsureLeaf must
// fail rather than return ErrLeafNotExpiringSoon.
func TestEnsureLeafRejectsCertKeyMismatch(t *testing.T) {
	root := t.TempDir()
	caCert, caKey, err := EnsureCA(root, false)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	role := Role{
		CommonName: "schedd.faas",
		Kind:       KindServer,
		Directory:  "schedd",
		Filename:   "server",
		AltNames:   ProductionSANs("schedd.faas"),
	}
	if err := EnsureLeaf(root, role, caCert, caKey, false); err != nil {
		t.Fatalf("first EnsureLeaf: %v", err)
	}
	_, keyPath := LeafPaths(root, role)

	// Replace the key with a freshly generated one — same cert,
	// different private key. Catches a previous rotate that left
	// the key file dangling.
	freshKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate fresh key: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(freshKey)
	if err != nil {
		t.Fatalf("marshal fresh key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	// Remove first — the existing key was written 0o400 by EnsureLeaf,
	// and os.WriteFile doesn't truncate a read-only file owned by us
	// on POSIX (no owner-w bit). Removing and re-writing mirrors what
	// writeLeaf does internally (removeIfExists → WriteFile).
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove stale key: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o400); err != nil {
		t.Fatalf("write fresh key: %v", err)
	}

	// Next EnsureLeaf must fail with a pair-mismatch error rather
	// than returning ErrLeafNotExpiringSoon (which would silently
	// skip the re-issue).
	err = EnsureLeaf(root, role, caCert, caKey, false)
	if err == nil {
		t.Fatal("EnsureLeaf accepted cert↔key pair mismatch")
	}
	if errors.Is(err, ErrLeafNotExpiringSoon) {
		t.Errorf("EnsureLeaf returned ErrLeafNotExpiringSoon on a mismatched pair — loader did not validate the pair")
	}
	if !strings.Contains(err.Error(), "cert↔key pair mismatch") {
		t.Errorf("EnsureLeaf error = %v, want to mention 'cert↔key pair mismatch'", err)
	}
}

// TestEnsureLeafRejectsMissingKeyWithCert is the second pair-mismatch
// path: a cert exists but the key file is missing (typically a
// half-finished rotate). The loader should refuse rather than return
// the cert as valid.
func TestEnsureLeafRejectsMissingKeyWithCert(t *testing.T) {
	root := t.TempDir()
	caCert, caKey, err := EnsureCA(root, false)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	role := Role{
		CommonName: "schedd.faas",
		Kind:       KindServer,
		Directory:  "schedd",
		Filename:   "server",
		AltNames:   ProductionSANs("schedd.faas"),
	}
	if err := EnsureLeaf(root, role, caCert, caKey, false); err != nil {
		t.Fatalf("first EnsureLeaf: %v", err)
	}
	certPath, keyPath := LeafPaths(root, role)
	// Remove the key — leave the cert dangling.
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove key: %v", err)
	}
	// The cert is still on disk from the first EnsureLeaf, so
	// loadExistingLeaf's cert-missing branch does NOT short-circuit.
	_ = certPath

	err = EnsureLeaf(root, role, caCert, caKey, false)
	if err == nil {
		t.Fatal("EnsureLeaf accepted a cert without a key")
	}
	if !strings.Contains(err.Error(), "is missing") {
		t.Errorf("EnsureLeaf error = %v, want to mention 'is missing'", err)
	}
}
