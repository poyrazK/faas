// cosign.go — cosign verify-blob wrapper for the canonical daemon
// tarball (ADR-113 canonical daemon tarball, PR-A commit 2).
//
// The sigstore `cosign verify-blob` CLI is the load-bearing trust
// path for Gregale releases: every release.tar.gz published by the
// Packer canary is signed with cosign's keyless-OIDC flow
// (Fulcio + Rekor), and every host that pulls the tarball runs
// `cosign verify-blob` before installing it. This file owns the
// in-process wrapper.
//
// Load-bearing invariants:
//
//  1. The CosignVerifier is an INTERFACE. Production is the
//     exec-backed impl (`ExecCosignVerifier`); tests use a fixture.
//     This is the test-seam pattern PR-B's
//     `commands_release.go:systemctlExec` established.
//  2. The OIDC issuer and certificate-identity-regexp are
//     PIPELINE CONFIGURATION, not file-level constants. They
//     travel with the operator's release source so a multi-org
//     deployment (e.g., a fork of the upstream Gregale repo
//     under a different GitHub org) can pin its own identity
//     without recompiling.
//  3. The verifier returns the certificate-identity that
//     produced the signature so callers can log it. Production
//     never accepts a blob whose identity doesn't match the
//     configured regex; an empty identity is a verifier bug
//     or a tampered cosign output, and surfaces as an error.
//
// NOTE on namespacing: `pkg/cosign/` (PR-3, schedd) is a
// local-ECDSA subsystem and is NOT used here. The names collide
// only at the literal `cosign` string; `pkg/releaseinstall` does
// not import `pkg/cosign`. Future contributors moving the
// schedd-side verifier here should NOT collapse the two: one is
// a primitive, the other an in-process CLI wrapper.
package releaseinstall

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// CosignVerifier verifies a blob's cosign-signed signature bundle.
//
// Implementations MUST be safe for concurrent use; the production
// ExecCosignVerifier holds no state.
type CosignVerifier interface {
	// VerifyBlob runs `cosign verify-blob` against the tarball at
	// tarballPath using the bundle at sigPath. Returns the
	// certificate identity string on success (a URL of the form
	// "https://github.com/<org>/<repo>/.github/workflows/<yml>@refs/tags/<tag>")
	// or a non-nil error on failure.
	VerifyBlob(ctx context.Context, tarballPath, sigPath string) (certIdentity string, err error)
}

// CosignVerifyConfig carries the OIDC issuer + identity regex
// pins. Lives at the wrapper level so swapping from
// token.actions.githubusercontent.com (GitHub) to
// accounts.google.com (GCP) is a one-line config change, not a
// recompile.
type CosignVerifyConfig struct {
	// Issuer is the OIDC token issuer that must have signed the
	// cosign bundle. Default to
	// "https://token.actions.githubusercontent.com" for the
	// upstream GitHub Actions integration; operators on a fork
	// override via /etc/faas/release-source.conf.
	Issuer string

	// IdentityRegexp is the regex that the certificate-identity
	// field must match. The upstream canonical pin is
	// "^https://github.com/poyrazK/faas/.github/workflows/release\\.yml@refs/tags/.*$".
	// Operators MUST verify both ends — the workflow path AND the
	// refs/tags/ peg — to defeat ref-based privilege escalation.
	IdentityRegexp *regexp.Regexp

	// CosignPath is the cosign binary's absolute path. Tests can
	// pin this to a fixture; production relies on PATH lookup.
	CosignPath string
}

// The cosign build every Gregale host verifies releases with, and since
// ADR-20261029155852 platform base provenance too: node join stages it at
// /usr/local/bin/cosign. release.yml and the CD workflows download the same
// build and check this digest, and gregalectl's deploy prepare and deploy join
// refuse any other binary (CheckCosignBinary).
const (
	PinnedCosignVersion = "2.4.1"
	PinnedCosignURL     = "https://github.com/sigstore/cosign/releases/download/v2.4.1/cosign-linux-amd64"
	PinnedCosignSHA256  = "8b24b946dd5809c6bd93de08033bcf6bc0ed7d336b7785787c080f574b89249b"
)

// CheckCosignBinary returns an error unless the file at path has the SHA-256
// digest want (hex). The error names the pinned build and where to get it.
func CheckCosignBinary(path, want string) error {
	got, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("releaseinstall: hash cosign binary %s: %w", path, err)
	}
	if got != want {
		return fmt.Errorf("releaseinstall: cosign binary %s has sha256 %s, not the pinned cosign %s linux/amd64 build (sha256 %s); download %s and check its digest",
			path, got, PinnedCosignVersion, want, PinnedCosignURL)
	}
	return nil
}

// DefaultGitHubOIDC returns the upstream-pinned config. Pulled
// into a helper so the production ExecCosignVerifier can be
// defaulted without each call site re-typing the GitHub issuer.
//
// Use NewExecCosignVerifier(cfg) with this cfg for the production
// install-time verifier.
func DefaultGitHubOIDC() CosignVerifyConfig {
	re := regexp.MustCompile(`^https://github\.com/poyrazK/faas/\.github/workflows/release\.yml@refs/tags/.+$`)
	return CosignVerifyConfig{
		Issuer:         "https://token.actions.githubusercontent.com",
		IdentityRegexp: re,
		CosignPath:     "cosign",
	}
}

// NewExecCosignVerifier returns the production verifier. It
// shells out to the binary named by cfg.CosignPath (typically the
// cosign on PATH); callers MUST ensure the binary is present at
// that path before install time. The Packer image bakes the
// upstream cosign binary, so this is satisfied automatically for
// the canonical install path; air-gap deployments using
// `--legacy-bundle-dir` (the sunset flag) do NOT exercise this
// verifier.
func NewExecCosignVerifier(cfg CosignVerifyConfig) *ExecCosignVerifier {
	if cfg.Issuer == "" {
		cfg.Issuer = DefaultGitHubOIDC().Issuer
	}
	if cfg.IdentityRegexp == nil {
		cfg.IdentityRegexp = DefaultGitHubOIDC().IdentityRegexp
	}
	if cfg.CosignPath == "" {
		cfg.CosignPath = "cosign"
	}
	return &ExecCosignVerifier{cfg: cfg}
}

// ExecCosignVerifier is the production CosignVerifier.
//
// `cosign verify-blob` invocation:
//
//	cosign verify-blob \
//	    --certificate-identity-regexp "$RE" \
//	    --certificate-oidc-issuer "$ISSUER" \
//	    --bundle "$SIG" \
//	    "$TARBALL"
//
// Verification deliberately keeps Rekor transparency-log checking enabled.
// A keyless Fulcio certificate is short-lived; the signed Rekor timestamp is
// what lets a host revalidate an otherwise-expired certificate after the
// release was published. Disabling tlog verification would make installs
// depend on the verifier clock being within the certificate's validity window.
type ExecCosignVerifier struct {
	cfg CosignVerifyConfig
}

// VerifyBlob runs `cosign verify-blob --certificate-identity-regexp ...`
// against the tarball. Returns the certificate identity on success.
//
// Output parsing: cosign v2 writes a human-readable success line rather than
// the certificate identity, so the verified bundle is parsed below for its
// URI SAN.
func (v *ExecCosignVerifier) VerifyBlob(ctx context.Context, tarballPath, sigPath string) (string, error) {
	if tarballPath == "" {
		return "", errors.New("releaseinstall: cosign: empty tarball path")
	}
	if sigPath == "" {
		return "", errors.New("releaseinstall: cosign: empty signature path")
	}
	args := []string{
		"verify-blob",
		"--certificate-identity-regexp", v.cfg.IdentityRegexp.String(),
		"--certificate-oidc-issuer", v.cfg.Issuer,
		"--bundle", sigPath,
		tarballPath,
	}
	cmd := exec.CommandContext(ctx, v.cfg.CosignPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("releaseinstall: cosign verify-blob: %w (stderr=%s)", err, stderr.String())
	}
	// cosign prints human-readable verification status, not the certificate
	// identity. Read the identity from the verified bundle instead; the
	// command has already enforced the issuer and SAN regexp above.
	identity, err := bundleCertificateIdentity(sigPath)
	if err != nil {
		return "", err
	}
	// Re-confirm the identity matches the configured regex. This
	// is defence-in-depth: if cosign ever changes its output
	// shape to print, say, "verify OK" instead of the cert
	// identity, the regex check is the canonical contract.
	if !v.cfg.IdentityRegexp.MatchString(identity) {
		return "", fmt.Errorf("releaseinstall: cosign verify-blob: identity %q does not match configured regex %s",
			identity, v.cfg.IdentityRegexp)
	}
	return identity, nil
}

// bundleCertificateIdentity extracts the URI SAN from both the legacy cosign
// bundle shape and the newer trusted-root bundle shape. Keyless Fulcio
// certificates carry the GitHub Actions workflow identity as a URI SAN.
func bundleCertificateIdentity(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("releaseinstall: read cosign bundle identity: %w", err)
	}
	var b struct {
		Cert                 string `json:"cert"`
		VerificationMaterial struct {
			X509CertificateChain struct {
				Certificates []struct {
					RawBytes string `json:"rawBytes"`
				} `json:"certificates"`
			} `json:"x509CertificateChain"`
		} `json:"verificationMaterial"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		return "", fmt.Errorf("releaseinstall: decode cosign bundle: %w", err)
	}
	var certBytes []byte
	if b.Cert != "" {
		certBytes, err = base64.StdEncoding.DecodeString(b.Cert)
		if err != nil {
			certBytes = []byte(b.Cert)
		}
	} else if len(b.VerificationMaterial.X509CertificateChain.Certificates) > 0 {
		certBytes, err = base64.StdEncoding.DecodeString(b.VerificationMaterial.X509CertificateChain.Certificates[0].RawBytes)
		if err != nil {
			return "", fmt.Errorf("releaseinstall: decode cosign certificate: %w", err)
		}
	}
	if block, _ := pem.Decode(certBytes); block != nil {
		certBytes = block.Bytes
	}
	cert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return "", fmt.Errorf("releaseinstall: parse cosign certificate: %w", err)
	}
	for _, uri := range cert.URIs {
		if uri != nil && strings.TrimSpace(uri.String()) != "" {
			return uri.String(), nil
		}
	}
	return "", errors.New("releaseinstall: cosign bundle has no URI certificate identity")
}

// FixtureCosignVerifier is the test seam. Constructor takes the
// expected identity and an optional error: nil = succeed;
// non-nil = verify-blob failed.
//
// Used by tarball_test.go for the cosign-half of the
// "tampered" acceptance tests PR-A commit 2 owns.
type FixtureCosignVerifier struct {
	Identity    string
	Err         error
	CallTarball string
	CallSig     string
	CallCount   int
}

// VerifyBlob records the call and returns Identity / Err.
func (f *FixtureCosignVerifier) VerifyBlob(_ context.Context, tarballPath, sigPath string) (string, error) {
	f.CallCount++
	f.CallTarball = tarballPath
	f.CallSig = sigPath
	if f.Err != nil {
		return "", f.Err
	}
	return f.Identity, nil
}

// ErrCosignSigMissing is the failure signal a Tarball.Verify run
// surfaces when the Sig field is empty but the verifier says "you
// must verify". Callers map this to a 4 (operator error).
var ErrCosignSigMissing = errors.New("releaseinstall: tarball has no signature; cosign verify is required")
