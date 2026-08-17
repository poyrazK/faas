// PKI leaf-CN mismatch Warn (issue #900 follow-up).
//
// The TLS CN verifier (pkg/wire/node_verifier.go::LookupCN) only accepts
// CNs that appear in the registered-set snapshot, which is populated from
// compute_nodes.name. PR #942's Commit 2 auto-appends `.faas` to the
// row's name so the registered-set side is correct.
//
// The TLS leaf cert is generated externally by `gregale pki init` using
// the operator's [compute_node].name verbatim as the CN. If the leaf's
// CN does not match the auto-rewritten row name, the verifier still
// rejects the handshake (the leaf CN is what the verifier sees on the
// wire, not the registered-set name). The mismatch is a PKI onboarding
// gap that vmmd cannot fix on its own — `pki init` would need to know
// about the rewrite too.
//
// Until `pki init` gains the equivalent auto-append (tracked as a
// follow-up, ADR-115 candidate), vmmd surfaces the mismatch at startup
// so an operator running `gregale pki init && systemctl start faas-vmmd`
// sees the gap before traffic starts to fail. The Warn is advisory-only:
// it does not change the registered-set behavior and does not change
// the cert path. vmmd still starts and serves traffic; only the
// handshake with schedd through the verifier fails when the leaf CN
// doesn't match the registered-set snapshot.
//
// Function-typed seam so tests can inject the leaf-CN reader without
// reaching for a real cert on disk.

package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"strings"
)

// leafCNFromTLSConfig extracts the Subject.CommonName from the leaf cert
// in cfg. Returns ("", nil) when cfg is nil or carries no certificate
// (the unix-socket / no-TLS single-box path). Returns (cn, err) on
// parse failures so the caller can surface the parser error rather than
// silently no-op.
//
// The leaf certificate is the FIRST entry in cfg.Certificates (the
// server cert; the rest of the chain follows). The first DER blob in
// tls.Certificate.Certificate is the leaf per RFC 5246 / 8446.
//
// Package-private; the only caller is warnIfPkiCNMismatch below.
func leafCNFromTLSConfig(cfg *tls.Config) (string, error) {
	if cfg == nil || len(cfg.Certificates) == 0 {
		return "", nil
	}
	cert := cfg.Certificates[0]
	if len(cert.Certificate) == 0 {
		return "", nil
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return "", fmt.Errorf("parse leaf certificate: %w", err)
	}
	return parsed.Subject.CommonName, nil
}

// warnIfPkiCNMismatch emits a slog.Warn when the loaded server-TLS
// leaf cert's CN does not match the registered-set name
// registerComputeNode will write. The compare uses the same
// `.faas`-append rule that registerComputeNode applies, so the
// expected name is `cfg.NodeName + ".faas"` when the operator's TOML
// name lacks the suffix.
//
// The Warn is silent (no-op) when:
//   - cfg.ComputeNode.NodeName is empty (single-box dev path → no
//     self-registration, no verifier matchup concern)
//   - serverTLS is nil (no TLS at all; the unix socket path)
//   - the leaf CN parses to the same string as the expected name
//   - the leaf CN cannot be parsed (the parse error path is logged at
//     the call site via the err return — never silently no-op'd)
//
// The function is pure-ish: it reads cfg+TLS, derives a Warn decision,
// and emits one log line. No goroutines, no global state. The test
// seam is the package-internal leafCNFromTLSConfig + the slog handler
// the test installs via testLogger (see register_test.go).
func warnIfPkiCNMismatch(cfg ComputeNodeConfig, serverTLS *tls.Config, log *slog.Logger) {
	name := strings.TrimSpace(cfg.NodeName)
	if name == "" {
		// Empty name = operator opted out of self-registration
		// (default-local legacy path). The leaf CN can't
		// mismatch a row that doesn't exist; the verifier
		// only fires on registered-set CNs.
		return
	}
	if serverTLS == nil {
		// unix socket / no TLS. The verifier isn't installed
		// on this path. No mismatch shape to surface.
		return
	}
	leafCN, err := leafCNFromTLSConfig(serverTLS)
	if err != nil {
		log.Warn("vmmd: could not parse leaf cert CN; pki mismatch check skipped",
			"err", err.Error())
		return
	}
	if leafCN == "" {
		// Empty CN on the leaf is itself a tamper signal
		// (legitimate per-daemon leaves carry a CN;
		// pkg/wire/node_verifier.go:99). Surface the
		// operator-facing path: the leaf will be rejected
		// by the verifier on the next handshake anyway.
		log.Warn("vmmd: leaf cert has empty CN; handshake will be rejected on the first dial")
		return
	}
	// Mirror the rewrite logic in registerComputeNode
	// (cmd/vmmd/register.go) so the compared values are
	// exactly what would reach the verifier.
	const faasSuffix = ".faas"
	expected := name
	if !strings.HasSuffix(expected, faasSuffix) {
		expected = expected + faasSuffix
	}
	if leafCN != expected {
		log.Warn("vmmd: pki leaf CN does not match [compute_node].name — the TLS verifier will reject handshakes until the leaf is reissued",
			"leaf_cn", leafCN,
			"config_name", name,
			"registered_name", expected,
			"fix", "regenerate the leaf with `gregale pki init --cn="+expected+"` (or copy the project's rewrite logic into your pki init script), then SIGHUP faas-vmmd")
	}
}
