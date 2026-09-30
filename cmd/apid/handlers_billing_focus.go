package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/focus"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// exportFOCUSInvoices reads only the authenticated account. It buffers the
// validated artifact before writing any CSV/ZIP bytes, so an error can never
// leave a successful-looking but truncated billing export.
func (s *server) exportFOCUSInvoices(w http.ResponseWriter, r *http.Request, acct state.Account) {
	month, format, problem := parseFOCUSExportParams(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	invoices, err := s.store.ListInvoicesForAccount(r.Context(), acct.ID, &month, time.Time{}, api.MaxFOCUSExportInvoices+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read billing invoices"))
		return
	}
	if len(invoices) > api.MaxFOCUSExportInvoices {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invoice export limit exceeded", "No partial export was produced; contact support for a larger invoice history.").
			WithLimit(api.MaxFOCUSExportInvoices, int64(len(invoices))).WithDocs(wire.DocsBaseURL+"/billing#focus-invoice-export"))
		return
	}
	dataset, err := focus.BuildInvoiceDetail(acct.ID, month, time.Now().UTC(), invoices)
	if err != nil {
		api.WriteProblem(w, focusExportProblem(err))
		return
	}
	body, contentType, filename, err := dataset.Artifact(month, format)
	if err != nil {
		api.WriteProblem(w, focusExportProblem(err))
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Gregale-FOCUS-Conformance", "partial")
	w.Header().Set("X-Gregale-FOCUS-Version", focus.Version)
	_, _ = w.Write(body)
}

func focusExportProblem(err error) *api.Problem {
	var limit *focus.LimitError
	if errors.As(err, &limit) {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invoice export limit exceeded", err.Error()).WithLimit(limit.Limit, limit.Observed).WithDocs(wire.DocsBaseURL + "/billing#focus-invoice-export")
	}
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Invoice export unavailable", err.Error()).WithDocs(wire.DocsBaseURL + "/billing#focus-invoice-export")
}

func parseFOCUSExportParams(r *http.Request) (time.Time, string, *api.Problem) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return time.Time{}, "", api.ErrValidation("invalid export query encoding")
	}
	for key, values := range query {
		if (key != "month" && key != "format") || len(values) != 1 {
			return time.Time{}, "", api.ErrValidation("expected one month and at most one format parameter")
		}
	}
	month, err := focus.ParseMonth(query.Get("month"))
	if err != nil {
		return time.Time{}, "", api.ErrValidation(err.Error())
	}
	format := query.Get("format")
	if format == "" {
		format = "zip"
	}
	if !focus.ValidFormat(format) {
		return time.Time{}, "", api.ErrValidation("expected format zip, csv, or metadata")
	}
	return month, format, nil
}
