package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxAPIConsumerUsageStatementInt64 = int64(1<<63 - 1)

// APIConsumerUsageStatementStore is the persistence boundary for durable
// usage snapshots. It is intentionally optional, like the usage and rate-card
// capabilities, so existing Store test doubles remain source-compatible.
type APIConsumerUsageStatementStore interface {
	CreateAPIConsumerUsageStatement(context.Context, APIConsumerUsageStatementInput) (APIConsumerUsageStatement, bool, error)
	GetAPIConsumerUsageStatement(context.Context, string, string, string, string) (APIConsumerUsageStatement, error)
	ListAPIConsumerUsageStatements(context.Context, string, string, string) ([]APIConsumerUsageStatement, error)
	FinalizeAPIConsumerUsageStatement(context.Context, string, string, string, string) (APIConsumerUsageStatement, bool, error)
}

func validateAPIConsumerUsageStatementInput(input APIConsumerUsageStatementInput) error {
	for name, value := range map[string]string{
		"account_id": input.AccountID, "app_id": input.AppID, "consumer_id": input.ConsumerID,
	} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("consumer usage statement: %s must be a UUID: %w", name, err)
		}
	}
	if input.PeriodStart.IsZero() || input.PeriodEnd.IsZero() {
		return fmt.Errorf("consumer usage statement: period_start and period_end are required")
	}
	if !input.PeriodStart.Equal(input.PeriodStart.UTC().Truncate(time.Minute)) ||
		!input.PeriodEnd.Equal(input.PeriodEnd.UTC().Truncate(time.Minute)) {
		return fmt.Errorf("consumer usage statement: period bounds must be UTC minutes")
	}
	if !input.PeriodEnd.After(input.PeriodStart) {
		return fmt.Errorf("consumer usage statement: period_end must be after period_start")
	}
	if input.AsOf.IsZero() || !input.AsOf.Equal(input.AsOf.UTC()) {
		return fmt.Errorf("consumer usage statement: as_of must be a UTC timestamp")
	}
	if input.Currency != "" && !isUpperASCIICurrency(input.Currency) {
		return fmt.Errorf("consumer usage statement: currency must be an uppercase ISO-4217 code")
	}
	if input.BillableUnits < 0 || input.UnpricedUnits < 0 || input.AmountMillicents < 0 {
		return fmt.Errorf("consumer usage statement: totals must be non-negative")
	}
	var units, unpriced, amount int64
	for i, bucket := range input.Buckets {
		if bucket.WindowStart.IsZero() || !bucket.WindowStart.Equal(bucket.WindowStart.UTC().Truncate(time.Minute)) {
			return fmt.Errorf("consumer usage statement: bucket %d window_start must be a UTC minute", i)
		}
		if bucket.WindowStart.Before(input.PeriodStart) || !bucket.WindowStart.Before(input.PeriodEnd) {
			return fmt.Errorf("consumer usage statement: bucket %d is outside the period", i)
		}
		if bucket.BillableUnits < 0 || bucket.PriceMillicentsPerUnit < 0 || bucket.AmountMillicents < 0 {
			return fmt.Errorf("consumer usage statement: bucket %d values must be non-negative", i)
		}
		if bucket.RateCardID == "" {
			if bucket.Currency != "" || bucket.PriceMillicentsPerUnit != 0 || bucket.AmountMillicents != 0 {
				return fmt.Errorf("consumer usage statement: bucket %d unpriced bucket has pricing fields", i)
			}
			if unpriced > maxAPIConsumerUsageStatementInt64-bucket.BillableUnits {
				return fmt.Errorf("consumer usage statement: unpriced units overflow")
			}
			unpriced += bucket.BillableUnits
		} else {
			if _, err := uuid.Parse(bucket.RateCardID); err != nil {
				return fmt.Errorf("consumer usage statement: bucket %d rate_card_id must be a UUID: %w", i, err)
			}
			if !isUpperASCIICurrency(bucket.Currency) {
				return fmt.Errorf("consumer usage statement: bucket %d currency must be an uppercase ISO-4217 code", i)
			}
			if input.Currency == "" || bucket.Currency != input.Currency {
				return fmt.Errorf("consumer usage statement: bucket %d currency does not match statement currency", i)
			}
		}
		if units > maxAPIConsumerUsageStatementInt64-bucket.BillableUnits || amount > maxAPIConsumerUsageStatementInt64-bucket.AmountMillicents {
			return fmt.Errorf("consumer usage statement: bucket totals overflow")
		}
		units += bucket.BillableUnits
		amount += bucket.AmountMillicents
	}
	if units != input.BillableUnits || unpriced != input.UnpricedUnits || amount != input.AmountMillicents {
		return fmt.Errorf("consumer usage statement: bucket totals do not match statement totals")
	}
	return nil
}

func cloneAPIConsumerUsageStatement(statement APIConsumerUsageStatement) APIConsumerUsageStatement {
	statement.Buckets = append([]APIConsumerUsageStatementBucket(nil), statement.Buckets...)
	if statement.FinalizedAt != nil {
		finalizedAt := *statement.FinalizedAt
		statement.FinalizedAt = &finalizedAt
	}
	return statement
}

func normalizeStatementCurrency(currency string) string {
	return strings.ToUpper(strings.TrimSpace(currency))
}

func isUpperASCIICurrency(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for i := 0; i < len(currency); i++ {
		if currency[i] < 'A' || currency[i] > 'Z' {
			return false
		}
	}
	return true
}
