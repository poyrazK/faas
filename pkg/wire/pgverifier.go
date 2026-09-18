// PG-backed NodeVerifier for production multi-box deployments (ADR-056).
//
// The verifier holds a snapshot of compute_nodes.name -> compute_nodes.id,
// refreshed on every 'compute_node_changed' pg_notify tick. The
// snapshot is read on every handshake (RLock-guarded, no per-handshake
// DB round-trip).
//
// Drain-loop shape mirrors pkg/sched.NodeKeyRegistry.Run — the same
// subscribeWithReconnect pattern + Run(ctx, ch) shape that every
// daemon already uses. nil-receiver is tolerated (no-op drain; the
// daemon simply doesn't wire the verifier when the multi-box gate is
// closed).
//
// Snapshot refresh contract (defensive): a transient loader failure
// keeps the last-known-good snapshot. A de-sync to "allow nothing"
// would brick the cluster's mTLS legs — a single bad Postgres read
// would force every daemon's mTLS handshake to fail. The fresh
// snapshot only swaps in on success.
//
// PR-5 issue #911 — payload discrimination. The 'compute_node_changed'
// pg_notify channel carries two payload shapes (migrations/00026 +
// migrations/00076): a JSON object `{node_id, active}` for compute_nodes
// writes, and the literal string `compute_node_keys` for compute_node_keys
// writes (table-name piggyback). The verifier reads ONLY compute_nodes,
// so the keys-table payload is filtered out at the receiver (Run inspects
// Payload before calling Refresh). Without this, every keys-table write
// forced an unnecessary Refresh + log line.
//
// PR-5 also adds the heartbeat-only short-circuit: a Refresh whose
// freshly-loaded map equals the prior snapshot is a no-op (no swap, no
// publish-equivalent event). This kills heartbeat-churn noise where
// `active=true → true` writes fire notify but produce an identical
// snapshot.

package wire

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/onebox-faas/faas/pkg/db"
)

// NodeLoader is the production-side seam that returns the registered
// (cn, compute_node.id) tuples. NewPGNodeVerifier calls this on every
// refresh; tests can swap an in-memory loader. The two-field shape is
// minimal: production only needs name (== CN lookup key); ID is
// retained for diagnostic logging in LookupCN errors.
type NodeLoader interface {
	LoadNodes(ctx context.Context) ([]NodeRow, error)
}

// NodeRow is the minimal projection of compute_nodes needed by the
// verifier: the leaf-CN (== per-daemon identity from the leaf cert
// and == compute_nodes.name), the compute_nodes.id (UUID) for
// diagnostics, and the cert fingerprint (sha256:<64hex> of the
// leaf cert) — PR-3 widening. The fingerprint is the join key
// the doctor (PR-4) uses to validate the running TLS leaf matches
// the registered one; PR-3 reads it but does not consume it on
// the handshake path (a missing fingerprint returns "" — the
// doctor surfaces this as a separate `Boot/<n>× CertFingerprintNotRegistered`
// issue, not as a handshake failure).
type NodeRow struct {
	CN              string // matches Subject.CommonName on the registered leaf (== compute_nodes.name)
	ID              string // compute_nodes.id (uuid); for diagnostics only
	CertFingerprint string // compute_nodes.cert_fingerprint (sha256:<64hex>); empty if unset
}

// ComputeNodeKeysPayload is the literal pg_notify payload string
// emitted by the compute_node_keys trigger (migrations/00076:
// `compute_node_keys_notify` writes `TG_TABLE_NAME`). The verifier
// does NOT read compute_node_keys — those writes are owned by
// pkg/sched.NodeKeyRegistry — so the discriminator lets Run skip
// the loader round-trip entirely on keys-only notifies.
const ComputeNodeKeysPayload = "compute_node_keys"

// PGNodeVerifier is the production NodeVerifier. Holds a snapshot
// (cn -> id) map, refreshed on every 'compute_node_changed' notify
// (covering both compute_nodes AND compute_node_keys per migration
// 00075's broader trigger). The snapshot is read on every handshake;
// the read path takes RLock, so concurrent dials don't serialize.
//
// PR-3 widening: the snapshot now also carries the cert fingerprint
// (sha256:<64hex> of the leaf cert) per CN. The handshake path
// (LookupCN) still only consults the CN→ID map; the fingerprint
// is read via CertFingerprintByCN by the doctor (PR-4) and the
// secrets-init path (PR-X). The extra field doesn't widen the
// hot path — the loader already loads one row per CN; the cert
// column is in the same SELECT.
type PGNodeVerifier struct {
	mu     sync.RWMutex
	snap   map[string]string // cn (== name) -> id
	fps    map[string]string // cn (== name) -> cert fingerprint; PR-3 widening
	loader NodeLoader
	log    *slog.Logger
}

// NewPGNodeVerifier constructs an empty verifier bound to loader+log.
// The wiring goroutine calls Refresh once at startup, then
// subscribeWithReconnect(ctx, ..., PGNodeVerifier.Run) drives the
// notify-driven refresh loop.
func NewPGNodeVerifier(loader NodeLoader, log *slog.Logger) *PGNodeVerifier {
	return &PGNodeVerifier{
		snap:   make(map[string]string),
		fps:    make(map[string]string),
		loader: loader,
		log:    log,
	}
}

// LookupCN implements NodeVerifier. RLock-guarded read on the
// snapshot; nil-receiver returns nil (AllowAll) so legacy callers
// that pass a nil verifier don't crash.
//
// The snapshot is keyed by CN (== compute_nodes.name), so the
// mismatch path can't have an associated id — the wrap is the CN
// alone. The id is retained in case a future id-discriminating
// policy wants to surface it; today, LookupCN only consumes the
// boolean hit/miss.
func (v *PGNodeVerifier) LookupCN(cn string) error {
	if v == nil {
		return nil
	}
	v.mu.RLock()
	id, ok := v.snap[cn]
	v.mu.RUnlock()
	if !ok {
		return nodeVerifierWithCN(ErrNodeVerifierCNMismatch, cn)
	}
	_ = id // retained for an id-discriminating policy; today unused
	return nil
}

// NodeIDByCN returns the durable compute_nodes.id associated with an
// authenticated leaf CN. It is the handler-layer half of ADR-052: the TLS
// verifier establishes that the CN is active, then a service compares this
// resolved ID with the resource identity carried by the RPC payload.
//
// A nil receiver, unknown CN, or malformed registry row fails closed with
// ErrNodeVerifierCNMismatch. The read is lock-protected and does not perform
// a database round-trip.
func (v *PGNodeVerifier) NodeIDByCN(cn string) (string, error) {
	if v == nil {
		return "", nodeVerifierWithCN(ErrNodeVerifierCNMismatch, cn)
	}
	v.mu.RLock()
	id, ok := v.snap[cn]
	v.mu.RUnlock()
	if !ok || id == "" {
		return "", nodeVerifierWithCN(ErrNodeVerifierCNMismatch, cn)
	}
	return id, nil
}

// CertFingerprintByCN returns the registered sha256:<64hex> leaf-cert
// fingerprint for the given CN (== compute_nodes.name). Used by the
// doctor (PR-4) to validate the running TLS leaf matches the
// registered one, and by the secrets-init path (PR-X) to detect a
// freshly-rotated cert.
//
// Returns ("", ErrCertFingerprintNotRegistered) when:
//   - the CN isn't in the snapshot at all (compute_nodes row missing
//     or inactive), OR
//   - the CN is registered but the fingerprint column is empty
//     (pre-PR-X box; the secrets init hasn't run yet).
//
// The two cases are conflated deliberately — the doctor's caller is
// either going to ignore the fingerprint entirely (a missing-row CN
// is also a missing-handshake-CN) or it will surface
// `Boot/<n>× CertFingerprintNotRegistered` as the single failure
// mode. Splitting them would force the doctor to do its own
// membership check first.
//
// nil receiver returns ("", ErrCertFingerprintNotRegistered).
func (v *PGNodeVerifier) CertFingerprintByCN(cn string) (string, error) {
	if v == nil {
		return "", ErrCertFingerprintNotRegistered
	}
	v.mu.RLock()
	fp, ok := v.fps[cn]
	v.mu.RUnlock()
	if !ok || fp == "" {
		return "", ErrCertFingerprintNotRegistered
	}
	return fp, nil
}

// Refresh swaps the snapshot for a fresh load. Errors keep the
// last-known-good snapshot (defensive: a transient loader failure
// must not de-sync the verifier to "allow nothing"). Returns the
// new size for diagnostics.
//
// PR-5 short-circuit: when the freshly-loaded CN->ID map is identical
// to the prior snapshot (same CN set, same IDs), Refresh returns
// without swapping and without publishing. This kills heartbeat-only
// churn where `active=true → true` writes fire notify but produce an
// identical snapshot. The size returned is the unchanged size; the
// only side effect is a Debug-level log line.
//
// A nil receiver returns an error so a forgotten wire at startup
// fails loudly (the daemon's NewPGNodeVerifier call must always
// succeed; if Refresh is never called, the snapshot stays empty
// and every handshake fails with ErrNodeVerifierCNMismatch).
func (v *PGNodeVerifier) Refresh(ctx context.Context) (int, error) {
	if v == nil {
		return 0, fmt.Errorf("wire: nil PGNodeVerifier")
	}
	if v.loader == nil {
		return 0, fmt.Errorf("wire: PGNodeVerifier has nil loader")
	}
	rows, err := v.loader.LoadNodes(ctx)
	if err != nil {
		if v.log != nil {
			v.log.Warn("wire: node verifier loader failed; keeping last-known-good",
				"err", err, "size", v.Size())
		}
		return v.Size(), fmt.Errorf("wire: load nodes: %w", err)
	}
	fresh := make(map[string]string, len(rows))
	freshFP := make(map[string]string, len(rows))
	for _, r := range rows {
		if r.CN == "" {
			// Skip malformed rows (defensive: an empty CN would
			// collide with every leaf's empty-CN error path).
			continue
		}
		fresh[r.CN] = r.ID
		freshFP[r.CN] = r.CertFingerprint
	}
	v.mu.Lock()
	prior := v.snap
	if snapshotEqual(prior, fresh) && fingerprintsEqual(v.fps, freshFP) {
		// Heartbeat-only refresh: same CN set, same IDs, same
		// fingerprints. Don't swap (publishing an unchanged
		// snapshot would still trigger downstream log/snapshot
		// churn), but keep the map identity intact so equality
		// semantics stay stable.
		v.mu.Unlock()
		if v.log != nil {
			v.log.Debug("wire: node verifier snapshot unchanged, skipping swap",
				"size", len(fresh))
		}
		return len(fresh), nil
	}
	v.snap = fresh
	v.fps = freshFP
	v.mu.Unlock()
	if v.log != nil {
		v.log.Info("wire: node verifier snapshot refreshed",
			"size", len(fresh), "prior_size", len(prior))
	}
	return len(fresh), nil
}

// Size returns the count of registered CNs (diagnostics + tests).
func (v *PGNodeVerifier) Size() int {
	if v == nil {
		return 0
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.snap)
}

// PublishSnapshot returns a deterministic, CN-sorted copy of the
// current snapshot as []NodeRow. This is the stable-publish-key
// contract for the doctor (PR-4) and release bundle (PR-3): any
// caller that needs to serialize the snapshot (a JSON marshal, a
// log line, a comparison) gets a stable order regardless of Go's
// randomized map iteration.
//
// The read path takes RLock; the returned slice is a fresh
// allocation that does NOT alias the verifier's internal map, so
// the caller can mutate it freely. nil-receiver returns nil.
//
// PR-3 widening: CertFingerprint is included in the published rows.
// Pre-PR-3 boxes that don't have a registered fingerprint will
// publish rows with an empty CertFingerprint — callers must tolerate
// this (the doctor surfaces it as a separate failure mode).
func (v *PGNodeVerifier) PublishSnapshot() []NodeRow {
	if v == nil {
		return nil
	}
	v.mu.RLock()
	out := make([]NodeRow, 0, len(v.snap))
	for cn, id := range v.snap {
		out = append(out, NodeRow{CN: cn, ID: id, CertFingerprint: v.fps[cn]})
	}
	v.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CN < out[j].CN })
	return out
}

// snapshotEqual reports whether two cn->id maps are identical
// (same CN set, same IDs per CN). Order doesn't matter — both
// sides are maps. Length mismatch short-circuits to false.
func snapshotEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for cn, id := range a {
		if bID, ok := b[cn]; !ok || bID != id {
			return false
		}
	}
	return true
}

// fingerprintsEqual reports whether two cn->fingerprint maps are
// identical (same CN set, same fingerprints per CN). Empty maps
// are equal. PR-3 helper for the heartbeat-only short-circuit —
// fingerprint changes (a cert rotation by secrets init) must still
// trigger a snapshot swap.
func fingerprintsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for cn, fp := range a {
		if bFP, ok := b[cn]; !ok || bFP != fp {
			return false
		}
	}
	return true
}

// Run drains an already-opened 'compute_node_changed' channel (or any
// other notification source) and refreshes the snapshot on every
// delivery. The loop survives transient loader failures because Refresh
// keeps the last-known-good snapshot. nil receiver is tolerated
// (no-op drain on ctx; identical shape to pkg/sched.NodeKeyRegistry.Run).
//
// PR-5 payload discrimination (issue #911): the channel carries two
// payload shapes — JSON {node_id,active} for compute_nodes writes
// (migrations/00026) and the literal "compute_node_keys" for keys
// writes (migrations/00076). The verifier reads only compute_nodes,
// so Run skips the loader round-trip on keys-table notifies. Any
// other payload (JSON or unknown literal) triggers Refresh.
//
// Panic-recovery: none. The reference drain loops (see
// pkg/sched.NodeKeyRegistry.Run and cmd/vmmd/egress_watcher.go::Run)
// don't recover either — a panic in the loader propagates and the
// daemon dies loudly, which is the right posture for a verifier.
// Adding recover() to swallow-and-retry would hide a sustained
// loader bug and leave the daemon with a stale snapshot under
// silently-misbehaving code.
//
// Return-value contract:
//   - ctx.Done() returns ctx.Err() (caller treats as benign shutdown).
//   - channel close (notify<ok==false) returns nil. db.Subscribe uses
//     channel-close as a reconnect signal, so the production
//     cmd/schedd + cmd/vmmd drain loop (see subscribeWithReconnect)
//     treats nil as "open a fresh Subscribe and dial again on transient
//     errors". A non-nil error here would force the loop to log a
//     spurious warning on every reconnect tick.
//   - loader-failure Refresh results are swallowed (Refresh already
//     logs at Warn and the snapshot stays on last-known-good).
//   - keys-only payloads (Payload == ComputeNodeKeysPayload) are
//     filtered out — no Refresh call, no log line.
func (v *PGNodeVerifier) Run(ctx context.Context, ch <-chan db.Notification) error {
	if v == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-ch:
			if !ok {
				// Channel close is a reconnect signal from
				// db.Subscribe; production drain loops interpret
				// nil as "open a fresh Subscribe". See the
				// package doc above for the full contract.
				return nil
			}
			if n.Payload == ComputeNodeKeysPayload {
				// The verifier doesn't read compute_node_keys —
				// pkg/sched.NodeKeyRegistry.Run handles those.
				// Skip the loader round-trip entirely.
				continue
			}
			if _, err := v.Refresh(ctx); err != nil {
				// Refresh already logs at Warn; the loop survives
				// on last-known-good.
				_ = err
			}
		}
	}
}

// Ensure PGNodeVerifier satisfies NodeVerifier at compile time.
var _ NodeVerifier = (*PGNodeVerifier)(nil)
