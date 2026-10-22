package apidserver

import (
	"context"
	"errors"
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

func (h consumerCreditsHost) TenantRoute(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, bool) {
	tenants, ok := h.s.platformTenantStore(w, acct)
	if !ok {
		return state.PlatformTenant{}, false
	}
	return h.s.platformTenantByPath(w, r, acct, tenants)
}

func (h consumerCreditsHost) QuoteTenantMonthUsage(ctx context.Context, accountID, tenantID string, now time.Time) (string, int64, error) {
	return h.s.quoteTenantMonthUsage(ctx, accountID, tenantID, now)
}

// quoteTenantMonthUsage is a tenant's usage charge for the month to date:
// what finalized revisions billed plus the unbilled usage priced now, less
// monthly fees, which spend caps do not count (ADR-20261019100331).
func (s *server) quoteTenantMonthUsage(ctx context.Context, accountID, tenantID string, now time.Time) (string, int64, error) {
	statements, ok := s.store.(state.PlatformTenantStatementStore)
	if !ok {
		return "", 0, state.ErrNotFound
	}
	start := billing.MonthStart(now)
	end := start.AddDate(0, 1, 0)
	revisions, err := statements.ListPlatformTenantStatements(ctx, accountID, tenantID, start, end)
	if err != nil {
		return "", 0, err
	}
	currency, usage := "", int64(0)
	for _, revision := range revisions {
		if revision.Status == state.APIConsumerUsageStatementFinalized && revision.PeriodStart.Equal(start) && revision.PeriodEnd.Equal(end) {
			currency, usage = revision.Currency, usage+revision.AmountMillicents-feeTotal(revision.Fees)
		}
	}
	plan, err := statements.PlanPlatformTenantStatement(ctx, accountID, tenantID, start, end)
	if err != nil {
		return "", 0, err
	}
	revision, prior := 1, state.APIConsumerUsageStatementStatus("")
	if plan.HasLatest {
		revision, prior = plan.Latest.Revision+1, plan.Latest.Status
	}
	input, err := s.quotePlatformTenantStatement(ctx, accountID, tenantID, start, end, revision, prior, plan.UsageDelta)
	if errors.Is(err, billing.ErrNoNewTenantUsage) {
		return currency, usage, nil
	}
	if err != nil {
		return "", 0, err
	}
	if input.Currency != "" {
		currency = input.Currency
	}
	return currency, usage + input.AmountMillicents - feeTotal(input.Fees), nil
}

func feeTotal(fees []state.APIConsumerStatementFee) int64 {
	var total int64
	for _, fee := range fees {
		total += fee.AmountMillicents
	}
	return total
}
