package pki

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"
)

// ValidateTrustBundle verifies the material that may be copied to a host
// which does not own the fleet CA. The bundle contains the public CA
// certificate and the leaves needed by the selected box role, but never the
// CA private key. It is deliberately read-only: a missing or stale leaf is a
// deployment error rather than an invitation for a remote host to mint one.
func ValidateTrustBundle(rootDir, hostRole string, extraSANs AltNames) error {
	return validateTrustBundle(rootDir, hostRole, extraSANs, "")
}

// ValidateTrustBundleForNode is the topology-aware trust-bundle validator.
// In addition to the canonical daemon CNs, it requires compute-only leaves
// that connect to control-plane node verifiers to use nodeCN. That CN is the
// compute_nodes identity used by the handshake verifier; accepting a generic
// daemon CN here would let a bundle pass staging and fail in production.
func ValidateTrustBundleForNode(rootDir, hostRole string, extraSANs AltNames, nodeCN string) error {
	return validateTrustBundle(rootDir, hostRole, extraSANs, nodeCN)
}

func validateTrustBundle(rootDir, hostRole string, extraSANs AltNames, nodeCN string) error {
	roles := RolesForBox(hostRole)
	if len(roles) == 0 {
		return fmt.Errorf("pki: no roles for host role %q", hostRole)
	}

	caCertPath, _ := CARoot(rootDir)
	caCert, err := loadPublicCertificate(caCertPath, "CA")
	if err != nil {
		return err
	}
	now := time.Now()
	if !caCert.IsCA {
		return fmt.Errorf("pki: CA certificate %q is not marked as a CA", caCertPath)
	}
	if now.Before(caCert.NotBefore) || !now.Before(caCert.NotAfter) {
		return fmt.Errorf("pki: CA certificate %q is outside its validity window", caCertPath)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	for _, role := range roles {
		certPath, keyPath := LeafPaths(rootDir, role)
		cert, err := loadExistingLeaf(certPath, keyPath)
		if err != nil {
			return fmt.Errorf("pki: validate %s/%s: %w", role.Directory, role.Filename, err)
		}
		if cert == nil {
			return fmt.Errorf("pki: trust bundle missing %s", certPath)
		}
		if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return fmt.Errorf("pki: leaf %q is outside its validity window", certPath)
		}
		expectedCN := role.CommonName
		if nodeCN != "" && hostRole == "compute-only" && RoleUsesNodeIdentity(role) {
			expectedCN = nodeCN
		}
		if cert.Subject.CommonName != expectedCN {
			return fmt.Errorf("pki: leaf %q has CN %q; want %q", certPath, cert.Subject.CommonName, expectedCN)
		}
		requiredSANs := mergeAltNames(role.AltNames, extraSANs)
		if !certificateHasSANs(cert, requiredSANs) {
			return fmt.Errorf("pki: leaf %q is missing one or more required SANs", certPath)
		}
		keyUsage := x509.ExtKeyUsageClientAuth
		if role.Kind == KindServer {
			keyUsage = x509.ExtKeyUsageServerAuth
		}
		verifyOptions := x509.VerifyOptions{
			Roots:     roots,
			KeyUsages: []x509.ExtKeyUsage{keyUsage},
		}
		if _, err := cert.Verify(verifyOptions); err != nil {
			return fmt.Errorf("pki: leaf %q does not verify against CA: %w", certPath, err)
		}
	}
	return nil
}

// ValidateIssuanceMaterial checks that rootDir is an operator-side PKI root
// capable of issuing leaves. It does not create or rotate anything and it
// permits missing leaves because PrepareTrustBundle may issue those leaves
// for a newly-added endpoint. The CA certificate and private key must both
// already exist and match.
func ValidateIssuanceMaterial(rootDir, hostRole string) error {
	if len(RolesForBox(hostRole)) == 0 {
		return fmt.Errorf("pki: no roles for host role %q", hostRole)
	}
	caCertPath, caKeyPath := CARoot(rootDir)
	caCert, caKey, err := loadExistingCA(caCertPath, caKeyPath)
	if err != nil {
		return fmt.Errorf("pki: validate issuance CA: %w", err)
	}
	if caCert == nil || caKey == nil || !caCert.IsCA {
		return fmt.Errorf("pki: issuance CA is incomplete or not a CA")
	}
	if err := caCert.CheckSignatureFrom(caCert); err != nil {
		return fmt.Errorf("pki: issuance CA is not self-signed: %w", err)
	}
	if time.Now().After(caCert.NotAfter) {
		return fmt.Errorf("pki: issuance CA is expired")
	}
	for _, role := range RolesForBox(hostRole) {
		certPath, keyPath := LeafPaths(rootDir, role)
		certExists := fileExists(certPath)
		keyExists := fileExists(keyPath)
		if !certExists && !keyExists {
			continue
		}
		if !certExists || !keyExists {
			return fmt.Errorf("pki: issuance leaf %s/%s has only one half", role.Directory, role.Filename)
		}
		if _, err := loadExistingLeaf(certPath, keyPath); err != nil {
			return fmt.Errorf("pki: validate issuance leaf %s/%s: %w", role.Directory, role.Filename, err)
		}
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func loadPublicCertificate(path, label string) (*x509.Certificate, error) {
	if err := enforceCertMode(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("pki: trust bundle missing %s certificate %q: %w", label, path, err)
		}
		return nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pki: read %s certificate %q: %w", label, path, err)
	}
	block, _ := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("pki: %s certificate %q is not PEM-encoded", label, path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pki: parse %s certificate %q: %w", label, path, err)
	}
	return cert, nil
}
