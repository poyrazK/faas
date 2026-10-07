package stripe

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr:381
func TestInvoiceSnapshotStripe(t *testing.T) {
	raw, err := os.ReadFile("../testdata/invoices/stripe.json")
	if err != nil {
		t.Fatal(err)
	}
	d := InvoiceDetailsFromWebhook(raw)
	if d == nil || d.IssuerName != "Gregale invoice business" || d.PaymentTerms != "Due by 2026-10-30T00:00:00Z" || d.IssuedAt != "2026-09-30T00:00:00Z" {
		t.Fatalf("details=%+v", d)
	}
	inv := state.Invoice{TotalCents: 1190, TaxCents: 190, Details: d}
	if state.InvoiceLineGap(inv) != "" || d.Lines.Items[0].ChargeCategory != "Purchase" || d.Lines.Items[1].ChargeCategory != "Usage" {
		t.Fatalf("invalid line projection: %+v", d.Lines)
	}
	if InvoiceTaxCentsFromWebhook(raw, 0) != 190 {
		t.Fatal("legacy tax missing")
	}
	for _, tc := range []struct{ name, old, replacement, gap string }{
		{"pagination", `"has_more":false`, `"has_more":true`, "incomplete"},
		{"unknown pagination", `"has_more":false,`, ``, "incomplete"},
		{"opaque price", `"price":{"recurring":{"usage_type":"licensed"}}`, `"pricing":{"price_details":{"price":"price_plan"}}`, "unclassified"},
		{"currency mismatch", `"currency":"eur","amount":1000`, `"currency":"usd","amount":1000`, "incomplete"},
		{"discount mismatch", `"amount":1000`, `"amount":1001`, "totals_mismatch"},
		{"inclusive tax", `"amount":1000`, `"amount":1171`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(string(raw), tc.old, tc.replacement, 1)
			if tc.name == "inclusive tax" {
				body = strings.Replace(body, `"inclusive":false`, `"inclusive":true`, 1)
			}
			inv.Details = InvoiceDetailsFromWebhook([]byte(body))
			if got := state.InvoiceLineGap(inv); got != tc.gap {
				t.Fatalf("gap=%s want=%s", got, tc.gap)
			}
		})
	}
	modern := []byte(`{"data":{"object":{"total_taxes":[{"amount":171},{"amount":19}],"currency":"eur","lines":{"has_more":false,"data":[{"id":"modern","amount":1190,"currency":"eur","taxes":[{"amount":190,"tax_behavior":"inclusive"}],"pricing":{"price_details":{"price":"price_opaque"}}}]}}}}`)
	if InvoiceTaxCentsFromWebhook(modern, 0) != 190 || InvoiceDetailsFromWebhook(modern).Lines.Items[0].NetCents != 1000 {
		t.Fatal("modern inclusive tax is not exact")
	}
	overflow := []byte(`{"data":{"object":{"total_taxes":[{"amount":9223372036854775807},{"amount":1}]}}}`)
	if InvoiceTaxCentsFromWebhook(overflow, 0) != -1 {
		t.Fatal("overflow accepted as a tax total")
	}
	large := []byte(`{"data":{"object":{"currency":"eur","lines":{"has_more":false,"data":[{"id":"large","amount":9007199254740993,"currency":"eur"}]}}}}`)
	if InvoiceDetailsFromWebhook(large).Lines.Items[0].NetCents != 9007199254740993 {
		t.Fatal("integer precision lost")
	}
}
