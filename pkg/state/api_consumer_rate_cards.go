package state

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
)

// App consumer rate card units (ADR-1308): billable requests, or the
// compute time or response size of billable requests, rounded to the
// nearest whole second or megabyte per consumer minute.
const (
	APIConsumerRateCardUnitRequest          = "request"
	APIConsumerRateCardUnitComputeSecond    = "compute_second"
	APIConsumerRateCardUnitResponseMegabyte = "response_megabyte"
)

// ValidAPIConsumerRateCardUnit reports whether unit is a known card unit:
// request, compute_second, response_megabyte, or an app meter "app:<name>"
// (ADR-1318).
func ValidAPIConsumerRateCardUnit(unit string) bool {
	_, app := AppMeterOfUnit(unit)
	return unit == APIConsumerRateCardUnitRequest || unit == APIConsumerRateCardUnitComputeSecond ||
		unit == APIConsumerRateCardUnitResponseMegabyte || app
}

// Ladder modes (ADR-1309): graduated prices each unit at the step its
// position in the month falls in (ADR-951); volume prices every unit of the
// month at the step the month's total reaches.
const (
	RateCardTierModeGraduated = "graduated"
	RateCardTierModeVolume    = "volume"
)

// normalizeRateCardTierMode defaults an empty mode to graduated and rejects
// an unknown mode or a volume card without a ladder.
func normalizeRateCardTierMode(mode string, tiers []APIConsumerRateCardTier) (string, error) {
	switch mode {
	case "", RateCardTierModeGraduated:
		return RateCardTierModeGraduated, nil
	case RateCardTierModeVolume:
		if len(tiers) == 0 {
			return "", fmt.Errorf("%w: tier_mode volume needs tiers", ErrInvalidArgument)
		}
		return mode, nil
	}
	return "", fmt.Errorf("%w: tier_mode must be graduated or volume", ErrInvalidArgument)
}

// APIConsumerRateCardStore is deliberately narrower than Store. The pricing
// surface is additive, so existing test doubles that only implement the core
// state contract do not need to grow methods before they opt into pricing.
type APIConsumerRateCardStore interface {
	CreateAPIConsumerRateCard(context.Context, string, string, string, int64, time.Time) (APIConsumerRateCard, error)
	GetAPIConsumerRateCardByID(context.Context, string, string) (APIConsumerRateCard, error)
	ListAPIConsumerRateCardsForApp(context.Context, string, string) ([]APIConsumerRateCard, error)
	// CreateAPIConsumerRateCardVersion creates a card with an optional
	// monthly allowance (ADR-950) or graduated ladder (ADR-951);
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
	// Unit is what the card prices; empty means request (ADR-1308).
	Unit string
	// TierMode is how Tiers price a month; empty means graduated (ADR-1309).
	TierMode string
	// Monthly base fee and minimum spend per consumer (ADR-1316), prorated
	// in a consumer's first month when ProrateMonthlyFees is set (ADR-1325).
	BaseFeeMillicentsPerMonth int64
	MinimumMillicentsPerMonth int64
	ProrateMonthlyFees        bool
}

// normalizeAPIConsumerRateCardInput validates a version and derives the
// stored flat price for a ladder.
func normalizeAPIConsumerRateCardInput(in APIConsumerRateCardInput) (APIConsumerRateCardInput, error) {
	in.Currency = normalizeAPIConsumerRateCardCurrency(in.Currency)
	in.EffectiveFrom = in.EffectiveFrom.UTC()
	if in.Unit == "" {
		in.Unit = APIConsumerRateCardUnitRequest
	}
	if !ValidAPIConsumerRateCardUnit(in.Unit) || (in.Unit != APIConsumerRateCardUnitRequest && len(in.RouteWeights) > 0) {
		return in, fmt.Errorf("CreateAPIConsumerRateCard: %w: route weights apply to request cards only", ErrInvalidArgument)
	}
	mode, err := normalizeRateCardTierMode(in.TierMode, in.Tiers)
	if err != nil {
		return in, fmt.Errorf("CreateAPIConsumerRateCard: %w", err)
	}
	in.TierMode = mode
	if len(in.Tiers) > 0 {
		if err := ValidateAPIConsumerRateCardTiers(in.Tiers); err != nil {
			return in, fmt.Errorf("CreateAPIConsumerRateCard: %w", err)
		}
		if in.IncludedUnitsPerMonth != 0 {
			return in, fmt.Errorf("CreateAPIConsumerRateCard: tiers replace included_units_per_month")
		}
		in.PriceMillicentsPerUnit = in.Tiers[len(in.Tiers)-1].PriceMillicentsPerUnit
	}
	if in.IncludedUnitsPerMonth < 0 || in.BaseFeeMillicentsPerMonth < 0 || in.MinimumMillicentsPerMonth < 0 {
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
