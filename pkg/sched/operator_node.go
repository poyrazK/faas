package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) processOperatorNode(ctx context.Context, intent state.OperatorIntent) error {
	store, ok := l.engine.Store().(state.OperatorNodeStore)
	if !ok {
		return fmt.Errorf("store lacks transactional operator node support")
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.OperatorNodePersistenceTimeout)
	defer cancel()
	var result state.OperatorNodeResult
	var err error
	for attempt := range api.OperatorNodePersistenceAttempts {
		result, err = store.ExecuteOperatorNode(persistCtx, intent.ID, intent.ClaimToken)
		if err == nil {
			break
		}
		if errors.Is(err, state.ErrConflict) || persistCtx.Err() != nil {
			return err
		}
		if attempt+1 < api.OperatorNodePersistenceAttempts {
			select {
			case <-time.After(api.OperatorNodePersistenceRetryDelay):
			case <-persistCtx.Done():
				return persistCtx.Err()
			}
		}
	}
	if err != nil {
		return fmt.Errorf("execute operator node intent: %w", err)
	}
	l.log.Info("sched: operator node outcome committed", "intent_id", intent.ID, "status", result.Status,
		"previous_lifecycle", result.PreviousLifecycle, "result_lifecycle", result.Lifecycle, "changed", result.Changed, "replayed", result.Replayed)
	if outbox, ok := l.engine.Store().(state.AuditEventOutboxStore); ok {
		if _, err := outbox.DeliverAuditEvent(persistCtx, result.AuditOutboxID); err != nil {
			l.log.Warn("sched: node outcome audit delivery deferred", "intent_id", intent.ID, "err", err)
		}
	}
	return nil
}
