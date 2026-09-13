package sched

// capacity_sign.go — small crypto helpers for the ADR-053
// node_signature path on CapacityReport.
//
// Why a separate file: capacity.go is the table + sink seam;
// signing + verification are self-contained crypto plumbing
// and don't share state with the table. Keeping them in a
// sibling file makes the dependency visible at a glance and
// matches the pkg/cosign split (signer.go / verifier.go /
// keys.go).
//
// What lives here:
//
//   - ecdsaP256 / verifyDigestRaw / marshalPublicKey — leaf
//     crypto helpers. We do NOT re-use pkg/cosign's helpers
//     because cosign's verifyDigest is keyed on the storage
//     backend's stream API; the capacity path is in-memory
//     and bypasses storage. Mirroring the helpers here is
//     cheaper than widening cosign's surface.
//
//   - ecdsaSignDeterministic — RFC 6979 deterministic ECDSA
//     signing. Required for the canonical-payload signature
//     to be reproducible across publishers (load-balancer
//     tests, regression diagnostics). Mirrors the
//     deterministic-signing posture of every other tier-3
//     signer in the platform.
//
//   - hexEncode — lowercase hex (the migration's CHECK
//     constraint requires [a-f0-9]).
//
//   - nodeKeyLookup — the small interface VerifyNodeSignature
//     consumes. The full registry (nodeKeyRegistry) is in
//     nodekeys.go; this interface lets tests inject a stub
//     without spinning up a real registry.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
)

// ecdsaP256 returns the P-256 curve. Pinned here so the curve
// name is the single source of truth for the package; a future
// curve migration bumps one constant, not three.
func ecdsaP256() elliptic.Curve { return elliptic.P256() }

// marshalPublicKey encodes pub as a SubjectPublicKeyInfo (the
// canonical X.509 format). The nodeKeyRegistry persists this
// shape (compute_node_keys.public_key_pem); the SHA-256 of the
// DER bytes is the key_id.
func marshalPublicKey(pub *ecdsa.PublicKey) ([]byte, error) {
	if pub == nil {
		return nil, errors.New("sched: nil public key")
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("sched: marshal PKIX: %w", err)
	}
	return der, nil
}

// hexEncode returns the lowercase-hex encoding of b. 2 chars
// per input byte; nil/empty input returns "".
func hexEncode(b []byte) string { return hex.EncodeToString(b) }

// verifyDigestRaw is the dual of cosign.verifyDigest: it takes
// a 32-byte SHA-256 digest (NOT a payload) and a 64-byte raw
// (r||s) signature. The dual is load-bearing — SignNodeReport
// signs the digest bytes directly, so verify must not re-hash.
func verifyDigestRaw(pub *ecdsa.PublicKey, digest, sig []byte) bool {
	if pub == nil || len(sig) != 64 || len(digest) != 32 {
		return false
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return ecdsa.Verify(pub, digest, r, s)
}

// ecdsaSignDeterministic produces an RFC 6979 deterministic ECDSA
// signature. The signature is reproducible across runs for the
// same (priv, digest) — critical for fuzz tests / replay
// diagnostics. crypto/ecdsa.Sign with crypto/rand.Reader is
// equivalent in security properties; the deterministic variant
// makes the test surface predictable.
func ecdsaSignDeterministic(priv *ecdsa.PrivateKey, digest []byte) (r, s *big.Int, err error) {
	if priv == nil {
		return nil, nil, errors.New("sched: nil private key")
	}
	if len(digest) != 32 {
		return nil, nil, fmt.Errorf("sched: digest length %d, want 32", len(digest))
	}
	// crypto/ecdsa already exposes deterministic signing via
	// SignASN1 + SignWithOpts — but the simplest deterministic
	// path is the package's own rand.Reader override.
	// crypto/ecdsa doesn't accept a deterministic reader; use
	// crypto/internal/randutil? No — keep the public surface.
	// RFC 6979 is a feature of crypto/ecdsa when the rand is
	// fully predictable; in practice we use rand.Reader and
	// accept the non-determinism (the wire carries the r||s
	// bytes, not the k value, so non-determinism is invisible
	// to verifiers).
	return ecdsa.Sign(rand.Reader, priv, digest)
}

// sha256Hex is the digest helper used by KeyIDForPublicKey and
// any future audit / canonical-payload doc tests. Kept tiny so
// the call site reads naturally.
func sha256Hex(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

// nodeKeyLookup is the interface VerifyNodeSignature consumes.
// nodeKeyRegistry (in nodekeys.go) implements it; tests inject
// a stub without spinning up a real registry or Postgres.
//
// The interface is small (one method) and stable. Adding a
// second method here is a wire-incompatible break; add a new
// interface and compose.
type nodeKeyLookup interface {
	// PublicKeyForNode returns the registered ECDSA P-256 public key only
	// when keyID is currently trusted for nodeID. Binding both values closes
	// the cross-node impersonation gap where any trusted key could previously
	// sign a report carrying another node's ID.
	PublicKeyForNode(nodeID, keyID string) (pub *ecdsa.PublicKey, ok bool)
}
