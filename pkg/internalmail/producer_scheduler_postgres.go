package internalmail

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func (s *PGStore) claimFair(ctx context.Context, params q.ClaimParams) (Recipient, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Recipient{}, err
	}
	defer rollbackOperator(ctx, tx)
	if err := s.queries.LockProducerScheduler(ctx, tx, s.namespace); err != nil {
		return Recipient{}, err
	}
	r, err := s.queries.Claim(ctx, tx, params)
	if err != nil {
		return Recipient{}, storeError(err)
	}
	if err := s.queries.AdvanceProducerTurn(ctx, tx, q.AdvanceProducerTurnParams{Namespace: s.namespace, ID: r.ID}); err != nil {
		return Recipient{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Recipient{}, err
	}
	return recipientRow(r), nil
}

// Lock order is delivery controls, sender domain, recipient, recipient-domain
// pacing, producer bucket. Claims alone take the scheduler lock, after refresh.
// Reservation, pacing and custody transfer commit or roll back together.
func (s *PGStore) reserveProducerDispatch(ctx context.Context, tx pgx.Tx, r Recipient, policy DispatchPolicy) error {
	id, err := s.queries.RecipientProducer(ctx, tx, q.RecipientProducerParams{Namespace: s.namespace, ID: pgID(r.ID)})
	if err != nil {
		return storeError(err)
	}
	limit, ok := policy.limit(id)
	if !ok {
		return ErrProducerSender
	}
	if err := s.queries.EnsureProducerDispatch(ctx, tx, q.EnsureProducerDispatchParams{Namespace: s.namespace, ProducerID: id}); err != nil {
		return err
	}
	if err := s.queries.LockProducerDispatch(ctx, tx, q.LockProducerDispatchParams{Namespace: s.namespace, ProducerID: id}); err != nil {
		return err
	}
	window := int32(api.InternalMailProducerDispatchWindow / time.Second)
	if err := s.queries.PruneDispatchReservations(ctx, tx, q.PruneDispatchReservationsParams{Namespace: s.namespace, ProducerID: id, WindowSeconds: window}); err != nil {
		return err
	}
	n, err := s.queries.ReserveProducerDispatch(ctx, tx, q.ReserveProducerDispatchParams{Namespace: s.namespace, ProducerID: id, LeaseID: pgID(r.LeaseID), WindowSeconds: window, DispatchLimit: limit})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrProducerThrottled
	}
	return nil
}

func (s *PGStore) producerDispatchStatus(ctx context.Context, policy DispatchPolicy) ([]ProducerDispatchStatus, error) {
	if !policy.Enabled || !policy.valid() {
		return nil, ErrInvalid
	}
	rows, err := s.queries.ProducerDispatchStatus(ctx, s.pool, q.ProducerDispatchStatusParams{Namespace: s.namespace, ProducerIds: policy.IDs, DispatchLimits: policy.Limits, WindowSeconds: int32(api.InternalMailProducerDispatchWindow / time.Second)})
	if err != nil {
		return nil, err
	}
	result := make([]ProducerDispatchStatus, 0, len(rows))
	for _, row := range rows {
		status := ProducerDispatchStatus{ProducerID: row.ProducerID, DispatchPerMinute: row.DispatchLimit, InWindow: row.InWindow,
			Remaining: max(0, int64(row.DispatchLimit)-row.InWindow), RateLimited: row.InWindow >= int64(row.DispatchLimit), DueRecipients: row.DueRecipients, OldestDueSeconds: row.OldestDueSeconds}
		if status.RateLimited {
			status.RetryAt = timePointer(row.NextReleaseAt)
		}
		result = append(result, status)
	}
	return result, nil
}
