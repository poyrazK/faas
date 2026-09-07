package renderer

import (
	"crypto/ecdsa"
	"crypto/x509"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/pki"
)

// PKIOutput carries one PKI leaf's path + whether it was issued fresh
// ("wrote") or skipped because it was already fresh ("unchanged").
// The renderer uses this to emit per-path OutputReport entries even
// on the idempotent second run — the doctor's PKI-health signal
// depends on every leaf being visible in the report (PR-4).
type PKIOutput struct {
	Path   string
	Issued bool // true if EnsureLeaf actually wrote a new leaf; false on ErrLeafNotExpiringSoon
}

// renderPKI ensures the per-host PKI leaves exist under rootDir. The
// leaf set is filtered by hostRole via `pkg/pki.RolesForBox` — a
// control-plane box doesn't get vmmd's leaves, a compute-only box
// doesn't get apid's, etc. Each leaf is issued via
// `pkg/pki.EnsureLeafWithSANs(..., false)` which is idempotent: leaves
// whose NotAfter is more than ReissueThreshold away and already contain
// the host's routing SANs are skipped (ErrLeafNotExpiringSoon).
//
// The CA is materialised via `pkg/pki.EnsureCA(rootDir, false)` —
// the renderer's job is to make the CA + leaves exist on disk. The
// operator's job is to seed the CA before the first render (PR-X
// secrets init owns the production CA on a clean host; PR-2's
// renderer is the canary that ships the same CA on a freshly-
// bootstrapped dev/lima box).
//
// Returns one PKIOutput per cert+key pair across all roles. Skipped
// leaves are reported with Issued=false so the second-run report
// surfaces every leaf (with Action="unchanged") — the doctor's
// PKI-health signal depends on every leaf being visible.
func renderPKI(rootDir, hostName, hostRole string, extraSANs pki.AltNames) ([]PKIOutput, error) {
	// EnsureCA is idempotent (force=false → don't re-issue if a
	// fresh CA already exists). On a fresh box this generates the
	// CA cert + key at <rootDir>/ca/{ca.crt,ca.key}. The mode
	// pinning (0o444 / 0o400) is owned by EnsureCA.
	caCert, caKey, err := pki.EnsureCA(rootDir, false)
	if err != nil {
		return nil, fmt.Errorf("renderer: pki: ensure CA: %w", err)
	}

	// Per-role leaves. RolesForBox returns an empty slice for
	// unknown roles (fail-closed — the operator sees "0 leaves
	// written" rather than a silent full-fleet issuance). PR-2
	// surfaces this as a render error.
	roles := pki.RolesForBox(hostRole)
	if len(roles) == 0 {
		return nil, fmt.Errorf("renderer: pki: no roles for host role %q (known: control-plane|compute-only|single-box)", hostRole)
	}

	var out []PKIOutput
	for _, role := range roles {
		err := ensureHostLeaf(rootDir, role, hostName, hostRole, caCert, caKey, false, extraSANs)
		issued := err == nil
		if err != nil && !errors.Is(err, pki.ErrLeafNotExpiringSoon) {
			return nil, fmt.Errorf("renderer: pki: ensure leaf %s/%s: %w", role.Directory, role.Filename, err)
		}
		certPath, keyPath := pki.LeafPaths(rootDir, role)
		out = append(out, PKIOutput{Path: filepath.ToSlash(certPath), Issued: issued})
		out = append(out, PKIOutput{Path: filepath.ToSlash(keyPath), Issued: issued})
	}
	return out, nil
}

// renderPKITrustOnly validates an already-issued per-host trust bundle. A
// compute node must not receive the fleet CA private key and must not be able
// to mint replacement leaves, so this path never calls EnsureCA or
// EnsureLeaf. It returns the existing leaf paths for the normal render
// report, preserving the renderer's idempotence/accounting semantics.
func renderPKITrustOnly(rootDir, hostName, hostRole string, extraSANs pki.AltNames) ([]PKIOutput, error) {
	if err := pki.ValidateTrustBundleForNode(rootDir, hostRole, extraSANs, nodeCommonName(hostName, hostRole)); err != nil {
		return nil, fmt.Errorf("renderer: pki: validate trust bundle: %w", err)
	}
	var out []PKIOutput
	for _, role := range pki.RolesForBox(hostRole) {
		certPath, keyPath := pki.LeafPaths(rootDir, role)
		out = append(out,
			PKIOutput{Path: filepath.ToSlash(certPath), Issued: false},
			PKIOutput{Path: filepath.ToSlash(keyPath), Issued: false},
		)
	}
	return out, nil
}

// ensureHostLeaf keeps the canonical daemon CNs except on compute-only leaves
// that authenticate the host itself to a control-plane verifier. Those leaves
// still carry the daemon SANs, while their subject matches compute_nodes.name.
func ensureHostLeaf(rootDir string, role pki.Role, hostName, hostRole string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, force bool, extraSANs pki.AltNames) error {
	if nodeCN := nodeCommonName(hostName, hostRole); nodeCN != "" && pki.RoleUsesNodeIdentity(role) {
		return pki.EnsureLeafWithCNAndSANs(rootDir, role, nodeCN, caCert, caKey, force, extraSANs)
	}
	return pki.EnsureLeafWithSANs(rootDir, role, caCert, caKey, force, extraSANs)
}

func nodeCommonName(hostName, hostRole string) string {
	if hostRole != "compute-only" || hostName == "" {
		return ""
	}
	if strings.HasSuffix(hostName, ".faas") {
		return hostName
	}
	return hostName + ".faas"
}
