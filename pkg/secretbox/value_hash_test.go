// pkg/secretbox/value_hash_test.go — ADR-117 PR-C pins.
//
// Six test functions covering the discriminator's surface:
// known-answer (cross-checks against crypto/hmac + crypto/sha256
// directly), different plaintexts differ, same plaintext same
// hash, different keys differ, empty key rejected, empty
// plaintext rejected.
package secretbox_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

// TestValueFingerprint_KnownAnswer pins the exact byte shape
// against a hand-rolled HMAC-SHA256 truncated to 16 hex. The
// hand-rolled path is the reference implementation; any drift
// here means a future "optimization" silently broke the wire
// shape — and the dashboard's "equal across environments"
// detector would render wrong answers.
func TestValueFingerprint_KnownAnswer(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef" // 32 bytes ASCII
	const plaintext = "supersecret"

	// Reference: full HMAC-SHA256.
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(plaintext))
	ref := hex.EncodeToString(mac.Sum(nil))[:16]

	got, err := secretbox.ValueFingerprint([]byte(plaintext), []byte(key))
	if err != nil {
		t.Fatalf("ValueFingerprint: %v", err)
	}
	if got != ref {
		t.Errorf("ValueFingerprint: got %q, want %q (hand-rolled HMAC-SHA256[:16])", got, ref)
	}
}

// TestValueFingerprint_DifferentPlaintexts_Differ pins the
// load-bearing collision-resistance property: distinct inputs
// must produce distinct hashes (with 2^-64 collision probability,
// negligible at customer scale).
func TestValueFingerprint_DifferentPlaintexts_Differ(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	a, err := secretbox.ValueFingerprint([]byte("postgres://prod-host/db"), key)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := secretbox.ValueFingerprint([]byte("postgres://stg-host/db"), key)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a == b {
		t.Errorf("distinct plaintexts produced the same hash %q — the discriminator is broken", a)
	}
}

// TestValueFingerprint_SamePlaintext_SameHash pins the
// contract: two calls over the same plaintext + same key produce
// the SAME hash. This is the dashboard's "all environments agree"
// detector. A regression here would falsely report divergence
// for every unchanged secret.
func TestValueFingerprint_SamePlaintext_SameHash(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	a, err := secretbox.ValueFingerprint([]byte("identical"), key)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := secretbox.ValueFingerprint([]byte("identical"), key)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a != b {
		t.Errorf("identical inputs produced different hashes: a=%q b=%q (HMAC must be deterministic)", a, b)
	}
}

// TestValueFingerprint_DifferentKeys_Differ pins the per-host
// key isolation property: the same plaintext sealed by two
// different hosts produces different hashes. A regression here
// would let a multi-host cluster trivially correlate secrets
// across customers — a §11 boundary violation.
func TestValueFingerprint_DifferentKeys_Differ(t *testing.T) {
	keyA := []byte("host-a-key-0123456789abcdef012345")
	keyB := []byte("host-b-key-0123456789abcdef012345")
	a, err := secretbox.ValueFingerprint([]byte("same-plaintext"), keyA)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := secretbox.ValueFingerprint([]byte("same-plaintext"), keyB)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a == b {
		t.Errorf("same plaintext + different keys produced the same hash %q — cross-host correlation vector (§11)", a)
	}
}

// TestValueFingerprint_EmptyKey_Rejected pins the loader
// failure posture: a missing or zero-length host HMAC key is
// rejected with a clear ADR-117 D2 reference. The apid loader
// refuses to start in this state, so this is a defense-in-depth
// test.
func TestValueFingerprint_EmptyKey_Rejected(t *testing.T) {
	_, err := secretbox.ValueFingerprint([]byte("plaintext"), []byte{})
	if err == nil {
		t.Fatal("empty key accepted; MUST be rejected (the apid loader must not allow a zero-length HMAC key)")
	}
	if !strings.Contains(err.Error(), "host HMAC key") {
		t.Errorf("error %q does not mention 'host HMAC key'; the message must guide the operator to the ADR-117 D2 file path", err)
	}
}

// TestValueFingerprint_EmptyPlaintext_Rejected pins the
// handler-contract posture: ValueFingerprint is computed BEFORE
// SealOne in handlers_secrets.go::sealAndPersist, and the
// plaintext at that point is the customer's PUT body — an empty
// plaintext would be a handler bug, not a normal input. The
// helper refuses the call so the bug surfaces at write time, not
// as a silent DB column NULL later.
func TestValueFingerprint_EmptyPlaintext_Rejected(t *testing.T) {
	_, err := secretbox.ValueFingerprint([]byte{}, []byte("0123456789abcdef0123456789abcdef"))
	if err == nil {
		t.Fatal("empty plaintext accepted; MUST be rejected (the handler computes ValueFingerprint BEFORE SealOne; empty plaintext is a handler bug — see ADR-117 D1)")
	}
	if !strings.Contains(err.Error(), "empty plaintext") {
		t.Errorf("error %q does not mention 'empty plaintext'; the message must guide the developer to ADR-117 D1", err)
	}
}
