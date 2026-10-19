package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Only the database operation is retried. It replays the immutable cycle receipt
// after a lost commit response, even if a newer maintenance cycle has started.
func (r *RecoveryRunner) completeNodeLifecycle(ctx context.Context, in state.NodeLifecycleCompletion) error {
	store, ok := r.store.(state.NodeLifecycleCompletionStore)
	if !ok {
		return fmt.Errorf("store lacks generation-fenced node completion support")
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.NodeLifecycleCompletionTimeout)
	defer cancel()
	var result state.NodeLifecycleCompletionResult
	var err error
	for attempt := range api.NodeLifecycleCompletionAttempts {
		result, err = store.CompleteNodeLifecycle(persistCtx, in)
		if err == nil {
			break
		}
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrInvalidArgument) || persistCtx.Err() != nil {
			return err
		}
		if attempt+1 < api.NodeLifecycleCompletionAttempts {
			select {
			case <-time.After(api.NodeLifecycleCompletionRetryDelay):
			case <-persistCtx.Done():
				return persistCtx.Err()
			}
		}
	}
	if err != nil {
		return fmt.Errorf("persist node lifecycle completion: %w", err)
	}
	if outbox, ok := r.store.(state.AuditEventOutboxStore); ok {
		delivered, err := outbox.DeliverAuditEvent(persistCtx, result.AuditOutboxID)
		if err != nil {
			r.log.Warn("sched: node completion audit delivery deferred", "node_id", in.NodeID, "generation", in.Generation, "err", err)
		} else if delivered && !result.Replayed && r.events != nil {
			r.events.PublishCommittedRecovery(persistCtx, result)
		}
	}
	return nil
}
