package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListProductCustomerActivity(ctx context.Context, account, app, tenant, raw string) (api.ProductCustomerActivityPage, error) {
	cursor, err := productActivityRequest(account, app, tenant, raw)
	if err != nil {
		return api.ProductCustomerActivityPage{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.ProductCustomerActivityPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = onboardingPGOwner(ctx, tx, account, app); err != nil {
		return api.ProductCustomerActivityPage{}, err
	}
	if err = productBillingScopePG(ctx, tx, ProductBillingScope{AccountID: account, AppID: app, TenantID: tenant, Period: productcontrol.Period(time.Now())}); err != nil {
		return api.ProductCustomerActivityPage{}, err
	}
	if _, err = productCustomerTx(ctx, tx, account, app, tenant); err != nil {
		return api.ProductCustomerActivityPage{}, err
	}
	observed := time.Now().UTC()
	p := sqlc.ProductCustomerActivityParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), TenantID: mustPgUUID(tenant), AfterID: cursor.ID, AfterKind: cursor.Kind, AfterAt: pgtype.Timestamptz{Time: cursor.At, Valid: !cursor.At.IsZero()}, PageLimit: api.ProductControlActivityPageSize + 1}
	if cursor.ID != "" {
		p.AfterUuid = mustPgUUID(cursor.ID)
	}
	rows, err := sqlc.New().ProductCustomerActivity(ctx, tx, p)
	if err != nil {
		return api.ProductCustomerActivityPage{}, fmt.Errorf("read Product Control activity: %w", err)
	}
	events := make([]api.ProductCustomerActivityEvent, 0, len(rows))
	for _, row := range rows {
		var e api.ProductCustomerActivityEvent
		if err = json.Unmarshal(row.Details, &e); err != nil {
			return api.ProductCustomerActivityPage{}, fmt.Errorf("decode Product Control activity: %w", err)
		}
		identity := productActivityEvent(row.Kind, pgUUIDString(row.SourceID), row.OccurredAt.Time)
		e.ID, e.Kind, e.SourceID, e.OccurredAt = identity.ID, identity.Kind, identity.SourceID, identity.OccurredAt
		events = append(events, e)
	}
	page := productActivityPage(account, app, tenant, observed, events)
	return page, tx.Commit(ctx)
}
