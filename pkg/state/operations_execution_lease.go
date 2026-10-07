package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OperationExecutionLeaseStore is scheduler authority. Renewal cannot revive
// an expired claim, cross an attempt fence, or extend the request deadline.
type OperationExecutionLeaseStore interface {
	RenewOperationExecution(context.Context, string, int, int) (bool, error)
}

func validOperationExecutionRenewal(attempt, leaseSeconds int) bool {
	return attempt > 0 && leaseSeconds > 0 && leaseSeconds <= int(api.OperationExecutionLeaseMax/time.Second)
}

func (s *PgStore) RenewOperationExecution(ctx context.Context, id string, attempt, leaseSeconds int) (bool, error) {
	if !validOperationExecutionRenewal(attempt, leaseSeconds) {
		return false, ErrInvalidArgument
	}
	uuid, err := operationUUID(id)
	if err != nil {
		return false, err
	}
	n, err := sqlc.New().RenewCustomerOperationExecution(ctx, s.pool, sqlc.RenewCustomerOperationExecutionParams{ID: uuid, Attempt: int32(attempt), LeaseSeconds: int32(leaseSeconds)})
	return n == 1, err
}

func (m *MemStore) RenewOperationExecution(_ context.Context, id string, attempt, leaseSeconds int) (bool, error) {
	if !validOperationExecutionRenewal(attempt, leaseSeconds) {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, exists := m.invocations[id]
	now := time.Now()
	if !exists || inv.OperationID == "" || inv.State != InvocationDispatching || inv.Attempts != attempt || inv.LeaseExpiresAt == nil || !inv.LeaseExpiresAt.After(now) || inv.DeadlineAt == nil || !inv.DeadlineAt.After(now) {
		return false, nil
	}
	expires := now.Add(time.Duration(leaseSeconds) * time.Second)
	if expires.After(*inv.DeadlineAt) {
		expires = *inv.DeadlineAt
	}
	inv.LeaseExpiresAt = &expires
	m.invocations[id] = inv
	return true, nil
}
