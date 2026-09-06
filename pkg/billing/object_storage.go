package billing

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// ObjectStorageLineItemSink is the provider adapter seam for a finalized
// period. Implementations should use record.ID as their external idempotency
// key and translate millicents into the provider's native currency unit.
// Keeping this optional interface separate from Provider avoids forcing every
// billing integration to support object-storage line items at once.
type ObjectStorageLineItemSink interface {
	PublishObjectStorageLineItem(context.Context, state.ObjectStorageBillingRecord) error
}

// FinalizeObjectStoragePeriod closes one completed UTC month and records the
// exact usage and rate card used to calculate its customer charge. The state
// store makes retries idempotent on (account, period); a changed rate card or
// usage snapshot cannot silently rewrite a finalized period.
//
// This function intentionally stops at a durable internal ledger. A concrete
// billing provider can consume the returned record through its own adapter
// without coupling object-storage accounting to Stripe, Paddle, Polar, or a
// particular storage backend.
func FinalizeObjectStoragePeriod(ctx context.Context, store state.ObjectStorageBillingStore, accountID string, pricing api.ObjectStoragePricing, periodStart, now time.Time) (state.ObjectStorageBillingRecord, error) {
	if store == nil || accountID == "" || !pricing.Valid() {
		return state.ObjectStorageBillingRecord{}, errors.New("billing: invalid object storage finalization request")
	}
	periodStart = state.ObjectStoragePeriod(periodStart)
	now = now.UTC()
	if !periodStart.Before(state.ObjectStoragePeriod(now)) {
		return state.ObjectStorageBillingRecord{}, state.ErrObjectBillingOpen
	}

	if existing, err := store.GetObjectStorageBillingPeriod(ctx, accountID, periodStart); err == nil {
		return existing, nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return state.ObjectStorageBillingRecord{}, err
	}

	snapshot, err := store.ObjectUsageForPeriod(ctx, accountID, periodStart)
	if err != nil {
		return state.ObjectStorageBillingRecord{}, err
	}
	usage, err := state.SummarizeObjectStorageBillingUsage(snapshot, periodStart)
	if err != nil {
		return state.ObjectStorageBillingRecord{}, err
	}
	charge, err := objectstorage.CalculateCharge(pricing, usage)
	if err != nil {
		return state.ObjectStorageBillingRecord{}, err
	}
	record := state.ObjectStorageBillingRecord{
		AccountID:                    accountID,
		PeriodStart:                  periodStart,
		PeriodEnd:                    periodStart.AddDate(0, 1, 0),
		Currency:                     charge.Currency,
		StoredByteHours:              usage.StoredByteHours,
		RequestCount:                 usage.RequestCount,
		EgressBytes:                  usage.EgressBytes,
		ProviderCostMillicents:       usage.CostMillicents,
		StorageMillicentsPerGiBMonth: pricing.StorageMillicentsPerGiBMonth,
		RequestsMillicentsPerMillion: pricing.RequestsMillicentsPerMillion,
		EgressMillicentsPerGiB:       pricing.EgressMillicentsPerGiB,
		StorageMillicents:            charge.StorageMillicents,
		RequestsMillicents:           charge.RequestsMillicents,
		EgressMillicents:             charge.EgressMillicents,
		TotalMillicents:              charge.TotalMillicents,
		FinalizedAt:                  now,
	}
	return store.RecordObjectStorageBillingPeriod(ctx, record)
}
