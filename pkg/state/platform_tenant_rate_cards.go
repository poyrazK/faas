package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const PlatformTenantRateCardUnitRequest = "request"

// PlatformTenantRateCardStore owns append-only customer tariffs. Cards are
// tenant-wide: one effective price applies to attributed usage from every app.
type PlatformTenantRateCardStore interface {
	CreatePlatformTenantRateCard(context.Context, string, string, string, int64, time.Time) (PlatformTenantRateCard, error)
	// CreatePlatformTenantRateCardVersion creates a card with an optional
	// tenant-wide monthly allowance or graduated ladder (ADR-975).
	CreatePlatformTenantRateCardVersion(context.Context, PlatformTenantRateCardInput) (PlatformTenantRateCard, error)
	ListPlatformTenantRateCards(context.Context, string, string) ([]PlatformTenantRateCard, error)
}

// PlatformTenantRateCardInput is one immutable tenant price version. With
// Tiers set, the ladder prices usage, IncludedUnitsPerMonth must be zero, and
// PriceMillicentsPerUnit records the last step's price.
type PlatformTenantRateCardInput struct {
	AccountID              string
	TenantID               string
	Currency               string
	PriceMillicentsPerUnit int64
	IncludedUnitsPerMonth  int64
	Tiers                  []APIConsumerRateCardTier
	EffectiveFrom          time.Time
}

func normalizePlatformTenantRateCardInput(in PlatformTenantRateCardInput) (PlatformTenantRateCardInput, error) {
	const op = "CreatePlatformTenantRateCard"
	in.Currency = normalizePlatformTenantRateCardCurrency(in.Currency)
	in.EffectiveFrom = in.EffectiveFrom.UTC()
	if len(in.Tiers) > 0 {
		if err := ValidateAPIConsumerRateCardTiers(in.Tiers); err != nil {
			return in, fmt.Errorf("%s: %w: %w", op, ErrInvalidArgument, err)
		}
		if in.IncludedUnitsPerMonth != 0 {
			return in, fmt.Errorf("%s: %w: tiers replace included_units_per_month", op, ErrInvalidArgument)
		}
		in.PriceMillicentsPerUnit = in.Tiers[len(in.Tiers)-1].PriceMillicentsPerUnit
	}
	if in.IncludedUnitsPerMonth < 0 {
		return in, fmt.Errorf("%s: %w: included_units_per_month must be non-negative", op, ErrInvalidArgument)
	}
	if err := validatePlatformTenantRateCardInput(op, in.AccountID, in.TenantID, in.Currency, in.PriceMillicentsPerUnit, in.EffectiveFrom); err != nil {
		return in, err
	}
	in.Tiers = cloneRateCardTiers(in.Tiers)
	return in, nil
}

var (
	_ PlatformTenantRateCardStore = (*PgStore)(nil)
	_ PlatformTenantRateCardStore = (*MemStore)(nil)
)

func validatePlatformTenantRateCardInput(op, accountID, tenantID, currency string, price int64, effectiveFrom time.Time) error {
	if _, err := uuid.Parse(accountID); err != nil {
		return fmt.Errorf("%s: account_id must be a UUID: %w", op, err)
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return fmt.Errorf("%s: tenant_id must be a UUID: %w", op, err)
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
	if effectiveFrom.IsZero() || !effectiveFrom.Equal(effectiveFrom.UTC().Truncate(time.Minute)) {
		return fmt.Errorf("%s: effective_from must be a UTC minute", op)
	}
	return nil
}

func normalizePlatformTenantRateCardCurrency(currency string) string {
	return strings.ToUpper(strings.TrimSpace(currency))
}

func validPlatformTenantRateCardIDs(accountID, tenantID string) bool {
	_, accountErr := uuid.Parse(accountID)
	_, tenantErr := uuid.Parse(tenantID)
	return accountErr == nil && tenantErr == nil
}
