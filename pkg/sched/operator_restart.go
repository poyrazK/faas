package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) processOperatorRestart(ctx context.Context, claimed state.OperatorIntent) error {
	store, ok := l.engine.Store().(state.OperatorRestartStore)
	if !ok {
		return fmt.Errorf("store lacks durable operator restart support")
	}
	intent, err := l.engine.Store().GetOperatorIntent(ctx, claimed.ID)
	if err != nil {
		return err
	}
	if intent.Status != state.OperatorIntentRunning || intent.ClaimToken == "" || intent.ClaimToken != claimed.ClaimToken {
		return state.ErrOperatorRestartClaimLost
	}
	execution, err := store.GetOperatorRestart(ctx, intent.ID)
	if errors.Is(err, state.ErrNotFound) {
		execution, err = l.prepareOperatorRestart(ctx, store, intent)
		if err != nil {
			// Only known pre-effect input failures become failed. Persistence
			// uncertainty is retried by reclaim against the existing plan.
			var inputErr *restartInputError
			if errors.As(err, &inputErr) {
				return l.completeOperatorRestart(ctx, store, intent, state.OperatorIntentFailed, err.Error())
			}
			return err
		}
	} else if err != nil {
		return err
	}
	if execution.DestroyedAt == nil && execution.Phase == state.RestartPrepared {
		instance, instanceErr := store.MigrationInstanceByID(ctx, execution.Target.InstanceID)
		if instanceErr != nil && !errors.Is(instanceErr, state.ErrNotFound) {
			return instanceErr
		}
		if instanceErr != nil || !execution.Target.MatchesInstance(instance) || instance.State != string(state.StateRunning) {
			return l.completeOperatorRestart(ctx, store, intent, state.OperatorIntentFailed, "Original instance placement or lifecycle changed before teardown.")
		}
		vmm, supported := l.engine.vmm.(operatorRestartVMM)
		if !supported {
			return fmt.Errorf("router lacks durable operator restart support")
		}
		rpcCtx, cancel := context.WithTimeout(ctx, api.OperatorRestartRPCTimeout)
		err = vmm.ExecuteOperatorRestartDestroy(rpcCtx, execution.Target.NodeID, intent.ID, intent.ClaimToken)
		cancel()
	}
	// The durable receipt is authoritative even when the RPC acknowledgement
	// was lost. A successful wire response alone is insufficient evidence.
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.OperatorRestartPersistenceTimeout)
	defer cancel()
	latest, readErr := store.GetOperatorRestart(reconcileCtx, intent.ID)
	if readErr != nil {
		return fmt.Errorf("read restart teardown receipt: %w", readErr)
	}
	if latest.DestroyedAt == nil {
		if latest.Phase == state.RestartPrepared {
			if err != nil {
				return fmt.Errorf("restart not dispatched; durable plan retained: %w", err)
			}
			return fmt.Errorf("restart not dispatched; durable plan retained")
		}
		reason := "VM teardown started without a durable acknowledgement; automatic redispatch stopped."
		if err != nil {
			reason += " " + err.Error()
		}
		return l.completeOperatorRestart(reconcileCtx, store, intent, state.OperatorIntentOutcomeUnknown, reason)
	}
	err = l.completeOperatorRestart(reconcileCtx, store, intent, state.OperatorIntentSucceeded, "")
	if errors.Is(err, state.ErrOperatorRestartTargetChanged) {
		return l.completeOperatorRestart(reconcileCtx, store, intent, state.OperatorIntentOutcomeUnknown,
			"VM teardown was acknowledged but the instance placement or lifecycle changed; operator reconciliation required.")
	}
	return err
}

type restartInputError struct{ err error }

func (e *restartInputError) Error() string { return e.err.Error() }
func (e *restartInputError) Unwrap() error { return e.err }

func (l *Loop) prepareOperatorRestart(ctx context.Context, store state.OperatorRestartStore, intent state.OperatorIntent) (state.OperatorRestartExecution, error) {
	instance, err := l.engine.Store().InstanceByID(ctx, intent.TargetID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return state.OperatorRestartExecution{}, &restartInputError{err}
		}
		return state.OperatorRestartExecution{}, err
	}
	release := l.engine.lockApp(instance.AppID)
	defer release()
	instance, err = l.engine.Store().InstanceByID(ctx, intent.TargetID)
	if err != nil {
		return state.OperatorRestartExecution{}, err
	}
	if instance.State != string(state.StateRunning) {
		return state.OperatorRestartExecution{}, &restartInputError{state.ErrInstanceNotRunning}
	}
	vmm, ok := l.engine.vmm.(operatorRestartVMM)
	if !ok {
		return state.OperatorRestartExecution{}, &restartInputError{fmt.Errorf("router lacks durable operator restart support")}
	}
	nodeID := l.engine.nodeForRoute(instance.NodeID)
	rpcCtx, cancel := context.WithTimeout(ctx, api.OperatorRestartRPCTimeout)
	defer cancel()
	generation, err := vmm.OperatorRestartGeneration(rpcCtx, nodeID, instance.ID)
	if err != nil {
		return state.OperatorRestartExecution{}, &restartInputError{err}
	}
	execution, err := store.PrepareOperatorRestart(ctx, intent.ID, intent.ClaimToken, state.OperatorRestartTarget{
		InstanceID: instance.ID, AppID: instance.AppID, DeploymentID: instance.DeploymentID,
		NodeID: nodeID, WakeID: instance.WakeID, Generation: generation,
	})
	if errors.Is(err, state.ErrOperatorRestartTargetChanged) {
		return execution, &restartInputError{err}
	}
	return execution, err
}

func (l *Loop) completeOperatorRestart(ctx context.Context, store state.OperatorRestartStore, intent state.OperatorIntent, status state.OperatorIntentStatus, reason string) error {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.OperatorRestartPersistenceTimeout)
	defer cancel()
	var original *state.Instance
	if status == state.OperatorIntentSucceeded {
		execution, readErr := store.GetOperatorRestart(persistCtx, intent.ID)
		if readErr != nil {
			return readErr
		}
		release := l.engine.lockApp(execution.Target.AppID)
		defer release()
		instance, instanceErr := store.MigrationInstanceByID(persistCtx, execution.Target.InstanceID)
		if instanceErr == nil && execution.Target.MatchesInstance(instance) {
			original = &instance
		}
	}
	completion := state.OperatorRestartCompletion{IntentID: intent.ID, ClaimToken: intent.ClaimToken, Status: status, Error: reason}
	var result state.OperatorRestartResult
	var err error
	for attempt := range api.OperatorRestartPersistenceAttempts {
		result, err = store.CompleteOperatorRestart(persistCtx, completion)
		if err == nil {
			break
		}
		if errors.Is(err, state.ErrConflict) || persistCtx.Err() != nil {
			return err
		}
		if attempt+1 < api.OperatorRestartPersistenceAttempts {
			select {
			case <-time.After(api.OperatorRestartPersistenceRetryDelay):
			case <-persistCtx.Done():
				return persistCtx.Err()
			}
		}
	}
	if err != nil {
		return fmt.Errorf("persist restart outcome: %w", err)
	}
	if status == state.OperatorIntentSucceeded {
		if original != nil {
			l.engine.ledger.Release(original.ID)
			l.engine.emitInstanceChanged(persistCtx, original.ID, original.AppID, state.StateStopped, original.WakeID)
			l.engine.reconcileAfterInstanceTransition(persistCtx, *original, state.StateStopped)
			if !result.Replayed {
				l.engine.appendInstanceTransitionEvent(persistCtx, *original, state.StateRunning, state.StateStopped, "force_restart", intent.Reason)
			}
		}
	}
	if status == state.OperatorIntentOutcomeUnknown {
		l.log.Warn("sched: operator restart outcome unknown", "intent_id", intent.ID, "reason", reason)
	} else {
		l.log.Info("sched: operator restart outcome committed", "intent_id", intent.ID, "status", status, "replayed", result.Replayed)
	}
	if outbox, supported := l.engine.Store().(state.AuditEventOutboxStore); supported {
		if _, deliveryErr := outbox.DeliverAuditEvent(persistCtx, result.AuditOutboxID); deliveryErr != nil {
			l.log.Warn("sched: restart audit delivery deferred", "intent_id", intent.ID, "err", deliveryErr)
		}
	}
	return nil
}
