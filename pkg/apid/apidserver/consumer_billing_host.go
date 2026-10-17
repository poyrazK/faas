package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/consumerbilling"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// consumerBillingHost adapts *server to consumerbilling.Host (ADR-982,
// ADR-1506).
type consumerBillingHost struct{ s *server }

var _ consumerbilling.Host = consumerBillingHost{}

func (h consumerBillingHost) Store() state.Store { return h.s.store }

func (h consumerBillingHost) QuoteConsumerUsage(ctx context.Context, accountID, appID, consumerID string, since, until time.Time) (billing.APIConsumerUsageQuote, error) {
	return h.s.quoteConsumerUsage(ctx, accountID, appID, consumerID, since, until)
}
