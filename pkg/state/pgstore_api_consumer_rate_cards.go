package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const apiConsumerRateCardSelectCols = `id, account_id, app_id, currency, unit,
       price_millicents_per_unit, effective_from, created_at`

type apiConsumerRateCardRowScanner interface {
	Scan(dest ...any) error
}

func scanAPIConsumerRateCardRow(row apiConsumerRateCardRowScanner) (APIConsumerRateCard, error) {
	var card APIConsumerRateCard
	if err := row.Scan(
		&card.ID,
		&card.AccountID,
		&card.AppID,
		&card.Currency,
		&card.Unit,
		&card.PriceMillicentsPerUnit,
		&card.EffectiveFrom,
		&card.CreatedAt,
	); err != nil {
		return APIConsumerRateCard{}, err
	}
	card.EffectiveFrom = card.EffectiveFrom.UTC()
	card.CreatedAt = card.CreatedAt.UTC()
	return card, nil
}

func (s *PgStore) CreateAPIConsumerRateCard(ctx context.Context, accountID, appID, currency string, price int64, effectiveFrom time.Time) (APIConsumerRateCard, error) {
	currency = normalizeAPIConsumerRateCardCurrency(currency)
	effectiveFrom = effectiveFrom.UTC()
	if err := validateAPIConsumerRateCardInput("CreateAPIConsumerRateCard", accountID, appID, currency, price, effectiveFrom); err != nil {
		return APIConsumerRateCard{}, err
	}
	row := s.pool.QueryRow(ctx,
		`insert into api_consumer_rate_cards
		       (account_id, app_id, currency, unit, price_millicents_per_unit, effective_from)
		 values ($1::uuid, $2::uuid, $3, $4, $5, $6)
		 returning `+apiConsumerRateCardSelectCols,
		accountID, appID, currency, APIConsumerRateCardUnitRequest, price, effectiveFrom)
	card, err := scanAPIConsumerRateCardRow(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return APIConsumerRateCard{}, ErrConflict
		}
		return APIConsumerRateCard{}, err
	}
	return card, nil
}

func (s *PgStore) ListAPIConsumerRateCardsForApp(ctx context.Context, accountID, appID string) ([]APIConsumerRateCard, error) {
	if accountID == "" || appID == "" {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx,
		`select `+apiConsumerRateCardSelectCols+`
		   from api_consumer_rate_cards
		  where account_id = $1::uuid and app_id = $2::uuid
		  order by effective_from asc, id asc`,
		accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIConsumerRateCard
	for rows.Next() {
		card, err := scanAPIConsumerRateCardRow(rows)
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

func (s *PgStore) GetAPIConsumerRateCardByID(ctx context.Context, accountID, cardID string) (APIConsumerRateCard, error) {
	if accountID == "" || cardID == "" {
		return APIConsumerRateCard{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx,
		`select `+apiConsumerRateCardSelectCols+`
		   from api_consumer_rate_cards
		  where id = $1::uuid and account_id = $2::uuid`,
		cardID, accountID)
	card, err := scanAPIConsumerRateCardRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerRateCard{}, ErrNotFound
	}
	return card, err
}
