package focus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func detailedFixture() state.Invoice {
	inv := fixtureInvoice()
	inv.Details = &state.InvoiceDetails{IssuerName: "Invoice issuer business", PaymentTerms: "Net 30", IssuedAt: "2026-09-30T03:00:00+03:00", DueAt: "2026-10-30T00:00:00Z",
		Lines: &state.InvoiceLines{Complete: true, Items: []state.InvoiceLineItem{
			{ID: "plan", Description: "Plan, monthly", ChargeCategory: "Purchase", NetCents: 1100, TaxCents: 190, CreatedAt: date(inv.CreatedAt), UpdatedAt: date(inv.UpdatedAt)},
			{ID: "credit", Description: "Promotional credit", ChargeCategory: "Credit", NetCents: -100, CreatedAt: date(inv.CreatedAt), UpdatedAt: date(inv.UpdatedAt)},
		}}}
	return inv
}

// adr:381
func TestFOCUSProviderDetailsReconciliation(t *testing.T) {
	inv := detailedFixture()
	d := buildFixture(t, inv)
	rows := readRecords(t, d)
	if len(rows) != 3 {
		t.Fatalf("rows=%+v", rows)
	}
	if rows[0]["ChargeCategory"] != "Credit" || rows[0]["BilledCost"] != "-1.00" || rows[1]["ChargeCategory"] != "Purchase" || rows[1]["BilledCost"] != "11.00" || rows[2]["BilledCost"] != "1.90" {
		t.Fatalf("incorrect line costs: %+v", rows)
	}
	for _, row := range rows {
		if row["InvoiceIssuerName"] != "Invoice issuer business" || row["PaymentTerms"] != "Net 30" || row["InvoiceIssueDate"] != "2026-09-30T00:00:00Z" || row["PaymentDueDate"] != "2026-10-30T00:00:00Z" {
			t.Fatalf("invoice facts missing: %+v", row)
		}
		if row["ReferenceInvoiceId"] != "order-1" || row["InvoiceDetailCreated"] != date(inv.CreatedAt) {
			t.Fatal("document identity or detail dates changed")
		}
		var grain map[string]string
		if json.Unmarshal([]byte(row["InvoiceDetailGrain"]), &grain) != nil || grain["x_GregaleProviderLineId"] == "" {
			t.Fatal("provider grain missing")
		}
	}
	m := readMetadata(t, d)
	if m.Projection.Status != "partial" || len(m.Projection.MissingRequiredFields) != 0 || m.Projection.SourceCoverage.DetailedInvoices != 1 || len(m.Projection.SourceCoverage.AggregateFallbackReasons) != 0 || m.Projection.BilledCostByCurrency["EUR"] != "11.90" {
		t.Fatalf("metadata=%+v", m.Projection)
	}
	inv.Details.Lines.Items[0], inv.Details.Lines.Items[1] = inv.Details.Lines.Items[1], inv.Details.Lines.Items[0]
	if !bytes.Equal(d.CSV, buildFixture(t, inv).CSV) {
		t.Fatal("provider array order changed stable IDs/rows")
	}
	if decimalCents(big.NewInt(-1)) != "-0.01" || decimalCents(big.NewInt(math.MinInt64)) != "-92233720368547758.08" {
		t.Fatal("negative minor units lost sign or precision")
	}
}

// adr:381
func TestFOCUSProviderDetailsFallbackCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		mutate       func(*state.Invoice)
	}{
		{"incomplete", "incomplete", func(inv *state.Invoice) { inv.Details.Lines.Complete = false }},
		{"unknown category", "unclassified", func(inv *state.Invoice) { inv.Details.Lines.Items[0].ChargeCategory = "" }},
		{"line sum", "totals_mismatch", func(inv *state.Invoice) { inv.Details.Lines.Items[0].NetCents++ }},
		{"tax sum", "totals_mismatch", func(inv *state.Invoice) { inv.Details.Lines.Items[0].TaxCents++ }},
		{"unavailable", "unavailable", func(inv *state.Invoice) { inv.Details.Lines = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := detailedFixture()
			tc.mutate(&inv)
			d := buildFixture(t, inv)
			rows := readRecords(t, d)
			m := readMetadata(t, d)
			if len(rows) != 2 || rows[0]["BilledCost"] != "10.00" || rows[1]["BilledCost"] != "1.90" || rows[0]["PaymentTerms"] != "Net 30" {
				t.Fatalf("bad fallback: %+v", rows)
			}
			if m.Projection.SourceCoverage.DetailedInvoices != 0 || m.Projection.SourceCoverage.AggregateFallbackReasons[tc.reason] != 1 {
				t.Fatalf("false completeness: %+v", m.Projection)
			}
		})
	}
}

// adr:381
func TestFOCUSDetailLimitsAndInvalidDates(t *testing.T) {
	inv := detailedFixture()
	inv.Details.Lines.Items[0].Description = strings.Repeat("x", api.MaxInvoiceDetailTextBytes)
	if got := readRecords(t, buildFixture(t, inv))[1]["InvoiceDetailDescription"]; len(got) != api.MaxInvoiceDetailTextBytes {
		t.Fatal("description truncated")
	}
	inv.Details.IssuedAt = "not-a-date"
	month, _ := ParseMonth("2026-09")
	if _, err := BuildInvoiceDetail(inv.AccountID, month, inv.UpdatedAt, []state.Invoice{inv}); err == nil {
		t.Fatal("invalid invoice issue date exported")
	}
	inv = detailedFixture()
	inv.Details.Lines.Items[0].UpdatedAt = "2026-09-29T00:00:00Z"
	if _, err := BuildInvoiceDetail(inv.AccountID, month, inv.UpdatedAt, []state.Invoice{inv}); err == nil {
		t.Fatal("line update preceded creation")
	}
}

// adr:381
func TestFOCUSDetailRowAndByteBounds(t *testing.T) {
	base := detailedFixture()
	base.TotalCents, base.TaxCents = 1000, 0
	base.Details.Lines.Items = make([]state.InvoiceLineItem, api.MaxInvoiceLineItems)
	for i := range base.Details.Lines.Items {
		base.Details.Lines.Items[i] = state.InvoiceLineItem{ID: fmt.Sprintf("item-%04d", i), Description: "Plan", ChargeCategory: "Purchase", NetCents: 1, CreatedAt: date(base.CreatedAt), UpdatedAt: date(base.UpdatedAt)}
	}
	invoices := make([]state.Invoice, api.MaxFOCUSExportRows/api.MaxInvoiceLineItems+1)
	for i := range invoices {
		invoices[i] = base
		invoices[i].ID = fmt.Sprintf("invoice-%04d", i)
	}
	month, _ := ParseMonth("2026-09")
	_, err := BuildInvoiceDetail(base.AccountID, month, base.UpdatedAt, invoices)
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Kind != "rows" {
		t.Fatalf("missing row bound: %v", err)
	}
	for i := range base.Details.Lines.Items {
		base.Details.Lines.Items[i].Description = strings.Repeat("x", api.MaxInvoiceDetailTextBytes)
	}
	_, err = BuildInvoiceDetail(base.AccountID, month, base.UpdatedAt, []state.Invoice{base})
	if !errors.As(err, &limit) || limit.Kind != "bytes" {
		t.Fatalf("missing byte bound: %v", err)
	}
}
