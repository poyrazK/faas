package state

import (
	"context"
	"fmt"
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
