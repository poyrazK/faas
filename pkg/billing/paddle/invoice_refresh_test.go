package paddle

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr:383
func TestInvoiceRefreshPaddleIdentityAndFacts(t *testing.T) {
	raw, err := os.ReadFile("../testdata/invoices/paddle.json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	data, err := billing.InvoiceFactsMap(envelope.Data)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/transactions/txn_invoice" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Paddle-Version") != "1" {
			t.Error("incorrect authenticated transaction read")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()
	p := &Provider{apiKey: "test-key", invoiceBaseURL: srv.URL, invoiceHTTPClient: srv.Client()}
	acct := state.Account{ID: "account", ProviderCustomerID: "ctm_invoice"}
	inv := state.Invoice{AccountID: acct.ID, Provider: "paddle", ProviderInvoiceID: "inv_invoice", ProviderChargeID: "txn_invoice", Currency: "eur", TotalCents: 1190, TaxCents: 190}
	d, err := p.FetchInvoiceDetails(t.Context(), acct, inv)
	if err != nil {
		t.Fatal(err)
	}
	inv.Details = d
	if state.InvoiceLineGap(inv) != "" || d.PaymentTerms != "Net 30 day(s)" || d.Lines.Items[1].ChargeCategory != "Usage" {
		t.Fatalf("facts=%+v", d)
	}
	if d.IssuerName != "" || d.DueAt != "" {
		t.Fatal("invented legal identity or due date")
	}
	for _, tc := range []struct {
		key   string
		value any
	}{{"customer_id", "ctm_foreign"}, {"invoice_id", "inv_foreign"}, {"id", "txn_foreign"}, {"currency_code", "USD"}} {
		old := data[tc.key]
		data[tc.key] = tc.value
		if _, err := p.FetchInvoiceDetails(t.Context(), acct, inv); !errors.Is(err, billing.ErrInvoiceSourceMismatch) {
			t.Fatalf("accepted mismatched %s", tc.key)
		}
		data[tc.key] = old
	}
	sourceDetails := data["details"].(map[string]any)
	originalItems := sourceDetails["line_items"].([]any)
	oversized := make([]any, api.MaxInvoiceLineItems+1)
	for i := range oversized {
		oversized[i] = originalItems[0]
	}
	sourceDetails["line_items"] = oversized
	var limit *billing.InvoiceRefreshLimitError
	if _, err := p.FetchInvoiceDetails(t.Context(), acct, inv); !errors.As(err, &limit) || limit.Observed != len(oversized) {
		t.Fatalf("raw line bound not enforced: %v", err)
	}
	sourceDetails["line_items"] = originalItems

	// Large provider integer amounts survive JSON normalization exactly.
	totals := paddleValueAt(data, "details", "totals").(map[string]any)
	items := paddleValueAt(data, "details", "line_items").([]any)
	lineTotals := paddleValueAt(items[0].(map[string]any), "totals").(map[string]any)
	totals["total"], lineTotals["total"], inv.TotalCents = "9007199254741283", "9007199254741164", 9007199254741283
	d, err = p.FetchInvoiceDetails(t.Context(), acct, inv)
	if err != nil || d.Lines.Items[0].NetCents != 9007199254740993 {
		t.Fatalf("large money lost precision: details=%+v err=%v", d, err)
	}
	inv.Details = d
	if state.InvoiceLineGap(inv) != "" {
		t.Fatal("large invoice failed exact reconciliation")
	}
}
