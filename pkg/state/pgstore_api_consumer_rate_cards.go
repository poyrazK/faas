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
       price_millicents_per_unit, included_units_per_month, tiers, route_weights, coalesce(plan_id::text, ''), effective_from, created_at, tier_mode,
       base_fee_millicents_per_month, minimum_millicents_per_month, prorate_monthly_fees, model_prices`

type apiConsumerRateCardRowScanner interface {
	Scan(dest ...any) error
}

func scanAPIConsumerRateCardRow(row apiConsumerRateCardRowScanner) (APIConsumerRateCard, error) {
	var card APIConsumerRateCard
	var tiers, weights, modelPrices []byte
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
		&card.TierMode,
		&card.BaseFeeMillicentsPerMonth,
		&card.MinimumMillicentsPerMonth,
		&card.ProrateMonthlyFees,
		&modelPrices,
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
	if err := json.Unmarshal(modelPrices, &card.ModelPrices); err != nil {
		return APIConsumerRateCard{}, err
	}
	card.ModelPrices = cloneRateCardValues(card.ModelPrices)
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
	modelPrices := in.ModelPrices
	if modelPrices == nil {
		modelPrices = map[string]int64{}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumerRateCard{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, appMeter := AppMeterOfUnit(in.Unit); appMeter {
		// Serialize names reserved by model cards with other app-meter cards.
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('consumer-meter:' || $1::text || ':' || $2::text, 0))`, in.AccountID, in.AppID); err != nil {
			return APIConsumerRateCard{}, err
		}
		if err := checkModelMeterHistory(ctx, tx, in); err != nil {
			return APIConsumerRateCard{}, err
		}
	}
	row := tx.QueryRow(ctx,
		`insert into api_consumer_rate_cards
		       (account_id, app_id, currency, unit, price_millicents_per_unit, included_units_per_month, tiers, route_weights, plan_id, effective_from, tier_mode,
		        base_fee_millicents_per_month, minimum_millicents_per_month, prorate_monthly_fees, model_prices)
		 values ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::jsonb, $8::jsonb, nullif($9::text, '')::uuid, $10, $11, $12, $13, $14, $15::jsonb)
		 returning `+apiConsumerRateCardSelectCols,
		in.AccountID, in.AppID, in.Currency, in.Unit, in.PriceMillicentsPerUnit, in.IncludedUnitsPerMonth, rawTiers, rawWeights, in.PlanID, in.EffectiveFrom, in.TierMode,
		in.BaseFeeMillicentsPerMonth, in.MinimumMillicentsPerMonth, in.ProrateMonthlyFees, modelPrices)
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
	return card, tx.Commit(ctx)
}

func checkModelMeterHistory(ctx context.Context, tx pgx.Tx, in APIConsumerRateCardInput) error {
	rows, err := tx.Query(ctx, `select distinct unit, model_prices from api_consumer_rate_cards
		where account_id = $1::uuid and app_id = $2::uuid and unit like 'app:%' and unit <> $3`, in.AccountID, in.AppID, in.Unit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var unit string
		var prices map[string]int64
		if err := rows.Scan(&unit, &prices); err != nil {
			return err
		}
		if err := CheckModelMeterOverlap(in.Unit, in.ModelPrices, unit, prices); err != nil {
			return err
		}
	}
	return rows.Err()
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
