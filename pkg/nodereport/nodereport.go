// Package nodereport is the signed evidence a compute node's vmmd sends
// schedd (ADR-053, ADR-419): the per-second CapacityReport and the
// authenticated instance Inventory, each signed with the node's ECDSA
// P-256 key over a domain-separated canonical payload.
//
// vmmd signs, schedd verifies; both sides must build the same bytes. The
// code used to live in pkg/sched, which made vmmd link the scheduler
// engine to sign a report. Keep this package a leaf: it must not import
// the scheduler or the store. pkg/sched re-exports it under its old
// names.
//
// Error messages keep their "sched:" prefix so schedd's and vmmd's log
// lines read as they did before the move.
package nodereport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"
)

// CapacityPayloadDomain is the domain separator that prevents a tier-3
// cosign signature on an ext4 layer (ADR-038) from being replayed as a
// CapacityReport signature. Mirrors sigstore's Type Hints. ADR-053 §2.
//
// Pinned constant: any change here is a wire-incompatible break of every
// node_signature that's been emitted under the old separator. Bumping
// this constant is a coordinated key rotation.
const CapacityPayloadDomain = "faas.capacity.v1"

// inventoryPayloadDomain separates inventory signatures from capacity
// signatures (ADR-419).
const inventoryPayloadDomain = "faas.instance-inventory.v1"

// ErrSignatureMismatch is the typed error VerifyCapacity returns when
// the signature bytes are valid ECDSA-P-256 (64-byte raw r||s) but the
// verification fails. Callers map this to codes.Unauthenticated on the
// wire.
var ErrSignatureMismatch = errors.New("sched: capacity signature mismatch")

// ErrUnknownNodeKey is the typed error returned when the report's
// node_key_id does not resolve in the key registry. Distinct from
// ErrSignatureMismatch so a stale-registry scenario is observable in logs
// (registry is missing the row, not the signature is wrong).
var ErrUnknownNodeKey = errors.New("sched: unknown node_key_id")

// ErrEmptySignature is the typed error returned when the report arrives
// with an empty node_signature field on a slice-3 schedd. Pre-slice-3
// schedd returns nil (the field is additive); the slice-3 handler returns
// this and rejects the stream.
var ErrEmptySignature = errors.New("sched: empty node_signature on slice-3 schedd")

// KeyLookup is the registry VerifyCapacity and VerifyInventory consult.
// schedd's NodeKeyRegistry implements it; tests inject a stub without a
// real registry or Postgres.
//
// The interface is small (one method) and stable. Adding a second method
// here is a wire-incompatible break; add a new interface and compose.
type KeyLookup interface {
	// PublicKeyForNode returns the registered ECDSA P-256 public key only
	// when keyID is currently trusted for nodeID. Binding both values closes
	// the cross-node impersonation gap where any trusted key could previously
	// sign a report carrying another node's ID.
	PublicKeyForNode(nodeID, keyID string) (pub *ecdsa.PublicKey, ok bool)
}

// CapacityReport mirrors scheddpb.CapacityReport at the engine boundary.
// Decoupled from the proto package so the chooser and tests don't import
// the gRPC generated types.
//
// SampledAt is informational; the chooser uses the table's lastSeen stamp
// (set in Replace) for the freshness budget, not the proto's
// sampled_at_unix_ms, so clock skew between hosts is invisible.
//
// NodeSignature + NodeKeyID (ADR-053) are populated by the publisher when
// the proto's optional fields are non-empty. The chooser does not consume
// them (signature verification happens at the gRPC handler boundary; the
// table caches already-trusted numbers).
type CapacityReport struct {
	NodeID        string
	SampledAt     time.Time
	LiveCount     int32
	LeasedCount   int32
	UsedMB        int32
	RAMHeadroomMB int32
	VCPUBusy      int32
	NodeSignature []byte
	NodeKeyID     string
}

// CanonicalPayload builds the byte slice that ECDSA-P-256-with-SHA-256 is
// computed over. ADR-053 §2:
//
//	"faas.capacity.v1" || node_id (UTF-8)
//	  || sampled_at_unix_ms (big-endian int64, must be ≥ 0)
//	  || live_count (big-endian uint32)
//	  || leased_count (big-endian uint32)
//	  || used_mb (big-endian uint32)
//	  || ram_headroom_mb (big-endian uint32)
//	  || vcpu_busy (big-endian uint32)
//
// The domain separator is a fixed prefix; the ints are fixed-width
// big-endian. Total length is
//
//	16 + len(node_id) + 8 + 5*4 = 44 + len(node_id)
//
// bytes. Pure function; used by both Sign (vmmd publisher) and Verify
// (schedd handler). The single source of truth for what gets signed.
//
// Precondition: every numeric field must be ≥ 0. A negative value
// silently wraps to a huge uint on the wire, producing a signature that
// won't verify against any honest reconstruction. SignCapacity rejects
// negative inputs up front so the publisher cannot mint a
// self-inconsistent report.
func (r CapacityReport) CanonicalPayload() []byte {
	// 16 (domain) + len(node_id) + 8 (int64) + 5*4 (int32s)
	buf := make([]byte, 0, len(CapacityPayloadDomain)+len(r.NodeID)+8+20)
	buf = append(buf, []byte(CapacityPayloadDomain)...)
	buf = append(buf, []byte(r.NodeID)...)
	var scratch [8]byte
	binary.BigEndian.PutUint64(scratch[:], uint64(r.SampledAt.UnixMilli()))
	buf = append(buf, scratch[:]...)
	binary.BigEndian.PutUint32(scratch[:4], uint32(r.LiveCount))
	buf = append(buf, scratch[:4]...)
	binary.BigEndian.PutUint32(scratch[:4], uint32(r.LeasedCount))
	buf = append(buf, scratch[:4]...)
	binary.BigEndian.PutUint32(scratch[:4], uint32(r.UsedMB))
	buf = append(buf, scratch[:4]...)
	binary.BigEndian.PutUint32(scratch[:4], uint32(r.RAMHeadroomMB))
	buf = append(buf, scratch[:4]...)
	binary.BigEndian.PutUint32(scratch[:4], uint32(r.VCPUBusy))
	buf = append(buf, scratch[:4]...)
	return buf
}

// HashCanonicalPayload returns sha256(CanonicalPayload). Exposed for tests
// that want to assert the digest shape directly without running ECDSA.
func (r CapacityReport) HashCanonicalPayload() []byte {
	h := sha256.Sum256(r.CanonicalPayload())
	return h[:]
}

// SignCapacity produces a 64-byte raw (r||s) ECDSA P-256 signature over
// r.CanonicalPayload(). The signature is computed against the SHA-256
// digest of the payload (not double-hashed — mirror of pkg/cosign's
// verifyDigest contract). Returned errors reflect crypto/rand failures
// (rare) and pre-flight validation failures (negative numeric fields).
//
// Used by the vmmd publisher (cmd/vmmd/capacity_publisher.go); not used on
// the schedd side. Production wires this once at startup with the loaded
// node signing key.
//
// Pre-flight validation: SampledAt must be ≥ unix epoch and every int32
// field must be ≥ 0. CanonicalPayload encodes these as unsigned
// big-endian (the wire contract is unsigned), so a negative value would
// silently wrap and produce a signature that doesn't verify against any
// honest reconstruction. Rejecting at sign-time keeps the publisher from
// minting a self-inconsistent report.
func SignCapacity(priv *ecdsa.PrivateKey, r CapacityReport) ([]byte, error) {
	if priv == nil {
		return nil, errors.New("sched: SignNodeReport: nil private key")
	}
	if priv.Curve != P256() {
		return nil, fmt.Errorf("sched: SignNodeReport: want P-256 curve, got %s", priv.Curve.Params().Name)
	}
	if err := validateReportNonNegative(r); err != nil {
		return nil, fmt.Errorf("sched: SignNodeReport: %w", err)
	}
	digest := r.HashCanonicalPayload()
	rInt, sInt, err := ecdsaSignDeterministic(priv, digest)
	if err != nil {
		return nil, fmt.Errorf("sched: SignNodeReport: %w", err)
	}
	out := make([]byte, 64)
	rb := rInt.Bytes()
	sb := sInt.Bytes()
	if len(rb) > 32 || len(sb) > 32 {
		return nil, fmt.Errorf("sched: SignNodeReport: oversized r/s (%d/%d)", len(rb), len(sb))
	}
	copy(out[32-len(rb):32], rb)
	copy(out[64-len(sb):64], sb)
	return out, nil
}

// validateReportNonNegative enforces the CanonicalPayload precondition
// (all numeric fields ≥ 0). The function is named for the precondition,
// not the wire encoding, so readers see the contract without reading the
// encoder.
func validateReportNonNegative(r CapacityReport) error {
	if r.SampledAt.UnixMilli() < 0 {
		return fmt.Errorf("SampledAt before unix epoch: %s", r.SampledAt)
	}
	for _, c := range []struct {
		name  string
		value int32
	}{
		{"LiveCount", r.LiveCount},
		{"LeasedCount", r.LeasedCount},
		{"UsedMB", r.UsedMB},
		{"RAMHeadroomMB", r.RAMHeadroomMB},
		{"VCPUBusy", r.VCPUBusy},
	} {
		if c.value < 0 {
			return fmt.Errorf("%s is negative: %d", c.name, c.value)
		}
	}
	return nil
}

// KeyID returns the canonical key_id for a public key: the SHA-256 hex of
// the SubjectPublicKeyInfo. schedd's key registry is keyed by this value;
// the wire's CapacityReport.node_key_id carries the same value so a report
// is routable to its registered key.
//
// The encoding is the standard "sha256:<lowercase-hex>" stripped of its
// prefix — 64 hex chars. The migration's CHECK constraint pins the same
// shape (compute_node_keys_key_id_shape).
func KeyID(pub *ecdsa.PublicKey) (string, error) {
	if pub == nil {
		return "", errors.New("sched: KeyIDForPublicKey: nil public key")
	}
	der, err := marshalPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("sched: KeyIDForPublicKey: %w", err)
	}
	return hex.EncodeToString(sha256Sum(der)), nil
}

// VerifyCapacity checks the report's node_signature against the public key
// registered under node_key_id in keys. Returns nil on success, or one of:
//
//   - ErrUnknownNodeKey: keys is nil or doesn't carry node_key_id
//   - ErrEmptySignature: sig is empty (slice-3 strict)
//   - ErrSignatureMismatch: signature didn't verify
//
// The function does NOT verify the timestamp — that's the freshness budget
// on schedd's capacity table. node_signature already binds
// sampled_at_unix_ms into the payload, so a replayed-old report fails
// verification anyway.
func VerifyCapacity(r CapacityReport, sig []byte, keys KeyLookup) error {
	if len(sig) == 0 {
		return ErrEmptySignature
	}
	if keys == nil {
		return ErrUnknownNodeKey
	}
	pub, ok := keys.PublicKeyForNode(r.NodeID, r.NodeKeyID)
	if !ok {
		return ErrUnknownNodeKey
	}
	if pub.Curve != P256() {
		return ErrSignatureMismatch
	}
	digest := r.HashCanonicalPayload()
	if !verifyDigestRaw(pub, digest, sig) {
		return ErrSignatureMismatch
	}
	return nil
}

// Inventory is an explicit, authenticated vmmd process inventory (ADR-419).
// Metrics are never an inventory: a failed cgroup read must not fail live
// VMs.
type Inventory struct {
	NodeID      string
	NodeKeyID   string
	SampledAt   time.Time
	Complete    bool
	InstanceIDs []string
	Signature   []byte
}

func (r Inventory) digest() []byte {
	buf := []byte(inventoryPayloadDomain)
	appendString := func(s string) {
		buf = binary.BigEndian.AppendUint32(buf, uint32(len(s)))
		buf = append(buf, s...)
	}
	appendString(r.NodeID)
	appendString(r.NodeKeyID)
	buf = binary.BigEndian.AppendUint64(buf, uint64(r.SampledAt.UnixMilli()))
	if r.Complete {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	ids := append([]string(nil), r.InstanceIDs...)
	sort.Strings(ids)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(ids)))
	for _, id := range ids {
		appendString(id)
	}
	digest := sha256.Sum256(buf)
	return digest[:]
}

// SignInventory signs r with the node key: 64-byte raw r||s over the
// inventory digest.
func SignInventory(key *ecdsa.PrivateKey, r Inventory) ([]byte, error) {
	if key == nil || key.Curve != P256() || r.SampledAt.UnixMilli() < 0 {
		return nil, errors.New("sched: inventory signing requires P-256 and a valid timestamp")
	}
	rInt, sInt, err := ecdsaSignDeterministic(key, r.digest())
	if err != nil {
		return nil, err
	}
	sig := make([]byte, 64)
	rInt.FillBytes(sig[:32])
	sInt.FillBytes(sig[32:])
	return sig, nil
}

// VerifyInventory checks r.Signature against the key registered for
// r.NodeID and r.NodeKeyID.
func VerifyInventory(r Inventory, keys KeyLookup) error {
	if len(r.Signature) == 0 {
		return ErrEmptySignature
	}
	if keys == nil {
		return ErrUnknownNodeKey
	}
	key, ok := keys.PublicKeyForNode(r.NodeID, r.NodeKeyID)
	if !ok {
		return ErrUnknownNodeKey
	}
	if key.Curve != P256() || !verifyDigestRaw(key, r.digest(), r.Signature) {
		return ErrSignatureMismatch
	}
	return nil
}

// P256 returns the P-256 curve, the only curve node keys use. Pinned here
// so the curve is the single source of truth; a future curve migration
// bumps one function, not three.
func P256() elliptic.Curve { return elliptic.P256() }

// marshalPublicKey encodes pub as a SubjectPublicKeyInfo (the canonical
// X.509 format). The key registry persists this shape
// (compute_node_keys.public_key_pem); the SHA-256 of the DER bytes is the
// key_id.
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

// verifyDigestRaw is the dual of cosign.verifyDigest: it takes a 32-byte
// SHA-256 digest (NOT a payload) and a 64-byte raw (r||s) signature. The
// dual is load-bearing — SignCapacity signs the digest bytes directly, so
// verify must not re-hash.
func verifyDigestRaw(pub *ecdsa.PublicKey, digest, sig []byte) bool {
	if pub == nil || len(sig) != 64 || len(digest) != 32 {
		return false
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return ecdsa.Verify(pub, digest, r, s)
}

// ecdsaSignDeterministic signs a 32-byte digest. crypto/ecdsa does not
// accept a deterministic reader, so this uses crypto/rand: the wire
// carries the r||s bytes, not k, so the non-determinism is invisible to
// verifiers.
func ecdsaSignDeterministic(priv *ecdsa.PrivateKey, digest []byte) (r, s *big.Int, err error) {
	if priv == nil {
		return nil, nil, errors.New("sched: nil private key")
	}
	if len(digest) != 32 {
		return nil, nil, fmt.Errorf("sched: digest length %d, want 32", len(digest))
	}
	return ecdsa.Sign(rand.Reader, priv, digest)
}

func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}
