package productemail

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productemail/sqlc"
)

func (p *Postgres) feedbackTx(ctx context.Context, now time.Time) (pgx.Tx, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	q := sqlc.New()
	err = q.LockScope(ctx, tx, "product-email-feedback/v1")
	if err == nil {
		err = q.PrunePendingFeedback(ctx, tx, sqlc.PrunePendingFeedbackParams{WindowStart: pgTime(now.Add(-api.ProductControlEmailPendingFeedbackTTL)), PruneLimit: api.ProductControlEmailPruneSize})
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func saveFeedback(ctx context.Context, tx pgx.Tx, d Delivery, now time.Time) error {
	q := sqlc.New()
	stamp := func(at *time.Time) time.Time {
		if at == nil {
			return time.Time{}
		}
		return *at
	}
	err := q.SaveFeedback(ctx, tx, sqlc.SaveFeedbackParams{ID: pgID(d.ID), Status: d.Status, DeliveredAt: pgTime(stamp(d.DeliveredAt)), BouncedAt: pgTime(stamp(d.BouncedAt)), ComplainedAt: pgTime(stamp(d.ComplainedAt)), HardBounced: d.HardBounced, UpdatedAt: pgTime(d.UpdatedAt)})
	if err != nil {
		return err
	}
	reason, _ := suppression(d)
	if reason == "" {
		return nil
	}
	err = q.PauseSubscriber(ctx, tx, sqlc.PauseSubscriberParams{ID: pgID(d.SubscriptionID), PauseReason: reason, PausedAt: pgTime(now), PauseRecipientSealed: append([]byte{}, d.RecipientSealed...), RecipientGeneration: d.Generation})
	if err == nil {
		err = q.CancelSuppressedPending(ctx, tx, sqlc.CancelSuppressedPendingParams{SubscriptionID: pgID(d.SubscriptionID), UpdatedAt: pgTime(now)})
	}
	return err
}
func (p *Postgres) Accept(ctx context.Context, d Delivery, providerID string, now time.Time) error {
	if !canonicalID(providerID) || len(d.RecipientSealed) > api.ProductControlEmailCipherMaxBytes {
		return ErrInvalid
	}
	tx, err := p.feedbackTx(ctx, now)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.ReceiptDelivery(ctx, tx, sqlc.ReceiptDeliveryParams{ID: pgID(d.ID), Attempts: int32(d.Attempts)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return ownerPGError(err)
	}
	current, err := delivery(row)
	if err != nil {
		return err
	}
	err = q.AcceptReceipt(ctx, tx, sqlc.AcceptReceiptParams{ID: pgID(d.ID), ProviderID: pgID(providerID), UpdatedAt: pgTime(now), RecipientSealed: append([]byte{}, d.RecipientSealed...)})
	if err != nil {
		var failure *pgconn.PgError
		if errors.As(err, &failure) && failure.Code == "23505" {
			return ErrConflict
		}
		return err
	}
	current.RecipientSealed = append([]byte{}, d.RecipientSealed...)
	current.Status, current.Reason, current.ProviderID, current.UpdatedAt = "provider_accepted", "", providerID, now
	rows, err := q.PendingFeedback(ctx, tx, sqlc.PendingFeedbackParams{ProviderID: pgID(providerID), WindowStart: pgTime(now.Add(-api.ProductControlEmailPendingFeedbackTTL))})
	if err != nil {
		return err
	}
	for _, f := range rows {
		current = applyFeedback(current, Feedback{ProviderID: providerID, Kind: f.Kind, OccurredAt: f.OccurredAt.Time, HardBounce: f.HardBounce}, now)
	}
	if err = saveFeedback(ctx, tx, current, now); err != nil {
		return err
	}
	if err = q.DeletePendingFeedback(ctx, tx, pgID(providerID)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) Feedback(ctx context.Context, f Feedback, now time.Time) error {
	if !f.valid(now) {
		return ErrInvalid
	}
	tx, err := p.feedbackTx(ctx, now)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.ProviderDelivery(ctx, tx, sqlc.ProviderDeliveryParams{ProviderID: pgID(f.ProviderID), WindowStart: pgTime(now.Add(-api.ProductControlNotificationWindow))})
	if err == nil {
		d, e := delivery(row)
		if e != nil {
			return e
		}
		if err = saveFeedback(ctx, tx, applyFeedback(d, f, now), now); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	exists, err := q.PendingFeedbackExists(ctx, tx, sqlc.PendingFeedbackExistsParams{ProviderID: pgID(f.ProviderID), Kind: f.Kind})
	if err != nil {
		return err
	}
	if !exists {
		count, e := q.CountPendingFeedback(ctx, tx)
		if e != nil {
			return e
		}
		if count >= api.ProductControlEmailPendingFeedbackLimit {
			return ErrLimit
		}
	}
	err = q.PutPendingFeedback(ctx, tx, sqlc.PutPendingFeedbackParams{ProviderID: pgID(f.ProviderID), Kind: f.Kind, OccurredAt: pgTime(f.OccurredAt), HardBounce: f.HardBounce, ReceivedAt: pgTime(now)})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
