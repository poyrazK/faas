package meter

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// MonthToDateSpendCents is what the account's month so far is billed beyond
// its plan: the compute overage, the live egress overage and the
// object-storage charge, the sum the spend cap compares
// (recordSpendCapHold). The account_spend_eur alert metric and the
// meterd_account_spend_eur gauge read it (ADR-20261109120000): reading
// compute alone left a customer whose bill was mostly egress or storage
// unwarned. A nil provider bills no egress.
func MonthToDateSpendCents(ctx context.Context, store state.Store, provider billing.Provider, acct state.Account, now time.Time) (int64, error) {
	compute, err := store.CurrentMonthOverageCents(ctx, acct.ID)
	if err != nil {
		return 0, fmt.Errorf("month spend: compute overage: %w", err)
	}
	egress, err := liveMeterOverageCents(ctx, store, provider, acct, now, state.BillingMeterEgress)
	if err != nil {
		return 0, fmt.Errorf("month spend: egress overage: %w", err)
	}
	storage, err := SpendCapObjectStorageCents(ctx, store, acct.ID, now)
	if err != nil {
		return 0, fmt.Errorf("month spend: object storage: %w", err)
	}
	return saturatedAdd(saturatedAdd(compute, egress), storage), nil
}
