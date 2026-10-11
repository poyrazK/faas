package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Invocation rows are locked before the operation row everywhere: claims,
// completion, reports, cancellation and recovery share that ordering.
func operationRecordForInvocationTx(ctx context.Context, tx pgx.Tx, invocationID string) (Operation, bool, error) {
	id, err := operationUUID(invocationID)
	if err != nil {
		return Operation{}, false, err
	}
	raw, err := sqlc.New().LockCustomerOperationExecution(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, false, nil
	}
	if err != nil {
		return Operation{}, false, err
	}
	op, err := operationPGRecord(raw)
	return op, err == nil, err
}

func operationForInvocationTx(ctx context.Context, tx pgx.Tx, invocationID string) (Operation, OperationDefinition, api.Limits, bool, error) {
	op, exists, err := operationRecordForInvocationTx(ctx, tx, invocationID)
	if err != nil || !exists {
		return Operation{}, OperationDefinition{}, api.Limits{}, false, err
	}
	q := sqlc.New()
	account, _ := operationUUID(op.AccountID)
	definition, _ := operationUUID(op.DefinitionID)
	row, err := q.GetCustomerOperationDefinition(ctx, tx, sqlc.GetCustomerOperationDefinitionParams{ID: definition, AccountID: account})
	if err != nil {
		return Operation{}, OperationDefinition{}, api.Limits{}, false, err
	}
	def, err := operationPGDefinition(row)
	if err != nil {
		return Operation{}, OperationDefinition{}, api.Limits{}, false, err
	}
	plan, err := q.CustomerOperationAccountPlan(ctx, tx, account)
	if err != nil {
		return Operation{}, OperationDefinition{}, api.Limits{}, false, err
	}
	return op, def, api.MustLimitsFor(api.Plan(plan)), true, nil
}

func operationSaveTx(ctx context.Context, tx pgx.Tx, op Operation, event api.OperationEvent) error {
	// Native Jobs retain their own image snapshot. Host outcome settlement
	// must remain possible after the app is tombstoned, while parent purge
	// waits for the claimed task to close. Admission still pins app code.
	if op.JobRunID == "" {
		if err := operationPinsTx(ctx, tx, op); err != nil {
			return err
		}
	}
	q := sqlc.New()
	id, _ := operationUUID(op.ID)
	if op.State.Terminal() || op.State == api.OperationRequiresReconciliation {
		until := op.UpdatedAt.Add(time.Duration(op.PlanLimits.IdempotencyRetentionSeconds) * time.Second)
		if err := q.RetainCustomerOperationIdempotency(ctx, tx, sqlc.RetainCustomerOperationIdempotencyParams{OperationID: id, ExpiresAt: pgtype.Timestamptz{Time: until, Valid: true}}); err != nil {
			return err
		}
	}
	invocation, _ := operationUUID(op.CurrentInvocationID)
	record, err := operationRecordJSON(op)
	if err != nil {
		return err
	}
	if err := q.UpdateCustomerOperation(ctx, tx, sqlc.UpdateCustomerOperationParams{ID: id, InvocationID: invocation, WorkflowRunID: mustPgUUID(op.WorkflowRunID), JobRunID: mustPgUUID(op.JobRunID), State: string(op.State), Record: record, ExpiresAt: pgtype.Timestamptz{Time: op.ExpiresAt, Valid: true}}); err != nil {
		return fmt.Errorf("state: update operation: %w", err)
	}
	execution, _ := operationUUID(event.ExecutionID)
	if err := q.InsertCustomerOperationEvent(ctx, tx, sqlc.InsertCustomerOperationEventParams{OperationID: id, Sequence: event.Sequence, EventType: event.Type, ExecutionID: execution, Attempt: int32(event.Attempt), Data: event.Data, CreatedAt: pgtype.Timestamptz{Time: event.CreatedAt, Valid: true}}); err != nil {
		return err
	}
	return q.NotifyCustomerOperation(ctx, tx, op.ID)
}

func operationClaimTx(ctx context.Context, tx pgx.Tx, inv Invocation) (Invocation, error) {
	op, def, _, exists, err := operationForInvocationTx(ctx, tx, inv.ID)
	if err != nil || !exists {
		return inv, err
	}
	capability, event, err := operationClaim(&op, inv, time.Now().UTC())
	if err != nil {
		return Invocation{}, err
	}
	if err := operationSaveTx(ctx, tx, op, event); err != nil {
		return Invocation{}, err
	}
	return operationExecutionHeaders(inv, op, def, capability), nil
}

func operationTransitionTx(ctx context.Context, tx pgx.Tx, inv Invocation, uncertain bool) error {
	op, def, limits, exists, err := operationForInvocationTx(ctx, tx, inv.ID)
	if err != nil || !exists {
		return err
	}
	event, err := operationInvocationTransition(&op, inv, def, limits, uncertain, time.Now().UTC())
	if err != nil {
		return err
	}
	if op.State.Terminal() {
		if err := operationCompletionTx(ctx, tx, &op, def); err != nil {
			return err
		}
	}
	return operationSaveTx(ctx, tx, op, event)
}

func operationCompletionTx(ctx context.Context, tx pgx.Tx, op *Operation, def OperationDefinition) error {
	if def.Spec.CompletionWebhookID == "" {
		return nil
	}
	q := sqlc.New()
	hook, err := operationUUID(def.Spec.CompletionWebhookID)
	if err != nil {
		return err
	}
	account, _ := operationUUID(op.AccountID)
	app, _ := operationUUID(op.AppID)
	enabled, err := q.CustomerOperationCompletionWebhook(ctx, tx, sqlc.CustomerOperationCompletionWebhookParams{ID: hook, AccountID: account, AppID: app})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !enabled) {
		op.CompletionDelivery = api.OperationDeliveryResponse{State: "configuration_failed", LastError: "completion destination is unavailable"}
		return nil
	}
	if err != nil {
		return err
	}
	id := newOperationID()
	op.CompletionDelivery = api.OperationDeliveryResponse{State: "pending", DeliveryID: id}
	payload, err := operationCompletionPayload(*op)
	if err != nil {
		return err
	}
	delivery, _ := operationUUID(id)
	if err := q.InsertCustomerOperationCompletionDelivery(ctx, tx, sqlc.InsertCustomerOperationCompletionDeliveryParams{ID: delivery, WebhookID: hook, AccountID: account, AppID: app, Payload: payload, Now: pgtype.Timestamptz{Time: op.UpdatedAt, Valid: true}}); err != nil {
		return err
	}
	return nil
}

func (s *PgStore) ReportOperationProgress(ctx context.Context, operationID string, authority OperationExecutionAuthority, report api.OperationReportRequest) (Operation, error) {
	if _, err := operationUUID(authority.InvocationID); err != nil {
		return Operation{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	inv, err := operationLockedInvocation(ctx, tx, authority.InvocationID)
	if err != nil {
		return Operation{}, mapErr(err)
	}
	op, def, limits, exists, err := operationForInvocationTx(ctx, tx, inv.ID)
	if err != nil {
		return Operation{}, err
	}
	if !exists || op.ID != operationID {
		return Operation{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := ValidateOperationExecutionAuthority(op, inv, authority, now); err != nil {
		return Operation{}, err
	}
	id, _ := operationUUID(op.ID)
	execution, _ := operationUUID(inv.ID)
	q := sqlc.New()
	fingerprint := operationReportFingerprint(report)
	prior, err := q.GetCustomerOperationReport(ctx, tx, sqlc.GetCustomerOperationReportParams{OperationID: id, ExecutionID: execution, Attempt: int32(inv.Attempts), ReportID: report.ReportID})
	if err == nil {
		if prior != fingerprint {
			return Operation{}, ErrOperationInputConflict
		}
		return op, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, err
	}
	event, err := operationProgress(&op, inv, def, report, limits.Operations, now)
	if err != nil {
		return Operation{}, err
	}
	if err := q.InsertCustomerOperationReport(ctx, tx, sqlc.InsertCustomerOperationReportParams{OperationID: id, ExecutionID: execution, Attempt: int32(inv.Attempts), ReportID: report.ReportID, Fingerprint: fingerprint}); err != nil {
		return Operation{}, err
	}
	if err := operationSaveTx(ctx, tx, op, event); err != nil {
		return Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, err
	}
	return op, nil
}

func recoverExpiredOperationExecutionsTx(ctx context.Context, tx pgx.Tx, now time.Time, limit int) (int, error) {
	q := sqlc.New()
	ids, err := q.ListExpiredCustomerOperationExecutions(ctx, tx, sqlc.ListExpiredCustomerOperationExecutionsParams{Now: pgtype.Timestamptz{Time: now, Valid: true}, PageLimit: int32(limit)})
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		inv, err := operationLockedInvocation(ctx, tx, id)
		if err != nil {
			return 0, err
		}
		op, def, _, exists, err := operationForInvocationTx(ctx, tx, id)
		if err != nil {
			return 0, err
		}
		if !exists || op.CurrentInvocationID != inv.ID {
			return 0, ErrOperationStaleAttempt
		}
		uncertain := def.Spec.Recovery != api.OperationRecoverySafeRetry
		reserved := inv.QuotaReserved
		inv.State, inv.LastError = InvocationPending, "dispatch lease expired; requeued"
		if uncertain {
			inv.State, inv.LastError = InvocationFailed, "dispatch lease expired; reconciliation required"
		}
		inv.QuotaReserved = false
		inv.LeaseExpiresAt = nil
		inv.InstanceID = ""
		inv.DueAt = now
		uuid, _ := operationUUID(id)
		if err := q.RecoverExpiredCustomerOperationExecution(ctx, tx, sqlc.RecoverExpiredCustomerOperationExecutionParams{ID: uuid, State: string(inv.State), Now: pgtype.Timestamptz{Time: now, Valid: true}, LastError: inv.LastError}); err != nil {
			return 0, err
		}
		if err := operationTransitionTx(ctx, tx, inv, uncertain); err != nil {
			return 0, err
		}
		if reserved {
			if err := decrementAccountAsyncInflightTx(ctx, tx, inv.AccountID); err != nil {
				return 0, err
			}
		}
	}
	return len(ids), nil
}
