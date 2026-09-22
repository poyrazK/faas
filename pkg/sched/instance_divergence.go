package sched

// instance_divergence.go — the reconciler for rows that claim a VM the
// owning vmmd does not have (ADR-191, decision 2).
//
// `instances` rows and running Firecracker processes drift apart. vmmd
// owns one direction: fcvm.ReapOrphanedJails tears down VMs with no live
// row on vmmd boot (measured on a production node 2026-09-04: 23 such
// VMs, oldest 3.7 days, 5.3 GB). The other direction had no owner. A row
// that says RUNNING while the VM is gone keeps billing the customer,
// keeps its admission slot reserved, and keeps receiving routed
// requests until something else happens to notice.
//
// ReconcileDeadNodeInstances covers only the case where the whole node's
// heartbeat went stale. A single VM dying under a healthy node — OOM
// kill outside the liveness path, an operator's kill -9, a failed
// destroy that left the row behind — is invisible to it.
//
// # The signal already exists
//
// vmmd streams per-instance capacity telemetry to schedd continuously.
// It lands in NodeTelemetryCache and is projected by the instance-stats
// poller into the reader behind InstanceActivityReader, which already
// drops stale samples (reader.go: "Stale samples are absent, not zero").
// A key present in SnapshotActivity is therefore "the owning vmmd
// reported this VM recently". No new RPC, no proto change, no second
// Stats call per node: the reconciler consumes what is already flowing.
//
// # Why this cannot fight the dead-node reconciler
//
// This sweep acts only on instances whose node reported *something* on
// the same snapshot. A node that reported nothing at all is skipped
// entirely — that is the dead-node reconciler's territory, and it fires
// on a different input (compute_nodes.last_heartbeat_at older than
// DeadNodeReconcilerStalenessSeconds). The two are mutually exclusive by
// construction, so they cannot both act on one row.
//
// Known gap, accepted for this slice: a node whose *only* instance dies
// reports nothing, is indistinguishable from a silent node, and is
// skipped. It stays live until the node's own heartbeat fails or the
// next wake reconciles it. Closing that needs a vmmd-side "I am up and
// I have zero VMs" assertion, which is a separate change.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// DivergenceEnforceEnv gates whether the reconciler writes. Unset or any
// value other than "1" keeps it report-only: it counts and logs what it
// would have done and touches no row. ADR-191 ships report-only so the
// divergence counter can be read against production reality before a bug
// here is allowed to park healthy apps fleet-wide.
const DivergenceEnforceEnv = "FAAS_SCHEDD_RECONCILE_ENFORCE"

// DivergenceEnforceEnabled reports whether enforcement is on.
func DivergenceEnforceEnabled() bool { return os.Getenv(DivergenceEnforceEnv) == "1" }

// InstanceDivergenceReconciler finds live rows the owning vmmd is not
// reporting. Construct with NewInstanceDivergenceReconciler; the zero
// value is not usable.
//
// Shape mirrors Watchdog (store + engine + injected clock) because it is
// the same kind of sweep: a periodic pass that repairs rows the happy
// path left behind.
type InstanceDivergenceReconciler struct {
	engine   *Engine
	reported InstanceActivityReader
	log      DeadNodeReconcilerLogger
	now      func() time.Time

	// candidates is the confirm-twice gate: an instance must be absent
	// from two consecutive snapshots before it counts. One dropped
	// telemetry batch must never be enough to park a live app.
	mu         sync.Mutex
	candidates map[string]time.Time
}

// NewInstanceDivergenceReconciler wires the sweep. A nil reader makes
// every tick a no-op, which is the correct behaviour for a schedd whose
// instance-stats poller is not wired: without the reported set there is
// no evidence of divergence, and absence of evidence must not act.
func NewInstanceDivergenceReconciler(engine *Engine, reported InstanceActivityReader, log DeadNodeReconcilerLogger) *InstanceDivergenceReconciler {
	return &InstanceDivergenceReconciler{
		engine:     engine,
		reported:   reported,
		log:        log,
		now:        time.Now,
		candidates: make(map[string]time.Time),
	}
}

// WithClock replaces the tick clock (tests). Mirrors Watchdog.WithClock.
func (r *InstanceDivergenceReconciler) WithClock(now func() time.Time) *InstanceDivergenceReconciler {
	r.now = now
	return r
}

// Reconcile runs one sweep and returns how many divergent instances it
// counted (in report-only mode) or repaired (under enforcement).
//
// The error return is reserved for a failure to read the input set. A
// per-row failure is logged and counted so one wedged row never stalls
// the sweep, matching ReconcileDeadNodeInstances.
func (r *InstanceDivergenceReconciler) Reconcile(ctx context.Context) (int, error) {
	if r == nil || r.engine == nil || r.reported == nil {
		return 0, nil
	}
	now := r.now().UTC()

	reported := r.reported.SnapshotActivity(now)
	if len(reported) == 0 {
		// No node reported anything. Either the poller has not ticked
		// yet or the whole fleet is silent; neither is evidence about
		// any individual VM. Clear the confirm-twice state so a
		// telemetry outage cannot accumulate false candidates that
		// all fire the moment it returns.
		r.resetCandidates()
		return 0, nil
	}

	rows, err := r.liveRows(ctx)
	if err != nil {
		return 0, fmt.Errorf("sched: reconcile instance divergence: list: %w", err)
	}

	// A node is only judgeable when it reported at least one of its own
	// instances on this snapshot. See the package comment for why.
	reporting := make(map[string]bool, len(rows))
	for _, ins := range rows {
		if ins.NodeID != "" && !reporting[ins.NodeID] {
			if _, ok := reported[ins.ID]; ok {
				reporting[ins.NodeID] = true
			}
		}
	}

	grace := time.Duration(api.InstanceDivergenceGraceSeconds) * time.Second
	seen := make(map[string]struct{}, len(rows))
	var confirmed []state.Instance

	for _, ins := range rows {
		if _, ok := reported[ins.ID]; ok {
			continue // vmmd has it
		}
		if ins.NodeID == "" || !reporting[ins.NodeID] {
			continue // silent node: dead-node reconciler's job
		}
		// A just-admitted VM may not be in the last telemetry batch.
		if !ins.StartedAt.IsZero() && now.Sub(ins.StartedAt) < grace {
			continue
		}
		seen[ins.ID] = struct{}{}
		if r.confirm(ins.ID, now) {
			confirmed = append(confirmed, ins)
		}
	}
	r.retainCandidates(seen)

	if len(confirmed) == 0 {
		return 0, nil
	}
	if limit := api.InstanceDivergenceTickLimit; len(confirmed) > limit {
		r.logf("warn", "sched: instance divergence: batch at cap", "cap", limit, "confirmed", len(confirmed))
		confirmed = confirmed[:limit]
	}

	enforce := DivergenceEnforceEnabled()
	acted := 0
	for _, ins := range confirmed {
		if !enforce {
			r.observe("suppressed")
			r.logf("warn", "sched: instance divergence detected (report-only; set "+DivergenceEnforceEnv+"=1 to repair)",
				"instance_id", ins.ID, "app_id", ins.AppID, "node_id", ins.NodeID, "state", ins.State, "ram_mb", ins.RAMMB)
			acted++
			continue
		}
		if r.repair(ctx, ins) {
			acted++
		}
	}
	return acted, nil
}

// repair transitions one confirmed row and releases its admission slot.
// Returns whether this sweep is the one that moved it.
func (r *InstanceDivergenceReconciler) repair(ctx context.Context, ins state.Instance) bool {
	// FAILED, not PARKED, for the reason ReconcileDeadNodeInstances
	// records: no snapshot was taken because the VM is gone, and
	// claiming PARKED asserts a snapshot that does not exist. FAILED is
	// cold-bootable (ADR-005: snapshots are cache, not truth), so the
	// customer's next request still serves.
	//
	// The conditional UPDATE is the race gate: a peer that already
	// parked, evicted or migrated the row wins and we do not
	// second-guess the state machine.
	err := r.engine.Store().UpdateInstanceStateIf(ctx, ins.ID, ins.State, string(state.StateFailed))
	switch {
	case err == nil:
		r.engine.Ledger().Release(ins.ID)
		r.observe("failed")
		r.logf("warn", "sched: instance divergence repaired; vmmd does not have this VM",
			"instance_id", ins.ID, "app_id", ins.AppID, "node_id", ins.NodeID, "ram_mb", ins.RAMMB)
		r.emit(ctx, ins)
		return true
	case errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrNotFound):
		// Peer wins. Release is idempotent and a no-op on an unknown
		// instance, so this also closes the billing side of the race
		// where the peer moved the row without freeing the slot.
		r.engine.Ledger().Release(ins.ID)
		r.observe("conflict")
		return false
	default:
		r.observe("error")
		r.logf("warn", "sched: instance divergence: transition failed",
			"instance_id", ins.ID, "node_id", ins.NodeID, "err", err)
		return false
	}
}

// liveRows returns this schedd's live instances. Empty owner keeps the
// single-box posture of listing everything, matching runReaper.
func (r *InstanceDivergenceReconciler) liveRows(ctx context.Context) ([]state.Instance, error) {
	store := r.engine.Store()
	var rows []state.Instance
	var err error
	if owner := r.engine.OwnerNodeID(); owner != "" {
		rows, err = store.ListInstancesByNodeID(ctx, owner)
	} else {
		rows, err = store.ListAllInstances(ctx)
	}
	if err != nil {
		return nil, err
	}
	live := rows[:0]
	for _, ins := range rows {
		if state.IsLive(ins.State) {
			live = append(live, ins)
		}
	}
	return live, nil
}

// confirm reports whether instanceID has now been absent on two
// consecutive sweeps.
func (r *InstanceDivergenceReconciler) confirm(instanceID string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.candidates[instanceID]; ok {
		return true
	}
	r.candidates[instanceID] = now
	return false
}

// retainCandidates drops any candidate that is no longer divergent, so
// an instance that reappears has to fail twice again before it counts.
func (r *InstanceDivergenceReconciler) retainCandidates(seen map[string]struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range r.candidates {
		if _, ok := seen[id]; !ok {
			delete(r.candidates, id)
		}
	}
}

func (r *InstanceDivergenceReconciler) resetCandidates() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.candidates)
}

// candidateCount exposes the confirm-twice state to tests.
func (r *InstanceDivergenceReconciler) candidateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.candidates)
}

func (r *InstanceDivergenceReconciler) observe(outcome string) {
	if r.engine == nil || r.engine.ops == nil {
		return
	}
	r.engine.ops.InstanceDivergence(outcome).Inc()
}

func (r *InstanceDivergenceReconciler) emit(ctx context.Context, ins state.Instance) {
	if r.engine == nil || r.engine.events == nil {
		return
	}
	r.engine.events.EmitRecovery(ctx, events.InstanceFailedEvent{
		EmitAt:     time.Now().UTC(),
		InstanceID: ins.ID,
		AppID:      ins.AppID,
		NodeID:     ins.NodeID,
		Reason:     "vmm_divergence",
	})
}

func (r *InstanceDivergenceReconciler) logf(level, msg string, args ...any) {
	if r.log == nil {
		return
	}
	if level == "warn" {
		r.log.Warn(msg, args...)
		return
	}
	r.log.Info(msg, args...)
}
