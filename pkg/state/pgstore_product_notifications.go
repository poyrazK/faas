package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func notificationPGParams(s ProductNotificationScope, v productHelpView, now time.Time) sqlc.ProductNotificationListParams {
	return sqlc.ProductNotificationListParams{AccountID: mustPgUUID(s.AccountID), AppID: mustPgUUID(s.AppID), TenantID: mustPgUUID(s.TenantID), AuthAppID: mustPgUUID(s.AuthAppID), OrganizationID: mustPgUUID(s.OrganizationID), CanViewWork: v.permissions.ViewWork, CanViewFeature: v.permissions.RunWork || v.permissions.ViewBilling, ObservedAt: pgtype.Timestamptz{Time: now, Valid: true}, WindowStart: pgtype.Timestamptz{Time: now.Add(-api.ProductControlNotificationWindow), Valid: true}, PageLimit: api.ProductControlNotificationPageSize + 1}
}

func notificationPGValue(s ProductNotificationScope, row sqlc.ProductNotificationListRow) (productNotificationSource, api.ProductNotification, error) {
	_, id, ok := productcontrol.NotificationReference(row.NotificationID)
	if !ok || !row.OccurredAt.Valid {
		return productNotificationSource{}, api.ProductNotification{}, ErrConflict
	}
	source := notificationSource(row.Kind, id, row.Feature, row.HelpKind, row.SourceVersion, row.OccurredAt.Time)
	if source.value.ID != row.NotificationID {
		return source, api.ProductNotification{}, ErrConflict
	}
	n := notificationProjection(s, source, productNotificationRead{}, time.Time{})
	n.Read, n.ReadVersion = row.IsRead, row.ReadVersion
	return source, n, nil
}

func (s *PgStore) ListProductNotifications(ctx context.Context, scope ProductNotificationScope, filter, raw string) (api.ProductNotificationPage, error) {
	cursor, err := notificationCursor(scope, filter, raw)
	if err != nil {
		return api.ProductNotificationPage{}, err
	}
	tx, v, err := s.helpCustomerTx(ctx, ProductHelpScope(scope))
	if err != nil {
		return api.ProductNotificationPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC().Truncate(time.Microsecond)
	page := notificationPage(scope, now)
	p := notificationPGParams(scope, v, now)
	p.UnreadOnly = filter == "unread"
	p.AfterKey = cursor.ID
	p.AfterAt = pgtype.Timestamptz{Time: cursor.At, Valid: !cursor.At.IsZero()}
	rows, err := sqlc.New().ProductNotificationList(ctx, tx, p)
	if err != nil {
		return page, err
	}
	for _, row := range rows {
		page.UnreadCount = row.UnreadCount
		if row.NotificationID == "" {
			continue
		}
		_, n, err := notificationPGValue(scope, row)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, n)
	}
	return notificationFinish(page, cursor), tx.Commit(ctx)
}

func notificationPGCurrent(ctx context.Context, tx pgx.Tx, scope ProductNotificationScope, p sqlc.ProductNotificationListParams, req api.ProductNotificationUpdate) (productNotificationSource, api.ProductNotification, error) {
	kind, id, _ := productcontrol.NotificationReference(req.ID)
	p.SourceFilter = kind
	p.SourceIDFilter = mustPgUUID(id)
	rows, err := sqlc.New().ProductNotificationList(ctx, tx, p)
	if err != nil {
		return productNotificationSource{}, api.ProductNotification{}, err
	}
	if len(rows) != 1 || rows[0].NotificationID != req.ID {
		return productNotificationSource{}, api.ProductNotification{}, ErrNotFound
	}
	return notificationPGValue(scope, rows[0])
}

func (s *PgStore) UpdateProductNotification(ctx context.Context, scope ProductNotificationScope, req api.ProductNotificationUpdate) (api.ProductNotification, error) {
	if productcontrol.ValidateNotificationUpdate(req) != nil {
		return api.ProductNotification{}, ErrInvalidArgument
	}
	tx, v, err := s.helpCustomerTx(ctx, ProductHelpScope(scope))
	if err != nil {
		return api.ProductNotification{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := notificationPGParams(scope, v, now)
	q := sqlc.New()
	if err = q.ProductNotificationPrune(ctx, tx, sqlc.ProductNotificationPruneParams{AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, ExpiresAt: p.ObservedAt}); err != nil {
		return api.ProductNotification{}, err
	}
	source, current, err := notificationPGCurrent(ctx, tx, scope, p, req)
	if err != nil {
		return current, err
	}
	value, changed, err := notificationUpdate(current, req)
	if err != nil {
		return value, err
	}
	if changed {
		if err = notificationPGWrite(ctx, tx, p, source, value); err != nil {
			return current, err
		}
	}
	return value, tx.Commit(ctx)
}

func notificationPGWrite(ctx context.Context, tx pgx.Tx, p sqlc.ProductNotificationListParams, source productNotificationSource, value api.ProductNotification) error {
	q := sqlc.New()
	if value.ReadVersion == 1 {
		count, err := q.ProductNotificationCountReads(ctx, tx, sqlc.ProductNotificationCountReadsParams{AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID})
		if err != nil {
			return err
		}
		if count >= api.ProductControlNotificationMaxReads {
			return NewOperationLimitError("product_notification_reads", api.ProductControlNotificationMaxReads, count+1)
		}
	}
	kind, id, _ := productcontrol.NotificationReference(value.ID)
	n, err := q.ProductNotificationWriteRead(ctx, tx, sqlc.ProductNotificationWriteReadParams{AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, AuthAppID: p.AuthAppID, AuthOrganizationID: p.OrganizationID, SourceKind: kind, SourceID: mustPgUUID(id), SourceVersion: source.version, IsRead: value.Read, Version: value.ReadVersion, UpdatedAt: p.ObservedAt, ExpiresAt: pgtype.Timestamptz{Time: value.OccurredAt.Add(api.ProductControlNotificationWindow), Valid: true}})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) GetProductNotification(ctx context.Context, scope ProductNotificationScope, id string) (api.ProductNotification, error) {
	if _, _, ok := productcontrol.NotificationReference(id); !ok {
		return api.ProductNotification{}, ErrInvalidArgument
	}
	tx, v, err := s.helpCustomerTx(ctx, ProductHelpScope(scope))
	if err != nil {
		return api.ProductNotification{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p := notificationPGParams(scope, v, time.Now().UTC().Truncate(time.Microsecond))
	_, n, err := notificationPGCurrent(ctx, tx, scope, p, api.ProductNotificationUpdate{ID: id})
	if err != nil {
		return n, err
	}
	return n, tx.Commit(ctx)
}
