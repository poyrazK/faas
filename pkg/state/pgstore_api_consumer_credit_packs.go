package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func fromConsumerCreditPurchase(row sqlc.ApiConsumerCreditPurchase) APIConsumerCreditPurchase {
	return APIConsumerCreditPurchase{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), ConsumerID: pgUUIDString(row.ConsumerID),
		Pack:        api.ConsumerCreditPack{ID: row.PackID, Label: row.Label, Currency: row.Currency, AmountMillicents: row.AmountMillicents},
		AmountMinor: row.AmountMinor, ReturnURL: row.ReturnUrl, Customer: row.StripeCustomer, SessionID: row.StripeSession.String,
		Livemode: row.Livemode, Status: row.Status, ExpiresAt: row.ExpiresAt.Time.UTC(), NextCheckAt: row.NextCheckAt.Time.UTC(),
	}
}

func (s *PgStore) GetConsumerCreditPackSettings(ctx context.Context, accountID, appID string) (api.ConsumerCreditPackSettings, error) {
	row, err := sqlc.New().GetAPIConsumerCreditPackSettings(ctx, s.pool, sqlc.GetAPIConsumerCreditPackSettingsParams{
		AccountID: uuidToPgtype(accountID), AppID: uuidToPgtype(appID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ConsumerCreditPackSettings{}, ErrNotFound
	}
	if err != nil {
		return api.ConsumerCreditPackSettings{}, fmt.Errorf("read credit pack settings: %w", err)
	}
	settings := api.ConsumerCreditPackSettings{ReturnURL: row.ReturnUrl}
	if err := json.Unmarshal(row.Packs, &settings.Packs); err != nil {
		return api.ConsumerCreditPackSettings{}, fmt.Errorf("decode credit packs: %w", err)
	}
	return settings, nil
}

func (s *PgStore) PutConsumerCreditPackSettings(ctx context.Context, accountID, appID string, settings api.ConsumerCreditPackSettings) error {
	if settings.Packs == nil {
		settings.Packs = []api.ConsumerCreditPack{}
	}
	packs, err := json.Marshal(settings.Packs)
	if err != nil {
		return fmt.Errorf("encode credit packs: %w", err)
	}
	return sqlc.New().PutAPIConsumerCreditPackSettings(ctx, s.pool, sqlc.PutAPIConsumerCreditPackSettingsParams{
		AccountID: uuidToPgtype(accountID), AppID: uuidToPgtype(appID), ReturnUrl: settings.ReturnURL, Packs: packs,
	})
}

func (s *PgStore) GetConsumerCreditPurchase(ctx context.Context, accountID, appID, consumerID, id string) (APIConsumerCreditPurchase, error) {
	return readConsumerCreditPurchase(ctx, s.pool, accountID, appID, consumerID, id)
}

func readConsumerCreditPurchase(ctx context.Context, db sqlc.DBTX, accountID, appID, consumerID, id string) (APIConsumerCreditPurchase, error) {
	row, err := sqlc.New().GetAPIConsumerCreditPurchase(ctx, db, sqlc.GetAPIConsumerCreditPurchaseParams{
		AccountID: uuidToPgtype(accountID), AppID: uuidToPgtype(appID), ConsumerID: uuidToPgtype(consumerID), ID: uuidToPgtype(id),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerCreditPurchase{}, ErrNotFound
	}
	if err != nil {
		return APIConsumerCreditPurchase{}, fmt.Errorf("read credit purchase: %w", err)
	}
	return fromConsumerCreditPurchase(row), nil
}

func (s *PgStore) StartConsumerCreditPurchase(ctx context.Context, p APIConsumerCreditPurchase) (APIConsumerCreditPurchase, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumerCreditPurchase{}, fmt.Errorf("begin credit purchase: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	consumer, err := q.LockAPIConsumerCreditBuyer(ctx, tx, sqlc.LockAPIConsumerCreditBuyerParams{
		AccountID: uuidToPgtype(p.AccountID), AppID: uuidToPgtype(p.AppID), ID: uuidToPgtype(p.ConsumerID), RequireActive: true,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerCreditPurchase{}, ErrNotFound
	}
	if err != nil {
		return APIConsumerCreditPurchase{}, fmt.Errorf("lock credit buyer: %w", err)
	}
	old, err := readConsumerCreditPurchase(ctx, tx, p.AccountID, p.AppID, p.ConsumerID, p.ID)
	if err == nil {
		if old.Pack.ID != p.Pack.ID {
			return APIConsumerCreditPurchase{}, ErrConflict
		}
		return old, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return APIConsumerCreditPurchase{}, err
	}
	count, err := q.CountReservedAPIConsumerCreditGrants(ctx, tx, consumer)
	if err != nil {
		return APIConsumerCreditPurchase{}, fmt.Errorf("count credit grants: %w", err)
	}
	if count >= api.ConsumerCreditOpenGrantsMax {
		return APIConsumerCreditPurchase{}, ErrConflict
	}
	row, err := q.InsertAPIConsumerCreditPurchase(ctx, tx, sqlc.InsertAPIConsumerCreditPurchaseParams{
		ID: uuidToPgtype(p.ID), AccountID: uuidToPgtype(p.AccountID), AppID: uuidToPgtype(p.AppID), ConsumerID: consumer,
		PackID: p.Pack.ID, Label: p.Pack.Label, Currency: p.Pack.Currency, AmountMillicents: p.Pack.AmountMillicents,
		AmountMinor: p.AmountMinor, ReturnUrl: p.ReturnURL, StripeCustomer: p.Customer, Livemode: p.Livemode, ExpiresAt: NewPgtypeTime(p.ExpiresAt),
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
		return APIConsumerCreditPurchase{}, ErrConflict
	}
	if err != nil {
		return APIConsumerCreditPurchase{}, fmt.Errorf("insert credit purchase: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return APIConsumerCreditPurchase{}, fmt.Errorf("commit credit purchase: %w", err)
	}
	return fromConsumerCreditPurchase(row), nil
}

func (s *PgStore) SetConsumerCreditPurchaseCheckout(ctx context.Context, id, sessionID string, nextCheckAt time.Time) (APIConsumerCreditPurchase, error) {
	row, err := sqlc.New().SetAPIConsumerCreditPurchaseCheckout(ctx, s.pool, sqlc.SetAPIConsumerCreditPurchaseCheckoutParams{
		ID: uuidToPgtype(id), StripeSession: sessionID, NextCheckAt: NewPgtypeTime(nextCheckAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerCreditPurchase{}, ErrConflict
	}
	if err != nil {
		return APIConsumerCreditPurchase{}, fmt.Errorf("save credit purchase checkout: %w", err)
	}
	return fromConsumerCreditPurchase(row), nil
}

func (s *PgStore) ClaimConsumerCreditPurchases(ctx context.Context, now, nextCheckAt time.Time, limit int32) ([]APIConsumerCreditPurchase, error) {
	rows, err := sqlc.New().ClaimAPIConsumerCreditPurchases(ctx, s.pool, sqlc.ClaimAPIConsumerCreditPurchasesParams{
		DueAt: NewPgtypeTime(now), NextCheckAt: NewPgtypeTime(nextCheckAt), BatchSize: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]APIConsumerCreditPurchase, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromConsumerCreditPurchase(row))
	}
	return out, nil
}

func (s *PgStore) ExpireConsumerCreditPurchase(ctx context.Context, id, sessionID string) error {
	return sqlc.New().ExpireAPIConsumerCreditPurchase(ctx, s.pool, sqlc.ExpireAPIConsumerCreditPurchaseParams{
		ID: uuidToPgtype(id), SessionID: sessionID,
	})
}

func (s *PgStore) CreditConsumerPurchase(ctx context.Context, id, sessionID string) error {
	_, err := sqlc.New().CreditAPIConsumerPurchase(ctx, s.pool, sqlc.CreditAPIConsumerPurchaseParams{
		PurchaseID: uuidToPgtype(id), SessionID: sessionID, GrantID: uuidToPgtype(consumerPurchaseGrantID(id)),
	})
	// Another worker can finish between reading Stripe and committing.
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}
