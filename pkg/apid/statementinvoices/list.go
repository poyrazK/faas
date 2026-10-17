package statementinvoices

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// ListInvoices pages the account's exported invoices, newest first, with
// their payment status (ADR-1504). status filters by a payment status, or
// unpaid or overdue.
func (s *Service) ListInvoices(w http.ResponseWriter, r *http.Request, acct state.Account) {
	payments, ok := s.Host.Store().(state.StatementInvoicePaymentStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("statement invoices are unavailable"))
		return
	}
	q := r.URL.Query()
	filter := q.Get("status")
	if !state.ValidInvoiceFilter(filter) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad status",
			"status must be unpaid, overdue, draft, open, paid, void, uncollectible or deleted"))
		return
	}
	problem, limit := api.ParseLimit(q.Get("limit"), api.StatementInvoiceListDefault, api.StatementInvoiceListMax, "statement invoices")
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	invoices, err := payments.ListStatementInvoices(r.Context(), acct.ID, filter, s.now(), q.Get("cursor"), limit)
	if errors.Is(err, state.ErrInvalidArgument) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", "invalid status or cursor"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list statement invoices"))
		return
	}
	out := api.StatementInvoiceListResponse{Invoices: make([]api.StatementInvoiceExportResponse, 0, len(invoices))}
	for _, invoice := range invoices {
		out.Invoices = append(out.Invoices, statementInvoiceExportResponse(invoice))
	}
	if len(invoices) == limit {
		out.NextCursor = state.InvoiceCursor(invoices[len(invoices)-1])
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
