package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/httpjson"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) postInvoiceRefresh(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if r.ContentLength != 0 || r.URL.RawQuery != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid invoice refresh", "This operation accepts no body or query parameters."))
		return
	}
	if _, err := uuid.Parse(r.PathValue("id")); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid invoice ID", "Invoice ID must be a UUID from invoice history."))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.InvoiceRefreshTimeout)
	defer cancel()
	inv, err := s.refreshInvoiceFacts(ctx, acct, r.PathValue("id"))
	if err != nil {
		s.log.ErrorContext(ctx, "invoice refresh failed", "account", acct.ID, "err", err)
		api.WriteProblem(w, invoiceRefreshProblem(err))
		return
	}
	count := 0
	if inv.Details != nil && inv.Details.Lines != nil {
		count = len(inv.Details.Lines.Items)
	}
	gap := state.InvoiceLineGap(inv)
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, api.InvoiceRefreshResponse{InvoiceID: inv.ID, Provider: inv.Provider, Detailed: gap == "", LineItems: count, SourceGap: gap, UpdatedAt: inv.UpdatedAt})
}

func (s *server) refreshInvoiceFacts(ctx context.Context, acct state.Account, id string) (state.Invoice, error) {
	inv, err := s.store.GetInvoiceByID(ctx, id)
	if err != nil || inv.AccountID != acct.ID {
		if err == nil {
			err = state.ErrNotFound
		}
		return state.Invoice{}, err
	}
	reader, _ := s.billingProvider.(billing.InvoiceDetailsReader)
	provider := providerName(s.billingProvider)
	if s.billingProvider == nil && s.billingProviderName == "stripe" {
		reader, provider = s.legacyStripeInvoiceReader, "stripe"
	}
	if reader == nil || !s.billingMode.Effective().Enabled() {
		return state.Invoice{}, billing.ErrNotImplemented
	}
	if inv.Provider != provider {
		return state.Invoice{}, billing.ErrInvoiceSourceMismatch
	}
	if s.billingProvider == nil {
		acct, err = s.accountForBillingProvider(ctx, acct, provider)
	} else {
		acct, err = s.accountForActiveBillingProvider(ctx, acct)
	}
	if err != nil {
		return state.Invoice{}, err
	}
	details, err := reader.FetchInvoiceDetails(ctx, acct, inv)
	if err != nil {
		return state.Invoice{}, err
	}
	if err := billing.ValidateRefreshedDetails(details); err != nil {
		return state.Invoice{}, err
	}
	return s.store.RefreshInvoiceDetails(ctx, acct.ID, inv.ID, inv.UpdatedAt, details)
}

func invoiceRefreshProblem(err error) *api.Problem {
	var limit *billing.InvoiceRefreshLimitError
	switch {
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Invoice not found", "No invoice belongs to this account with that ID.")
	case errors.Is(err, state.ErrConflict), errors.Is(err, billing.ErrInvoiceSourceMismatch):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Invoice refresh conflict", "The invoice changed or the provider identity/amounts differ; reconcile provider deliveries and retry.")
	case errors.Is(err, billing.ErrNotImplemented):
		return api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented, "Invoice refresh unavailable", "The configured billing provider does not support invoice refresh.")
	case errors.As(err, &limit):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invoice refresh limit exceeded", limit.Error()).WithLimit(int64(limit.Limit), int64(limit.Observed)).WithDocs("https://gregale.dev/docs/billing#refresh-invoice-facts")
	case errors.Is(err, httpjson.ErrResponseTooLarge):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invoice refresh limit exceeded", "Provider invoice response is too large.").WithByteLimit(api.MaxInvoiceProviderResponseBytes, api.MaxInvoiceProviderResponseBytes+1).WithDocs("https://gregale.dev/docs/billing#refresh-invoice-facts")
	default:
		return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Invoice refresh unavailable", "Provider facts could not be fetched or persisted; the invoice was not refreshed.")
	}
}
