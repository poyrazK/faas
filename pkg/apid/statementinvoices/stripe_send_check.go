package statementinvoices

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// CheckProductStripeInvoiceSend resolves an expired send using Stripe's
// event history. The provider operations in this path are read-only.
func (s *Service) CheckProductStripeInvoiceSend(parent context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID string) (api.ProductStripeInvoiceSendCheck, *api.Problem) {
	var empty api.ProductStripeInvoiceSendCheck
	issue, problem := s.stripeIssueContext(parent, acct, scope, documentID)
	if problem != nil {
		return empty, problem
	}
	token := uuid.NewString()
	_, err := issue.exports.ClaimStatementInvoiceExport(parent, acct.ID, issue.export.ID, token, time.Now().Add(paymentRefreshLease))
	if errors.Is(err, state.ErrStatementInvoiceExportInProgress) {
		return empty, exportInProgress()
	}
	if err != nil {
		return empty, api.ErrInternal("could not claim the Stripe invoice")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancel()
		_ = issue.exports.ReleaseStatementInvoiceExport(ctx, acct.ID, issue.export.ID, token)
	}()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), paymentRefreshTimeout)
	defer cancel()
	return s.checkStripeSend(ctx, issue, token)
}

func (s *Service) checkStripeSend(ctx context.Context, issue stripeInvoiceIssue, token string) (api.ProductStripeInvoiceSendCheck, *api.Problem) {
	var empty api.ProductStripeInvoiceSendCheck
	attempt, problem := issue.attempt(ctx)
	if problem != nil {
		return empty, problem
	}
	invoiceID := issue.export.ProviderInvoiceID
	if attempt.Stage == state.StripeInvoiceSent {
		return api.ProductStripeInvoiceSendCheck{InvoiceID: invoiceID, EventID: attempt.SendEventID, EventAt: attempt.SendEventAt}, nil
	}
	now := s.now()
	retryBefore := now.Add(-api.ProductStripeInvoiceSendRetryWindow)
	if attempt.Stage != state.StripeInvoiceSending || attempt.SendStartedAt.After(retryBefore) {
		return empty, exportConflict("Check Stripe send is available after an unconfirmed send's retry window ends.")
	}

	// Events have second precision. Limit the query to Stripe's retention.
	historyStart := now.Add(-stripeinvoice.InvoiceSendEventLookback).Truncate(time.Second)
	sendStart := attempt.SendStartedAt.Truncate(time.Second)
	from := sendStart
	if from.Before(historyStart) {
		from = historyStart
	}
	event, err := issue.client.FindInvoiceSendEvent(ctx, issue.conn.key, invoiceID, issue.export.Livemode, from, now)
	switch {
	case errors.Is(err, stripeinvoice.ErrKeyRejected):
		return empty, exportConflict("Stripe Events access is unavailable. Check the invoice in Stripe or reconnect with read access to Events.")
	case errors.Is(err, stripeinvoice.ErrInvoiceSendEventSearchLimit):
		return empty, exportConflict("The Stripe event check reached its search limit. Review the invoice's send history in Stripe.")
	case err != nil:
		return empty, providerProblem(http.StatusBadGateway, "Stripe event check unavailable", "Stripe's event history is temporarily unavailable. Try again or review the invoice in Stripe.")
	case event.ID == "" && sendStart.Before(historyStart):
		return empty, exportConflict("No matching event appeared in Stripe's last 30 days of history. Review the invoice in Stripe.")
	case event.ID == "":
		return empty, exportConflict("Stripe returned no matching send event. Review the invoice in Stripe.")
	}

	if err := issue.sends.VerifyProductStripeInvoiceSend(ctx, issue.export.AccountID, issue.export.ID, token, event.ID, event.CreatedAt, retryBefore); err != nil {
		return empty, api.ErrInternal("could not save the verified Stripe send")
	}
	s.Host.Audit(ctx, "statement_invoice.stripe_send_verified", issue.export.AccountID, map[string]any{
		"export_id": issue.export.ID, "statement_id": issue.export.StatementID, "invoice_id": invoiceID,
		"stripe_event_id": event.ID, "stripe_event_at": event.CreatedAt, "livemode": issue.export.Livemode,
	})
	if payments, ok := s.Host.Store().(state.StatementInvoicePaymentStore); ok {
		s.refreshStripeInvoicePayment(ctx, payments, issue)
	}
	return api.ProductStripeInvoiceSendCheck{InvoiceID: invoiceID, EventID: event.ID, EventAt: event.CreatedAt}, nil
}
