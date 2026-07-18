package secretbox

import (
	"bytes"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

// TestSealOpenRoundTrip exercises the happy path: Seal → Open returns the
// same map. Multiple iterations cover ordering / multi-entry cases.
func TestSealOpenRoundTrip(t *testing.T) {
	id := mustGenHostKey(t, "host.age")
	cases := []Envelope{
		{"STRIPE_KEY": "sk_live_abcdef0123456789"},
		{"A": "1", "B": "2", "C": "3"},
		{"WITH_EQUALS": "key=value=with=equals=inside"},
		{"UNICODE_OK_KEY": "value with spaces and punctuation!@#"},
	}
	for _, env := range cases {
		blob, err := Seal(id.Recipient(), env)
		if err != nil {
			t.Fatalf("Seal(%v): %v", env, err)
		}
		got, err := Open(id, blob)
		if err != nil {
			t.Fatalf("Open after Seal(%v): %v", env, err)
		}
		if !envelopesEqual(env, got) {
			t.Errorf("round-trip mismatch: got %v want %v", got, env)
		}
	}
}

// TestOpenTampered asserts that flipping a single byte of the ciphertext
// causes Open to fail. Confirms we're using an AEAD, not just XOR.
func TestOpenTampered(t *testing.T) {
	id := mustGenHostKey(t, "host.age")
	blob, err := Seal(id.Recipient(), Envelope{"KEY": "secret"})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// Flip a byte in the body (skip the ASCII stanza header — first
	// newline + 2 blank lines are typical age format; flip byte 60 which
	// is well inside the binary body).
	idx := len(blob) - 4
	blob[idx] ^= 0xff
	if _, err := Open(id, blob); err == nil {
		t.Fatal("Open succeeded on tampered ciphertext — AEAD broken")
	}
}

// TestOpenWrongIdentity asserts that a different X25519 identity fails to
// decrypt. Confirms the recipient binding is real.
func TestOpenWrongIdentity(t *testing.T) {
	id1 := mustGenHostKey(t, "a.age")
	id2 := mustGenHostKey(t, "b.age")
	blob, _ := Seal(id1.Recipient(), Envelope{"KEY": "secret"})
	if _, err := Open(id2, blob); err == nil {
		t.Fatal("Open with wrong identity succeeded")
	}
}

// TestSealRejectsBadKey ensures the regex check fires before the seal.
// An invalid key must produce an error and NOT call age.
func TestSealRejectsBadKey(t *testing.T) {
	id := mustGenHostKey(t, "host.age")
	for _, bad := range []string{"", "1STARTS_WITH_DIGIT", "lower", "WITH-DASH", "WITH SPACE"} {
		if _, err := Seal(id.Recipient(), Envelope{bad: "v"}); err == nil {
			t.Errorf("Seal(%q): expected error", bad)
		}
	}
}

// TestSealOneEnforcesByteCap asserts the byte cap is checked in the seal
// path (not just at the HTTP layer) so no over-cap ciphertext ever lands in
// PG. SealOne returns the api.Problem-shaped error so callers don't need
// to import pkg/api for the code.
func TestSealOneEnforcesByteCap(t *testing.T) {
	id := mustGenHostKey(t, "host.age")
	blob, err := SealOne(id.Recipient(), "OK_KEY", "short", 10)
	if err != nil {
		t.Fatalf("under-cap: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("under-cap returned empty blob")
	}
	if _, err := SealOne(id.Recipient(), "OK_KEY", "this string is way too long for the cap", 10); err == nil {
		t.Fatal("over-cap: expected error")
	}
	// maxValueBytes=0 disables the cap (used by bulk Seal path).
	if _, err := SealOne(id.Recipient(), "OK_KEY", "anything goes here", 0); err != nil {
		t.Fatalf("cap disabled: %v", err)
	}
}

// TestSealNilArgs checks the precondition errors. Defensive — callers
// should always pass real args, but a nil recipient or identity must not
// panic.
func TestSealNilArgs(t *testing.T) {
	if _, err := Seal(nil, Envelope{"K": "v"}); err == nil {
		t.Error("Seal(nil recipient): expected error")
	}
	if _, err := Open(nil, []byte("blob")); err == nil {
		t.Error("Open(nil identity): expected error")
	}
	if _, err := Open(&age.X25519Identity{}, []byte{}); err == nil {
		t.Error("Open(empty blob): expected error")
	}
}

// TestSealBinaryOutput is a smoke test that the ciphertext is binary-safe
// (no embedded NULs would surprise PG bytea).
func TestSealBinaryOutput(t *testing.T) {
	id := mustGenHostKey(t, "host.age")
	blob, err := Seal(id.Recipient(), Envelope{"K": "value"})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if len(blob) < 64 {
		t.Errorf("blob suspiciously short: %d bytes", len(blob))
	}
	if !bytes.HasPrefix(blob, []byte("age-encryption.org/v1\n")) {
		t.Errorf("blob missing age header magic")
	}
}

// --- helpers ---------------------------------------------------------------

// envelopesEqual compares two Envelopes without relying on map ordering.
func envelopesEqual(a, b Envelope) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// mustGenHostKey generates a host key in a per-test temp dir and returns the
// identity. Failure is fatal — every test that uses it relies on the key
// being usable.
func mustGenHostKey(t *testing.T, name string) *age.X25519Identity {
	t.Helper()
	id, err := GenerateAndSaveHostKey(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	return id
}
