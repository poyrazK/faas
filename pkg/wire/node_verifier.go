// Handshake-layer leaf-CN binding for the multi-box wire (ADR-056).
//
// ADR-052 §Handler-layer peer binding covers per-handler role checks via
// wire.PeerCN(ctx). This file adds a SECOND layer — invoked by
// crypto/tls itself after the stdlib chain/SAN/EKU check passes —
// that rejects any peer whose leaf-CN is not present in a registered
// set (production: a snapshot of compute_nodes.name; tests: an
// in-memory CN set).
//
// The composition with stdlib is the load-bearing detail:
//
//   - crypto/tls runs chain trust against RootCAs / ClientCAs, RFC
//     6125 SAN matching, and EKU enforcement BEFORE the
//     VerifyPeerCertificate hook is consulted. If any of those
//     fail, the handshake aborts and the hook is never invoked.
//     Our verifier therefore can NEVER see an untrusted leaf.
//   - InsecureSkipVerify stays false. ADR-052 §Rejected alternatives
//     flagged InsecureSkipVerify=true + a custom verifier as
//     CodeQL alert #58; this file does NOT touch that field.
//
// The interface is intentionally narrow — single method LookupCN —
// because every implementation (production PG, test in-memory,
// default allow-all) maps cleanly onto "is this CN registered?".

package wire

import (
	"crypto/x509"
	"errors"
	"fmt"
)

// ErrNodeVerifierCNMismatch is returned when the leaf-CN is not in
// the verifier's registered set. The closure built by VerifyCNClosure
// surfaces it to crypto/tls as a handshake failure; gRPC maps that
// to codes.Unauthenticated on the dial side and a transport error
// on the listener side.
var ErrNodeVerifierCNMismatch = errors.New("wire: peer CN not in compute_node registry")

// ErrCertFingerprintNotRegistered is returned by
// PGNodeVerifier.CertFingerprintByCN (PR-3) when the CN is unknown
// or the cert_fingerprint column is empty (pre-PR-X box). The
// doctor (PR-4) uses this as a sentinel to surface
// `Boot/<n>× CertFingerprintNotRegistered` as a single failure
// mode; consumers must errors.Is this sentinel.
var ErrCertFingerprintNotRegistered = errors.New("wire: cert fingerprint not registered for CN")

// NodeVerifier is the handshake-layer CN-binding seam. The stdlib TLS
// verifier (chain/SAN/EKU) runs first in the same pass; this hook is
// invoked AFTER stdlib trust succeeds, so the verifier augments
// (never replaces) stdlib's chain check.
//
// The verifier is a thin LookupCN: production implementations resolve
// against an in-memory snapshot of compute_nodes.name (the friendly
// label that matches Subject.CommonName). The snapshot is rebuilt
// from Postgres on every 'compute_node_changed' pg_notify tick — see
// PGNodeVerifier.
//
// The verifier MUST be safe for concurrent use: gRPC's tlsCreds
// ServerHandshake invokes the hook on a single goroutine per
// connection, but multiple connections invoke it in parallel.
//
// A nil receiver is treated as AllowAll — guards any future caller
// that forgets to wire the verifier on the prod path.
type NodeVerifier interface {
	// LookupCN returns nil when the given CN is registered.
	// Any non-nil error causes the handshake to abort.
	LookupCN(cn string) error
}

// NodeIdentityResolver maps an authenticated leaf Common Name to the
// durable compute_nodes.id it represents. Handlers use this second, explicit
// binding after the TLS handshake to ensure a peer cannot present a valid
// certificate and then claim a different node in an application payload.
//
// PGNodeVerifier implements this interface for production multi-node
// deployments. It is intentionally separate from NodeVerifier so existing
// handshake-only callers keep the narrow LookupCN contract.
type NodeIdentityResolver interface {
	NodeIDByCN(cn string) (string, error)
}

// VerifyCNClosure builds a tls.Config.VerifyPeerCertificate callback
// that consults v.LookupCN on the verified leaf certificate. The
// callback is invoked by crypto/tls AFTER the stdlib verifier has
// confirmed chain trust + SAN + EKU, so the function expects
// verifiedChains[0][0] to be a fully-trusted leaf cert.
//
// The closure is the canonical installation point: callers store the
// returned func in tls.Config.VerifyPeerCertificate via the
// Load*TLSConfigWithVerifier factory variants. Returning nil is
// permitted (= AllowAll — the verifier is empty or disabled).
//
// On any non-nil LookupCN error, the closure returns that error so
// crypto/tls surfaces it as a TLS handshake failure; gRPC maps that
// to codes.Unauthenticated.
func VerifyCNClosure(v NodeVerifier) func([][]byte, [][]*x509.Certificate) error {
	if v == nil {
		return nil
	}
	return func(_ [][]byte, verifiedChains [][]*x509.Certificate) error {
		if len(verifiedChains) == 0 || len(verifiedChains[0]) == 0 {
			// stdlib didn't trust the leaf — this shouldn't happen
			// because stdlib runs first, but a defensive guard keeps
			// a future pkg/wire refactor from silently no-op-ing.
			return errors.New("wire: verifier invoked without verified chain")
		}
		leaf := verifiedChains[0][0]
		cn := leaf.Subject.CommonName
		if cn == "" {
			// Mirrors wire.PeerCN's ErrPeerCNUnavailable contract:
			// empty CN on a TLS-handshake peer is itself a tamper
			// signal (legitimate per-daemon leaves carry a CN).
			return errors.New("wire: verifier invoked with empty CN on leaf")
		}
		return v.LookupCN(cn)
	}
}

// AllowAllNodeVerifier is the no-op default. Used by single-box dev
// installs and pre-slice-3 paths where the registry is irrelevant
// (no TLS on unix sockets in the legacy single-box path; or no
// compute_nodes table on legacy schedd).
//
// A nil *AllowAllNodeVerifier also returns nil (nil-safe) so the
// factory variants can wire an unconfigured verifier without
// special-casing.
type AllowAllNodeVerifier struct{}

// LookupCN returns nil unconditionally — every CN is "registered".
func (*AllowAllNodeVerifier) LookupCN(string) error { return nil }

// Ensure AllowAllNodeVerifier satisfies NodeVerifier at compile time.
// A forgotten method would fail here, not at the first dial.
var _ NodeVerifier = (*AllowAllNodeVerifier)(nil)

// AnyNodeVerifier accepts a peer when at least one of its child verifiers
// accepts it.  Multi-box daemons sometimes have more than one peer class:
// vmmd accepts registered compute/service peers on its server surface, while
// its outbound capacity stream must validate the control-plane schedd leaf.
// A single compute_nodes-backed verifier cannot represent both classes.
type AnyNodeVerifier struct {
	verifiers []NodeVerifier
}

// NewAnyNodeVerifier composes the supplied verifiers. Nil entries are ignored
// so callers can build a direction-specific verifier without special cases.
func NewAnyNodeVerifier(verifiers ...NodeVerifier) *AnyNodeVerifier {
	filtered := make([]NodeVerifier, 0, len(verifiers))
	for _, verifier := range verifiers {
		if verifier != nil {
			filtered = append(filtered, verifier)
		}
	}
	return &AnyNodeVerifier{verifiers: filtered}
}

// LookupCN implements NodeVerifier. It returns the last rejection when every
// child rejects the peer so diagnostics retain the canonical CN-bearing error.
func (v *AnyNodeVerifier) LookupCN(cn string) error {
	if v == nil {
		return nodeVerifierWithCN(ErrNodeVerifierCNMismatch, cn)
	}
	var last error
	for _, verifier := range v.verifiers {
		if err := verifier.LookupCN(cn); err == nil {
			return nil
		} else {
			last = err
		}
	}
	if last != nil {
		return last
	}
	return nodeVerifierWithCN(ErrNodeVerifierCNMismatch, cn)
}

var _ NodeVerifier = (*AnyNodeVerifier)(nil)

// nodeVerifierWithCN wraps an error with the rejected CN for
// diagnostics. Wrapping is via %w so errors.Is matches
// ErrNodeVerifierCNMismatch across calls (closed from the stdlib
// trust path; the wrap carries only the CN because the snapshot is
// keyed by CN, so the lookup-mismatch path has no id to surface).
//
// A nil err short-circuits to nil so callers can compose the helper
// at the rejection site without an explicit guard. The helper is
// package-private: there is no plan to expose it because every
// call site is the verifier's LookupCN method, and those methods
// already produce an error in the canonical ErrNodeVerifierCNMismatch
// shape.
//
// Future id-based policies ("this CN must match a specific
// compute_nodes.id") would carry the id through here. The id is
// already in the PGNodeVerifier snapshot; the helper just needs an
// id-aware sibling at that time — adding one now without a caller
// would trip the `unused` linter.
func nodeVerifierWithCN(err error, cn string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: cn=%q", err, cn)
}
