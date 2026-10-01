package paddle

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr:381
func TestInvoiceSnapshotPaddle(t *testing.T) {
	raw, err := os.ReadFile("../testdata/invoices/paddle.json")
	if err != nil {
		t.Fatal(err)
	}
	decode := func(body string) map[string]any {
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		d := json.NewDecoder(strings.NewReader(body))
		d.UseNumber()
		if err := d.Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	data := decode(string(raw))
	inv := paddleInvoice("transaction.completed", data)
	if inv.Details.PaymentTerms != "Net 30 day(s)" || inv.Details.IssuedAt != "2026-09-30T00:00:00Z" || inv.Details.IssuerName != "" || inv.Details.DueAt != "" {
		t.Fatalf("fabricated/missing invoice facts: %+v", inv.Details)
	}
	stored := state.Invoice{TotalCents: inv.TotalCents, TaxCents: inv.TaxCents, Details: inv.Details}
	if state.InvoiceLineGap(stored) != "" || inv.Details.Lines.Items[0].ChargeCategory != "Purchase" || inv.Details.Lines.Items[1].ChargeCategory != "Usage" {
		t.Fatalf("bad breakdown: %+v", inv.Details.Lines)
	}
	for _, tc := range []struct{ old, new, gap string }{
		{`"total":"1071"`, `"total":"1072"`, "totals_mismatch"},
		{`"total":"1071"`, `"total":"1071.5"`, "incomplete"},
		{`faas-plan-pro-monthly`, `arbitrary subscription`, "unclassified"},
	} {
		d := paddleInvoiceDetails(decode(strings.Replace(string(raw), tc.old, tc.new, 1)))
		stored.Details = d
		if got := state.InvoiceLineGap(stored); got != tc.gap {
			t.Fatalf("gap=%s want=%s", got, tc.gap)
		}
	}
	data["details"].(map[string]any)["adjusted_totals"] = map[string]any{"total": "1000", "tax": "100"}
	inv = paddleInvoice("transaction.completed", data)
	if state.InvoiceLineGap(state.Invoice{TotalCents: inv.TotalCents, TaxCents: inv.TaxCents, Details: inv.Details}) != "totals_mismatch" {
		t.Fatal("original lines mislabeled as adjusted totals")
	}
}
