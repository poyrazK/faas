package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) processOperatorColdBoot(ctx context.Context, intent state.OperatorIntent) error {
	store, ok := l.engine.Store().(state.OperatorColdBootStore)
	if !ok {
		return fmt.Errorf("store lacks transactional operator cold-boot support")
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.OperatorColdBootPersistenceTimeout)
	defer cancel()
	var result state.OperatorColdBootResult
	var err error
	for attempt := range api.OperatorColdBootPersistenceAttempts {
		result, err = store.ExecuteOperatorColdBoot(persistCtx, intent.ID, intent.ClaimToken)
		if err == nil {
			break
		}
		if errors.Is(err, state.ErrConflict) || persistCtx.Err() != nil {
			return err
		}
		if attempt+1 < api.OperatorColdBootPersistenceAttempts {
			select {
			case <-time.After(api.OperatorColdBootPersistenceRetryDelay):
			case <-persistCtx.Done():
				return persistCtx.Err()
			}
		}
	}
	if err != nil {
		return fmt.Errorf("execute operator cold boot: %w", err)
	}
	l.log.Info("sched: operator cold-boot outcome committed", "intent_id", intent.ID,
		"status", result.Status, "generation", result.Generation, "replayed", result.Replayed)
	if outbox, ok := l.engine.Store().(state.AuditEventOutboxStore); ok {
		if _, err := outbox.DeliverAuditEvent(persistCtx, result.AuditOutboxID); err != nil {
			l.log.Warn("sched: cold-boot audit delivery deferred", "intent_id", intent.ID, "err", err)
		}
	}
	return nil
}

func (e *Engine) bindSnapshotRecovery(ctx context.Context, instance state.Instance, snapshotID string) error {
	if store, ok := e.store.(state.SnapshotRecoveryStore); ok {
		if err := store.BindInstanceSnapshotRecovery(ctx, instance.ID, instance.WakeID, snapshotID); err != nil {
			return fmt.Errorf("sched: bind snapshot recovery admission: %w", err)
		}
	}
	return nil
}

func (e *Engine) snapshotRecoveryFresh(ctx context.Context, instanceID string) (bool, error) {
	if store, ok := e.store.(state.SnapshotRecoveryStore); ok {
		return store.InstanceSnapshotRecoveryFresh(ctx, instanceID)
	}
	return true, nil
}
