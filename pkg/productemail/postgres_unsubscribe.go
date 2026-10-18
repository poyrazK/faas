package productemail

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productemail/sqlc"
)

func (p *Postgres) PrepareUnsubscribe(ctx context.Context, d Delivery, hash, sealed []byte, now time.Time) ([]byte, error) {
	if !validUnsubscribeMaterial(hash, sealed) {
		return nil, ErrInvalid
	}
	value, err := sqlc.New().PrepareUnsubscribe(ctx, p.pool, sqlc.PrepareUnsubscribeParams{DeliveryID: pgID(d.ID), Attempts: int32(d.Attempts), TokenHash: hash, TokenSealed: sealed, ObservedAt: pgTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	return value, ownerPGError(err)
}
func optOutReceiptRow(r sqlc.OptOutReceiptRow) api.ProductEmailOptOutReceipt {
	return api.ProductEmailOptOutReceipt{ID: stringID(r.ID), RecipientID: stringID(r.SubscriptionID), TenantID: stringID(r.TenantID), DeliveryID: stringID(r.DeliveryID), ExpectedVersion: r.ExpectedVersion, Version: r.Version, Generation: r.Generation, UnsubscribedAt: r.UnsubscribedAt.Time.UTC()}
}
func unsubscribeDelivery(ctx context.Context, db sqlc.DBTX, hash []byte, now time.Time) (Subscription, Delivery, error) {
	row, err := sqlc.New().UnsubscribeDelivery(ctx, db, sqlc.UnsubscribeDeliveryParams{TokenHash: hash, WindowStart: pgTime(now.Add(-api.ProductControlNotificationWindow))})
	if err != nil {
		return Subscription{}, Delivery{}, ownerPGError(err)
	}
	d, err := delivery(row.ProductControlEmailDelivery)
	return subscription(row.ProductControlEmailSubscription), d, err
}
func (p *Postgres) Unsubscribe(ctx context.Context, hash []byte, now time.Time) (api.ProductEmailUnsubscribeResult, error) {
	var out api.ProductEmailUnsubscribeResult
	if len(hash) != sha256.Size {
		return out, ErrInvalid
	}
	now = now.UTC().Truncate(time.Microsecond)
	s, _, err := unsubscribeDelivery(ctx, p.pool, hash, now)
	if err != nil {
		return out, err
	}
	err = p.WithConsent(ctx, s.Key, func() error {
		tx, e := p.locked(ctx, s.Key)
		if e != nil {
			return e
		}
		defer func() { _ = tx.Rollback(ctx) }()
		out, e = unsubscribePostgres(ctx, tx, hash, now)
		if e != nil {
			return e
		}
		return tx.Commit(ctx)
	})
	return out, err
}
func unsubscribePostgres(ctx context.Context, tx pgx.Tx, hash []byte, now time.Time) (api.ProductEmailUnsubscribeResult, error) {
	var out api.ProductEmailUnsubscribeResult
	s, d, err := unsubscribeDelivery(ctx, tx, hash, now)
	if err != nil {
		return out, err
	}
	q := sqlc.New()
	r, err := q.OptOutReceipt(ctx, tx, sqlc.OptOutReceiptParams{SubscriptionID: pgID(s.ID()), ExpectedVersion: d.Version, WindowStart: pgTime(now.Add(-api.ProductControlNotificationWindow))})
	if err == nil {
		return unsubscribeResult(optOutReceiptRow(r), s), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	next, receipt, err := unsubscribe(s, d, now)
	if err != nil {
		return out, err
	}
	err = q.InsertOptOut(ctx, tx, sqlc.InsertOptOutParams{ID: pgID(receipt.ID), SubscriptionID: pgID(s.ID()), DeliveryID: pgID(d.ID), ExpectedVersion: receipt.ExpectedVersion, Version: receipt.Version, Generation: receipt.Generation, UnsubscribedAt: pgTime(now)})
	if err == nil {
		err = q.Unsubscribe(ctx, tx, sqlc.UnsubscribeParams{ID: pgID(s.ID()), Version: next.Version, WorkSince: pgTime(now)})
	}
	if err == nil {
		err = q.CancelUnsubscribedPending(ctx, tx, sqlc.CancelUnsubscribedPendingParams{SubscriptionID: pgID(s.ID()), UpdatedAt: pgTime(now)})
	}
	return unsubscribeResult(receipt, next), err
}
func (p *Postgres) OptOuts(ctx context.Context, s OwnerScope, recipient, cursor string, now time.Time) (api.ProductEmailOptOutPage, error) {
	page := newOptOutPage(s, recipient, now)
	before, err := decodeOptOutCursor(s, recipient, cursor)
	if err != nil {
		return page, err
	}
	if before.ID == "" {
		before.ID, before.At = uuid.Nil.String(), now
	}
	selected := recipient
	if selected == "" {
		selected = uuid.Nil.String()
	}
	rows, err := sqlc.New().OptOuts(ctx, p.pool, sqlc.OptOutsParams{AccountID: pgID(s.AccountID), AppID: pgID(s.AppID), TenantID: pgID(scopeTenant(s)), FilterTenant: s.TenantID != "", RecipientID: pgID(selected), FilterRecipient: recipient != "", WindowStart: pgTime(page.WindowStart), HasCursor: cursor != "", AfterAt: pgTime(before.At), AfterID: pgID(before.ID), PageLimit: api.ProductControlEmailOwnerPageSize + 1})
	if err != nil {
		return page, err
	}
	for _, row := range rows {
		page.Receipts = append(page.Receipts, optOutReceiptRow(sqlc.OptOutReceiptRow(row)))
	}
	finishOptOutPage(s, &page)
	return page, nil
}
