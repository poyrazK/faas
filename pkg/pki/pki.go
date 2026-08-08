// Package pki is the local-dev PKI bootstrap for the multi-box control
// plane (ADR-052). It issues an ECDSA P-256 CA + one leaf per daemon
// under /etc/faas/tls/{ca,<daemon>/, reads existing certs to skip the
// re-issue when NotAfter is at least 30 days away, and refuses to write
// keys with anything looser than 0o400 (private) / 0o444 (public) mode.
//
// This package is operator-facing; it is invoked by `gregale pki
// init|status|rotate`. It is NOT used by daemons at runtime — daemons
// only consume the produced certs via tls.LoadX509KeyPair (which is
// already covered by pkg/wire.LoadServerTLSConfig* and friends).
//
// File-mode policy mirrors pkg/cosign/keys.go:
//   - private key  : 0o400 root:root
//   - public cert  : 0o444 root:root
//
// A future operator who wants 0o440 root:faas for a non-root daemon can
// wrap the writer — this slice does not change the canonical install.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// DefaultRootDir is the canonical install path for control-plane PKI
// material. The default-local and multi-box topologies share the same
// layout; the only per-host variation is which daemon leaves are issued
// (compute nodes that don't run schedd still get a schedd server leaf so
// they can verify schedd's cert in the response chain).
const DefaultRootDir = "/etc/faas/tls"

// ReissueThreshold is the cutoff below which leaves are re-issued on
// `gregale pki init` / `rotate`. The 30-day window matches the
// operator runbook for the cosign keypair (cmd/gregale/commands_sign_keys.go).
const ReissueThreshold = 30 * 24 * time.Hour

// LeafValidity is the NotAfter window for newly-issued leaves. 90 days
// is short enough to bound blast radius on key compromise and long
// enough that operator PagerDuty rotation flows are not a monthly chore.
const LeafValidity = 90 * 24 * time.Hour

// CertValidity is the NotAfter window for the CA. 5 years matches the
// step-ca default and is the longest cert lifetime most CAs will mint
// under modern root programs. Operators rotate the CA with
// `--rotate-ca` (out of scope for the first cut; see ADR-052 §Risks).
const CertValidity = 5 * 365 * 24 * time.Hour

// ErrInsecurePrivKeyPerms mirrors cosign.ErrInsecurePrivKeyPerms — kept
// as a separate sentinel so cmd/apid's "refuse to start" check (which
// imports cosign) is not coupled to PKI material.
var ErrInsecurePrivKeyPerms = errors.New("pki: private key mode permits group/other access")

// ErrInsecurePubKeyPerms mirrors cosign.ErrInsecurePubKeyPerms.
var ErrInsecurePubKeyPerms = errors.New("pki: certificate mode permits group/other write/exec/setuid")

// ErrLeafNotExpiringSoon is returned by EnsureLeaf when the existing
// leaf on disk is still well within its validity window. Caller decides
// whether to skip (init) or re-issue (rotate).
var ErrLeafNotExpiringSoon = errors.New("pki: existing leaf is not within reissue threshold")

// Role describes one certificate a daemon needs. A daemon can have
// multiple Roles (e.g. vmmd has a server leaf for its own listener and
// separate client leaves for each remote role it dials — schedd, apid).
//
// The CN of every issued leaf is "<role.CommonName>" (e.g. "schedd.faas",
// "vmmd.faas"). The SAN list per role is fixed so the stdlib verifier
// (chain/SAN/EKU) is the load-bearing gate; no operator-supplied SAN is
// accepted.
type Role struct {
	// CommonName becomes the leaf cert's Subject CN. MUST match the
	// daemon's role so the handler-layer PeerCN() check (ADR-052
	// §Handler-layer peer binding) can refuse mismatches.
	CommonName string

	// Kind is "server" for listener-side leaves, "client" for outbound
	// dial-side leaves. Maps to ExtKeyUsage (ServerAuth / ClientAuth).
	Kind RoleKind

	// Directory is the per-daemon subdirectory under DefaultRootDir
	// (e.g. "vmmd", "schedd"). One dir per daemon regardless of how
	// many leaves it owns; the per-leaf filename distinguishes server
	// vs. client and the remote role being dialled.
	Directory string

	// Filename is the leaf's basename (without the .crt/.key suffix).
	// For server leaves the convention is "server"; for client leaves
	// it's the remote role being dialled (e.g. "schedd-client",
	// "apid-client"). This filename is what the per-daemon TOML
	// references.
	Filename string

	// AltNames (SANs) lists the hostnames/IPs the leaf is valid for.
	// Local-dev leaves always include 127.0.0.1, ::1, and localhost;
	// distributed leaves add the per-daemon CommonName ("schedd.faas",
	// etc.) plus the operator-set per-hostnames via the EnvSAN hook
	// (future patch; not in this slice).
	AltNames AltNames
}

// RoleKind distinguishes server-side from client-side leaves. The
// ExtKeyUsage on the leaf is set from this value.
type RoleKind string

const (
	KindServer RoleKind = "server"
	KindClient RoleKind = "client"
)

// AltNames carries the SANs for a leaf. Empty slices are valid (the
// CommonName alone is the SAN).
type AltNames struct {
	DNSNames    []string
	IPAddresses []net.IP
}

// LocalDevSANs returns the SANs every local-dev leaf carries. The
// stdlib verifier requires a SAN match on every dial; without these,
// every `127.0.0.1` / `localhost` dial in unit tests fails closed.
func LocalDevSANs() AltNames {
	return AltNames{
		DNSNames:    []string{"localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
}

// ProductionSANs returns the SANs every distributed leaf carries.
// Today this is just the per-daemon CommonName; a future slice that
// adds per-host operator SANs (e.g. "vmmd-1.faas.example.com") will
// merge them here.
func ProductionSANs(cn string) AltNames {
	return AltNames{
		DNSNames: []string{cn},
	}
}

// Roles returns the canonical set of leaves every box on the fleet
// needs. The list is intentionally redundant across roles (every box
// gets a schedd server leaf even if it doesn't run schedd) because the
// CA path and dial path are independent — a compute-01 that doesn't
// run schedd still needs the CA to verify schedd's responses.
//
// This is the one place the operator changes when a new daemon is
// added or a new dial relationship lands; the rest of the package
// iterates over it.
//
// Every role populates AltNames with the production SAN set (the
// per-daemon CommonName) so that RFC 6125 SAN matching — which
// ignores the CN when SANs exist — actually accepts a peer dialling
// "<daemon>.faas". LocalDevSANs (127.0.0.1, ::1, localhost) are
// merged in by generateLeaf, so unit tests stay correct.
func Roles() []Role {
	// Per-daemon server leaves.
	serverRoles := []Role{
		{CommonName: "schedd.faas", Kind: KindServer, Directory: "schedd", Filename: "server", AltNames: ProductionSANs("schedd.faas")},
		{CommonName: "vmmd.faas", Kind: KindServer, Directory: "vmmd", Filename: "server", AltNames: ProductionSANs("vmmd.faas")},
		{CommonName: "builderd.faas", Kind: KindServer, Directory: "builderd", Filename: "server", AltNames: ProductionSANs("builderd.faas")},
		{CommonName: "egress.faas", Kind: KindServer, Directory: "egress", Filename: "egress", AltNames: ProductionSANs("egress.faas")},
		{CommonName: "apid.faas", Kind: KindServer, Directory: "apid", Filename: "advisory", AltNames: ProductionSANs("apid.faas")},
		{CommonName: "githubd.faas", Kind: KindServer, Directory: "githubd", Filename: "server", AltNames: ProductionSANs("githubd.faas")},
		{CommonName: "meterd.faas", Kind: KindServer, Directory: "meterd", Filename: "server", AltNames: ProductionSANs("meterd.faas")},
	}

	// Per-daemon outbound client leaves. vmmd dials schedd + apid;
	// meterd dials schedd + gatewayd; gatewayd dials schedd + vmmd;
	// apid dials githubd. Builderd and schedd dials only vmmd (covered
	// by the per-daemon TOML that points at vmmd's "server" leaf as
	// the CA-trustable remote).
	clientRoles := []Role{
		{CommonName: "vmmd.faas", Kind: KindClient, Directory: "vmmd", Filename: "schedd-client", AltNames: ProductionSANs("vmmd.faas")},
		{CommonName: "vmmd.faas", Kind: KindClient, Directory: "vmmd", Filename: "apid-client", AltNames: ProductionSANs("vmmd.faas")},
		{CommonName: "meterd.faas", Kind: KindClient, Directory: "meterd", Filename: "schedd-client", AltNames: ProductionSANs("meterd.faas")},
		{CommonName: "meterd.faas", Kind: KindClient, Directory: "meterd", Filename: "egress-client", AltNames: ProductionSANs("meterd.faas")},
		{CommonName: "gatewayd.faas", Kind: KindClient, Directory: "gatewayd", Filename: "schedd-client", AltNames: ProductionSANs("gatewayd.faas")},
		{CommonName: "gatewayd.faas", Kind: KindClient, Directory: "gatewayd", Filename: "vmmd-client", AltNames: ProductionSANs("gatewayd.faas")},
		{CommonName: "apid.faas", Kind: KindClient, Directory: "apid", Filename: "githubd-client", AltNames: ProductionSANs("apid.faas")},
		{CommonName: "builderd.faas", Kind: KindClient, Directory: "builderd", Filename: "vmmd-client", AltNames: ProductionSANs("builderd.faas")},
		{CommonName: "schedd.faas", Kind: KindClient, Directory: "schedd", Filename: "vmmd-client", AltNames: ProductionSANs("schedd.faas")},
	}

	all := make([]Role, 0, len(serverRoles)+len(clientRoles))
	all = append(all, serverRoles...)
	all = append(all, clientRoles...)
	return all
}

// LeafPaths returns the absolute cert + key paths for role, rooted at
// rootDir (typically DefaultRootDir). The cert is named "<Filename>.crt"
// and the key is "<Filename>.key" inside
// "<rootDir>/<Directory>/". Returns paths the caller can stat / write.
func LeafPaths(rootDir string, role Role) (certPath, keyPath string) {
	dir := filepath.Join(rootDir, role.Directory)
	return filepath.Join(dir, role.Filename+".crt"),
		filepath.Join(dir, role.Filename+".key")
}

// CARoot returns the absolute paths to the CA cert + key under rootDir.
// Convention is "<rootDir>/ca/ca.crt" + "<rootDir>/ca/ca.key"; this is
// what `wire.LoadServerTLSConfig` / `wire.LoadClientTLSConfig` accept
// as the ca_path argument.
func CARoot(rootDir string) (certPath, keyPath string) {
	dir := filepath.Join(rootDir, "ca")
	return filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
}

// EnsureLeaf ensures a leaf for role exists at rootDir/role.Directory/.
// If a leaf is already on disk and NotAfter >= now + ReissueThreshold,
// EnsureLeaf returns ErrLeafNotExpiringSoon (the caller decides whether
// to skip or re-issue). Otherwise a fresh leaf is generated, signed by
// caCert/caKey, and written with strict mode (0o400 private, 0o444 public).
//
// force=true skips the NotAfter check and re-issues unconditionally —
// used by `gregale pki rotate`.
func EnsureLeaf(rootDir string, role Role, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, force bool) error {
	certPath, keyPath := LeafPaths(rootDir, role)

	if !force {
		existing, err := loadExistingLeaf(certPath, keyPath)
		if err != nil {
			return err
		}
		if existing != nil && time.Until(existing.NotAfter) >= ReissueThreshold {
			return ErrLeafNotExpiringSoon
		}
	}

	certPEM, keyPEM, err := generateLeaf(role, caCert, caKey)
	if err != nil {
		return err
	}
	return writeLeaf(certPath, keyPEM, certPEM)
}

// EnsureCA ensures the CA cert + key exist at rootDir/ca/. force=true
// re-issues even if the existing CA is well within its validity window.
// Idempotent under default force=false: if the CA already exists with
// NotAfter > ReissueThreshold from now, returns nil with no I/O.
func EnsureCA(rootDir string, force bool) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPath, keyPath := CARoot(rootDir)

	if !force {
		existingCert, existingKey, err := loadExistingCA(certPath, keyPath)
		if err != nil {
			return nil, nil, err
		}
		if existingCert != nil {
			return existingCert, existingKey, nil
		}
	}

	cert, key, certPEM, keyPEM, err := generateCA()
	if err != nil {
		return nil, nil, err
	}
	if err := writeLeaf(certPath, keyPEM, certPEM); err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// generateCA produces a fresh self-signed CA cert + key. Caller is
// responsible for writing to disk via writeLeaf or via the EnsureCA path.
func generateCA() (*x509.Certificate, *ecdsa.PrivateKey, []byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("pki: generate CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "faas-local-dev-ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(CertValidity),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("pki: create CA cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("pki: parse CA cert: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("pki: marshal CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return cert, key, certPEM, keyPEM, nil
}

// generateLeaf produces a fresh leaf for role signed by caCert/caKey.
func generateLeaf(role Role, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (certPEM, keyPEM []byte, err error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("pki: generate leaf key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	var eku []x509.ExtKeyUsage
	if role.Kind == KindServer {
		// ServerAuth only.
		eku = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	} else {
		// ClientAuth only. Distinguishing the two means a stolen
		// server leaf cannot be used as a client credential, and
		// vice versa — defense in depth on top of the CN binding
		// the stdlib verifier already performs.
		eku = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	sans := mergeAltNames(LocalDevSANs(), role.AltNames)
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: role.CommonName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(LeafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  eku,
		DNSNames:     sans.DNSNames,
		IPAddresses:  sans.IPAddresses,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("pki: create leaf cert: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, nil, fmt.Errorf("pki: marshal leaf key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// writeLeaf writes certPEM at certPath (mode 0o444) and keyPEM at
// keyPath (mode 0o400). The directory is created with 0o755 root:root.
// All existing files at the target paths are removed before the write
// (mirrors cosign.WriteKeyPair's rotate path: a 0400 key has no owner-w
// bit, so a plain open-with-O_WRONLY returns EACCES).
func writeLeaf(certPath string, keyPEM, certPEM []byte) error {
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		return fmt.Errorf("pki: mkdir %q: %w", filepath.Dir(certPath), err)
	}
	if err := removeIfExists(certPath); err != nil {
		return err
	}
	if err := os.WriteFile(certPath, certPEM, 0o444); err != nil {
		return fmt.Errorf("pki: write cert %q: %w", certPath, err)
	}
	if err := os.Chmod(certPath, 0o444); err != nil {
		return fmt.Errorf("pki: chmod cert %q: %w", certPath, err)
	}
	keyPath := certPath[:len(certPath)-len(".crt")] + ".key"
	if err := removeIfExists(keyPath); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o400); err != nil {
		return fmt.Errorf("pki: write key %q: %w", keyPath, err)
	}
	if err := os.Chmod(keyPath, 0o400); err != nil {
		return fmt.Errorf("pki: chmod key %q: %w", keyPath, err)
	}
	return nil
}

// loadExistingLeaf returns the parsed cert at certPath if both cert
// and key exist with valid mode and the cert↔key pair matches.
// Returns (nil, nil) when the cert is missing — caller treats that
// as "issue a fresh leaf". Returns a non-nil error only on real
// problems (bad perms, parse failure, cert↔key mismatch).
//
// The pair check is the load-bearing detection of a misconfigured
// install: a leaf whose cert was rotated but whose key was left
// dangling from a previous rotate. Without the pair check, the
// loader would happily return the cert, EnsureLeaf would skip the
// re-issue (ErrLeafNotExpiringSoon), and the operator would discover
// the mismatch at the first TLS handshake with an opaque "private
// key does not match public key" error weeks later. The pair-roundtrip
// uses the standard library's tls.X509KeyPair so we don't reinvent
// the parsing.
func loadExistingLeaf(certPath, keyPath string) (*x509.Certificate, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("pki: read cert %q: %w", certPath, err)
	}
	if err := enforceCertMode(certPath); err != nil {
		return nil, err
	}
	// Read the key BEFORE enforceKeyMode so a missing key surfaces as
	// the clear "is missing" error rather than enforceKeyMode's
	// "stat ... no such file or directory" diagnostic. The pair-mismatch
	// detection depends on the key bytes anyway.
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("pki: cert %q exists but key %q is missing (rotate stale): %w", certPath, keyPath, err)
		}
		return nil, fmt.Errorf("pki: read key %q: %w", keyPath, err)
	}
	if err := enforceKeyMode(keyPath); err != nil {
		return nil, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("pki: cert %q is not PEM-encoded", certPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pki: parse cert %q: %w", certPath, err)
	}
	// cert↔key pair validation. The stdlib's tls.X509KeyPair round-trip
	// is the canonical check; stdlib matches the public key in the
	// cert against the private key and returns an error on mismatch.
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return nil, fmt.Errorf("pki: cert↔key pair mismatch for %q: %w", certPath, err)
	}
	return cert, nil
}

// loadExistingCA returns the parsed cert + key at the standard CA
// paths. Returns (nil, nil, nil) if either is missing.
func loadExistingCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cert, err := loadExistingLeaf(certPath, keyPath)
	if err != nil || cert == nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("pki: read CA key %q: %w", keyPath, err)
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("pki: CA key %q is not PEM-encoded", keyPath)
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("pki: parse CA key %q: %w", keyPath, err)
	}
	return cert, key, nil
}

// enforceCertMode refuses to read a cert whose file mode permits
// group/other write/exec/setuid. Mirrors cosign.LoadPublicKeyFile.
func enforceCertMode(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm() & ^os.FileMode(0o444) != 0 {
		return fmt.Errorf("pki: cert %q mode %#o: %w",
			path, info.Mode().Perm(), ErrInsecurePubKeyPerms)
	}
	return nil
}

// enforceKeyMode refuses to read a key whose file mode is not exactly
// 0o400 or 0o440. Mirrors cosign.LoadPrivateKeyFile.
func enforceKeyMode(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	perm := info.Mode().Perm()
	if perm != 0o400 && perm != 0o440 {
		return fmt.Errorf("pki: key %q mode %#o: %w",
			path, perm, ErrInsecurePrivKeyPerms)
	}
	return nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("pki: remove %q: %w", path, err)
	}
	return nil
}

func mergeAltNames(a, b AltNames) AltNames {
	dns := append([]string(nil), a.DNSNames...)
	dns = append(dns, b.DNSNames...)
	ips := append([]net.IP(nil), a.IPAddresses...)
	ips = append(ips, b.IPAddresses...)
	return AltNames{DNSNames: dns, IPAddresses: ips}
}

// randomSerial returns a 128-bit positive integer — RFC 5280 §4.1.2.2
// requires serials be ≤ 20 octets. Using rand.Int is overkill but
// trivially correct; the only constraint is uniqueness within the CA.
func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("pki: random serial: %w", err)
	}
	// Ensure positive.
	return n.Abs(n), nil
}
