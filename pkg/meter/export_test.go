package meter

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// RecordFinancialPricingForTest records every billing subject's price terms
// at now, as the loop does before each sampling minute (ADR-566).
func RecordFinancialPricingForTest(ctx context.Context, store state.Store, provider billing.Provider, now time.Time) error {
	l := &Loop{store: store, pusher: provider}
	return l.recordFinancialPricing(ctx, now)
}
