package apidserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

const consumerBillingProgressDays = 30

func (s *server) getAPIConsumerBillingProgress(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, consumer, _, ok := s.apiConsumerUsageStatementStore(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.APIConsumerBillingProgressStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("consumer billing progress is unavailable"))
		return
	}
	ctx := r.Context()
	progress, err := s.consumerBillingProgress(ctx, store, acct.ID, app.ID, consumer, time.Now().UTC())
	if err != nil {
		s.log.ErrorContext(ctx, "load consumer billing progress", "app_id", app.ID, "consumer_id", consumer.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not load consumer billing progress"))
		return
	}
	writeJSON(w, http.StatusOK, progress)
}

func (s *server) consumerBillingProgress(ctx context.Context, store state.APIConsumerBillingProgressStore,
	accountID, appID string, consumer state.APIConsumer, now time.Time) (api.APIConsumerBillingProgressResponse, error) {
	out := api.APIConsumerBillingProgressResponse{ConsumerID: consumer.ID, RegisteredAt: consumer.CreatedAt.UTC()}
	registeredMinute := consumer.CreatedAt.UTC().Truncate(time.Minute)
	until := now.UTC().Truncate(time.Minute)
	since := until.AddDate(0, 0, -consumerBillingProgressDays)
	if since.Before(registeredMinute) {
		since = registeredMinute
	}
	if until.Before(since) {
		until = since
	}
	usage, firstPriced, err := s.consumerBillingProgressUsage(ctx, accountID, appID, consumer.ID, since, until)
	if err != nil {
		return out, err
	}
	if since.After(registeredMinute) {
		// Measure signup activation over a fixed, bounded window, even
		// after the recent-usage view rolls past the customer's signup.
		_, firstPriced, err = s.consumerBillingProgressUsage(ctx, accountID, appID, consumer.ID,
			registeredMinute, registeredMinute.AddDate(0, 0, consumerBillingProgressDays))
		if err != nil {
			return out, err
		}
	}
	out.Usage, out.FirstPricedAt = usage, firstPriced
	record, err := store.GetAPIConsumerBillingRecord(ctx, accountID, appID, consumer.ID)
	if err != nil {
		return out, err
	}
	if record.StatementID != "" {
		out.Statement = &api.APIConsumerBillingStatementProgress{ID: record.StatementID, Status: string(record.StatementStatus),
			AmountDueMillicents: record.AmountDueMillicents, UnpricedUnits: record.UnpricedUnits}
	}
	if record.InvoiceID != "" {
		out.Invoice = &api.APIConsumerBillingInvoiceProgress{ID: record.InvoiceID, Provider: record.Provider,
			PaymentStatus: record.PaymentStatus, Livemode: record.Livemode, SentAt: record.SentAt}
		if record.ConfirmedPayment() {
			out.Invoice.PaidAt = record.PaidAt
		}
	}
	if record.LivePaid() {
		out.FirstLivePaidAt = record.PaidAt
	}
	return out, nil
}

func (s *server) consumerBillingProgressUsage(ctx context.Context, accountID, appID, consumerID string,
	since, until time.Time) (api.APIConsumerBillingUsageProgress, *time.Time, error) {
	out := api.APIConsumerBillingUsageProgress{Since: since, Until: until, Status: "waiting"}
	if !until.After(since) {
		return out, nil, nil
	}
	quote, ledger, err := s.quoteConsumerUsageWithLedger(ctx, accountID, appID, consumerID, since, until, false)
	mixedCurrency := errors.Is(err, billing.ErrMixedAPIConsumerRateCardCurrency)
	if err != nil && !mixedCurrency {
		return out, nil, err
	}
	for _, bucket := range ledger {
		if !bucket.WindowStart.Before(since) && bucket.WindowStart.Before(until) {
			out.RequestCount += bucket.RequestCount
		}
	}
	if mixedCurrency {
		out.Status = "mixed_currency"
		return out, nil, nil
	}
	out.UnpricedUnits, out.AmountMillicents, out.Currency = quote.UnpricedUnits, quote.AmountMillicents, quote.Currency
	switch {
	case quote.UnpricedUnits > 0:
		out.Status = "unpriced"
	case quote.AmountMillicents > 0:
		out.Status = "priced"
	case quote.BillableUnits > 0:
		out.Status = "included"
	case out.RequestCount > 0:
		out.Status = "not_billable"
	}
	var first *time.Time
	for _, bucket := range quote.Buckets {
		if bucket.AmountMillicents > 0 && (first == nil || bucket.WindowStart.Before(*first)) {
			minute := bucket.WindowStart.UTC()
			first = &minute
		}
	}
	return out, first, nil
}
