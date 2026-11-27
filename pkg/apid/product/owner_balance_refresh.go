package product

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

const ownerBalanceRefreshAction = "refresh_selected_balances"

type ownerBalanceRefreshInvoice struct {
	invoiceBatchStatement
	Period         string `json:"period"`
	RemainingMinor *int64 `json:"remaining_minor,omitempty"`
}

type ownerBalanceRefreshPage struct {
	Selection ownerBalanceSelection        `json:"selection"`
	Invoices  []ownerBalanceRefreshInvoice `json:"invoices"`
}

func (s *Service) prepareOwnerBalanceRefresh(w http.ResponseWriter, c *ownerContext, view *dashboard.ProductControlOutstandingBalances,
	selection ownerBalanceSelection, invoices []state.ProductOwnerBalanceInvoice) error {
	if s.RefreshInvoicePayment == nil || len(invoices) == 0 {
		return nil
	}
	// Bind the visible rows and filter to the form. A replay can still
	// check a paid invoice after it disappears from the current page.
	page := ownerBalanceRefreshPage{Selection: selection, Invoices: make([]ownerBalanceRefreshInvoice, 0, len(invoices))}
	for _, invoice := range invoices {
		item := invoice.Payment
		page.Invoices = append(page.Invoices, ownerBalanceRefreshInvoice{
			invoiceBatchStatement: invoiceBatchStatement{Tenant: item.TenantID, Document: item.DocumentID},
			Period:                item.Period, RemainingMinor: item.AmountRemainingMinor,
		})
	}
	payload := ownerPayload(page)
	token, err := s.ownerToken(w, c, ownerBalanceRefreshAction, ownerConfirmationSubject(c, payload))
	if err != nil {
		return err
	}
	view.RefreshPayload, view.RefreshToken = payload, token
	view.RefreshLimit = api.ProductControlBalanceRefreshBatchMax
	return nil
}

func (s *Service) OwnerRefreshSelectedBalances(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	if s.RefreshInvoicePayment == nil {
		s.ownerError(w, r, c, state.ErrNotFound)
		return
	}
	form, err := ownerParseFormFields(w, r, []string{"csrf_token", "payload", "statement"}, "statement", api.ProductControlOwnerFormMaxBytes)
	if err == nil {
		err = s.ownerVerify(r, c, ownerBalanceRefreshAction, ownerConfirmationSubject(c, form.Get("payload")))
	}
	var page ownerBalanceRefreshPage
	if err == nil {
		err = ownerDecodeReviewedPayload(form.Get("payload"), &page)
	}
	var invoices []ownerBalanceRefreshInvoice
	if err == nil {
		invoices, err = page.selectedInvoices(form["statement"])
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}

	results := s.refreshSelectedBalances(r.Context(), c, page.Selection.Currency, invoices)
	if err := s.loadOwnerBalances(w, r, c, page.Selection); err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	view := c.data.OutstandingBalances
	applyOwnerBalanceRefreshResults(view, results)
	s.OwnerRender(w, r, c.account, c.data)
}

func (p ownerBalanceRefreshPage) selectedInvoices(values []string) ([]ownerBalanceRefreshInvoice, error) {
	if !p.Selection.validDrilldown() {
		return nil, state.ErrInvalidArgument
	}
	statements, err := parseInvoiceBatchStatements(values, api.ProductControlBalanceRefreshBatchMax)
	if err != nil {
		return nil, err
	}
	visible := make(map[invoiceBatchStatement]ownerBalanceRefreshInvoice, len(p.Invoices))
	for _, row := range p.Invoices {
		visible[row.invoiceBatchStatement] = row
	}
	invoices := make([]ownerBalanceRefreshInvoice, 0, len(statements))
	for _, statement := range statements {
		invoice, ok := visible[statement]
		if !ok || !ownerClosedBillingPeriod(invoice.Period) {
			return nil, state.ErrInvalidArgument
		}
		invoices = append(invoices, invoice)
	}
	return invoices, nil
}

func (s *Service) refreshSelectedBalances(parent context.Context, c *ownerContext, currency string,
	invoices []ownerBalanceRefreshInvoice) []dashboard.ProductControlBalanceRefreshResult {
	// Each provider read has its own deadline and claim. A slow or failing
	// provider does not prevent the other selected invoices from refreshing.
	ctx, cancel := context.WithTimeout(parent, 35*time.Second)
	defer cancel()
	results := make([]dashboard.ProductControlBalanceRefreshResult, len(invoices))
	var group sync.WaitGroup
	for i, invoice := range invoices {
		group.Go(func() {
			results[i] = s.refreshSelectedBalance(ctx, c, currency, invoice)
		})
	}
	group.Wait()
	return results
}

func (s *Service) refreshSelectedBalance(ctx context.Context, c *ownerContext, currency string,
	invoice ownerBalanceRefreshInvoice) dashboard.ProductControlBalanceRefreshResult {
	scope := state.ProductBillingScope{AccountID: c.account.ID, AppID: c.app.ID, TenantID: invoice.Tenant, Period: invoice.Period}
	row := invoiceBatchRow(ctx, c, scope, invoice.Document)
	result := dashboard.ProductControlBalanceRefreshResult{
		DocumentID: invoice.Document, TenantID: invoice.Tenant, Name: row.Name, URL: row.URL, Balance: "Unknown",
	}
	if invoice.RemainingMinor != nil {
		result.Balance = ownerMinor(currency, *invoice.RemainingMinor)
	}
	refreshed, problem := s.RefreshInvoicePayment(ctx, c.account, scope, invoice.Document)
	if problem != nil {
		result.NeedsRefresh = true
		result.Status, result.Detail = "Needs refresh", problem.Detail
		if result.Detail == "" {
			result.Detail = problem.Title
		}
		return result
	}
	result.InvoiceID = refreshed.InvoiceID
	result.Balance = "Unknown"
	if refreshed.AmountRemainingMinor != nil {
		result.Balance = ownerMinor(refreshed.Currency, *refreshed.AmountRemainingMinor)
	}
	result.Status = ownerRefreshedBalanceStatus(refreshed, invoice.RemainingMinor)
	return result
}

func ownerRefreshedBalanceStatus(refreshed api.StatementInvoiceExportResponse, prior *int64) string {
	switch refreshed.PaymentStatus {
	case state.InvoicePaymentPaid:
		return "Paid"
	case state.InvoicePaymentVoid:
		return "Void"
	case state.InvoicePaymentDeleted:
		return "Deleted"
	case state.InvoicePaymentUncollectible:
		return "Uncollectible"
	case state.InvoicePaymentDraft:
		return "Draft"
	case state.InvoicePaymentOpen:
		if refreshed.AmountRemainingMinor != nil && (prior == nil || *refreshed.AmountRemainingMinor != *prior) {
			return "Balance updated"
		}
		return "Still open"
	}
	return "Checked"
}

func applyOwnerBalanceRefreshResults(view *dashboard.ProductControlOutstandingBalances, results []dashboard.ProductControlBalanceRefreshResult) {
	failed := map[string]int{}
	for i, result := range results {
		if result.NeedsRefresh {
			failed[result.DocumentID] = i
		}
	}
	for i := range view.Invoices.Items {
		row := &view.Invoices.Items[i]
		if index, ok := failed[row.DocumentID]; ok {
			row.NeedsRefresh = true
			results[index].Balance = row.Remaining
		}
	}
	view.RefreshResults = results
}
