package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OperationDiagnosticStore adds observational reads without changing the
// transactional admission seam or requiring methods on existing test doubles.
type OperationDiagnosticStore interface {
	OperationPendingCount(context.Context, string) (int64, error)
}

func (m *MemStore) OperationPendingCount(_ context.Context, account string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.accounts[account]; !exists {
		return 0, ErrNotFound
	}
	var count int64
	now := time.Now().UTC()
	if m.operationData == nil {
		return 0, nil
	}
	for _, op := range m.operationData.operations {
		if op.AccountID == account && operationCountsPending(op, now) {
			count++
		}
	}
	return count, nil
}

func (s *PgStore) OperationPendingCount(ctx context.Context, account string) (int64, error) {
	if _, err := s.AccountByID(ctx, account); err != nil {
		return 0, err
	}
	id, err := operationUUID(account)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	count, err := sqlc.New().CountPendingCustomerOperations(ctx, s.pool, sqlc.CountPendingCustomerOperationsParams{AccountID: id, ObservedAt: operationPGTime(&now)})
	if err != nil {
		return 0, fmt.Errorf("state: observe pending operations: %w", mapErr(err))
	}
	return count, nil
}
