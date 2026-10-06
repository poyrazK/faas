package main

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getFinancialCosts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	now := time.Now().UTC()
	start, problem := financialMonth(r, now)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	report, err := billing.ReadFinancialCosts(r.Context(), s.store, acct.ID, start, now)
	if err != nil {
		api.WriteProblem(w, financialProblem(err))
		return
	}
	invoices, err := s.store.ListInvoicesForAccount(r.Context(), acct.ID, &start, time.Time{}, api.MaxFOCUSExportInvoices+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read billing invoices"))
		return
	}
	if len(invoices) > api.MaxFOCUSExportInvoices {
		api.WriteProblem(w, api.ErrValidation("invoice history exceeds the financial report limit"))
		return
	}
	for _, invoice := range invoices {
		report.Invoices = append(report.Invoices, invoiceResponse(invoice))
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, report)
}

func (s *server) getFinancialForecast(w http.ResponseWriter, r *http.Request, acct state.Account) {
	now := time.Now().UTC()
	start, problem := financialMonth(r, now)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	report, err := billing.ReadFinancialCosts(r.Context(), s.store, acct.ID, start, now)
	if err != nil {
		api.WriteProblem(w, financialProblem(err))
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, api.FinancialForecastResponse{PeriodStart: report.PeriodStart, PeriodEnd: report.PeriodEnd, AsOf: report.AsOf, Currency: report.Currency, Meters: report.Meters, MissingBillComponents: report.MissingBillComponents})
}

func financialMonth(r *http.Request, now time.Time) (time.Time, *api.Problem) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return time.Time{}, api.ErrValidation("invalid billing query")
	}
	for key, values := range q {
		if key != "month" || len(values) != 1 {
			return time.Time{}, api.ErrValidation("expected at most one month parameter")
		}
	}
	month := q.Get("month")
	if month == "" {
		month = now.UTC().Format("2006-01")
	}
	start, err := time.Parse("2006-01", month)
	if err != nil || start.IsZero() || start.After(now) {
		return time.Time{}, api.ErrValidation("expected a current or historical month in YYYY-MM format")
	}
	return start, nil
}

func financialProblem(err error) *api.Problem {
	if errors.Is(err, billing.ErrFinancialUnavailable) {
		return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Financial history unavailable", "Retained financial evidence is not available on this deployment.")
	}
	if errors.Is(err, state.ErrFinancialAllocationLimit) {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Cost report limit exceeded", "No partial cost report was produced.").WithLimit(api.FinancialAllocationMax, api.FinancialAllocationMax+1)
	}
	if errors.Is(err, state.ErrInvalidArgument) {
		return api.ErrValidation("invalid financial policy or period")
	}
	if errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Budget scope not found", "The requested scope does not exist on this account.")
	}
	return api.ErrCapacity("could not read retained financial evidence")
}

func (s *server) previewFinancialBudget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if r.URL.RawQuery != "" {
		api.WriteProblem(w, api.ErrValidation("budget previews use the current UTC usage month and accept no query parameters"))
		return
	}
	var req api.FinancialBudgetPreviewRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("expected a budget spec"))
		return
	}
	preview, err := billing.PreviewFinancialBudget(r.Context(), s.store, acct.ID, req.Spec, time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, financialProblem(err))
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, preview)
}
