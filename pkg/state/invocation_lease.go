package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// InvocationLeaseStore renews the dispatch lease of a plain (non-operation)
// invocation while its dispatcher delivers it (ADR-1180). Renewal cannot
// revive a lapsed lease or cross an attempt fence, so a requeued invocation
// is never extended by its previous dispatcher.
type InvocationLeaseStore interface {
	RenewInvocationLease(ctx context.Context, id string, attempt, leaseSeconds int) (bool, error)
}

func (s *PgStore) RenewInvocationLease(ctx context.Context, id string, attempt, leaseSeconds int) (bool, error) {
	if attempt <= 0 || leaseSeconds <= 0 {
		return false, ErrInvalidArgument
	}
	uuid, err := operationUUID(id)
	if err != nil {
		return false, err
	}
	n, err := sqlc.New().RenewInvocationLease(ctx, s.pool, sqlc.RenewInvocationLeaseParams{ID: uuid, Attempt: int32(attempt), LeaseSeconds: int32(leaseSeconds)})
	return n == 1, err
}

func (m *MemStore) RenewInvocationLease(_ context.Context, id string, attempt, leaseSeconds int) (bool, error) {
	if attempt <= 0 || leaseSeconds <= 0 {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, exists := m.invocations[id]
	now := time.Now()
	if !exists || inv.OperationID != "" || inv.State != InvocationDispatching || inv.Attempts != attempt || inv.LeaseExpiresAt == nil || !inv.LeaseExpiresAt.After(now) {
		return false, nil
	}
	expires := now.Add(time.Duration(leaseSeconds) * time.Second)
	inv.LeaseExpiresAt = &expires
	m.invocations[id] = inv
	return true, nil
}
