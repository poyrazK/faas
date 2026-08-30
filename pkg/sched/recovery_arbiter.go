// recovery_arbiter.go — Workstream B (issue #1184, ADR-137)
// single decision policy for the per-tick recovery sweep.
//
// Why a single arbiter rather than ad-hoc decisions inside
// live_migrator + deadnode_reconciler: the two paths previously
// could both fire on the same instance (live-migrate enqueue +
// reconcile-fail) and the race had no clean winner. Folding both
// into one Decide() function means the per-tick loop has exactly
// one source of truth for the migrate-vs-recreate question.
//
// Per tick:
//
//   - List nodes whose lifecycle is in {draining, unavailable,
//     recovering} (the NodeListRecoverable surface from
//     pkg/state/store.go).
//   - For each, list live instances on the node
//     (InstanceListByNodeForRecovery).
//   - For each instance, call Decide(node, instance) → LiveMigrate
//     or Recreate or None; dispatch via the LiveMigrator
//     (Task #61 wires live_migrator to be a consumer) and the
//     Engine.RecreateInstance primitive (Task #60).
//
// The arbiter is a pure function over (node, instance) — no
// goroutines, no DB access. The tick loop (cmd/schedd main.go)
// owns the goroutine boundary, matching the Heartbeat and
// DeadNodeReconciler precedent.
package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// Decision is the per-instance verdict the arbiter hands the
// tick loop. Closed enum — a future Recover vs Defer distinction
// would extend this rather than add a parallel flag.
type Decision int

const (
	// DecisionNone — skip this instance. PARKED rows fall
	// here (the arbiter doesn't touch them; the rebalancer
	// owns parked-row pickup).
	DecisionNone Decision = iota
	// DecisionLiveMigrate — enqueue a live-migration. The
	// instance has a usable snapshot and the destination
	// peer (when picked) has headroom; the LiveMigrator
	// primitive owns the rest.
	DecisionLiveMigrate
	// DecisionRecreate — the instance has no usable
	// snapshot. The Engine.RecreateInstance primitive
	// (Task #60) transitions the row to PARKED with
	// kind='recovery_recreate' and emits the recovery
	// event. The recreation re-uses the snapshot at the
	// next wake.
	DecisionRecreate
)

// String is the human-readable form used in the recovery
// dashboard and slog lines. Not part of any contract; for
// wire-level labelling use the kind set on the typed recovery
// events (events.RecoveryEvent.Kind).
func (d Decision) String() string {
	switch d {
	case DecisionLiveMigrate:
		return "live-migrate"
	case DecisionRecreate:
		return "recreate"
	}
	return "none"
}

// MigrationDispatcher is the minimum surface the arbiter needs
// to dispatch a live-migration verdict. The existing
// LiveMigrator (live_migrator.go) implements this once Task #61
// wires the Enqueue method; the interface keeps the test seam
// tight.
type MigrationDispatcher interface {
	Enqueue(ctx context.Context, instanceID string) error
}

// RecreateDispatcher is the minimum surface the arbiter needs
// to dispatch a recreate verdict. Engine.RecreateInstance
// (Task #60) implements this.
type RecreateDispatcher interface {
	RecreateInstance(ctx context.Context, instanceID string) error
}

// Arbiter is the pure-function core. The tick loop holds one
// per schedd; nil is tolerated at construction time so the
// cmd/schedd bootstrap can wire the primitives after the
// engine is built.
type Arbiter struct {
	liveMig  MigrationDispatcher
	recreate RecreateDispatcher
}

// NewArbiter wires the two dispatch targets. Either may be
// nil — the corresponding decision becomes a no-op (the
// arbiter still returns the right verdict; dispatch logs a
// warn and moves on).
func NewArbiter(lm MigrationDispatcher, rp RecreateDispatcher) *Arbiter {
	return &Arbiter{liveMig: lm, recreate: rp}
}

// Decide returns the per-instance verdict. Pure function:
// same inputs → same output, no goroutines, no DB access.
// The caller (Tick) owns the dispatch side-effect.
//
// Decision matrix (ADR-137 §"Decision policy"):
//
//	node.Lifecycle    instance.State   → Decision
//	───────────────────────────────────────────────
//	draining          *                → LiveMigrate
//	                                  (drain is operator-initiated;
//	                                  never recreate)
//	recovering        parked           → None
//	recovering        running|waking   → LiveMigrate
//	                                  (peer is back; rebuild via
//	                                  migration is faster than
//	                                  cold boot)
//	recovering        cold_booting     → LiveMigrate
//	unavailable       parked           → None
//	unavailable       running|waking   → LiveMigrate
//	unavailable       cold_booting     → Recreate
//	                                  (cold-boot rows have no
//	                                  usable snapshot to migrate;
//	                                  cleanest path is recreate)
//	*                 failed|terminal  → None
//	                                  (already terminal; nothing
//	                                  to recover)
//
// A future extension would consult HasSnapshotHistory per
// instance to swap a "running on unavailable with no usable
// snapshot" → Recreate (today: LiveMigrate, with a
// follow-up recreate pass if the migration fails). The
// SnapshotReplication column lands in Task #64's
// snapshot_backoff companion migration.
func (a *Arbiter) Decide(node state.ComputeNode, instance state.RecoveryInstance) Decision {
	switch instance.State {
	case "parked", "failed", "terminated":
		return DecisionNone
	}
	switch node.Lifecycle {
	case state.NodeLifecycleDraining:
		return DecisionLiveMigrate
	case state.NodeLifecycleRecovering:
		return DecisionLiveMigrate
	case state.NodeLifecycleUnavailable:
		switch instance.State {
		case "cold_booting":
			return DecisionRecreate
		}
		return DecisionLiveMigrate
	}
	// active / empty lifecycle / unknown — out of scope.
	return DecisionNone
}

// Tick runs one recovery sweep. The caller (cmd/schedd main.go)
// owns the goroutine + interval; the arbiter owns the
// dispatch policy. Errors are logged via slog (the caller
// passes the logger via the dispatch closures).
//
// Tick returns the count of (live-migrate, recreate, none)
// verdicts so the caller can feed the §12 recovery panel
// without the arbiter itself owning Prometheus primitives.
func (a *Arbiter) Tick(ctx context.Context, nodes []state.ComputeNode, instancesByNode map[string][]state.RecoveryInstance) (liveMig, recreate, skipped int, err error) {
	for _, node := range nodes {
		if node.Lifecycle != state.NodeLifecycleDraining &&
			node.Lifecycle != state.NodeLifecycleUnavailable &&
			node.Lifecycle != state.NodeLifecycleRecovering {
			continue
		}
		instances := instancesByNode[node.ID]
		for _, instance := range instances {
			verdict := a.Decide(node, instance)
			switch verdict {
			case DecisionNone:
				skipped++
			case DecisionLiveMigrate:
				if a.liveMig != nil {
					if e := a.liveMig.Enqueue(ctx, instance.ID); e != nil {
						err = e
						continue
					}
				}
				liveMig++
			case DecisionRecreate:
				if a.recreate != nil {
					if e := a.recreate.RecreateInstance(ctx, instance.ID); e != nil {
						err = e
						continue
					}
				}
				recreate++
			}
		}
	}
	return liveMig, recreate, skipped, err
}
