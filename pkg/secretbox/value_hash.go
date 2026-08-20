// pkg/secretbox/value_hash.go — ADR-117 env-diff matrix
// discriminator (PR-C). Sibling of host_hash.go's
// HostHashSalt (§11) and kid.go's IdentityFingerprint (PR-A).
//
// Three different per-host keys / three different purposes live
// in this package — do not confuse them:
//
//   - /etc/faas/secrets/host.hmac.key (this file)
//     ValueFingerprint — value-equality discriminator across
//     environments, 16-hex truncated HMAC-SHA256 of plaintext.
//     Per-host. Loaded once at apid startup. Never crosses the
//     apid → schedd → vmmd boundary (only the truncated hash
//     travels).
//   - /etc/faas/secrets/host.age.identity
//     IdentityFingerprint (kid.go) — identity-of-sealer marker,
//     age-1... recipient fingerprint. Per-host. Travels on every
//     secret row.
//   - /etc/faas/secrets/host_hash_salt
//     HostHashSalt (host_hash.go) — §11 barrier salt for hashing
//     plaintext host names into host_redacted_hash. Per-cluster.
//     The salt is one-way (rotation would orphan every existing
//     data_upstreams row per ADR-098 §D6).
package secretbox

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// ValueFingerprint returns the first 16 hex chars of
// HMAC-SHA256(plaintext, hostHMACKey). Two plaintexts with
// identical hash therefore share byte-identical plaintext under
// the same host key (with collision probability 2^-64 at the
// SecretCountMax cap of 100 secrets per app — negligible for
// customer-facing use).
//
// CRITICAL: caller MUST pass the PLAINTEXT, NOT the ciphertext.
// age X25519 + ChaCha20-Poly1305 is probabilistically
// non-deterministic (fresh ephemeral X25519 + fresh nonce per
// Seal), so two calls over the same plaintext produce
// byte-different ciphertexts. A ciphertext-derived hash would
// diverge for every row and the discriminator would be useless.
// The handler computes this BEFORE SealOne so the same plaintext
// byte string feeds both the HMAC and the seal.
//
// Distinct from IdentityFingerprint (kid, identity-shaped) — same
// package, but a different key (host.hmac.key, not
// host.age.identity) and a different purpose
// (equality-without-unsealing, not identity-of-sealer).
//
// ADR-117 closes the "did this secret rotate everywhere?"
// question without ever unsealing the ciphertext.
func ValueFingerprint(plaintext []byte, hostHMACKey []byte) (string, error) {
	if len(hostHMACKey) == 0 {
		return "", errors.New("secretbox: empty host HMAC key (load /etc/faas/secrets/host.hmac.key — see ADR-117 D2)")
	}
	if len(plaintext) == 0 {
		return "", errors.New("secretbox: empty plaintext (the handler computes ValueFingerprint BEFORE SealOne; an empty plaintext is a handler bug — see ADR-117 D1)")
	}
	mac := hmac.New(sha256.New, hostHMACKey)
	mac.Write(plaintext)
	full := mac.Sum(nil)
	// 16 hex chars = 64 bits. The wire shape
	// (pkg/api.AppSecretResponse.ValueHash) is 16 hex, and the
	// migration CHECK caps length <= 16. Truncation is exact
	// (NOT 15 or 17) — the SDK validator pattern is ^[a-f0-9]{16}$.
	return hex.EncodeToString(full[:8]), nil
}
