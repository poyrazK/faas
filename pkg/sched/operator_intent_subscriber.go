// Schedd claims durable operator intents. Each known kind owns its atomic
// completion protocol; no lifecycle mutation uses the legacy unfenced
// setters, except the native database operations (ADR-1027) until they
// adopt the protocol.
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if state.IsOperatorDatabaseKind(intent.Kind) {
		l.processOperatorDatabase(ctx, intent)
		return
	}
	// Schema rejects unknown kinds. Keep unexpected rows visible for investigation
	// instead of completing an unsupported operation through an unfenced setter.
	l.log.Error("sched: unsupported operator intent kind", "intent_id", intent.ID, "kind", intent.Kind)
}

// DatabaseMover starts a native database's move to another database node
// (ADR-1027): it returns once the move is recorded, and the copy finishes
// in the background.
type DatabaseMover interface {
	StartDatabaseMove(ctx context.Context, databaseID, targetNode string) (DatabaseMove, error)
}

// DatabaseMove is a started move.
type DatabaseMove struct {
	From, To string
}

// DatabaseRestarter restarts a running native database in place, onto its
// node's current base (ADR-1027); a parked one is left for its next wake.
// It returns the database's state afterwards.
type DatabaseRestarter interface {
	RestartDatabase(ctx context.Context, databaseID string) (string, error)
}

func (l *Loop) restartDatabase(ctx context.Context, intent state.OperatorIntent) error {
	if l.databaseRestarter == nil {
		return errors.New("database_restart: the database scheduler is not enabled on this schedd")
	}
	after, err := l.databaseRestarter.RestartDatabase(ctx, intent.TargetID)
	if err != nil {
		return fmt.Errorf("database_restart: %w", err)
	}
	l.log.Info("sched: operator_intent: database restarted", "intent_id", intent.ID,
		"database_id", intent.TargetID, "state", after)
	return nil
}

// DatabaseSwitcher hands a production-class native database to its
// synchronized standby on purpose (ADR-1027): a running primary stops
// cleanly and the standby is promoted, or a parked database's record
// moves to the standby's node. It returns the node the database is on
// afterwards.
type DatabaseSwitcher interface {
	SwitchoverDatabase(ctx context.Context, databaseID string) (string, error)
}

func (l *Loop) switchoverDatabase(ctx context.Context, intent state.OperatorIntent) error {
	if l.databaseSwitcher == nil {
		return errors.New("database_switchover: the database scheduler is not enabled on this schedd")
	}
	node, err := l.databaseSwitcher.SwitchoverDatabase(ctx, intent.TargetID)
	if err != nil {
		return fmt.Errorf("database_switchover: %w", err)
	}
	l.log.Info("sched: operator_intent: database switched over", "intent_id", intent.ID,
		"database_id", intent.TargetID, "node_id", node)
	return nil
}

func (l *Loop) startDatabaseMove(ctx context.Context, intent state.OperatorIntent) error {
	if l.databaseMover == nil {
		return errors.New("database_move: the database scheduler is not enabled on this schedd")
	}
	var meta state.DatabaseMoveIntentMetadata
	if len(intent.Metadata) > 0 {
		if err := json.Unmarshal(intent.Metadata, &meta); err != nil {
			return fmt.Errorf("database_move: metadata: %w", err)
		}
	}
	move, err := l.databaseMover.StartDatabaseMove(ctx, intent.TargetID, meta.TargetNode)
	if err != nil {
		return fmt.Errorf("database_move: %w", err)
	}
	l.log.Info("sched: operator_intent: database move started", "intent_id", intent.ID,
		"database_id", intent.TargetID, "from", move.From, "to", move.To)
	return nil
}

// processOperatorDatabase runs a native database operation (ADR-1027)
// and records its outcome. These kinds keep the legacy setters, which
// main's store still accepts for them, until they adopt the claim-fenced
// protocol the other kinds use (ADR-1522..1526).
func (l *Loop) processOperatorDatabase(ctx context.Context, intent state.OperatorIntent) {
	var err error
	switch intent.Kind {
	case state.OperatorIntentKindDatabaseMove:
		// The move parks the database and records it moving before this
		// returns; the copy, which can take hours, finishes in the
		// background, past the intent's claim.
		err = l.startDatabaseMove(ctx, intent)
	case state.OperatorIntentKindDatabaseRestart:
		// A park and a wake: seconds, well inside the intent's claim.
		err = l.restartDatabase(ctx, intent)
	case state.OperatorIntentKindDatabaseSwitchover:
		// A clean stop and a promotion: seconds too.
		err = l.switchoverDatabase(ctx, intent)
	}
	result := "succeeded"
	if err != nil {
		result = "failed"
		if mErr := l.engine.Store().MarkOperatorIntentFailed(ctx, intent.ID, err.Error(), nil); mErr != nil {
			l.log.Warn("sched: operator_intent: mark failed", "intent_id", intent.ID, "kind", intent.Kind, "err", mErr)
		}
		l.log.Warn("sched: operator_intent: database operation failed", "intent_id", intent.ID, "kind", intent.Kind,
			"target_id", intent.TargetID, "err", err)
	} else if mErr := l.engine.Store().MarkOperatorIntentSucceeded(ctx, intent.ID, nil); mErr != nil {
		l.log.Warn("sched: operator_intent: mark succeeded", "intent_id", intent.ID, "kind", intent.Kind, "err", mErr)
	}
	if l.audit == nil {
		return
	}
	data := map[string]any{"actor": intent.ActorID, "intent_id": intent.ID, "target_id": intent.TargetID, "reason": intent.Reason,
		"result": result, "started_at": intent.StartedAt, "finished_at": time.Now().UTC()}
	if len(intent.Metadata) > 0 {
		var metadata any
		if json.Unmarshal(intent.Metadata, &metadata) == nil {
			data["metadata"] = metadata
		}
	}
	if intent.TraceID != nil {
		data["trace_id"] = *intent.TraceID
	}
	if err != nil {
		data["error"] = err.Error()
	}
	l.audit.Emit(ctx, "operator.action."+string(intent.Kind)+".outcome", intent.AccountID, data)
}
