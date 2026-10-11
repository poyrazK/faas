// adr: 521
package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// OperationExecutionControlStore observes intent and a live claim together.
// This seam never renews, settles or changes an execution or operation.
type OperationExecutionControlStore interface {
	OperationExecutionControl(context.Context, string, OperationExecutionAuthority) (api.OperationExecutionControlResponse, error)
}

func operationExecutionControl(op Operation, inv Invocation, authority OperationExecutionAuthority, now time.Time) (api.OperationExecutionControlResponse, error) {
	if err := ValidateOperationExecutionAuthority(op, inv, authority, now); err != nil {
		return api.OperationExecutionControlResponse{}, err
	}
	lease := *inv.LeaseExpiresAt
	if lease.After(*inv.DeadlineAt) {
		lease = *inv.DeadlineAt
	}
	return api.OperationExecutionControlResponse{OperationID: op.ID, InvocationID: inv.ID, Attempt: inv.Attempts,
		CancellationRequested: op.CancellationRequested, DeadlineAt: *inv.DeadlineAt, LeaseExpiresAt: lease,
		ObservedAt: now, PollAfterMS: max(api.OperationControlPollMinIntervalMS, min(api.OperationControlPollIntervalMS, int(lease.Sub(now).Milliseconds()/3)))}, nil
}

func (m *MemStore) OperationExecutionControl(_ context.Context, id string, authority OperationExecutionAuthority) (api.OperationExecutionControlResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, _, exists := m.operationForInvocationLocked(authority.InvocationID)
	if !exists || op.ID != id {
		return api.OperationExecutionControlResponse{}, ErrNotFound
	}
	return operationExecutionControl(op, m.invocations[authority.InvocationID], authority, time.Now().UTC())
}

func (s *PgStore) OperationExecutionControl(ctx context.Context, id string, authority OperationExecutionAuthority) (api.OperationExecutionControlResponse, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.OperationExecutionControlResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Match the lifecycle lock order so a read cannot combine a replacement
	// invocation with an earlier cancellation or capability projection.
	inv, err := operationLockedInvocation(ctx, tx, authority.InvocationID)
	if err != nil {
		return api.OperationExecutionControlResponse{}, err
	}
	op, exists, err := operationRecordForInvocationTx(ctx, tx, inv.ID)
	if err != nil {
		return api.OperationExecutionControlResponse{}, err
	}
	if !exists || op.ID != id {
		return api.OperationExecutionControlResponse{}, ErrNotFound
	}
	return operationExecutionControl(op, inv, authority, time.Now().UTC())
}
