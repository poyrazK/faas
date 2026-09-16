// capacity.go — schedd's live-capacity cache for vmmd's per-host
// CapacityReport stream (ADR-025 axis 5).
//
// Background. PR #429 (placement scheduler, axis 3) shipped
// schedd's chooser reading `store.ComputeNodeUsedMB(ctx, n.ID)`,
// a stale sum of `instances.ram_mb + 8` rows. PR #429's
// sticky-warm affinity (axis 3) and the axis-4 warm-hint push
// (PR #431) addressed which node to bias traffic toward, but
// the chooser was still operating on a stale per-node accounting.
// On a multi-box fleet, this lets the chooser over-admit a node
// whose actual cgroup memory.current exceeds AdmissionCeilingMB —
// §6.2-2 violation territory.
//
// This file is the schedd-side sink: a per-node in-memory cache
// keyed by compute_nodes.id (uuid). vmmd's publisher
// (cmd/vmmd/capacity_publisher.go) dials schedd at startup and
// pushes one report per second; the gRPC handler
// (pkg/scheddgrpc.Server.ReportCapacity) decodes and calls
// table.Replace on each. The chooser
// (pkg/sched/engine.go::Engine.applyLiveCapacityMB, Tier A1) consults
// Lookup with a ledger-floor and falls back to the legacy store sum.
//
// Trust model. Capacity is bias, not authority — the chooser
// reads capacity as ONE input to ChoosePlacement, never the only
// input. The per-node AdmissionCeilingMB check inside
// ChoosePlacement and the ledger's per-node floor
// (Engine.applyLiveCapacityMB's `max(report, ledger.ResidentRAMForNode)`)
// are the load-bearing enforcement. A stale-low or hostile vmmd
// cannot shrink the live accounting and force schedd to
// over-admit. ADR-005 cold-boot safety is preserved by
// construction: an empty table falls through to
// store.ComputeNodeUsedMB (the legacy single-box behaviour).
//
// ADR-009 (snapshot reuse / bias-not-gate) is preserved because
// capacity is bias-only on the consumer side: saturation falls
// through to per-node healthyCount scoring inside ChoosePlacement.
//
// Concurrency model.
//
//   - Replace takes the WLock once and atomically swaps one
//     node's entry. The handler goroutine owns all Replace calls
//     (one per Recv). The chooser goroutine reads under the
//     RLock via Lookup.
//
//   - The lastSeen timestamp is stamped on Replace so the
//     freshness budget is "since this daemon received the
//     report", not "since vmmd sampled it" — clock skew between
//     hosts can be tens of ms and the budget is in seconds.
//
//   - nil receiver is tolerated everywhere (Replace / Lookup)
//     so a pre-axis-5 Engine fixture that doesn't construct a
//     table continues to behave as legacy single-box.
//
// Backpressure: none. Replace is synchronous and bounded —
// the lock is held for one map assignment. The publisher is
// best-effort; a slow handler would back-pressure the gRPC
// stream which the publisher's reconnect loop treats as
// transient (cmd/vmmd/reconnect.go).

package sched

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

// capacityPayloadDomain is the domain separator that prevents a
// tier-3 cosign signature on an ext4 layer (ADR-038) from being
// replayed as a CapacityReport signature. Mirrors sigstore's
// Type Hints. ADR-053 §2.
//
// Pinned constant: any change here is a wire-incompatible break
// of every node_signature that's been emitted under the old
// separator. Bumping this constant is a coordinated key rotation.
const capacityPayloadDomain = "faas.capacity.v1"

// ErrSignatureMismatch is the typed error VerifyNodeSignature
// returns when the signature bytes are valid ECDSA-P-256 (64-byte
// raw r||s) but the verification fails. Callers map this to
// codes.Unauthenticated on the wire.
var ErrSignatureMismatch = errors.New("sched: capacity signature mismatch")

// ErrUnknownNodeKey is the typed error returned when the report's
// node_key_id does not resolve in the nodeKeyRegistry. Distinct
// from ErrSignatureMismatch so a stale-registry scenario is
// observable in logs (registry is missing the row, not the
// signature is wrong).
var ErrUnknownNodeKey = errors.New("sched: unknown node_key_id")

// ErrEmptySignature is the typed error returned when the report
// arrives with an empty node_signature field on a slice-3 schedd.
// Pre-slice-3 schedd returns nil (the field is additive); the
// slice-3 handler returns this and rejects the stream.
var ErrEmptySignature = errors.New("sched: empty node_signature on slice-3 schedd")

// CanonicalPayload builds the byte slice that
// ECDSA-P-256-with-SHA-256 is computed over. ADR-053 §2:
//
//	"faas.capacity.v1" || node_id (UTF-8)
//	  || sampled_at_unix_ms (big-endian int64, must be ≥ 0)
//	  || live_count (big-endian uint32)
//	  || leased_count (big-endian uint32)
//	  || used_mb (big-endian uint32)
//	  || ram_headroom_mb (big-endian uint32)
//	  || vcpu_busy (big-endian uint32)
//
// The domain separator is a fixed prefix; the ints are
// fixed-width big-endian. Total length is
//
//	16 + len(node_id) + 8 + 5*4 = 44 + len(node_id)
//
// bytes. Pure function; used by both Sign (vmmd publisher) and
// Verify (schedd handler). The single source of truth for what
// gets signed.
//
// Precondition: every numeric field must be ≥ 0. A negative
// value silently wraps to a huge uint on the wire, producing a
// signature that won't verify against any honest reconstruction.
// SignNodeReport rejects negative inputs up front so the
// publisher cannot mint a self-inconsistent report.
func (r CapacityReport) CanonicalPayload() []byte {
	// 16 (domain) + len(node_id) + 8 (int64) + 5*4 (int32s)
	buf := make([]byte, 0, len(capacityPayloadDomain)+len(r.NodeID)+8+20)
	buf = append(buf, []byte(capacityPayloadDomain)...)
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

// HashCanonicalPayload returns sha256(CanonicalPayload). Exposed
// for tests that want to assert the digest shape directly without
// running ECDSA.
func (r CapacityReport) HashCanonicalPayload() []byte {
	h := sha256.Sum256(r.CanonicalPayload())
	return h[:]
}

// SignNodeReport produces a 64-byte raw (r||s) ECDSA P-256
// signature over r.CanonicalPayload(). The signature is computed
// against the SHA-256 digest of the payload (not double-hashed —
// mirror of pkg/cosign's verifyDigest contract). Returned errors
// reflect crypto/rand failures (rare) and pre-flight validation
// failures (negative numeric fields).
//
// Used by the vmmd publisher (cmd/vmmd/capacity_publisher.go);
// not used on the schedd side. Production wires this once at
// startup with the loaded node signing key.
//
// Pre-flight validation: SampledAt must be ≥ unix epoch and
// every int32 field must be ≥ 0. CanonicalPayload encodes
// these as unsigned big-endian (the wire contract is unsigned),
// so a negative value would silently wrap and produce a
// signature that doesn't verify against any honest
// reconstruction. Rejecting at sign-time keeps the publisher
// from minting a self-inconsistent report.
func SignNodeReport(priv *ecdsa.PrivateKey, r CapacityReport) ([]byte, error) {
	if priv == nil {
		return nil, errors.New("sched: SignNodeReport: nil private key")
	}
	if priv.Curve != ecdsaP256() {
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

// validateReportNonNegative enforces the CanonicalPayload
// precondition (all numeric fields ≥ 0). The function is
// named for the precondition, not the wire encoding, so
// readers see the contract without reading the encoder.
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

// KeyIDForPublicKey returns the canonical key_id for a public
// key: the SHA-256 hex of the SubjectPublicKeyInfo. The schedd
// nodeKeyRegistry is keyed by this value; the wire's
// CapacityReport.node_key_id carries the same value so a report
// is routable to its registered key.
//
// The encoding is the standard "sha256:<lowercase-hex>" stripped
// of its prefix — 64 hex chars. The migration's CHECK constraint
// pins the same shape (compute_node_keys_key_id_shape).
func KeyIDForPublicKey(pub *ecdsa.PublicKey) (string, error) {
	if pub == nil {
		return "", errors.New("sched: KeyIDForPublicKey: nil public key")
	}
	der, err := marshalPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("sched: KeyIDForPublicKey: %w", err)
	}
	return hexEncode(sha256Hex(der)), nil
}

// VerifyNodeSignature checks the report's node_signature against
// the public key registered under node_key_id in keys. Returns
// nil on success, or one of:
//
//   - ErrUnknownNodeKey: keys is nil or doesn't carry node_key_id
//   - ErrEmptySignature: sig is empty (slice-3 strict)
//   - ErrSignatureMismatch: signature didn't verify
//
// The function does NOT verify the timestamp — that's the
// freshness budget on the table (CapacityFreshness = 5 s).
// node_signature already binds sampled_at_unix_ms into the
// payload, so a replayed-old report fails verification anyway.
func VerifyNodeSignature(r CapacityReport, sig []byte, keys nodeKeyLookup) error {
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
	if pub.Curve != ecdsaP256() {
		return ErrSignatureMismatch
	}
	digest := r.HashCanonicalPayload()
	if !verifyDigestRaw(pub, digest, sig) {
		return ErrSignatureMismatch
	}
	return nil
}

// CapacityFreshness is the staleness budget a chooser applies
// before trusting a vmmd report. Reports older than this fall
// back to ComputeNodeUsedMB. 5 s = 5× the publisher's 1 s
// cadence; a missed tick is transient, a missed 5 ticks is a
// real outage and the chooser should stop biasing.
//
// Aligned with pkg/sched/instancestats.Poller's local 200 ms projection, but
// on the push side: the poller's freshness window is governed by its local
// tick, not by this constant. Both paths converge on "fresh = last 5 s";
// the engine's freshness gate can fall back to the bulk store observation
// independently if the table ages out.
const CapacityFreshness = 5 * time.Second

// CapacityReport mirrors scheddpb.CapacityReport at the engine
// boundary. Decoupled from the proto package so the chooser +
// tests don't import the gRPC generated types.
//
// SampledAt is informational; the chooser uses the table's
// lastSeen stamp (set in Replace) for the freshness budget,
// not the proto's sampled_at_unix_ms, so clock skew between
// hosts is invisible.
//
// NodeSignature + NodeKeyID (ADR-053) are populated by the
// publisher when the proto's optional fields are non-empty.
// The chooser does not consume them (signature verification
// happens at the gRPC handler boundary; the table caches
// already-trusted numbers).
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

// CapacitySink is the per-event callback the ReportCapacity
// handler invokes for each CapacityReport decoded from the
// gRPC stream. Same shape as WarmHintSink — non-nil error
// aborts the stream, nil keeps delivering.
//
// Type-aliased by pkg/scheddgrpc (server.go region) so the
// SchedAPI interface can name sched.CapacitySink without an
// import cycle. The handler composes this with its wire-side
// send on the per-stream Recv loop; the cache application
// (table.Replace) is what this sink ultimately drives.
type CapacitySink func(r CapacityReport) error

// nodeCapacityTable is the per-node live-capacity cache. RWMutex
// guards the map; Replace takes the write lock, Lookup takes
// the read lock. The map is initialised eagerly inside
// NewEngine (not lazily via a setter) so a missed wiring shows
// up at daemon startup rather than as silent fallback at runtime.
type nodeCapacityTable struct {
	mu       sync.RWMutex
	resident map[string]capacityEntry // node_id -> entry
}

// capacityEntry is one node's last-received report + the
// time this daemon received it. lastSeen is stamped on every
// Replace — Lookup uses it to apply the freshness budget.
type capacityEntry struct {
	report   CapacityReport
	lastSeen time.Time
}

// newNodeCapacityTable returns an empty table ready for Replace
// + Lookup. The caller (Engine.NewEngine) wires it under
// e.capacityTable and exposes it via Engine.CapacityTable()
// for the gRPC handler to drive.
func newNodeCapacityTable() *nodeCapacityTable {
	return &nodeCapacityTable{
		resident: make(map[string]capacityEntry),
	}
}

// Replace atomically swaps one node's entry. Empty nodeID is a
// no-op (the publisher is responsible for stamping a real id;
// the handler rejects empty-id reports with codes.InvalidArgument
// before calling this). lastSeen is stamped to time.Now so the
// freshness budget is "since this daemon received the report",
// not "since vmmd sampled it".
//
// nil receiver is tolerated — a pre-axis-5 fixture's nil table
// returns without panic.
func (t *nodeCapacityTable) Replace(r CapacityReport) {
	if t == nil {
		return
	}
	if r.NodeID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resident[r.NodeID] = capacityEntry{report: r, lastSeen: time.Now()}
}

// Lookup returns the live entry for nodeID and a boolean
// reporting freshness. The chooser uses the boolean to decide
// whether to apply the report or fall back to the store. The
// caller passes `now` so tests can inject a fake clock.
//
// Lookup returns (zero, false) when:
//   - t is nil (pre-axis-5 fixture)
//   - the node has no entry (vmmd has not reported yet)
//   - the entry is older than CapacityFreshness (vmmd went
//     silent or fell behind its 1 s cadence)
//
// nil receiver is tolerated.
func (t *nodeCapacityTable) Lookup(nodeID string, now time.Time) (CapacityReport, bool) {
	if t == nil {
		return CapacityReport{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.resident[nodeID]
	if !ok {
		return CapacityReport{}, false
	}
	if now.Sub(e.lastSeen) > CapacityFreshness {
		return CapacityReport{}, false
	}
	return e.report, true
}

// CapacitySink returns a closure the handler can pass as the
// sink for the SchedAPI.ReportCapacity shape. The closure
// applies the report to the table; a non-nil error aborts
// the stream. Today the closure is a pure Replace — no error
// path — so this returns a closure that never errors. Kept as
// a func-returning-closure to match the SchedAPI / WarmHintSink
// shape and to give tests a stable seam to assert on.
//
// nil receiver returns a no-op closure.
func (t *nodeCapacityTable) CapacitySink() CapacitySink {
	if t == nil {
		return func(r CapacityReport) error { return nil }
	}
	return func(r CapacityReport) error {
		t.Replace(r)
		return nil
	}
}
