package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const apiConsumerRateCardSelectCols = `id, account_id, app_id, currency, unit,
       price_millicents_per_unit, included_units_per_month, tiers, route_weights, coalesce(plan_id::text, ''), effective_from, created_at`

type apiConsumerRateCardRowScanner interface {
	Scan(dest ...any) error
}

func scanAPIConsumerRateCardRow(row apiConsumerRateCardRowScanner) (APIConsumerRateCard, error) {
	var card APIConsumerRateCard
	var tiers, weights []byte
	if err := row.Scan(
		&card.ID,
		&card.AccountID,
		&card.AppID,
		&card.Currency,
		&card.Unit,
		&card.PriceMillicentsPerUnit,
		&card.IncludedUnitsPerMonth,
		&tiers,
		&weights,
		&card.PlanID,
		&card.EffectiveFrom,
		&card.CreatedAt,
	); err != nil {
		return APIConsumerRateCard{}, err
	}
	if err := json.Unmarshal(tiers, &card.Tiers); err != nil {
		return APIConsumerRateCard{}, err
	}
	if len(card.Tiers) == 0 {
		card.Tiers = nil
	}
	if err := json.Unmarshal(weights, &card.RouteWeights); err != nil {
		return APIConsumerRateCard{}, err
	}
	if len(card.RouteWeights) == 0 {
		card.RouteWeights = nil
	}
	card.EffectiveFrom = card.EffectiveFrom.UTC()
	card.CreatedAt = card.CreatedAt.UTC()
	return card, nil
}

func (s *PgStore) CreateAPIConsumerRateCard(ctx context.Context, accountID, appID, currency string, price int64, effectiveFrom time.Time) (APIConsumerRateCard, error) {
	return s.CreateAPIConsumerRateCardVersion(ctx, APIConsumerRateCardInput{
		AccountID: accountID, AppID: appID, Currency: currency, PriceMillicentsPerUnit: price, EffectiveFrom: effectiveFrom,
	})
}

func (s *PgStore) CreateAPIConsumerRateCardVersion(ctx context.Context, in APIConsumerRateCardInput) (APIConsumerRateCard, error) {
	in, err := normalizeAPIConsumerRateCardInput(in)
	if err != nil {
		return APIConsumerRateCard{}, err
	}
	tiers := in.Tiers
	if tiers == nil {
		tiers = []APIConsumerRateCardTier{}
	}
	rawTiers, err := json.Marshal(tiers)
	if err != nil {
		return APIConsumerRateCard{}, err
	}
	weights := in.RouteWeights
	if weights == nil {
		weights = map[string]int64{}
	}
	rawWeights, err := json.Marshal(weights)
	if err != nil {
		return APIConsumerRateCard{}, err
	}
	row := s.pool.QueryRow(ctx,
		`insert into api_consumer_rate_cards
		       (account_id, app_id, currency, unit, price_millicents_per_unit, included_units_per_month, tiers, route_weights, plan_id, effective_from)
		 values ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::jsonb, $8::jsonb, nullif($9::text, '')::uuid, $10)
		 returning `+apiConsumerRateCardSelectCols,
		in.AccountID, in.AppID, in.Currency, APIConsumerRateCardUnitRequest, in.PriceMillicentsPerUnit, in.IncludedUnitsPerMonth, rawTiers, rawWeights, in.PlanID, in.EffectiveFrom)
	card, err := scanAPIConsumerRateCardRow(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return APIConsumerRateCard{}, ErrConflict
		}
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return APIConsumerRateCard{}, ErrNotFound // plan not in this app
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
