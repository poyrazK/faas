package productemail

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productemail/sqlc"
)

func ownerPGError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func scopeTenant(scope OwnerScope) string {
	if scope.TenantID == "" {
		return uuid.Nil.String()
	}
	return scope.TenantID
}
func (p *Postgres) OwnerPage(ctx context.Context, scope OwnerScope, status, cursor string, now time.Time) (api.ProductEmailOwnerPage, error) {
	page := newOwnerPage(scope, status, now)
	after, err := decodeOwnerCursor(scope, status, cursor)
	if err != nil {
		return page, err
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	counts, err := q.OwnerCounts(ctx, tx, sqlc.OwnerCountsParams{AccountID: pgID(scope.AccountID), AppID: pgID(scope.AppID), TenantID: pgID(scopeTenant(scope)), FilterTenant: scope.TenantID != "", WindowStart: pgTime(page.WindowStart)})
	if err != nil {
		return page, err
	}
	page.Counts = api.ProductEmailCounts{Queued: counts.Queued, Leased: counts.Leased, Captured: counts.Captured, ProviderAccepted: counts.ProviderAccepted, Failed: counts.Failed, Cancelled: counts.Cancelled, Delivered: counts.Delivered, Bounced: counts.Bounced, Complained: counts.Complained}
	if counts.OldestPendingAt.Valid {
		at := counts.OldestPendingAt.Time
		page.OldestPendingAt = &at
	}
	if after.ID == "" {
		after.ID = uuid.Nil.String()
		after.At = now
	}
	rows, err := q.OwnerDeliveries(ctx, tx, sqlc.OwnerDeliveriesParams{AccountID: pgID(scope.AccountID), AppID: pgID(scope.AppID), TenantID: pgID(scopeTenant(scope)), FilterTenant: scope.TenantID != "", WindowStart: pgTime(page.WindowStart), Status: status, HasCursor: cursor != "", AfterID: pgID(after.ID), AfterAt: pgTime(after.At), PageLimit: api.ProductControlEmailOwnerPageSize + 1})
	if err != nil {
		return page, err
	}
	for _, r := range rows {
		d, e := delivery(r.ProductControlEmailDelivery)
		if e != nil {
			return page, e
		}
		sub := Subscription{Key: Key{TenantID: stringID(r.TenantID)}, Version: r.Version, Work: r.WorkOutcomes, Help: r.HelpReplies, WorkSince: r.WorkSince.Time, HelpSince: r.HelpSince.Time, ExpiresAt: r.ExpiresAt.Time, PauseReason: r.PauseReason, PausedAt: r.PausedAt.Time}
		page.Deliveries = append(page.Deliveries, ownerDelivery(sub, d, now))
	}
	finishOwnerPage(scope, &page)
	return page, tx.Commit(ctx)
}
func (p *Postgres) OwnerGet(ctx context.Context, scope OwnerScope, id string, now time.Time) (Subscription, Delivery, error) {
	if !scope.valid() || scope.TenantID == "" || !canonicalID(id) {
		return Subscription{}, Delivery{}, ErrInvalid
	}
	r, err := sqlc.New().OwnerGet(ctx, p.pool, sqlc.OwnerGetParams{AccountID: pgID(scope.AccountID), AppID: pgID(scope.AppID), TenantID: pgID(scope.TenantID), DeliveryID: pgID(id), WindowStart: pgTime(now.Add(-api.ProductControlNotificationWindow))})
	if err != nil {
		return Subscription{}, Delivery{}, ownerPGError(err)
	}
	d, err := delivery(r.ProductControlEmailDelivery)
	return subscription(r.ProductControlEmailSubscription), d, err
}
func retryReceipt(ctx context.Context, db sqlc.DBTX, scope OwnerScope, id string, attempt int, now time.Time) (api.ProductEmailRetryReceipt, error) {
	r, err := sqlc.New().OwnerRetryReceipt(ctx, db, sqlc.OwnerRetryReceiptParams{AccountID: pgID(scope.AccountID), AppID: pgID(scope.AppID), TenantID: pgID(scope.TenantID), DeliveryID: pgID(id), FailedAttempt: int32(attempt), WindowStart: pgTime(now.Add(-api.ProductControlNotificationWindow))})
	return api.ProductEmailRetryReceipt{DeliveryID: stringID(r.DeliveryID), TenantID: stringID(r.TenantID), FailedAttempt: int(r.FailedAttempt), QueuedAt: r.QueuedAt.Time.UTC()}, ownerPGError(err)
}
func (p *Postgres) RetryReceipt(ctx context.Context, scope OwnerScope, id string, attempt int, now time.Time) (api.ProductEmailRetryReceipt, error) {
	if !scope.valid() || scope.TenantID == "" || !canonicalID(id) || attempt < 1 || attempt >= api.ProductControlEmailMaxAttempts {
		return api.ProductEmailRetryReceipt{}, ErrInvalid
	}
	return retryReceipt(ctx, p.pool, scope, id, attempt, now)
}
func (p *Postgres) Retry(ctx context.Context, scope OwnerScope, d Delivery, now time.Time) (api.ProductEmailRetryReceipt, error) {
	if !scope.valid() || scope.TenantID == "" || !canonicalID(d.ID) || d.Attempts < 1 || d.Attempts >= api.ProductControlEmailMaxAttempts {
		return api.ProductEmailRetryReceipt{}, ErrInvalid
	}
	tx, err := p.locked(ctx, Key{AccountID: scope.AccountID, AppID: scope.AppID, TenantID: scope.TenantID})
	if err != nil {
		return api.ProductEmailRetryReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.OwnerLock(ctx, tx, sqlc.OwnerLockParams{AccountID: pgID(scope.AccountID), AppID: pgID(scope.AppID), TenantID: pgID(scope.TenantID), DeliveryID: pgID(d.ID), WindowStart: pgTime(now.Add(-api.ProductControlNotificationWindow))})
	if err != nil {
		return api.ProductEmailRetryReceipt{}, ownerPGError(err)
	}
	r, err := retryReceipt(ctx, tx, scope, d.ID, d.Attempts, now)
	if err == nil {
		return r, tx.Commit(ctx)
	}
	if !errors.Is(err, ErrNotFound) {
		return r, err
	}
	current, err := delivery(row.ProductControlEmailDelivery)
	if err != nil {
		return r, err
	}
	if current.Attempts != d.Attempts || current.Version != d.Version || current.Notification.Revision != d.Notification.Revision || RetryBlocked(subscription(row.ProductControlEmailSubscription), current, now) != "" {
		return r, ErrConflict
	}
	n, err := q.OwnerRetry(ctx, tx, sqlc.OwnerRetryParams{DeliveryID: pgID(d.ID), FailedAttempt: int32(d.Attempts), ObservedAt: pgTime(now), MaxAttempts: api.ProductControlEmailMaxAttempts})
	if err != nil {
		return r, err
	}
	if n != 1 {
		return r, ErrConflict
	}
	err = q.InsertRetryReceipt(ctx, tx, sqlc.InsertRetryReceiptParams{DeliveryID: pgID(d.ID), FailedAttempt: int32(d.Attempts), QueuedAt: pgTime(now)})
	if err != nil {
		return r, err
	}
	r = api.ProductEmailRetryReceipt{DeliveryID: d.ID, TenantID: scope.TenantID, FailedAttempt: d.Attempts, QueuedAt: now}
	return r, tx.Commit(ctx)
}
