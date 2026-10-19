package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1504 — invoices pages through every exported invoice with its
// payment status, marking overdue ones.
func TestCmdAccountInvoices(t *testing.T) {
	t.Setenv("FAAS_API_MONETIZATION_ENABLED", "1") // adr: 20261018090210 — exercises released monetization
	var statuses []string
	remaining := int64(375)
	due := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	stdout := withConsumersTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method+" "+r.URL.Path != "GET /v1/account/statement-invoices" {
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		statuses = append(statuses, r.URL.Query().Get("status"))
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(api.StatementInvoiceListResponse{NextCursor: "c1", Invoices: []api.StatementInvoiceExportResponse{
				{InvoiceID: "in_1", Customer: "cus_A", PaymentStatus: "open", Overdue: true, Currency: "EUR", AmountMinor: 375,
					AmountRemainingMinor: &remaining, DueAt: &due}}})
			return
		}
		_ = json.NewEncoder(w).Encode(api.StatementInvoiceListResponse{Invoices: []api.StatementInvoiceExportResponse{
			{InvoiceID: "in_2", Customer: "cus_B", PaymentStatus: "draft", Currency: "EUR", AmountMinor: 100}}})
	})
	if code := cmdAccount([]string{"invoices", "--status", "unpaid"}); code != 0 {
		t.Fatalf("invoices exit = %d", code)
	}
	out := stdout.String()
	if len(statuses) != 2 || statuses[0] != "unpaid" || !strings.Contains(out, "open (overdue)") || !strings.Contains(out, "EUR 3.75") ||
		!strings.Contains(out, "2026-10-01") || !strings.Contains(out, "in_2") {
		t.Fatalf("statuses = %v, output = %q", statuses, out)
	}
}
