// adr: 380
package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/focus"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedFOCUSInvoice(e testEnv, id, accountID string, end time.Time) {
	memSeedInvoice(e.store, state.Invoice{
		ID: id, AccountID: accountID, Provider: "polar", ProviderInvoiceID: "provider-" + id,
		Status: "paid", PeriodStart: end.AddDate(0, 0, -20), PeriodEnd: end,
		SubtotalCents: 1200, TaxCents: 190, TotalCents: 1190, Currency: "eur", CreatedAt: end, UpdatedAt: end,
	})
}

func TestFOCUSExportAccountAndMonthIsolation(t *testing.T) {
	e := setup(t, api.PlanFree)
	sept := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	seedFOCUSInvoice(e, "own", e.acct.ID, sept)
	seedFOCUSInvoice(e, "foreign", "another-account", sept)
	seedFOCUSInvoice(e, "old", e.acct.ID, sept.AddDate(0, -1, 0))
	seedFOCUSInvoice(e, "boundary", e.acct.ID, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	rec := e.do(t, http.MethodGet, "/v1/billing/focus?month=2026-09&format=csv", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatalf("CSV rows=%v error=%v", rows, err)
	}
	// InvoiceId is the provider document, not a local payment handle.
	if rows[1][11] != "provider-own" || rows[2][11] != "provider-own" {
		t.Fatalf("foreign or out-of-month data: %v", rows)
	}
	for header, want := range map[string]string{
		"Content-Type": "text/csv; charset=utf-8", "Cache-Control": "private, no-store",
		"X-Gregale-FOCUS-Conformance": "partial", "X-Gregale-FOCUS-Version": "1.4", "X-Content-Type-Options": "nosniff",
	} {
		if rec.Header().Get(header) != want {
			t.Errorf("%s=%q want=%q", header, rec.Header().Get(header), want)
		}
	}
}

func TestFOCUSExportFormatsAndEmptyHistory(t *testing.T) {
	e := setup(t, api.PlanHobby)
	for _, format := range []string{"", "zip", "csv", "metadata"} {
		t.Run(format, func(t *testing.T) {
			rec := e.do(t, http.MethodGet, "/v1/billing/focus?month=2026-09&format="+format, nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			switch format {
			case "", "zip":
				r, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
				if err != nil || len(r.File) != 2 {
					t.Fatalf("ZIP: %v", err)
				}
			case "csv":
				rows, err := csv.NewReader(rec.Body).ReadAll()
				if err != nil || len(rows) != 1 {
					t.Fatalf("empty CSV: %v %v", rows, err)
				}
			case "metadata":
				var m focus.Metadata
				if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m.Projection.RowCount != 0 || m.Projection.Status != "partial" {
					t.Fatalf("empty metadata: %+v %v", m, err)
				}
			}
		})
	}
}

func TestFOCUSExportRejectsBadQueries(t *testing.T) {
	e := setup(t, api.PlanPro)
	for _, query := range []string{
		"", "month=2026-13", "month=0000-01", "month=2026-9", "month=2026-09&format=parquet",
		"month=2026-09&account_id=another", "month=2026-09&month=2026-10", "month=2026-09&format=csv&format=zip", "month=2026-09&format=%GG",
	} {
		t.Run(query, func(t *testing.T) {
			rec := e.do(t, http.MethodGet, "/v1/billing/focus?"+query, nil, nil)
			assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
		})
	}
}

func TestFOCUSExportDoesNotSilentlyTruncate(t *testing.T) {
	e := setup(t, api.PlanPro)
	end := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	for i := range api.MaxFOCUSExportInvoices + 1 {
		seedFOCUSInvoice(e, fmt.Sprintf("invoice-%04d", i), e.acct.ID, end)
	}
	rec := e.do(t, http.MethodGet, "/v1/billing/focus?month=2026-09&format=csv", nil, nil)
	assertProblem(t, rec, http.StatusUnprocessableEntity, api.CodeValidation)
	if strings.Contains(rec.Body.String(), "BilledCost,") || rec.Header().Get("Content-Disposition") != "" {
		t.Fatal("overflow produced a partial CSV")
	}
	var p api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Limit == nil || p.Observed == nil || *p.Limit != api.MaxFOCUSExportInvoices || *p.Observed != api.MaxFOCUSExportInvoices+1 {
		t.Fatalf("missing actionable limit details: %+v %v", p, err)
	}
}

func TestFOCUSExportInvalidInvoiceFailsBeforeDownload(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedInvoiceDirect(t, e.store, e.acct.ID, "polar", "bad", time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), 100)
	// This historical fixture has no created/updated times. Never invent them.
	rec := e.do(t, http.MethodGet, "/v1/billing/focus?month=2026-09", nil, nil)
	assertProblem(t, rec, http.StatusConflict, api.CodeConflict)
	if rec.Header().Get("Content-Disposition") != "" {
		t.Fatal("invalid invoice began a successful download")
	}
}

func TestFOCUSExportAuthenticationScopeAndMFA(t *testing.T) {
	for _, tc := range []struct {
		scope string
		want  int
	}{{"usage:read", http.StatusOK}, {"apps:read", http.StatusForbidden}, {"deploy:write", http.StatusForbidden}} {
		t.Run(tc.scope, func(t *testing.T) {
			e := setupWithScopes(t, []string{tc.scope})
			rec := e.do(t, http.MethodGet, "/v1/billing/focus?month=2026-09", nil, nil)
			if rec.Code != tc.want {
				t.Fatalf("scope=%s status=%d body=%s", tc.scope, rec.Code, rec.Body)
			}
		})
	}
	e := setup(t, api.PlanFree)
	rec := e.do(t, http.MethodGet, "/v1/billing/focus?month=2026-09", nil, map[string]string{"Authorization": ""})
	assertProblem(t, rec, http.StatusUnauthorized, api.CodeUnauthorized)
	h, acct, mgr, sid := setupMW(t, api.PlanPro, true)
	cookie := reissueWithMFAFlag(t, mgr, sid, acct.ID, true)
	rec = cookieDo(t, h, cookie, http.MethodGet, "/v1/billing/focus?month=2026-09", nil)
	assertProblem(t, rec, http.StatusForbidden, api.CodeMFARequired)
}
