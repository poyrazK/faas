package state

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
)

const APIConsumerRateCardUnitRequest = "request"

// APIConsumerRateCardStore is deliberately narrower than Store. The pricing
// surface is additive, so existing test doubles that only implement the core
// state contract do not need to grow methods before they opt into pricing.
type APIConsumerRateCardStore interface {
	CreateAPIConsumerRateCard(context.Context, string, string, string, int64, time.Time) (APIConsumerRateCard, error)
	GetAPIConsumerRateCardByID(context.Context, string, string) (APIConsumerRateCard, error)
	ListAPIConsumerRateCardsForApp(context.Context, string, string) ([]APIConsumerRateCard, error)
	// CreateAPIConsumerRateCardVersion creates a card with an optional
	// monthly allowance (ADR-971) or graduated ladder (ADR-972);
	// CreateAPIConsumerRateCard creates a single flat price.
	CreateAPIConsumerRateCardVersion(context.Context, APIConsumerRateCardInput) (APIConsumerRateCard, error)
}

// APIConsumerRateCardInput is one immutable price version to create. With
// Tiers set, the ladder prices usage, IncludedUnitsPerMonth must be zero, and
// PriceMillicentsPerUnit records the last step's price.
type APIConsumerRateCardInput struct {
	AccountID              string
	AppID                  string
	Currency               string
	PriceMillicentsPerUnit int64
	IncludedUnitsPerMonth  int64
	Tiers                  []APIConsumerRateCardTier
	RouteWeights           map[string]int64
	PlanID                 string
	EffectiveFrom          time.Time
}

// normalizeAPIConsumerRateCardInput validates a version and derives the
// stored flat price for a ladder.
func normalizeAPIConsumerRateCardInput(in APIConsumerRateCardInput) (APIConsumerRateCardInput, error) {
	in.Currency = normalizeAPIConsumerRateCardCurrency(in.Currency)
	in.EffectiveFrom = in.EffectiveFrom.UTC()
	if len(in.Tiers) > 0 {
		if err := ValidateAPIConsumerRateCardTiers(in.Tiers); err != nil {
			return in, fmt.Errorf("CreateAPIConsumerRateCard: %w", err)
		}
		if in.IncludedUnitsPerMonth != 0 {
			return in, fmt.Errorf("CreateAPIConsumerRateCard: tiers replace included_units_per_month")
		}
		in.PriceMillicentsPerUnit = in.Tiers[len(in.Tiers)-1].PriceMillicentsPerUnit
	}
	if in.IncludedUnitsPerMonth < 0 {
		return in, ErrInvalidArgument
	}
	if err := validateAPIConsumerRateCardInput("CreateAPIConsumerRateCard", in.AccountID, in.AppID, in.Currency, in.PriceMillicentsPerUnit, in.EffectiveFrom); err != nil {
		return in, err
	}
	if err := ValidateAPIConsumerRouteWeights(in.RouteWeights); err != nil {
		return in, fmt.Errorf("CreateAPIConsumerRateCard: %w", err)
	}
	if in.PlanID != "" {
		if _, err := uuid.Parse(in.PlanID); err != nil {
			return in, fmt.Errorf("CreateAPIConsumerRateCard: plan_id must be a UUID: %w", err)
		}
	}
	in.Tiers = cloneRateCardTiers(in.Tiers)
	in.RouteWeights = cloneRouteWeights(in.RouteWeights)
	return in, nil
}

func cloneRouteWeights(weights map[string]int64) map[string]int64 {
	if len(weights) == 0 {
		return nil
	}
	return maps.Clone(weights)
}

func cloneRateCardTiers(tiers []APIConsumerRateCardTier) []APIConsumerRateCardTier {
	if len(tiers) == 0 {
		return nil
	}
	out := make([]APIConsumerRateCardTier, len(tiers))
	for i, tier := range tiers {
		out[i].PriceMillicentsPerUnit = tier.PriceMillicentsPerUnit
		if tier.UpTo != nil {
			upTo := *tier.UpTo
			out[i].UpTo = &upTo
		}
	}
	return out
}

func validateAPIConsumerRateCardInput(op, accountID, appID, currency string, price int64, effectiveFrom time.Time) error {
	if _, err := uuid.Parse(accountID); err != nil {
		return fmt.Errorf("%s: account_id must be a UUID: %w", op, err)
	}
	if _, err := uuid.Parse(appID); err != nil {
		return fmt.Errorf("%s: app_id must be a UUID: %w", op, err)
	}
	if len(currency) != 3 {
		return fmt.Errorf("%s: currency must be a three-letter ISO code", op)
	}
	for i := 0; i < len(currency); i++ {
		if currency[i] < 'A' || currency[i] > 'Z' {
			return fmt.Errorf("%s: currency must contain uppercase ASCII letters", op)
		}
	}
	if price < 0 {
		return fmt.Errorf("%s: price_millicents_per_unit must be non-negative", op)
	}
	if effectiveFrom.IsZero() {
		return fmt.Errorf("%s: effective_from is required", op)
	}
	if !effectiveFrom.Equal(effectiveFrom.UTC().Truncate(time.Minute)) {
		return fmt.Errorf("%s: effective_from must be a UTC minute", op)
	}
	return nil
}

func normalizeAPIConsumerRateCardCurrency(currency string) string {
	return strings.ToUpper(strings.TrimSpace(currency))
}
