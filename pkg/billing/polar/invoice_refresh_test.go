package polar

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
func TestInvoiceRefreshPolarIdentityAndFacts(t *testing.T) {
	raw, err := os.ReadFile("../testdata/invoices/polar.json")
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
		if r.Method != http.MethodGet || r.URL.Path != "/v1/orders/order_invoice" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("incorrect authenticated order read")
		}
		_ = json.NewEncoder(w).Encode(data)
	}))
	defer srv.Close()
	p := &Provider{apiKey: "test-key", baseURL: srv.URL, client: srv.Client()}
	acct := state.Account{ID: "account", ProviderCustomerID: "customer_invoice"}
	inv := state.Invoice{AccountID: acct.ID, Provider: "polar", ProviderInvoiceID: "order_invoice", Currency: "eur", TotalCents: 1190, TaxCents: 190}
	d, err := p.FetchInvoiceDetails(t.Context(), acct, inv)
	if err != nil {
		t.Fatal(err)
	}
	inv.Details = d
	if state.InvoiceLineGap(inv) != "" || d.Lines.Items[1].ChargeCategory != "Usage" {
		t.Fatalf("facts=%+v", d)
	}
	if d.PaymentTerms != "" || d.IssuerName != "" || d.IssuedAt != "" || d.DueAt != "" {
		t.Fatal("buyer/order facts substituted for invoice facts")
	}
	for _, tc := range []struct {
		key   string
		value any
	}{{"customer_id", "customer_foreign"}, {"id", "order_foreign"}, {"currency", "usd"}, {"tax_amount", json.Number("190.5")}} {
		old := data[tc.key]
		data[tc.key] = tc.value
		if _, err := p.FetchInvoiceDetails(t.Context(), acct, inv); !errors.Is(err, billing.ErrInvoiceSourceMismatch) {
			t.Fatalf("accepted mismatched %s", tc.key)
		}
		data[tc.key] = old
	}
	originalItems := data["items"].([]any)
	oversized := make([]any, api.MaxInvoiceLineItems+1)
	for i := range oversized {
		oversized[i] = originalItems[0]
	}
	data["items"] = oversized
	var limit *billing.InvoiceRefreshLimitError
	if _, err := p.FetchInvoiceDetails(t.Context(), acct, inv); !errors.As(err, &limit) || limit.Observed != len(oversized) {
		t.Fatalf("raw line bound not enforced: %v", err)
	}
	data["items"] = originalItems

	items := data["items"].([]any)
	items[0].(map[string]any)["amount"] = json.Number("9007199254740993")
	data["total_amount"], inv.TotalCents = json.Number("9007199254741283"), 9007199254741283
	d, err = p.FetchInvoiceDetails(t.Context(), acct, inv)
	if err != nil || d.Lines.Items[0].NetCents != 9007199254740993 {
		t.Fatalf("large money lost precision: details=%+v err=%v", d, err)
	}
	inv.Details = d
	if state.InvoiceLineGap(inv) != "" {
		t.Fatal("large order failed exact reconciliation")
	}
}
