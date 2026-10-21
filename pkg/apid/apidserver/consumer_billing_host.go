package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/consumerbilling"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// consumerBillingHost adapts *server to consumerbilling.Host (ADR-982,
// ADR-1506).
type consumerBillingHost struct{ s *server }

var _ consumerbilling.Host = consumerBillingHost{}

func (h consumerBillingHost) Store() state.Store { return h.s.store }

func (h consumerBillingHost) Released(accountID string) bool {
	return h.s.apiMonetizationFor(accountID)
}

func (h consumerBillingHost) QuoteConsumerUsage(ctx context.Context, accountID, appID, consumerID string, since, until time.Time) (billing.APIConsumerUsageQuote, error) {
	return h.s.quoteConsumerUsage(ctx, accountID, appID, consumerID, since, until)
}

func (h consumerBillingHost) OfferedPlans(ctx context.Context, app state.App, settings state.APIConsumerSignupSettings) ([]api.ConsumerSignupPlan, error) {
	return h.s.consumerSignup.OfferedPlans(ctx, app, settings)
}

func (h consumerBillingHost) StripeKey(ctx context.Context, accountID string) (string, bool, *api.Problem) {
	return h.s.statementInvoices.StripeKey(ctx, accountID)
}

func (h consumerBillingHost) Audit(ctx context.Context, event, accountID string, fields map[string]any) {
	h.s.audit.Emit(ctx, event, &accountID, fields)
}
