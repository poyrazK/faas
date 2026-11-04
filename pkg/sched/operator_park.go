package sched

// adr: 1525. Nil from an idle park is not evidence of an operator outcome.

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) processOperatorPark(ctx context.Context, claimed state.OperatorIntent) error {
	store, ok := l.engine.Store().(state.OperatorParkStore)
	if !ok {
		return fmt.Errorf("store lacks durable operator park support")
	}
	intent, err := l.engine.Store().GetOperatorIntent(ctx, claimed.ID)
	if err != nil {
		return err
	}
	if intent.Status != state.OperatorIntentRunning || intent.ClaimToken == "" || intent.ClaimToken != claimed.ClaimToken {
		return state.ErrOperatorParkClaimLost
	}
	execution, err := store.GetOperatorPark(ctx, intent.ID)
	if errors.Is(err, state.ErrNotFound) {
		execution, err = l.prepareOperatorPark(ctx, store, intent)
		if errors.Is(err, state.ErrOperatorParkCancelled) {
			return l.completeOperatorPark(ctx, store, intent, state.OperatorIntentCancelled, "Fresh traffic or active tail work cancelled parking before VM execution.", false)
		}
		if err != nil {
			var inputErr *restartInputError
			if errors.As(err, &inputErr) {
				return l.completeOperatorPark(ctx, store, intent, state.OperatorIntentFailed, err.Error(), false)
			}
			return err
		}
	} else if err != nil {
		return err
	}
	if execution.Phase == state.ParkPrepared {
		instance, readErr := store.MigrationInstanceByID(ctx, execution.Target.InstanceID)
		if readErr != nil && !errors.Is(readErr, state.ErrNotFound) {
			return readErr
		}
		if readErr != nil || !execution.Target.MatchesInstance(instance) || state.State(instance.State) != execution.Target.PreparedState() {
			return l.completeOperatorPark(ctx, store, intent, state.OperatorIntentOutcomeUnknown, "Original instance placement or lifecycle changed after preparation; no VM redispatch authorized.", false)
		}
		vmm, ok := l.engine.vmm.(operatorParkVMM)
		if !ok {
			return fmt.Errorf("router lacks durable operator park support")
		}
		budget := api.OperatorParkRPCTimeout
		if grace := time.Duration(execution.Target.GraceSeconds)*time.Second + api.OperatorParkCleanupTimeout; grace > budget {
			budget = grace
		}
		rpcCtx, cancel := context.WithTimeout(ctx, budget)
		err = vmm.ExecuteOperatorPark(rpcCtx, execution.Target.NodeID, intent.ID, intent.ClaimToken)
		cancel()
	}
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.OperatorRestartPersistenceTimeout)
	defer cancel()
	latest, readErr := store.GetOperatorPark(reconcileCtx, intent.ID)
	if readErr != nil {
		return fmt.Errorf("read park receipt: %w", readErr)
	}
	if latest.Receipt == nil {
		if latest.Phase == state.ParkPrepared {
			if err != nil {
				return fmt.Errorf("park not dispatched; durable plan retained: %w", err)
			}
			return fmt.Errorf("park not dispatched; durable plan retained")
		}
		return l.completeOperatorPark(reconcileCtx, store, intent, state.OperatorIntentOutcomeUnknown, "VM operation started without a durable cleanup receipt; automatic redispatch stopped.", false)
	}
	discard, err := l.publishOperatorParkCapture(reconcileCtx, latest)
	if err != nil {
		return err
	}
	err = l.completeOperatorPark(reconcileCtx, store, intent, state.OperatorIntentSucceeded, "", discard)
	if errors.Is(err, state.ErrOperatorParkTargetChanged) {
		return l.completeOperatorPark(reconcileCtx, store, intent, state.OperatorIntentOutcomeUnknown, "VM cleanup was acknowledged but original placement or lifecycle changed; operator reconciliation required.", false)
	}
	return err
}

func (l *Loop) prepareOperatorPark(ctx context.Context, store state.OperatorParkStore, intent state.OperatorIntent) (state.OperatorParkExecution, error) {
	ins, err := l.engine.Store().InstanceByID(ctx, intent.TargetID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return state.OperatorParkExecution{}, &restartInputError{err}
		}
		return state.OperatorParkExecution{}, err
	}
	release := l.engine.lockApp(ins.AppID)
	defer release()
	ins, err = l.engine.Store().InstanceByID(ctx, intent.TargetID)
	if err != nil {
		return state.OperatorParkExecution{}, err
	}
	if ins.State != string(state.StateRunning) && ins.State != string(state.StateWaking) && ins.State != string(state.StateColdBooting) {
		return state.OperatorParkExecution{}, &restartInputError{state.ErrInstanceNotRunning}
	}
	if ins.TailCount > 0 {
		return state.OperatorParkExecution{}, state.ErrOperatorParkCancelled
	}
	app, err := state.AppForInstance(ctx, l.engine.Store(), ins)
	if err != nil {
		return state.OperatorParkExecution{}, err
	}
	t := state.OperatorParkTarget{OperatorRestartTarget: state.OperatorRestartTarget{InstanceID: ins.ID, AppID: ins.AppID, DeploymentID: ins.DeploymentID, NodeID: l.engine.nodeForRoute(ins.NodeID), WakeID: ins.WakeID},
		InitialState: ins.State, Mode: ins.Mode, Action: state.OperatorParkStop, Reason: "Instance is not ready for snapshot capture.", LastRequestAt: ins.LastRequestAt, RequestCount: ins.RequestCount, StartedAt: ins.StartedAt}
	switch state.InstanceMode(ins.Mode) {
	case state.InstanceModeWorker, state.InstanceModeJob:
		t.Signal = int32(parseStopSignal(app.Manifest.StopSignal))
		if t.Signal == 0 {
			t.Signal = int32(syscall.SIGTERM)
		}
		t.GraceSeconds = int32(app.Manifest.StopGracePeriodS)
		if t.GraceSeconds <= 0 {
			account, err := l.engine.Store().AccountByID(ctx, app.AccountID)
			if err != nil {
				return state.OperatorParkExecution{}, err
			}
			t.GraceSeconds = int32(api.MustLimitsFor(account.Plan).DefaultStopGracePeriodS)
		}
		t.GraceSeconds = min(t.GraceSeconds, api.OperatorParkMaxGraceSeconds)
		t.Reason = "Worker/job graceful stop; cold boot required."
	default:
		if ins.State == string(state.StateRunning) {
			ephemeral, err := l.engine.hasEphemeralSecretForInstance(ctx, ins, app)
			if err != nil {
				return state.OperatorParkExecution{}, err
			}
			if app.RAMMB == ins.RAMMB && !l.engine.runtimeConfigStale(ctx, ins) && !ephemeral {
				measured, err := l.engine.measuredSnapshotSource(ctx, ins)
				if err != nil {
					return state.OperatorParkExecution{}, err
				}
				t.Action, t.Reason, t.Measured = state.OperatorParkCapture, "", measured
				t.MemoryKey = state.SnapshotCaptureMemKey(ins.DeploymentID, state.SnapshotTierInit, uuid.NewString())
				t.VMStateKey = state.SnapshotVMStateKey(state.Snapshot{StorageKey: t.MemoryKey})
				t.FCVersion = l.engine.fcVer
				t.BaseImageVersion = fcvm.FAAS_BASE_IMAGE_VERSION
				t.CPUTemplate, t.CPUModel = l.engine.nodeCPUIdentity(ctx, ins.NodeID)
				t.BeforeCheckpoint = app.Manifest.BeforeCheckpoint != nil || app.Manifest.Profiling != nil && app.Manifest.Profiling.Enabled
			} else {
				t.Reason = "Runtime configuration, RAM or ephemeral-secret policy requires a cold boot."
			}
		}
	}
	vmm, ok := l.engine.vmm.(operatorParkVMM)
	if !ok {
		return state.OperatorParkExecution{}, &restartInputError{fmt.Errorf("router lacks durable operator park support")}
	}
	rpcCtx, cancel := context.WithTimeout(ctx, api.OperatorRestartRPCTimeout)
	defer cancel()
	t.Generation, err = vmm.OperatorRestartGeneration(rpcCtx, t.NodeID, ins.ID)
	if err != nil {
		return state.OperatorParkExecution{}, &restartInputError{err}
	}
	e, err := store.PrepareOperatorPark(ctx, intent.ID, intent.ClaimToken, t)
	if errors.Is(err, state.ErrOperatorParkTargetChanged) {
		return e, &restartInputError{err}
	}
	return e, err
}

func (l *Loop) publishOperatorParkCapture(ctx context.Context, e state.OperatorParkExecution) (bool, error) {
	if e.Receipt == nil || e.Receipt.Outcome != string(state.StateParked) {
		return false, nil
	}
	ins, err := l.engine.Store().InstanceByID(ctx, e.Target.InstanceID)
	if err != nil {
		return false, err
	}
	if l.engine.runtimeConfigStale(ctx, ins) {
		return true, nil
	}
	if e.Target.Measured {
		store, ok := l.engine.Store().(state.ApplicationStandardSnapshotCaptureStore)
		if !ok {
			return false, fmt.Errorf("store lacks measured snapshot capture support")
		}
		if err := store.PublishApplicationStandardSnapshotCapture(ctx, *e.Receipt.Acknowledgment); err != nil {
			if errors.Is(err, state.ErrApplicationStandardRuntimeStale) || errors.Is(err, state.ErrInvalidArgument) {
				return true, nil
			}
			return false, err
		}
	}
	return false, nil
}

func (l *Loop) completeOperatorPark(ctx context.Context, store state.OperatorParkStore, intent state.OperatorIntent, status state.OperatorIntentStatus, reason string, discard bool) error {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.OperatorRestartPersistenceTimeout)
	defer cancel()
	var original *state.Instance
	var execution state.OperatorParkExecution
	if status == state.OperatorIntentSucceeded {
		var err error
		execution, err = store.GetOperatorPark(persistCtx, intent.ID)
		if err != nil {
			return err
		}
		release := l.engine.lockApp(execution.Target.AppID)
		defer release()
		ins, err := store.MigrationInstanceByID(persistCtx, execution.Target.InstanceID)
		if err != nil {
			return err
		}
		original = &ins
	}
	completion := state.OperatorParkCompletion{IntentID: intent.ID, ClaimToken: intent.ClaimToken, Status: status, Error: reason, DiscardCapture: discard}
	var result state.OperatorParkResult
	var err error
	for attempt := range api.OperatorRestartPersistenceAttempts {
		result, err = store.CompleteOperatorPark(persistCtx, completion)
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
		return fmt.Errorf("persist park outcome: %w", err)
	}
	if original != nil {
		terminal := state.State(result.TerminalState)
		l.engine.ledger.Release(original.ID)
		l.engine.emitInstanceChanged(persistCtx, original.ID, original.AppID, terminal, original.WakeID)
		l.engine.reconcileAfterInstanceTransition(persistCtx, *original, terminal)
		if !result.Replayed {
			l.engine.appendInstanceTransitionEvent(persistCtx, *original, state.State(original.State), terminal, "force_park", intent.Reason)
		}
		if terminal == state.StateParked && original.Mode == string(state.InstanceModeService) {
			l.engine.scheduleServiceReconcile(persistCtx, original.DeploymentID)
		}

	}
	if outbox, ok := l.engine.Store().(state.AuditEventOutboxStore); ok {
		if _, err := outbox.DeliverAuditEvent(persistCtx, result.AuditOutboxID); err != nil {
			l.log.Warn("sched: park audit delivery deferred", "intent_id", intent.ID, "err", err)
		}
	}
	l.log.Info("sched: operator park outcome committed", "intent_id", intent.ID, "status", status, "replayed", result.Replayed)
	return nil
}
