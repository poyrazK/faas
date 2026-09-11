// pkg/sched/operator_intent_subscriber.go — schedd's
// operator-intent dispatcher (PR #1099 P2 redesign step 2).
//
// Producer: apid's POST /v1/admin/instances/{id}/force-park
// and POST /v1/admin/apps/{slug}/force-cold-boot handlers
// insert a row into operator_intents
// (migrations/00445, status='pending') and emit
// db.NotifyOperatorIntent on the wire.
//
// Consumer: loop.go's Run() multiplexes db.NotifyOperatorIntent
// onto its existing LISTEN (pkg/sched/loop.go:479-498) plus
// a 30s safety ticker. Both wakeup sources call
// drainPendingOperatorIntents, the row-level dispatcher. This
// avoids a dedicated long-term pool connection (the pool is
// tight — see the cron_run_now precedent's long doc comment
// at loop.go:483-484).
//
// Per-row dispatch flow:
//
//   - drainPendingOperatorIntents calls
//     ClaimPendingOperatorIntent which atomically transitions
//     one pending row to `running` via FOR UPDATE SKIP LOCKED
//     LIMIT 1.
//   - Dispatches by kind:
//
//     * force_park → engine.Park(ctx, intent.TargetID)
//       (which uses lockApp + machine.go::CanTransition to
//       enforce the §6.2 invariant; CLAUDE.md ownership is
//       preserved because schedd is still the only writer
//       to instances).
//
//     * force_cold_boot → engine.ForceColdBootNextWake(ctx,
//       intent.TargetID) (snapshot-policy flip; no state-
//       machine write, no lockApp; returns the snap IDs
//       that were marked stale).
//
//   - Stamps the row's terminal state via
//     MarkOperatorIntentSucceeded (with snap IDs) /
//     MarkOperatorIntentFailed (with error).
//   - Emits a terminal audit row
//     (operator.action.<verb>.outcome) so the operator
//     dashboard can join intent_id to outcome.
//
// Why a 30s safety tick (vs cron_run_now's 60s): operator
// recovery primitives are time-sensitive (the on-call engineer
// is paged and staring at a console). 30s bounds post-restart
// latency tighter than cron fire-now, at the cost of one extra
// sweep per minute.

package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// operatorIntentSafetyTick is the recovery cadence for missed-
// notify scenarios. When a NotifyOperatorIntent delivery is
// dropped (Postgres bounce, network blip, schedd restart), the
// pending rows in the table survive — the safety tick re-claims
// them. 30s matches the operator-action SLA: an SRE on the
// incident bridge expects the recovery primitive to take effect
// within a minute, not two.
const operatorIntentSafetyTick = 30 * time.Second

// operatorIntentStuckRunningTimeout is the maximum time a
// `running` row may sit before the safety tick reclaims it back
// to `pending`. Mirrors fire-now's stuck-row pattern; 5 minutes
// gives a long dispatch (snap-walk through ForceColdBootNextWake)
// enough headroom without making a wedged process silently hold
// a row forever.
const operatorIntentStuckRunningTimeout = 5 * time.Minute

// drainPendingOperatorIntents claims + dispatches every
// pending row, one at a time. A single drain call claims all
// currently-pending rows sequentially; a follow-up notify or
// tick will pick up anything that arrives mid-drain. The
// single-row LIMIT is the contention primitive — N schedd
// instances process N rows in parallel without colliding.
//
// The function does not return an error; per-row failures are
// logged and the row is stamped to `failed` so a poison row
// cannot wedge the queue forever. Stuck `running` rows (older
// than operatorIntentStuckRunningTimeout) are reclaimed to
// `pending` so the next claim picks them up; matches fire-now's
// stuck-row safety pattern.
func (l *Loop) drainPendingOperatorIntents(ctx context.Context) {
	// Reclaim stuck-running rows FIRST. If schedd crashed
	// mid-dispatch (between Claim and Mark*), the row is
	// orphaned in `running` and the 30s safety tick is the
	// only thing that frees it. Doing the reclaim before the
	// drain means a freshly-reclaimed row is eligible for
	// claim on this same drain call (no need to wait for the
	// next tick).
	if n, err := l.engine.Store().ReclaimStuckRunningOperatorIntents(ctx, time.Now().Add(-operatorIntentStuckRunningTimeout)); err != nil {
		l.log.Warn("sched: operator_intent: reclaim stuck-running failed",
			"err", err, "threshold", operatorIntentStuckRunningTimeout.String())
	} else if n > 0 {
		l.log.Info("sched: operator_intent: reclaimed stuck-running rows",
			"count", n, "threshold", operatorIntentStuckRunningTimeout.String())
	}

	for {
		intent, err := l.engine.Store().ClaimPendingOperatorIntent(ctx)
		if errors.Is(err, state.ErrOperatorIntentNotFound) {
			// Empty queue — caller exits cleanly. This is the
			// common case at startup before any apid action has
			// fired.
			return
		}
		if err != nil {
			l.log.Warn("sched: operator_intent: claim failed", "err", err)
			return // transient error; the safety tick retries
		}
		l.processOperatorIntent(ctx, intent)
	}
}

// processOperatorIntent is the per-row dispatch. Dispatches
// by kind (force_park → Engine.Park, force_cold_boot →
// Engine.ForceColdBootNextWake), stamps the terminal state,
// and emits the terminal `operator.action.<verb>.outcome`
// audit row.
//
// Errors are mapped to a bounded message; the row is stamped
// `failed` so the operator's GET endpoint surfaces the
// reason. CanTransition failures (e.g. instance already
// PARKED because of a customer-driven park racing the admin
// action) are NOT errors in the operational sense — the
// desired end state was achieved — but we still stamp the
// intent `failed` so the audit trail records that the admin
// click did not actually mutate state.
func (l *Loop) processOperatorIntent(ctx context.Context, intent state.OperatorIntent) {
	var (
		snapIDs []string
		err     error
	)
	switch intent.Kind {
	case state.OperatorIntentKindForcePark:
		// ParkWithReason (rather than the bare Park) stamps a
		// structured log line with the operator's reason so the
		// post-incident review can answer "why did the on-call
		// park this instance" without grepping the audit table.
		err = l.engine.ParkWithReason(ctx, intent.TargetID, intent.Reason)
	case state.OperatorIntentKindForceColdBoot:
		snapIDs, err = l.engine.ForceColdBootNextWake(ctx, intent.TargetID)
	case state.OperatorIntentKindForceRestart:
		// ForceRestart (P2d follow-on to PR #1099) kills the
		// instance (RUNNING → STOPPED) and marks the
		// deployment's latest warm + init snaps stale. The
		// returned snap IDs are stamped on the intent row so
		// the operator's GET endpoint surfaces "what did this
		// action affect". Engine.ForceRestart returns
		// state.ErrInstanceNotRunning on the race-loser posture
		// (customer-driven Park/Destroy won the lockApp
		// re-read) — the row is stamped failed with the error
		// verbatim; the audit trail records that the admin
		// click was an idempotent no-op. See Engine.ForceRestart
		// for the full state-machine contract.
		snapIDs, err = l.engine.ForceRestart(ctx, intent.TargetID, intent.Reason)
	case state.OperatorIntentKindNodeDrain,
		state.OperatorIntentKindNodeForceDrain,
		state.OperatorIntentKindNodeActivate,
		state.OperatorIntentKindNodeRetire:
		// Fleet-level node operations are applied by schedd, next to
		// the recovery runner that owns the resulting migration work.
		// The durable intent is already present before this lifecycle
		// CAS can run, so a successful mutation can never be orphaned
		// from its actor, reason, preflight, and trace metadata.
		err = applyNodeOperatorIntent(ctx, l.engine.Store(), intent)
	default:
		// Should be impossible — the schema CHECK rejects any
		// unknown kind — but if we somehow receive one, stamp
		// the row failed and move on. No audit emit; the row's
		// state is the source of truth.
		const unknownKindMsg = "operator_intent: unknown kind"
		if mErr := l.engine.Store().MarkOperatorIntentFailed(ctx, intent.ID, unknownKindMsg, nil); mErr != nil {
			l.log.Warn("sched: operator_intent: mark failed (unknown kind)",
				"intent_id", intent.ID, "kind", intent.Kind, "err", mErr)
		}
		l.log.Warn("sched: operator_intent: unknown kind", "intent_id", intent.ID, "kind", intent.Kind)
		return
	}

	resultLabel := operatorIntentResultLabelSucceeded
	if err != nil {
		resultLabel = operatorIntentResultLabelFailed
		msg := err.Error()
		// P2d R4 review fix: persist the snap IDs we collected
		// on partial-success (snaps flipped stale but destroy
		// errored). The terminal operator.action.<verb>.outcome
		// audit row already carries them, but the operator_intent
		// row's snap_ids_marked_stale column was previously
		// only populated on the succeeded branch — so an operator
		// querying GET /v1/admin/operator-intents/{id} after a
		// partial-success failure saw status='failed' but no
		// evidence that the snap-stale work had landed. Now both
		// branches persist the same field; the audit row +
		// operator_intent row stay in lock-step.
		if mErr := l.engine.Store().MarkOperatorIntentFailed(ctx, intent.ID, msg, snapIDs); mErr != nil {
			l.log.Warn("sched: operator_intent: mark failed",
				"intent_id", intent.ID, "kind", intent.Kind, "err", mErr)
		}
		l.log.Warn("sched: operator_intent: dispatch failed",
			"intent_id", intent.ID, "kind", intent.Kind, "target_id", intent.TargetID,
			"snap_ids_marked_stale", snapIDs, "err", err)
	} else {
		if mErr := l.engine.Store().MarkOperatorIntentSucceeded(ctx, intent.ID, snapIDs); mErr != nil {
			l.log.Warn("sched: operator_intent: mark succeeded",
				"intent_id", intent.ID, "kind", intent.Kind, "err", mErr)
		}
		l.log.Info("sched: operator_intent: succeeded",
			"intent_id", intent.ID, "kind", intent.Kind, "target_id", intent.TargetID,
			"snap_ids_marked_stale", snapIDs)
	}

	// Emit the terminal operator.action.<verb>.outcome audit
	// row so the operator dashboard can join intent_id to
	// outcome. Distinct kind from the request kind stamped at
	// apid (operator.action.<verb> vs operator.action.<verb>.
	// outcome) so dashboards filter on kind_prefix='operator.
	// action.park_instance' for requests vs
	// kind='operator.action.park_instance.outcome' for
	// outcomes. Best-effort: a nil auditor (test fixture) is
	// tolerated via the nil check.
	if l.audit != nil {
		outcomeKind := "operator.action." + string(intent.Kind) + ".outcome"
		data := map[string]any{
			"actor":                 intent.ActorID,
			"intent_id":             intent.ID,
			"target_id":             intent.TargetID,
			"reason":                intent.Reason,
			"result":                resultLabel,
			"started_at":            intent.StartedAt,
			"finished_at":           time.Now().UTC(),
			"snap_ids_marked_stale": snapIDs,
		}
		if len(intent.Metadata) > 0 {
			var metadata any
			if json.Unmarshal(intent.Metadata, &metadata) == nil {
				data["metadata"] = metadata
			}
		}
		// PR-#TBD / C3 — propagate the OTel W3C trace_id from
		// the inbound HTTP request (apid → row) onto the
		// terminal outcome audit row so the dashboard can join
		// alert ↔ action ↔ outcome on one column. The existing
		// OTel lift at pkg/audit/audit.go:221-232 also writes
		// data["trace_id"] from a span context when present;
		// an explicit value here wins because the audit emit
		// happens AFTER the OTel lift runs.
		if intent.TraceID != nil {
			data["trace_id"] = *intent.TraceID
		}
		if err != nil {
			data["error"] = err.Error()
		}
		l.audit.Emit(ctx, outcomeKind, intent.AccountID, data)
	}
}

// applyNodeOperatorIntent converts one closed-vocabulary fleet intent into a
// compare-and-swap lifecycle transition. Re-reading at dispatch time closes
// the gap between the API preflight and the controller action. Reaching the
// desired state is idempotent success; incompatible controller-owned states
// fail loudly and remain visible on the intent row.
func applyNodeOperatorIntent(ctx context.Context, store state.Store, intent state.OperatorIntent) error {
	node, err := store.NodeGet(ctx, intent.TargetID)
	if err != nil {
		return fmt.Errorf("operator_intent: read compute node: %w", err)
	}
	current := node.Lifecycle
	if current == "" {
		if node.Active {
			current = state.NodeLifecycleActive
		} else {
			current = state.NodeLifecycleUnavailable
		}
	}

	switch intent.Kind {
	case state.OperatorIntentKindNodeDrain:
		if current == state.NodeLifecycleDraining {
			return nil
		}
		if current != state.NodeLifecycleActive {
			return fmt.Errorf("operator_intent: node_drain requires active lifecycle, got %s", current)
		}
		return store.NodeSetLifecycle(ctx, node.ID, current, state.NodeLifecycleDraining)
	case state.OperatorIntentKindNodeForceDrain:
		if current == state.NodeLifecycleMaintenance {
			return nil
		}
		if current == state.NodeLifecycleForceDraining {
			return nil
		}
		return store.NodeSetLifecycle(ctx, node.ID, current, state.NodeLifecycleForceDraining)
	case state.OperatorIntentKindNodeActivate:
		if current == state.NodeLifecycleActive {
			return nil
		}
		if current != state.NodeLifecycleUnavailable && current != state.NodeLifecycleMaintenance {
			return fmt.Errorf("operator_intent: node_activate refuses controller-owned lifecycle %s", current)
		}
		return store.NodeSetLifecycle(ctx, node.ID, current, state.NodeLifecycleActive)
	case state.OperatorIntentKindNodeRetire:
		return applyNodeRetirement(ctx, store, node, current)
	default:
		return fmt.Errorf("operator_intent: unsupported node kind %s", intent.Kind)
	}
}

func applyNodeRetirement(ctx context.Context, store state.Store, node state.ComputeNode, current state.NodeLifecycle) error {
	if current == state.NodeLifecycleRetired {
		return nil
	}
	if current != state.NodeLifecycleMaintenance {
		return fmt.Errorf("operator_intent: node_retire requires maintenance lifecycle, got %s", current)
	}
	instances, err := store.ListInstancesOnNodeID(ctx, node.ID)
	if err != nil {
		return fmt.Errorf("operator_intent: inspect node instances: %w", err)
	}
	for _, instance := range instances {
		if state.IsLive(strings.ToLower(instance.State)) {
			return fmt.Errorf("operator_intent: node_retire requires zero live instances")
		}
	}
	return store.NodeSetLifecycle(ctx, node.ID, current, state.NodeLifecycleRetired)
}

// operatorIntentResultLabel* mirror fireNowResultLabel* —
// closed vocabulary for the result field on the terminal
// audit row. Declared as constants so goconst stops flagging
// the repeated literal across the switch arms in
// processOperatorIntent.
const (
	operatorIntentResultLabelSucceeded = "succeeded"
	operatorIntentResultLabelFailed    = "failed"
)
