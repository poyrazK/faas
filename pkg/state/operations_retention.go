package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type OperationRetentionStore interface {
	PruneOperationState(context.Context, time.Time, int) (int64, error)
}

func (s *PgStore) PruneOperationState(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit < 1 || limit > api.OperationRetentionPageMax {
		return 0, ErrInvalidArgument
	}
	q := sqlc.New()
	cutoff := pgtype.Timestamptz{Time: now.UTC(), Valid: true}
	if _, err := q.PruneCustomerOperationStreams(ctx, s.pool, sqlc.PruneCustomerOperationStreamsParams{Now: cutoff, PageLimit: int32(limit)}); err != nil {
		return 0, err
	}
	if _, err := q.PruneCustomerOperationEvents(ctx, s.pool, sqlc.PruneCustomerOperationEventsParams{Now: cutoff, PageLimit: int32(limit)}); err != nil {
		return 0, err
	}
	deleted, err := q.PruneCustomerOperations(ctx, s.pool, sqlc.PruneCustomerOperationsParams{Now: cutoff, PageLimit: int32(limit)})
	if err != nil {
		return 0, err
	}
	if _, err := q.PruneCustomerOperationIdempotency(ctx, s.pool, sqlc.PruneCustomerOperationIdempotencyParams{Now: cutoff, PageLimit: int32(limit)}); err != nil {
		return deleted, err
	}
	return deleted, nil
}

func (m *MemStore) PruneOperationState(_ context.Context, now time.Time, limit int) (int64, error) {
	if limit < 1 || limit > api.OperationRetentionPageMax {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	for id, lease := range data.streams {
		if !lease.ExpiresAt.After(now) {
			delete(data.streams, id)
		}
	}
	deleted := int64(0)
	for id, op := range data.operations {
		if !op.EventExpiresAt.After(now) {
			delete(data.events, id)
		}
		if deleted >= int64(limit) || (!op.State.Terminal() && op.State != api.OperationRequiresReconciliation) || op.ExpiresAt.After(now) {
			continue
		}
		m.forgetOperationLocked(id)
		deleted++
	}
	pruned := 0
	for key, receipt := range data.receipts {
		if pruned >= limit {
			break
		}
		if !receipt.ExpiresAt.After(now) {
			delete(data.receipts, key)
			pruned++
		}
	}
	return deleted, nil
}
