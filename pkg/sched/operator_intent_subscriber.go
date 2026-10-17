// Schedd claims durable operator intents. Each known kind owns its atomic
// completion protocol; no lifecycle mutation uses the legacy unfenced setters.
package sched

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const operatorIntentSafetyTick = 30 * time.Second
const operatorIntentStuckRunningTimeout = 5 * time.Minute

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

func (l *Loop) processOperatorIntent(ctx context.Context, intent state.OperatorIntent) {
	if intent.Kind == state.OperatorIntentKindForcePark {
		if err := l.processOperatorPark(ctx, intent); err != nil {
			l.log.Warn("sched: operator park requires reconciliation", "intent_id", intent.ID, "err", err)
		}
		return
	}
	if intent.Kind == state.OperatorIntentKindForceColdBoot {
		if err := l.processOperatorColdBoot(ctx, intent); err != nil {
			l.log.Warn("sched: operator cold boot awaits retry", "intent_id", intent.ID, "err", err)
		}
		return
	}
	if intent.Kind == state.OperatorIntentKindForceRestart {
		if err := l.processOperatorRestart(ctx, intent); err != nil {
			l.log.Warn("sched: operator restart requires reconciliation", "intent_id", intent.ID, "err", err)
		}
		return
	}
	if state.IsOperatorNodeKind(intent.Kind) {
		if err := l.processOperatorNode(ctx, intent); err != nil {
			l.log.Warn("sched: operator node intent awaits reconciliation", "intent_id", intent.ID, "err", err)
		}
		return
	}
	// Schema rejects unknown kinds. Keep unexpected rows visible for investigation
	// instead of completing an unsupported operation through an unfenced setter.
	l.log.Error("sched: unsupported operator intent kind", "intent_id", intent.ID, "kind", intent.Kind)
}
