package statementinvoices

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// RefreshStripePayment checks the current Stripe balance for one account-owned invoice.
func (s *Service) RefreshStripePayment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	exports, ok := s.statementInvoiceStore(w, acct)
	if !ok {
		return
	}
	payments, ok := s.Host.Store().(state.StatementInvoicePaymentStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("invoice payment status is unavailable"))
		return
	}
	statement, err := uuid.Parse(r.PathValue("statement_id"))
	if err != nil {
		httpkit.NotFound(w, "no such statement invoice")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	export, err := exports.GetStatementInvoiceExport(ctx, acct.ID, state.StatementSourceAppConsumer, statement.String())
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no such statement invoice")
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not load the statement invoice"))
	default:
		refreshed, problem := s.refreshStripePayment(ctx, exports, payments, export)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, statementInvoiceExportResponse(refreshed))
	}
}

func (s *Service) refreshStripePayment(ctx context.Context, exports state.StatementInvoiceExportStore,
	payments state.StatementInvoicePaymentStore, export state.StatementInvoiceExport) (state.StatementInvoiceExport, *api.Problem) {
	if export.Provider != state.BillingProviderStripe || export.Status != state.StatementInvoiceExportCompleted ||
		!stripeInvoiceID.MatchString(export.ProviderInvoiceID) {
		return export, exportConflict("a completed Stripe invoice is required to check the payment")
	}
	token := uuid.NewString()
	current, err := exports.ClaimStatementInvoiceExport(ctx, export.AccountID, export.ID, token, time.Now().Add(api.StatementInvoiceExportClaimTTL))
	if errors.Is(err, state.ErrStatementInvoiceExportInProgress) {
		return export, exportConflict("this invoice is being updated; try again shortly")
	}
	if err != nil {
		return export, api.ErrInternal("could not claim the Stripe invoice")
	}
	defer func() {
		_ = exports.ReleaseStatementInvoiceExport(context.WithoutCancel(ctx), export.AccountID, export.ID, token)
	}()
	client := s.stripeInvoiceClient()
	conn, problem := s.openConnection(ctx, exports, current.AccountID, state.BillingProviderStripe, stripeProvider{client: client})
	if problem != nil {
		return current, problem
	}
	if conn.livemode != current.Livemode {
		return current, draftModeConflict()
	}
	invoice, err := client.GetInvoice(ctx, conn.key, current.ProviderInvoiceID)
	if errors.Is(err, stripeinvoice.ErrKeyRejected) {
		return current, providerProblem(http.StatusUnprocessableEntity, "Stripe key rejected",
			"reconnect Stripe with a key that can read invoices")
	}
	if err != nil {
		return current, providerProblem(http.StatusBadGateway, "Could not check Stripe", "try again to check the current balance")
	}
	// A missing invoice can also mean a different Stripe account is now connected.
	if invoice.Deleted || invoice.ID != current.ProviderInvoiceID || invoice.Livemode == nil ||
		*invoice.Livemode != current.Livemode || invoice.Customer != current.Customer ||
		!strings.EqualFold(invoice.Currency, current.Currency) {
		return current, exportConflict("the Stripe invoice could not be matched to this statement")
	}
	payment, known, err := stripeInvoicePayment(invoice)
	if err != nil || !known {
		return current, providerProblem(http.StatusBadGateway, "Could not read the Stripe balance", "try again to check the current balance")
	}
	if stripeIssueInvoiceMatches(current, invoice) && (payment.Status == state.InvoicePaymentPaid || payment.AmountPaidMinor > 0) {
		_, _ = s.reconcileStripeInvoiceSend(ctx, current, token, conn.key)
	}
	if err := s.recordInvoicePayment(ctx, payments, current, payment, s.now()); err != nil {
		return current, api.ErrInternal("could not save the Stripe payment status")
	}
	refreshed, err := exports.GetStatementInvoiceExport(ctx, current.AccountID, current.SourceKind, current.StatementID)
	if err != nil {
		return current, api.ErrInternal("could not reload the Stripe payment status")
	}
	return refreshed, nil
}
