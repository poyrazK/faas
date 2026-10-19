package productemail

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productemail/sqlc"
)

type Postgres struct{ pool *pgxpool.Pool }

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }
func pgID(id string) pgtype.UUID {
	v, err := uuid.Parse(id)
	return pgtype.UUID{Bytes: v, Valid: err == nil}
}
func pgTime(at time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: at, Valid: !at.IsZero()}
}
func stringID(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }
func subscription(row sqlc.ProductControlEmailSubscription) Subscription {
	return Subscription{Key: Key{AccountID: stringID(row.AccountID), AppID: stringID(row.AppID), TenantID: stringID(row.TenantID), AuthAppID: stringID(row.AuthAppID), OrganizationID: stringID(row.OrganizationID), ActorID: stringID(row.ActorID)}, AppSlug: row.AppSlug, AuthSlug: row.AuthSlug, Version: row.Version, Work: row.WorkOutcomes, Help: row.HelpReplies, WorkSince: row.WorkSince.Time, HelpSince: row.HelpSince.Time, ExpiresAt: row.ExpiresAt.Time, Grant: row.GrantSealed, Cursor: row.ScanCursor, PauseReason: row.PauseReason, PausedAt: row.PausedAt.Time, Generation: row.RecipientGeneration, PauseRecipient: row.PauseRecipientSealed}
}
func delivery(row sqlc.ProductControlEmailDelivery) (Delivery, error) {
	d := Delivery{ProductEmailDelivery: api.ProductEmailDelivery{ID: stringID(row.ID), Status: row.Status, Reason: row.Reason, Attempts: int(row.Attempts), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}, SubscriptionID: stringID(row.SubscriptionID), Version: row.PreferenceVersion, NextAt: row.NextAt.Time, Deadline: row.Deadline.Time}
	if err := json.Unmarshal(row.Notification, &d.Notification); err != nil {
		return d, err
	}
	d.NotificationID, d.Kind = d.Notification.ID, d.Notification.Kind
	d.Generation, d.RecipientSealed = row.RecipientGeneration, row.RecipientSealed
	d.UnsubscribeHash, d.UnsubscribeSealed = row.UnsubscribeHash, row.UnsubscribeSealed
	if row.ProviderID.Valid {
		d.ProviderID = stringID(row.ProviderID)
	}
	d.DeliveredAt, d.BouncedAt, d.ComplainedAt, d.HardBounced = timePointer(row.DeliveredAt), timePointer(row.BouncedAt), timePointer(row.ComplainedAt), row.HardBounced
	switch d.Status {
	case "delivered":
		d.ReportedAt = d.DeliveredAt
	case "bounced":
		d.ReportedAt = d.BouncedAt
	case "complained":
		d.ReportedAt = d.ComplainedAt
	}
	return d, nil
}
func get(ctx context.Context, db sqlc.DBTX, k Key) (Subscription, error) {
	row, err := sqlc.New().Get(ctx, db, pgID(k.ID()))
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{Key: k}, nil
	}
	return subscription(row), err
}
func (p *Postgres) Get(ctx context.Context, k Key) (Subscription, error) { return get(ctx, p.pool, k) }
func (p *Postgres) locked(ctx context.Context, k Key) (pgx.Tx, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if err = sqlc.New().LockScope(ctx, tx, "product-email-feedback/v1"); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	if err = sqlc.New().LockScope(ctx, tx, "product-email/"+k.AccountID+"/"+k.AppID+"/"+k.TenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func (p *Postgres) Put(ctx context.Context, s Subscription, expected int64, now time.Time) (Subscription, error) {
	tx, err := p.locked(ctx, s.Key)
	if err != nil {
		return s, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	old, err := get(ctx, tx, s.Key)
	if err != nil {
		return s, err
	}
	next, err := update(old, s, expected, now)
	if err != nil {
		return s, err
	}
	q := sqlc.New()
	if old.Version == 0 {
		count, e := q.CountSubscribers(ctx, tx, sqlc.CountSubscribersParams{AccountID: pgID(s.AccountID), AppID: pgID(s.AppID), TenantID: pgID(s.TenantID)})
		if e != nil {
			return s, e
		}
		if count >= api.ProductControlEmailMaxSubscribers {
			return s, ErrLimit
		}
	}
	grant := append([]byte{}, next.Grant...)
	err = q.Put(ctx, tx, sqlc.PutParams{ID: pgID(next.ID()), AccountID: pgID(next.AccountID), AppID: pgID(next.AppID), TenantID: pgID(next.TenantID), AuthAppID: pgID(next.AuthAppID), OrganizationID: pgID(next.OrganizationID), ActorID: pgID(next.ActorID), AppSlug: next.AppSlug, AuthSlug: next.AuthSlug, Version: next.Version, WorkOutcomes: next.Work, HelpReplies: next.Help, WorkSince: pgTime(next.WorkSince), HelpSince: pgTime(next.HelpSince), ExpiresAt: pgTime(next.ExpiresAt), GrantSealed: grant})
	if err == nil {
		err = q.CancelPending(ctx, tx, sqlc.CancelPendingParams{SubscriptionID: pgID(next.ID()), UpdatedAt: pgTime(now)})
	}
	if err != nil {
		return s, err
	}
	next, err = get(ctx, tx, s.Key)
	if err != nil {
		return s, err
	}
	return next, tx.Commit(ctx)
}
func (p *Postgres) List(ctx context.Context, after string, now time.Time) ([]Subscription, error) {
	if after == "" {
		after = uuid.Nil.String()
	}
	rows, err := sqlc.New().List(ctx, p.pool, sqlc.ListParams{AfterID: pgID(after), ObservedAt: pgTime(now), PageLimit: api.ProductControlEmailBatchSize})
	out := []Subscription{}
	for _, r := range rows {
		out = append(out, subscription(r))
	}
	return out, err
}
func (p *Postgres) Progress(ctx context.Context, id string, version int64, cursor string) error {
	n, err := sqlc.New().Progress(ctx, p.pool, sqlc.ProgressParams{ID: pgID(id), Version: version, ScanCursor: cursor})
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}
func (p *Postgres) Enqueue(ctx context.Context, s Subscription, items []api.ProductNotification, now time.Time) error {
	tx, err := p.locked(ctx, s.Key)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := get(ctx, tx, s.Key)
	if err != nil {
		return err
	}
	if current.Version != s.Version {
		return ErrConflict
	}
	q := sqlc.New()
	if err = q.Prune(ctx, tx, sqlc.PruneParams{WindowStart: pgTime(now.Add(-api.ProductControlNotificationWindow)), PruneLimit: api.ProductControlEmailPruneSize}); err != nil {
		return err
	}
	count, err := q.CountDeliveries(ctx, tx, pgID(s.ID()))
	if err != nil {
		return err
	}
	for _, n := range items {
		if !Enabled(current, n, now) {
			continue
		}
		d := newDelivery(s, n, now)
		exists, e := q.DeliveryExists(ctx, tx, pgID(d.ID))
		if e != nil {
			return e
		}
		if exists {
			continue
		}
		if count >= api.ProductControlEmailMaxDeliveries {
			return ErrLimit
		}
		data, e := json.Marshal(n)
		if e != nil {
			return e
		}
		inserted, e := q.InsertDelivery(ctx, tx, sqlc.InsertDeliveryParams{ID: pgID(d.ID), SubscriptionID: pgID(s.ID()), PreferenceVersion: s.Version, SourceRevision: n.Revision, Notification: data, CreatedAt: pgTime(now), Deadline: pgTime(d.Deadline), RecipientGeneration: d.Generation})
		if e != nil {
			return e
		}
		count += inserted
	}
	return tx.Commit(ctx)
}
func (p *Postgres) Claim(ctx context.Context, now time.Time) ([]Delivery, error) {
	q := sqlc.New()
	if err := q.Prune(ctx, p.pool, sqlc.PruneParams{WindowStart: pgTime(now.Add(-api.ProductControlNotificationWindow)), PruneLimit: api.ProductControlEmailPruneSize}); err != nil {
		return nil, err
	}
	if err := q.Expire(ctx, p.pool, sqlc.ExpireParams{UpdatedAt: pgTime(now), MaxAttempts: api.ProductControlEmailMaxAttempts}); err != nil {
		return nil, err
	}
	rows, err := q.Claim(ctx, p.pool, sqlc.ClaimParams{ObservedAt: pgTime(now), LeaseUntil: pgTime(now.Add(api.ProductControlEmailLease)), MaxAttempts: api.ProductControlEmailMaxAttempts, PageLimit: api.ProductControlEmailClaimSize})
	if err != nil {
		return nil, err
	}
	out := []Delivery{}
	for _, r := range rows {
		d, e := delivery(r)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, nil
}
func (p *Postgres) Ready(ctx context.Context, d Delivery, now time.Time) (Subscription, error) {
	r, err := sqlc.New().Ready(ctx, p.pool, sqlc.ReadyParams{ID: pgID(d.ID), Attempts: int32(d.Attempts), ObservedAt: pgTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, ErrConflict
	}
	s := subscription(r)
	if err == nil && s.PauseReason != "" {
		return s, ErrSuppressed
	}
	if err == nil && !Enabled(s, d.Notification, now) {
		err = ErrConflict
	}
	return s, err
}

func timePointer(at pgtype.Timestamptz) *time.Time {
	if !at.Valid {
		return nil
	}
	v := at.Time.UTC()
	return &v
}
func (p *Postgres) Finish(ctx context.Context, d Delivery, status, reason string, next, now time.Time) error {
	if !validFinish(status, reason) {
		return ErrConflict
	}
	n, err := sqlc.New().Finish(ctx, p.pool, sqlc.FinishParams{ID: pgID(d.ID), Attempts: int32(d.Attempts), Status: status, Reason: reason, NextAt: pgTime(next), UpdatedAt: pgTime(now)})
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}
func (p *Postgres) Recent(ctx context.Context, k Key) ([]api.ProductEmailDelivery, error) {
	rows, err := sqlc.New().Recent(ctx, p.pool, sqlc.RecentParams{SubscriptionID: pgID(k.ID()), WindowStart: pgTime(time.Now().Add(-api.ProductControlNotificationWindow)), PageLimit: api.ProductControlEmailBatchSize})
	if err != nil {
		return nil, err
	}
	out := []api.ProductEmailDelivery{}
	for _, r := range rows {
		d, e := delivery(r)
		if e != nil {
			return nil, e
		}
		v := d.ProductEmailDelivery
		if v.Status == "leased" {
			v.Status = "queued"
		}
		out = append(out, v)
	}
	return out, nil
}
