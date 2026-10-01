package polar

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr:381
func TestInvoiceSnapshotPolar(t *testing.T) {
	raw, err := os.ReadFile("../testdata/invoices/polar.json")
	if err != nil {
		t.Fatal(err)
	}
	event, err := parsePolarEvent(raw, "evt_invoice", &Provider{})
	if err != nil {
		t.Fatal(err)
	}
	d := event.Invoice.Details
	if d.IssuerName != "" || d.IssuedAt != "" || d.DueAt != "" || d.PaymentTerms != "" {
		t.Fatalf("invented invoice facts: %+v", d)
	}
	inv := state.Invoice{TotalCents: event.Invoice.TotalCents, TaxCents: event.Invoice.TaxCents, Details: d}
	if state.InvoiceLineGap(inv) != "" || d.Lines.Items[0].ChargeCategory != "Purchase" || d.Lines.Items[1].ChargeCategory != "Usage" {
		t.Fatalf("line details=%+v", d.Lines)
	}
	for _, tc := range []struct{ old, new, gap string }{
		{`"amount_type":"fixed"`, `"amount_type":"future_price"`, "unclassified"},
		{`"amount":900`, `"amount":901`, "totals_mismatch"},
		{`"amount":900`, `"amount":900.5`, "incomplete"},
	} {
		e, err := parsePolarEvent([]byte(strings.Replace(string(raw), tc.old, tc.new, 1)), "evt", &Provider{})
		if err != nil {
			t.Fatal(err)
		}
		inv.Details = e.Invoice.Details
		if got := state.InvoiceLineGap(inv); got != tc.gap {
			t.Fatalf("gap=%s want=%s", got, tc.gap)
		}
	}
}
