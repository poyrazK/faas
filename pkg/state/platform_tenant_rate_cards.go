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
	ListPlatformTenantRateCards(context.Context, string, string) ([]PlatformTenantRateCard, error)
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
