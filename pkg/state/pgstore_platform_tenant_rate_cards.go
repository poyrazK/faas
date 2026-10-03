package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const platformTenantRateCardSelectCols = `id, account_id, platform_tenant_id, currency, unit,
	price_millicents_per_unit, effective_from, created_at`

type platformTenantRateCardRowScanner interface {
	Scan(dest ...any) error
}

func scanPlatformTenantRateCardRow(row platformTenantRateCardRowScanner) (PlatformTenantRateCard, error) {
	var card PlatformTenantRateCard
	err := row.Scan(&card.ID, &card.AccountID, &card.TenantID, &card.Currency, &card.Unit,
		&card.PriceMillicentsPerUnit, &card.EffectiveFrom, &card.CreatedAt)
	if err != nil {
		return PlatformTenantRateCard{}, err
	}
	card.EffectiveFrom = card.EffectiveFrom.UTC()
	card.CreatedAt = card.CreatedAt.UTC()
	return card, nil
}

func (s *PgStore) CreatePlatformTenantRateCard(ctx context.Context, accountID, tenantID, currency string, price int64, effectiveFrom time.Time) (PlatformTenantRateCard, error) {
	currency = normalizePlatformTenantRateCardCurrency(currency)
	effectiveFrom = effectiveFrom.UTC()
	if err := validatePlatformTenantRateCardInput("CreatePlatformTenantRateCard", accountID, tenantID, currency, price, effectiveFrom); err != nil {
		return PlatformTenantRateCard{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlatformTenantRateCard{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantIDLocked string
	if err := tx.QueryRow(ctx, `select id from platform_tenants where account_id = $1::uuid and id = $2::uuid for update`, accountID, tenantID).Scan(&tenantIDLocked); errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantRateCard{}, ErrNotFound
	} else if err != nil {
		return PlatformTenantRateCard{}, err
	}
	var existingCurrency string
	err = tx.QueryRow(ctx, `select currency from platform_tenant_rate_cards
		where account_id = $1::uuid and platform_tenant_id = $2::uuid limit 1`, accountID, tenantID).Scan(&existingCurrency)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantRateCard{}, err
	}
	if err == nil && existingCurrency != currency {
		return PlatformTenantRateCard{}, ErrInvalidArgument
	}
	card, err := scanPlatformTenantRateCardRow(tx.QueryRow(ctx,
		`insert into platform_tenant_rate_cards
			(account_id, platform_tenant_id, currency, unit, price_millicents_per_unit, effective_from)
		 select account_id, id, $3, $4, $5, $6 from platform_tenants
		 where account_id = $1::uuid and id = $2::uuid
		 returning `+platformTenantRateCardSelectCols,
		accountID, tenantID, currency, PlatformTenantRateCardUnitRequest, price, effectiveFrom))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return PlatformTenantRateCard{}, ErrConflict
		}
		return PlatformTenantRateCard{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenantRateCard{}, err
	}
	return card, nil
}

func (s *PgStore) ListPlatformTenantRateCards(ctx context.Context, accountID, tenantID string) ([]PlatformTenantRateCard, error) {
	if !validPlatformTenantRateCardIDs(accountID, tenantID) {
		return nil, ErrInvalidArgument
	}
	if _, err := s.GetPlatformTenant(ctx, accountID, tenantID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `select `+platformTenantRateCardSelectCols+`
		from platform_tenant_rate_cards where account_id = $1::uuid and platform_tenant_id = $2::uuid
		order by effective_from asc, id asc`, accountID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PlatformTenantRateCard, 0)
	for rows.Next() {
		card, err := scanPlatformTenantRateCardRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, card)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
