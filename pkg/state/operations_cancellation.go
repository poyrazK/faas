package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func validateOperationCancellation(op Operation, accountID, tenantID string, generation int, now time.Time) error {
	if op.AccountID != accountID || (tenantID != "" && op.PlatformTenantID != tenantID) {
		return ErrNotFound
	}
	if !operationRetained(op, now) {
		return ErrOperationExpired
	}
	if generation < 1 {
		return ErrInvalidArgument
	}
	if generation != op.Generation || op.State == api.OperationRequiresReconciliation {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) CancelOperation(_ context.Context, accountID, tenantID, operationID string, generation int) (Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, exists := m.operationMemoryLocked().operations[operationID]
	if !exists {
		return Operation{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := validateOperationCancellation(op, accountID, tenantID, generation, now); err != nil {
		return Operation{}, err
	}
	if op.State.Terminal() || op.CancellationRequested {
		return cloneOperation(m.operationDeliveryLocked(op)), nil
	}
	if op.WorkflowRunID != "" {
		return m.cancelOperationWorkflowLocked(op, generation)
	}
	if op.JobRunID != "" {
		return m.cancelOperationJobLocked(op)
	}
	inv := m.invocations[op.CurrentInvocationID]
	switch inv.State {
	case InvocationDispatching:
		op.CancellationRequested = true
		event := operationEvent(&op, inv, "cancellation_requested", map[string]bool{"cancellation_requested": true}, now)
		m.operationSaveLocked(op, event)
	case InvocationPending:
		reserved := inv.QuotaReserved
		inv.State, inv.QuotaReserved, inv.CompletedAt = InvocationCancelled, false, &now
		if err := m.operationTransitionLocked(inv, false); err != nil {
			return Operation{}, err
		}
		m.invocations[inv.ID] = inv
		if reserved {
			m.decrementAccountAsyncInflightLocked(inv.AccountID)
		}
		op = m.operationData.operations[op.ID]
	default:
		return Operation{}, ErrConflict
	}
	return cloneOperation(m.operationDeliveryLocked(op)), nil
}

func (s *PgStore) CancelOperation(ctx context.Context, accountID, tenantID, operationID string, generation int) (Operation, error) {
	snapshot, err := s.OperationByID(ctx, accountID, tenantID, operationID)
	if err != nil {
		return Operation{}, err
	}
	if snapshot.WorkflowRunID != "" {
		return s.cancelOperationWorkflow(ctx, snapshot, generation)
	}
	if snapshot.JobRunID != "" {
		return s.cancelOperationJob(ctx, snapshot, generation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	inv, err := operationLockedInvocation(ctx, tx, snapshot.CurrentInvocationID)
	if err != nil {
		return Operation{}, mapErr(err)
	}
	op, _, _, exists, err := operationForInvocationTx(ctx, tx, inv.ID)
	if err != nil {
		return Operation{}, err
	}
	if !exists || op.ID != operationID || op.CurrentInvocationID != inv.ID {
		return Operation{}, ErrConflict
	}
	now := time.Now().UTC()
	if err := validateOperationCancellation(op, accountID, tenantID, generation, now); err != nil {
		return Operation{}, err
	}
	if op.State.Terminal() || op.CancellationRequested {
		return op, nil
	}
	switch inv.State {
	case InvocationDispatching:
		op.CancellationRequested = true
		event := operationEvent(&op, inv, "cancellation_requested", map[string]bool{"cancellation_requested": true}, now)
		if err := operationSaveTx(ctx, tx, op, event); err != nil {
			return Operation{}, err
		}
	case InvocationPending:
		id, _ := operationUUID(inv.ID)
		if err := sqlc.New().CancelCustomerOperationExecution(ctx, tx, sqlc.CancelCustomerOperationExecutionParams{ID: id, Now: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
			return Operation{}, err
		}
		inv.State = InvocationCancelled
		if err := operationTransitionTx(ctx, tx, inv, false); err != nil {
			return Operation{}, err
		}
		if inv.QuotaReserved {
			if err := decrementAccountAsyncInflightTx(ctx, tx, inv.AccountID); err != nil {
				return Operation{}, err
			}
		}
	default:
		return Operation{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, err
	}
	return s.OperationByID(ctx, accountID, tenantID, operationID)
}

// The scheduler must bind progress authority to the instance of its own lease.
// An old wake finishing after a retry cannot stamp the replacement attempt.
type OperationExecutionStampStore interface {
	StampOperationExecutionAttempt(context.Context, string, string, int) error
}

func (m *MemStore) StampOperationExecutionAttempt(_ context.Context, id, instanceID string, attempt int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, exists := m.invocations[id]
	_, _, operation := m.operationForInvocationLocked(id)
	if !exists || !operation || inv.State != InvocationDispatching || inv.Attempts != attempt || inv.LeaseExpiresAt == nil || !inv.LeaseExpiresAt.After(time.Now()) {
		return ErrNotFound
	}
	inv.InstanceID = instanceID
	m.invocations[id] = inv
	return nil
}

func (s *PgStore) StampOperationExecutionAttempt(ctx context.Context, id, instanceID string, attempt int) error {
	i, err := operationUUID(id)
	if err != nil {
		return err
	}
	instance, err := operationUUID(instanceID)
	if err != nil {
		return err
	}
	n, err := sqlc.New().StampCustomerOperationExecutionAttempt(ctx, s.pool, sqlc.StampCustomerOperationExecutionAttemptParams{ID: i, InstanceID: instance, Attempt: int32(attempt), Now: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
