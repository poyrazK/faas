// adr: 053
// capacity_sign_test.go — ADR-053 node_signature tests.
//
// Pins the canonical-payload / sign / verify triangle that
// the vmmd publisher and schedd handler both depend on:
//
//  1. CanonicalPayload shape (domain || node_id || int64 || 5×int32)
//  2. HashCanonicalPayload returns 32-byte SHA-256
//  3. KeyIDForPublicKey is stable + lowercase-hex + 64 chars
//  4. SignNodeReport + VerifyNodeSignature round-trip
//  5. Tampered NodeID → ErrSignatureMismatch
//  6. Replayed SampledAt → ErrSignatureMismatch
//  7. Wrong key → ErrUnknownNodeKey
//  8. Unknown key_id → ErrUnknownNodeKey
//  9. Empty sig → ErrEmptySignature
// 10. Nil registry → ErrUnknownNodeKey
// 11. Non-P-256 curve → SignNodeReport error
// 12. Nil key → SignNodeReport error
//
// White-box test (package sched) so it can reach nodeKeyLookup
// directly without re-exporting the helper for production
// callers. Companion tests live in:
//   - pkg/scheddgrpc/capacity_test.go (handler-side: end-to-end
//     unsigned + signed report wire round-trip)
//   - cmd/vmmd/capacity_publisher_e2e_test.go (publisher-side:
//     the producer stamps node_signature before Send)

package sched

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

// stubKeyLookup is a nodeKeyLookup backed by a map. Tests inject
// this into VerifyNodeSignature without spinning up a real
// NodeKeyRegistry (which has a Postgres loader behind it).
type stubKeyLookup struct {
	keys  map[string]*ecdsa.PublicKey
	owner string
}

func (s *stubKeyLookup) PublicKeyForNode(nodeID, keyID string) (*ecdsa.PublicKey, bool) {
	if s.owner != "" && s.owner != nodeID {
		return nil, false
	}
	pub, ok := s.keys[keyID]
	return pub, ok
}

// generateTestP256 returns a fresh ECDSA P-256 key + its key_id
// (SHA-256 hex of the SPKI). Helper for the happy-path +
// negative tests below.
func generateTestP256(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	keyID, err := KeyIDForPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("KeyIDForPublicKey: %v", err)
	}
	return priv, keyID
}

// sampleReport returns a deterministic CapacityReport for the
// signature tests. The struct fields are small enough to fit
// in a single integer-line for the round-trip + negative tests.
func sampleReport(nodeID string) CapacityReport {
	return CapacityReport{
		NodeID:        nodeID,
		SampledAt:     time.UnixMilli(1730000000000),
		LiveCount:     12,
		LeasedCount:   3,
		UsedMB:        4096,
		RAMHeadroomMB: 32000,
		VCPUBusy:      24,
	}
}

// TestCanonicalPayload_Shape asserts the byte layout matches
// the ADR-053 spec: domain || node_id || int64 || 5×int32.
// Pin the leading bytes so a future refactor that drops the
// domain separator (or rearranges the int fields) breaks a
// load-bearing test, not a regression in production.
func TestCanonicalPayload_Shape(t *testing.T) {
	t.Parallel()
	r := sampleReport("node-1")
	got := r.CanonicalPayload()

	// Domain prefix.
	if string(got[:len(capacityPayloadDomain)]) != capacityPayloadDomain {
		t.Errorf("domain prefix = %q, want %q",
			got[:len(capacityPayloadDomain)], capacityPayloadDomain)
	}
	if string(got[len(capacityPayloadDomain):len(capacityPayloadDomain)+len("node-1")]) != "node-1" {
		t.Errorf("node_id segment = %q, want %q",
			got[len(capacityPayloadDomain):len(capacityPayloadDomain)+len("node-1")], "node-1")
	}

	// Total length: 16 (domain) + 6 (node-1) + 8 (int64) + 5*4 (int32s)
	// = 16 + 6 + 8 + 20 = 50 bytes.
	if wantLen := len(capacityPayloadDomain) + len("node-1") + 8 + 20; len(got) != wantLen {
		t.Errorf("payload length = %d, want %d", len(got), wantLen)
	}
}

// TestHashCanonicalPayload_Digest asserts the digest is exactly
// 32 bytes (SHA-256 output). The signing path takes the digest
// directly, so verify also takes the digest — no re-hashing.
func TestHashCanonicalPayload_Digest(t *testing.T) {
	t.Parallel()
	r := sampleReport("node-1")
	got := r.HashCanonicalPayload()
	if len(got) != sha256.Size {
		t.Errorf("digest length = %d, want %d", len(got), sha256.Size)
	}
}

// TestKeyIDForPublicKey_Stable asserts the same key produces
// the same key_id across calls. The schedd-side registry is
// keyed by this value, so a non-stable encoding would de-sync
// the wire's node_key_id from the registry's map.
func TestKeyIDForPublicKey_Stable(t *testing.T) {
	t.Parallel()
	priv, keyID1 := generateTestP256(t)
	keyID2, err := KeyIDForPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("KeyIDForPublicKey: %v", err)
	}
	if keyID1 != keyID2 {
		t.Errorf("key_id non-stable: %s vs %s", keyID1, keyID2)
	}
	if len(keyID1) != 64 {
		t.Errorf("key_id length = %d, want 64 (hex of SHA-256)", len(keyID1))
	}
	// Hex-decode to ensure the encoding is lowercase alphabet
	// only (the migration's CHECK constraint pins [a-f0-9]).
	decoded, err := hex.DecodeString(keyID1)
	if err != nil {
		t.Errorf("key_id not hex: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("decoded key_id length = %d, want 32", len(decoded))
	}
}

// TestKeyIDForPublicKey_NilKey: a nil public key is rejected.
// Defensive guard so a future caller that bypasses the
// capacityStreamer interface doesn't get a hex-encode of nil.
func TestKeyIDForPublicKey_NilKey(t *testing.T) {
	t.Parallel()
	if _, err := KeyIDForPublicKey(nil); err == nil {
		t.Error("KeyIDForPublicKey(nil) succeeded; want error")
	}
}

// TestSignAndVerify_HappyPath: a freshly-signed report verifies
// against the issuing key. The load-bearing case — if this
// fails, no vmmd will ever get a report into the schedd's
// cache.
func TestSignAndVerify_HappyPath(t *testing.T) {
	t.Parallel()
	priv, keyID := generateTestP256(t)
	r := sampleReport("node-1")

	sig, err := SignNodeReport(priv, r)
	if err != nil {
		t.Fatalf("SignNodeReport: %v", err)
	}
	if len(sig) != 64 {
		t.Errorf("signature length = %d, want 64 (raw r||s)", len(sig))
	}

	report := r
	report.NodeSignature = sig
	report.NodeKeyID = keyID

	keys := &stubKeyLookup{keys: map[string]*ecdsa.PublicKey{
		keyID: &priv.PublicKey,
	}}
	if err := VerifyNodeSignature(report, sig, keys); err != nil {
		t.Errorf("VerifyNodeSignature: %v", err)
	}
}

// TestVerifyNodeSignature_CrossNodeKeyRejected proves that possession of a
// valid key cannot be used to publish capacity under another compute node's
// identity. The node_id is part of the signed payload, but it is also supplied
// by the caller; the registry ownership check supplies the authorization bind.
func TestVerifyNodeSignature_CrossNodeKeyRejected(t *testing.T) {
	t.Parallel()
	priv, keyID := generateTestP256(t)
	report := sampleReport("node-2")
	sig, err := SignNodeReport(priv, report)
	if err != nil {
		t.Fatalf("SignNodeReport: %v", err)
	}
	report.NodeSignature = sig
	report.NodeKeyID = keyID

	keys := &stubKeyLookup{
		owner: "node-1",
		keys:  map[string]*ecdsa.PublicKey{keyID: &priv.PublicKey},
	}
	if err := VerifyNodeSignature(report, sig, keys); !errors.Is(err, ErrUnknownNodeKey) {
		t.Fatalf("cross-node key: err = %v, want ErrUnknownNodeKey", err)
	}
}

// TestVerifyNodeSignature_TamperedPayload: flipping the NodeID
// after signing breaks the signature. The payload is bound at
// sign time, so any mutation produces a different digest and
// fails verification.
func TestVerifyNodeSignature_TamperedPayload(t *testing.T) {
	t.Parallel()
	priv, keyID := generateTestP256(t)
	r := sampleReport("node-1")
	sig, err := SignNodeReport(priv, r)
	if err != nil {
		t.Fatalf("SignNodeReport: %v", err)
	}

	tampered := r
	tampered.NodeID = "node-2"
	tampered.NodeSignature = sig
	tampered.NodeKeyID = keyID

	keys := &stubKeyLookup{keys: map[string]*ecdsa.PublicKey{
		keyID: &priv.PublicKey,
	}}
	if err := VerifyNodeSignature(tampered, sig, keys); !errors.Is(err, ErrSignatureMismatch) {
		t.Errorf("tampered payload: err = %v, want ErrSignatureMismatch", err)
	}
}

// TestVerifyNodeSignature_ReplayedTimestamp: a different
// sampled_at (the report claims a different time) breaks the
// signature. The bind is on the canonical payload, not the
// wire's node_signature field, so a future replay where the
// vmmd-side timestamp is forwarded unchanged would still
// verify; that scenario is the freshness budget, not the
// signature path.
func TestVerifyNodeSignature_ReplayedTimestamp(t *testing.T) {
	t.Parallel()
	priv, keyID := generateTestP256(t)
	r := sampleReport("node-1")
	sig, err := SignNodeReport(priv, r)
	if err != nil {
		t.Fatalf("SignNodeReport: %v", err)
	}

	replayed := r
	replayed.SampledAt = r.SampledAt.Add(1 * time.Hour)
	replayed.NodeSignature = sig
	replayed.NodeKeyID = keyID

	keys := &stubKeyLookup{keys: map[string]*ecdsa.PublicKey{
		keyID: &priv.PublicKey,
	}}
	if err := VerifyNodeSignature(replayed, sig, keys); !errors.Is(err, ErrSignatureMismatch) {
		t.Errorf("replayed timestamp: err = %v, want ErrSignatureMismatch", err)
	}
}

// TestVerifyNodeSignature_WrongKey: a different (genuine) ECDSA
// P-256 key fails verification. The pin is per-key, not per-
// node: even a same-curve key from the same node is rejected.
func TestVerifyNodeSignature_WrongKey(t *testing.T) {
	t.Parallel()
	signer, keyID := generateTestP256(t)
	_, otherKeyID := generateTestP256(t)
	r := sampleReport("node-1")
	sig, err := SignNodeReport(signer, r)
	if err != nil {
		t.Fatalf("SignNodeReport: %v", err)
	}

	report := r
	report.NodeSignature = sig
	report.NodeKeyID = keyID // signed by signer

	// Registry has the OTHER key (rotation scenario).
	wrongPriv, _ := generateTestP256(t)
	keys := &stubKeyLookup{keys: map[string]*ecdsa.PublicKey{
		otherKeyID: &wrongPriv.PublicKey,
	}}
	if err := VerifyNodeSignature(report, sig, keys); !errors.Is(err, ErrUnknownNodeKey) {
		t.Errorf("wrong key: err = %v, want ErrUnknownNodeKey", err)
	}
}

// TestVerifyNodeSignature_UnknownKeyID: the registry has no
// entry for the report's key_id. Distinct from
// ErrSignatureMismatch so a stale-registry scenario is
// observable in logs.
func TestVerifyNodeSignature_UnknownKeyID(t *testing.T) {
	t.Parallel()
	priv, keyID := generateTestP256(t)
	r := sampleReport("node-1")
	sig, err := SignNodeReport(priv, r)
	if err != nil {
		t.Fatalf("SignNodeReport: %v", err)
	}

	report := r
	report.NodeSignature = sig
	report.NodeKeyID = keyID

	keys := &stubKeyLookup{keys: map[string]*ecdsa.PublicKey{}}
	if err := VerifyNodeSignature(report, sig, keys); !errors.Is(err, ErrUnknownNodeKey) {
		t.Errorf("unknown key: err = %v, want ErrUnknownNodeKey", err)
	}
}

// TestVerifyNodeSignature_EmptySignature: a slice-3 schedd
// rejects empty sigs. Pre-slice-3 schedd would skip
// verification entirely (the engine has keys == nil and the
// handler returns codes.OK).
func TestVerifyNodeSignature_EmptySignature(t *testing.T) {
	t.Parallel()
	_, keyID := generateTestP256(t)
	r := sampleReport("node-1")
	r.NodeKeyID = keyID

	keys := &stubKeyLookup{keys: map[string]*ecdsa.PublicKey{
		keyID: nil, // populated elsewhere; the empty-sig check fires first
	}}
	if err := VerifyNodeSignature(r, nil, keys); !errors.Is(err, ErrEmptySignature) {
		t.Errorf("empty sig: err = %v, want ErrEmptySignature", err)
	}
}

// TestVerifyNodeSignature_NilRegistry: a nil registry returns
// ErrUnknownNodeKey. The handler maps this to keeping the
// stream alive but logging; production must NEVER reach this
// path because the engine wires a registry before the gRPC
// handler runs.
func TestVerifyNodeSignature_NilRegistry(t *testing.T) {
	t.Parallel()
	priv, keyID := generateTestP256(t)
	r := sampleReport("node-1")
	sig, err := SignNodeReport(priv, r)
	if err != nil {
		t.Fatalf("SignNodeReport: %v", err)
	}

	report := r
	report.NodeSignature = sig
	report.NodeKeyID = keyID

	if err := VerifyNodeSignature(report, sig, nil); !errors.Is(err, ErrUnknownNodeKey) {
		t.Errorf("nil registry: err = %v, want ErrUnknownNodeKey", err)
	}
}

// TestSignNodeReport_RejectsNonP256: a non-P-256 curve is
// rejected at sign time. Future curve migration flips the
// constant; the test pin ensures the helper stays in sync.
func TestSignNodeReport_RejectsNonP256(t *testing.T) {
	t.Parallel()
	priv, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-224: %v", err)
	}
	r := sampleReport("node-1")
	if _, err := SignNodeReport(priv, r); err == nil {
		t.Error("SignNodeReport on P-224 key succeeded; want error")
	}
}

// TestSignNodeReport_RejectsNilKey: a nil key is rejected.
//
// Pins the nil-safety guard so the publisher (cmd/vmmd) can
// safely call SignNodeReport with a nil key when vmmd is in
// pre-slice-3 mode (the caller guards against this, but the
// crypto helper should also be defensive).
func TestSignNodeReport_RejectsNilKey(t *testing.T) {
	t.Parallel()
	r := sampleReport("node-1")
	if _, err := SignNodeReport(nil, r); err == nil {
		t.Error("SignNodeReport on nil key succeeded; want error")
	}
}

// TestSignNodeReport_RejectsNegativeFields pins the
// CanonicalPayload precondition: every numeric field must be
// ≥ 0. A negative value silently wraps to a huge uint on the
// wire, producing a signature that won't verify against any
// honest reconstruction. SignNodeReport must reject up front
// so the publisher cannot mint a self-inconsistent report.
//
// Tests SampledAt pre-epoch + each of the five int32 fields.
// A regression that drops the validator would slip a
// silently-broken signature into the wire and break every
// downstream verification — but only in the field, not in
// unit tests. This test pins the contract at the boundary.
func TestSignNodeReport_RejectsNegativeFields(t *testing.T) {
	t.Parallel()
	priv, _ := generateTestP256(t)
	base := sampleReport("node-1")

	cases := []struct {
		name string
		mut  func(*CapacityReport)
	}{
		{"pre-epoch SampledAt", func(r *CapacityReport) { r.SampledAt = time.UnixMilli(-1) }},
		{"negative LiveCount", func(r *CapacityReport) { r.LiveCount = -1 }},
		{"negative LeasedCount", func(r *CapacityReport) { r.LeasedCount = -1 }},
		{"negative UsedMB", func(r *CapacityReport) { r.UsedMB = -1 }},
		{"negative RAMHeadroomMB", func(r *CapacityReport) { r.RAMHeadroomMB = -1 }},
		{"negative VCPUBusy", func(r *CapacityReport) { r.VCPUBusy = -1 }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := base
			tc.mut(&r)
			if _, err := SignNodeReport(priv, r); err == nil {
				t.Errorf("SignNodeReport(%s) succeeded; want error", tc.name)
			}
		})
	}
}
