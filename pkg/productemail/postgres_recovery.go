package productemail

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productemail/sqlc"
)

func recoveryReceiptRow(r sqlc.RecoveryReceiptRow) api.ProductEmailRecoveryReceipt {
	return api.ProductEmailRecoveryReceipt{ID: stringID(r.ID), RecipientID: stringID(r.SubscriptionID), TenantID: stringID(r.TenantID), ExpectedVersion: r.ExpectedVersion, PreviousGeneration: r.PreviousGeneration, Version: r.Version, Generation: r.Generation, WorkOutcomes: r.WorkOutcomes, HelpReplies: r.HelpReplies, RecoveredAt: r.RecoveredAt.Time}
}
func readRecovery(ctx context.Context, db sqlc.DBTX, k Key, id string, now time.Time) (api.ProductEmailRecoveryReceipt, error) {
	row, err := sqlc.New().RecoveryReceipt(ctx, db, sqlc.RecoveryReceiptParams{ID: pgID(id), SubscriptionID: pgID(k.ID()), WindowStart: pgTime(now.Add(-api.ProductControlNotificationWindow))})
	return recoveryReceiptRow(row), ownerPGError(err)
}
func (p *Postgres) RecoveryReceipt(ctx context.Context, k Key, id string, now time.Time) (api.ProductEmailRecoveryReceipt, error) {
	if !canonicalID(id) {
		return api.ProductEmailRecoveryReceipt{}, ErrInvalid
	}
	return readRecovery(ctx, p.pool, k, id, now)
}
func (p *Postgres) Recover(ctx context.Context, next Subscription, r api.ProductEmailRecoveryRequest, now time.Time) (api.ProductEmailRecoveryReceipt, error) {
	var v api.ProductEmailRecoveryReceipt
	now = now.UTC().Truncate(time.Microsecond)
	if !ValidRecoveryRequest(r) {
		return v, ErrInvalid
	}
	tx, err := p.locked(ctx, next.Key)
	if err != nil {
		return v, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	v, err = readRecovery(ctx, tx, next.Key, r.ID, now)
	if err == nil {
		if !MatchesRecovery(v, r) {
			return api.ProductEmailRecoveryReceipt{}, ErrConflict
		}
		return v, tx.Commit(ctx)
	}
	if !errors.Is(err, ErrNotFound) {
		return v, err
	}
	old, err := get(ctx, tx, next.Key)
	if err != nil {
		return v, err
	}
	next, v, err = recovered(old, next, r, now)
	if err != nil {
		return v, err
	}
	q := sqlc.New()
	err = q.PruneRecoveries(ctx, tx, sqlc.PruneRecoveriesParams{WindowStart: pgTime(now.Add(-api.ProductControlNotificationWindow)), PruneLimit: api.ProductControlEmailPruneSize})
	if err != nil {
		return v, err
	}
	count, err := q.CountRecoveries(ctx, tx, sqlc.CountRecoveriesParams{SubscriptionID: pgID(next.ID()), WindowStart: pgTime(now.Add(-api.ProductControlNotificationWindow))})
	if err != nil {
		return v, err
	}
	if count >= api.ProductControlEmailRecoveryLimit {
		return v, ErrLimit
	}
	err = q.InsertRecovery(ctx, tx, sqlc.InsertRecoveryParams{ID: pgID(v.ID), SubscriptionID: pgID(v.RecipientID), ExpectedVersion: v.ExpectedVersion, PreviousGeneration: v.PreviousGeneration, Version: v.Version, Generation: v.Generation, WorkOutcomes: v.WorkOutcomes, HelpReplies: v.HelpReplies, RecoveredAt: pgTime(now)})
	if err != nil {
		var failure *pgconn.PgError
		if errors.As(err, &failure) && failure.Code == "23505" {
			return v, ErrConflict
		}
		return v, err
	}
	err = q.CancelSuppressedPending(ctx, tx, sqlc.CancelSuppressedPendingParams{SubscriptionID: pgID(next.ID()), UpdatedAt: pgTime(now)})
	if err == nil {
		err = q.RecoverSubscription(ctx, tx, sqlc.RecoverSubscriptionParams{ID: pgID(next.ID()), Version: next.Version, RecipientGeneration: next.Generation, WorkOutcomes: next.Work, HelpReplies: next.Help, WorkSince: pgTime(now), ExpiresAt: pgTime(next.ExpiresAt), GrantSealed: append([]byte{}, next.Grant...)})
	}
	if err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}
func (p *Postgres) Recoveries(ctx context.Context, s OwnerScope, recipient, cursor string, now time.Time) (api.ProductEmailRecoveryPage, error) {
	page := newRecoveryPage(s, recipient, now)
	before, err := decodeRecoveryCursor(s, recipient, cursor)
	if err != nil {
		return page, err
	}
	if before.ID == "" {
		before.ID = uuid.Nil.String()
		before.At = now
	}
	selected := recipient
	if selected == "" {
		selected = uuid.Nil.String()
	}
	rows, err := sqlc.New().Recoveries(ctx, p.pool, sqlc.RecoveriesParams{AccountID: pgID(s.AccountID), AppID: pgID(s.AppID), TenantID: pgID(scopeTenant(s)), FilterTenant: s.TenantID != "", RecipientID: pgID(selected), FilterRecipient: recipient != "", WindowStart: pgTime(page.WindowStart), HasCursor: cursor != "", AfterAt: pgTime(before.At), AfterID: pgID(before.ID), PageLimit: api.ProductControlEmailOwnerPageSize + 1})
	if err != nil {
		return page, err
	}
	for _, row := range rows {
		page.Receipts = append(page.Receipts, recoveryReceiptRow(sqlc.RecoveryReceiptRow(row)))
	}
	finishRecoveryPage(s, &page)
	return page, nil
}
