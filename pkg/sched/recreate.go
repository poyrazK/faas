// recreate.go — Engine.RecreateInstance, the arbiter's recreate
// primitive (Workstream B / issue #1184 / ADR-137).
//
// Why a dedicated primitive rather than calling Park: Park is a
// snapshot-then-stop flow (RUNNING → SNAPSHOTTING → PARKED). The
// arbiter's recreate verdict fires when the source node has no
// usable snapshot to migrate; there is nothing to snapshot, and
// the source VM is dead. RecreateInstance is the dead-VM-shaped
// twin: RUNNING/COLD_BOOTING/WAKING → PARKED, ledger released,
// kind='recovery_recreate' so the §6.1 audit row reads cleanly.
//
// The transition itself is the same `state.StateParked` row write
// Park would land on. The differentiation is purely in the audit
// trail and the events row:
//
//   - transitionWithKind emits an events row with kind="recovery_recreate"
//     instead of Park's "state_transition" default. Dashboard
//     queries for "why was this row parked?" can answer via the
//     kind without joining the state_transition rows.
//
//   - e.events.EmitRecovery(InstanceRecreatedEvent) emits the
//     typed recovery envelope (TopicRecovery) so the recovery
//     timeline (separate from the wake timeline) records the
//     recreate alongside any sibling InstanceMigrated events.
//
// Race contract: the arbiter is the only caller. Per-instance
// race-safety comes from UpdateInstanceState's CAS at the store
// layer (state = $from AND id = $1). If a peer already parked
// or migrated the row, the UPDATE lands 0 rows, we count it as
// a peer-wins no-op, and the dedup with the deadnode_reconciler
// stays clean (Task #62 source-ledger backstop closes the
// billing side of the same race).
//
// The method is synchronous on the store side: ledger release is
// idempotent and a no-op on unknown instances, so a re-entry
// after the row has already been parked by a peer is harmless.
package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// recreateReason is the closed-set reason label the arbiter
// passes to the audit row + InstanceRecreatedEvent payload. The
// string is the same one the dashboard query for "last recreate
// reason" filters on; a closed set keeps the regex simple. The
// no_peer / no_snapshot / lease_expired labels are reserved for
// future discriminators (Task #62 source-ledger backstop may
// stamp them) — only arbiter_recreate is in production today.
const (
	recreateReasonArbiter   = "arbiter_recreate"
	recreateReasonNoSnap    = "no_snapshot"
	recreateReasonNoPeer    = "no_peer_admitting"
	recreateReasonLeaseLost = "lease_expired"
)

// RecreateInstance is the per-instance recreate primitive. The
// arbiter (Task #59) is the only caller; per-tick dispatch lives
// in cmd/schedd/main.go on a 1s ticker.
//
// The transition is the dead-VM-shaped PARKED landing:
//
//   - Loads the instance (ErrNotFound = peer already removed it;
//     benign — counted as peer-wins).
//   - Validates the row is in a state worth recreating from
//     (RUNNING / COLD_BOOTING / WAKING). Anything else returns
//     nil with a debug log so the tick loop isn't noisy on a
//     healthy fleet.
//   - Releases the ledger (idempotent) — Task #62 source-ledger
//     backstop makes this the same call path as the deadnode
//     reconcile path.
//   - Transitions the row to PARKED with kind="recovery_recreate"
//     and emits the InstanceRecreatedEvent envelope.
//
// Returns nil on the benign "already gone" case so the caller
// (arbiter) keeps counting the dispatch as a no-op rather than
// an error.
//
// Nil-safety: the primitive tolerates a nil ledger / events /
// ops so unit tests can wire the bare fields the primitive
// touches. Production wiring (cmd/schedd/main.go) always
// constructs a non-nil ledger; the nil guards are the same
// bootstrap-window pattern recovery_arbiter_test.go uses.
func (e *Engine) RecreateInstance(ctx context.Context, instanceID string) error {
	if e == nil {
		return nil
	}
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			if e.ops != nil {
				e.ops.RecreateDecisions("not_found").Inc()
			}
			e.log.Debug("sched: recreate: instance already gone", "instance_id", instanceID)
			return nil
		}
		return fmt.Errorf("sched: recreate: load %s: %w", instanceID, err)
	}
	// Validate the row is in a state the arbiter's recreate
	// verdict covers. PARKED rows skip — the rebalancer owns
	// parked-row pickup. STOPPED / FAILED rows skip — they're
	// terminal; recreating them would re-allocate a cold-boot
	// from a row that already served its purpose.
	switch state.State(ins.State) {
	case state.StateRunning, state.StateColdBooting, state.StateWaking:
		// in scope — fall through to the transition.
	default:
		if e.ops != nil {
			e.ops.RecreateDecisions("skipped").Inc()
		}
		e.log.Debug("sched: recreate: row out of scope",
			"instance_id", ins.ID, "state", ins.State)
		return nil
	}
	// Release the admission reservation so a replacement
	// instance can be admitted immediately. Release is idempotent
	// and a no-op on unknown instances (admission.go), so this
	// is safe even when the reservation was already freed by
	// another path (the Task #62 source-ledger backstop makes
	// this the same call path the deadnode reconciler uses).
	// Guard the nil-ledger bootstrap window explicitly so a
	// unit-test fixture without a wired ledger doesn't panic.
	if e.ledger != nil {
		e.ledger.Release(ins.ID)
	}
	// Transition with kind="recovery_recreate" so the audit row
	// distinguishes the arbiter's recreate landing from a normal
	// idle-timeout Park (which uses kind="state_transition").
	e.transitionWithKind(ctx, ins.ID, ins.AppID, state.StateParked, "recovery_recreate", recreateReasonArbiter)
	if e.ops != nil {
		e.ops.RecreateDecisions("succeeded").Inc()
	}
	// Recovery envelope — typed payload so the dashboard's
	// TopicRecovery subscriber renders one timeline row per
	// recreate. The arbiter-side sweeper in cmd/schedd uses the
	// same `events.NewPlatform` instance the wake path uses;
	// e.events is nil-safe (no-op when cmd/schedd has not wired
	// the broadcaster yet).
	if e.events != nil {
		e.events.EmitRecovery(ctx, events.InstanceRecreatedEvent{
			EmitAt:       time.Now().UTC(),
			InstanceID:   ins.ID,
			AppID:        ins.AppID,
			DeploymentID: ins.DeploymentID,
			NodeID:       ins.NodeID,
			Reason:       recreateReasonArbiter,
		})
	}
	e.log.Info("sched: recreate: instance transitioned to parked",
		"instance_id", ins.ID, "app_id", ins.AppID, "node_id", ins.NodeID,
		"previous_state", ins.State, "reason", recreateReasonArbiter)
	return nil
}
