package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) postInvoiceHistoryBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	query, queryErr := url.ParseQuery(r.URL.RawQuery)
	limit, cursor, err := parseInvoiceHistoryBackfillQuery(query)
	if queryErr != nil {
		err = queryErr
	}
	if err != nil || r.ContentLength != 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid invoice history backfill", "Provide only an optional cursor and page limit; the request has no body."))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.InvoiceHistoryTimeout)
	defer cancel()
	response, err := s.backfillInvoiceHistory(ctx, acct, cursor, limit)
	if err != nil {
		s.log.ErrorContext(ctx, "invoice history backfill failed", "account", acct.ID, "err", err)
		api.WriteProblem(w, invoiceHistoryBackfillProblem(err))
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, response)
}

func parseInvoiceHistoryBackfillQuery(query url.Values) (int, string, error) {
	for key, values := range query {
		if (key != "cursor" && key != "limit") || len(values) != 1 {
			return 0, "", errors.New("invalid invoice history query")
		}
	}
	limit := api.MaxInvoiceHistoryPageSize
	if values, exists := query["limit"]; exists {
		raw := values[0]
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > api.MaxInvoiceHistoryPageSize {
			return 0, "", errors.New("invalid invoice history limit")
		}
		limit = parsed
	}
	cursor := query.Get("cursor")
	if len(cursor) > 2048 {
		return 0, "", billing.ErrInvoiceHistoryCursor
	}
	return limit, cursor, nil
}

func (s *server) backfillInvoiceHistory(ctx context.Context, acct state.Account, cursor string, limit int) (api.InvoiceHistoryBackfillResponse, error) {
	var response api.InvoiceHistoryBackfillResponse
	if err := billing.ValidateInvoiceHistoryLimit(limit); err != nil {
		return response, err
	}
	provider := providerName(s.billingProvider)
	var reader billing.InvoiceHistoryReader
	if s.billingProvider == nil && s.billingProviderName == "stripe" {
		provider, reader = "stripe", nil
		reader, _ = s.legacyStripeInvoiceReader.(billing.InvoiceHistoryReader)
	} else if s.billingProvider != nil {
		reader, _ = s.billingProvider.(billing.InvoiceHistoryReader)
	}
	if reader == nil || provider == "" || !s.billingMode.Effective().Enabled() {
		return response, billing.ErrNotImplemented
	}
	if s.billingProvider == nil {
		var err error
		acct, err = s.accountForBillingProvider(ctx, acct, provider)
		if err != nil {
			return response, err
		}
	} else {
		var err error
		acct, err = s.accountForActiveBillingProvider(ctx, acct)
		if err != nil {
			return response, err
		}
	}
	if acct.ProviderCustomerID == "" {
		return response, billing.ErrInvoiceSourceMismatch
	}
	page, err := reader.FetchInvoiceHistory(ctx, acct, cursor, limit)
	if err != nil {
		return response, err
	}
	if err := billing.ValidateInvoiceHistoryPage(page, provider, limit); err != nil {
		return response, err
	}
	imported, err := s.store.ImportInvoiceHistory(ctx, acct.ID, provider, page.Invoices)
	if err != nil {
		return response, err
	}
	response = api.InvoiceHistoryBackfillResponse{
		Provider: provider, Scanned: page.Scanned, Imported: imported,
		Skipped: page.Scanned - imported, HasMore: page.HasMore, NextCursor: page.NextCursor,
	}
	return response, nil
}

func invoiceHistoryBackfillProblem(err error) *api.Problem {
	switch {
	case errors.Is(err, billing.ErrInvoiceHistoryCursor):
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid invoice history cursor", "The cursor is malformed or belongs to another provider customer.")
	case errors.Is(err, billing.ErrInvoiceSourceMismatch):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Invoice provider identity mismatch", "Provider history could not be verified for this account.")
	case errors.Is(err, billing.ErrNotImplemented):
		return api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented, "Invoice history backfill unavailable", "The active billing provider does not support invoice history discovery.")
	default:
		return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Invoice history backfill unavailable", "Provider history could not be fetched or imported; this page was not partially committed.")
	}
}
