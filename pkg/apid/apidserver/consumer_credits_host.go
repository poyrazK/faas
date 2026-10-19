package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/consumercredits"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// consumerCreditsHost adapts *server to consumercredits.Host (ADR-982,
// ADR-1306).
type consumerCreditsHost struct{ s *server }

var _ consumercredits.Host = consumerCreditsHost{}

func (h consumerCreditsHost) Store() state.Store { return h.s.store }

func (h consumerCreditsHost) ConsumerRoute(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.APIConsumer, bool) {
	app, consumer, _, ok := h.s.apiConsumerUsageStatementStore(w, r, acct)
	return app, consumer, ok
}

func (h consumerCreditsHost) QuoteConsumerUsage(ctx context.Context, accountID, appID, consumerID string, since, until time.Time) (billing.APIConsumerUsageQuote, error) {
	return h.s.quoteConsumerUsage(ctx, accountID, appID, consumerID, since, until)
}

func (h consumerCreditsHost) Audit(ctx context.Context, event, accountID string, fields map[string]any) {
	h.s.audit.Emit(ctx, event, &accountID, fields)
}
