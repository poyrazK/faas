package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) exclusiveCronBinding(ctx context.Context, cron state.Cron, accountID string) (state.ExclusiveTriggerBinding, bool, error) {
	bindings, ok := l.engine.Store().(state.ExclusiveTriggerBindingStore)
	if !ok {
		return state.ExclusiveTriggerBinding{}, false, nil
	}
	binding, err := bindings.ExclusiveTriggerBinding(ctx, accountID, "cron", cron.ID)
	if errors.Is(err, state.ErrNotFound) {
		return state.ExclusiveTriggerBinding{}, false, nil
	}
	if err != nil {
		return state.ExclusiveTriggerBinding{}, false, err
	}
	if binding.AppID != cron.AppID {
		return state.ExclusiveTriggerBinding{}, false, state.ErrNotFound
	}
	return binding, true, nil
}

func (l *Loop) dispatchExclusiveCron(ctx context.Context, cron state.Cron, accountID string, trigger CronDispatchTrigger, at time.Time, idempotencyKey string) (CronRun, error) {
	bindings, ok := l.engine.Store().(state.ExclusiveTriggerBindingStore)
	if !ok {
		return CronRun{}, errors.New("exclusive operation trigger bindings are unavailable")
	}
	owners, ok := l.engine.Store().(state.ExclusiveWorkStore)
	if !ok {
		return CronRun{}, errors.New("exclusive operation store is unavailable")
	}
	binding, err := bindings.ExclusiveTriggerBinding(ctx, accountID, "cron", cron.ID)
	if err != nil {
		return CronRun{}, err
	}
	if binding.AppID != cron.AppID {
		return CronRun{}, state.ErrNotFound
	}
	app, err := l.engine.Store().AppByID(ctx, cron.AppID)
	if err != nil || app.AccountID != accountID {
		return CronRun{}, state.ErrNotFound
	}
	account, err := l.engine.Store().AccountByID(ctx, accountID)
	if err != nil {
		return CronRun{}, err
	}
	if !account.Active() {
		return CronRun{}, ErrAccountSuspended
	}
	prepared := state.Invocation{AppID: app.ID, AccountID: accountID, PlatformTenantID: binding.PlatformTenantID,
		Source: state.InvocationCron, Method: "POST", Path: cron.Path}
	prepared, _, err = state.ResolveInvocationVersion(ctx, l.engine.Store(), prepared)
	if err != nil {
		return CronRun{}, fmt.Errorf("resolve cron operation version: %w", err)
	}
	request, err := json.Marshal(api.InvokeRequest{Method: prepared.Method, Path: prepared.Path, Payload: prepared.Payload, Headers: prepared.Headers})
	if err != nil {
		return CronRun{}, err
	}
	operation, joined, err := owners.AdmitExclusiveOperation(ctx, state.ExclusiveAdmission{
		AccountID: accountID, AppID: app.ID, PlatformTenantID: binding.PlatformTenantID,
		PolicyName: binding.PolicyName, Key: binding.Key, Request: request,
		EquivalenceKey: binding.EquivalenceKey, IdempotencyKey: idempotencyKey,
	})
	if l.ops != nil {
		l.ops.ObserveExclusiveOperationAdmission("cron", exclusiveAdmissionOutcome(err, joined, operation.Replayed))
	}
	if err != nil {
		return CronRun{}, err
	}
	return CronRun{ExclusiveOperationID: operation.ID, Success: true}, nil
}

func exclusiveAdmissionOutcome(err error, joined, replayed bool) string {
	if err == nil {
		switch {
		case joined:
			return "joined"
		case replayed:
			return "replayed"
		default:
			return "accepted"
		}
	}
	if errors.Is(err, exclusivework.ErrBusy) || errors.Is(err, exclusivework.ErrIdentityConflict) ||
		errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrInvalidArgument) ||
		errors.Is(err, state.ErrPlatformTenantSuspended) || errors.Is(err, state.ErrQuotaExceeded) ||
		errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrAppTaskCronOverlap) ||
		errors.Is(err, state.ErrAppTaskCronDisabled) || errors.Is(err, state.ErrAppTaskCronSuspended) {
		return "rejected"
	}
	return "error"
}

func (l *Loop) emitExclusiveCronFired(ctx context.Context, cron state.Cron, accountID string, at time.Time, trigger CronDispatchTrigger, operationID string, dispatchErr error) {
	if l.audit == nil {
		return
	}
	status := "ok"
	if dispatchErr != nil {
		status = "err"
	}
	event := AuditEventCronFired
	if trigger == TriggerManual {
		event = AuditEventCronFiredManually
	}
	payload := map[string]any{"cron_id": cron.ID, "app_id": cron.AppID, "schedule": cron.Schedule,
		"path": cron.Path, "fired_at": at.UTC().Format(time.RFC3339Nano), "status": status,
		"trigger": string(trigger), "exclusive_operation_id": operationID}
	if dispatchErr != nil {
		payload["error"] = dispatchErr.Error()
	}
	l.audit.Emit(ctx, event, &accountID, payload)
}

func exclusiveCronScheduleIdempotencyKey(cronID string, at time.Time) string {
	return "cron:" + cronID + ":" + at.UTC().Format(time.RFC3339Nano)
}

func exclusiveCronManualIdempotencyKey(requestID string) string {
	return "cron-manual:" + requestID
}

func exclusiveCommandCronAdmission(binding state.ExclusiveTriggerBinding, cron state.Cron, accountID, idempotencyKey string) (state.ExclusiveAdmission, error) {
	request, err := json.Marshal(struct {
		Kind   string `json:"kind"`
		CronID string `json:"cron_id"`
	}{Kind: "command_cron", CronID: cron.ID})
	if err != nil {
		return state.ExclusiveAdmission{}, err
	}
	return state.ExclusiveAdmission{
		AccountID: accountID, AppID: cron.AppID, PlatformTenantID: binding.PlatformTenantID,
		PolicyName: binding.PolicyName, Key: binding.Key, Request: request,
		EquivalenceKey: binding.EquivalenceKey, IdempotencyKey: idempotencyKey,
	}, nil
}

func exclusiveCommandCronScheduleIdempotencyKey(cronID string, at time.Time) string {
	return "command-cron:" + cronID + ":" + at.UTC().Format(time.RFC3339Nano)
}

func isExclusiveCronTerminalAdmissionError(err error) bool {
	return errors.Is(err, exclusivework.ErrBusy) || errors.Is(err, exclusivework.ErrIdentityConflict) ||
		errors.Is(err, state.ErrInvalidArgument) || errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrConflict)
}
