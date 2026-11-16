package billing

// Branches of the statement invoice exporters that the weekly mutation run
// (make test-mutation-billing) showed no test noticed changing.

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func guardTenantLine(appCard, tenantCard string, units, price int64) state.PlatformTenantStatementLine {
	return state.PlatformTenantStatementLine{AppID: "a", RateCardID: appCard, PlatformTenantRateCardID: tenantCard,
		BillableUnits: units, PriceMillicentsPerUnit: price, AmountMillicents: units * price, Currency: "EUR"}
}

// Prepaid credit a statement drew appears as its own negative line, and the
// invoice totals what is still due (ADR-20261019100331).
func TestStatementInvoicesCarryAppliedCredit(t *testing.T) {
	tenant := state.PlatformTenantStatement{Currency: "EUR", AmountMillicents: 5000, CreditAppliedMillicents: 2000,
		Lines: []state.PlatformTenantStatementLine{guardTenantLine("", "t1", 10, 500)}}
	inv, err := TenantStatementInvoice(tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Lines) != 2 || !inv.Lines[1].Credit || inv.Lines[1].AmountMillicents != -2000 || inv.TotalMillicents != 3000 {
		t.Fatalf("tenant invoice = %+v, want a -2000 credit line and 3000 due", inv)
	}
	// 5 cents of usage less 2 cents of credit is the 3 cents due: no rounding.
	if inv.Lines[1].AmountMinor != -2 || inv.TotalMinor != 3 || inv.RoundingMinor != 0 {
		t.Fatalf("credit minor units = line %d, total %d, rounding %d; want -2, 3, 0", inv.Lines[1].AmountMinor, inv.TotalMinor, inv.RoundingMinor)
	}
	// The credit line counts toward the provider's line limit.
	full := state.PlatformTenantStatement{Currency: "EUR", CreditAppliedMillicents: 1}
	for i := range MaxStatementInvoiceLines {
		full.Lines = append(full.Lines, guardTenantLine("", "t", 1, int64(i+1)))
		full.AmountMillicents += int64(i + 1)
	}
	if _, err := TenantStatementInvoice(full); !errors.Is(err, ErrStatementNotInvoiceable) {
		t.Fatalf("%d lines plus a credit line err = %v", MaxStatementInvoiceLines, err)
	}

	app := state.APIConsumerUsageStatement{Currency: "EUR", AmountMillicents: 1800, CreditAppliedMillicents: 800,
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			BillableUnits: 3, RateCardID: "flat", PriceMillicentsPerUnit: 600, ChargedUnits: charged(3), AmountMillicents: 1800}}}
	inv, err = AppStatementInvoice(app, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Lines) != 2 || !inv.Lines[1].Credit || inv.TotalMillicents != 1000 {
		t.Fatalf("app invoice = %+v, want a credit line and 1000 due", inv)
	}
}

// A line priced by an app rate card alone is invoiceable; only a line with
// neither card is refused as unpriced.
func TestTenantStatementInvoiceAcceptsAppPricedLines(t *testing.T) {
	statement := state.PlatformTenantStatement{Currency: "EUR", AmountMillicents: 1000,
		Lines: []state.PlatformTenantStatementLine{guardTenantLine("app-card", "", 2, 500)}}
	if _, err := TenantStatementInvoice(statement); err != nil {
		t.Fatalf("app-card line refused: %v", err)
	}
	statement.Lines = []state.PlatformTenantStatementLine{guardTenantLine("", "", 2, 500)}
	if _, err := TenantStatementInvoice(statement); !errors.Is(err, ErrStatementNotInvoiceable) {
		t.Fatalf("unpriced line err = %v", err)
	}
}

// Invoice lines are ordered by price source, label, fee kind and unit price,
// so a re-export of the same statement is byte-identical.
func TestStatementInvoiceLineOrder(t *testing.T) {
	statement := state.PlatformTenantStatement{Currency: "EUR", AmountMillicents: 2*900 + 2*700 + 2*300,
		Lines: []state.PlatformTenantStatementLine{guardTenantLine("", "b", 2, 900), guardTenantLine("", "a", 2, 700), guardTenantLine("", "a", 2, 300)}}
	inv, err := TenantStatementInvoice(statement)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range inv.Lines {
		got = append(got, fmt.Sprintf("%s:%d", line.PlatformTenantRateCardID, line.PriceMillicentsPerUnit))
	}
	want := []string{"a:300", "a:700", "b:900"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("line order = %v, want %v", got, want)
	}

	fees := []state.APIConsumerStatementFee{{Kind: state.StatementFeeMinimum, AmountMillicents: 100}, {Kind: state.StatementFeeBase, AmountMillicents: 100}}
	sorted := func(lines []StatementInvoiceLine) bool {
		for i := 1; i < len(lines); i++ {
			if lines[i-1].Fee > lines[i].Fee {
				return false
			}
		}
		return true
	}
	statement = state.PlatformTenantStatement{Currency: "EUR", AmountMillicents: 200, Fees: fees}
	if inv, err := TenantStatementInvoice(statement); err != nil || !sorted(inv.Lines) {
		t.Fatalf("fee lines = %+v %v, want ordered by fee kind", inv.Lines, err)
	}
}

// A Product Control fee states a trial only when both ends are known, and a
// second fee with the same label is not invoiced twice.
func TestProductControlStatementInvoiceFeeEdges(t *testing.T) {
	trialStart := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	fee := api.ProductBillingFee{Plan: "starter", Revision: "r1", AmountMillicents: 2_000_000, TrialStartsAt: &trialStart}
	doc := api.ProductBillingDocument{Kind: "statement", Currency: "EUR", AmountMillicents: 2_000_000,
		Snapshot: &api.ProductBillingReview{Fees: []api.ProductBillingFee{fee}}}
	inv, err := ProductControlStatementInvoice(doc)
	if err != nil || len(inv.Lines) != 1 || inv.Lines[0].Label != "Monthly fee for plan starter" {
		t.Fatalf("half-known trial = %+v %v, want the plain fee label", inv.Lines, err)
	}

	second := fee
	second.AmountMillicents = 500_000
	doc.Snapshot.Fees = []api.ProductBillingFee{fee, second}
	if inv, err := ProductControlStatementInvoice(doc); err != nil || len(inv.Lines) != 1 || inv.Lines[0].AmountMillicents != 2_000_000 {
		t.Fatalf("duplicate fee = %+v %v, want only the first", inv.Lines, err)
	}
}

// Only finalized statements with a snapshot export.
func TestProductControlStatementInvoiceRefusesOtherDocuments(t *testing.T) {
	snapshot := &api.ProductBillingReview{Fees: []api.ProductBillingFee{{Plan: "starter", AmountMillicents: 100}}}
	for _, doc := range []api.ProductBillingDocument{
		{Kind: "credit_note", Currency: "EUR", AmountMillicents: 100, Snapshot: snapshot},
		{Kind: "statement", Currency: "EUR", AmountMillicents: 100},
	} {
		if _, err := ProductControlStatementInvoice(doc); !errors.Is(err, ErrStatementNotInvoiceable) {
			t.Fatalf("%s (snapshot %v) err = %v", doc.Kind, doc.Snapshot != nil, err)
		}
	}
}

// A line whose amount would overflow is refused even when its units fit.
func TestStatementInvoiceLineAmountOverflow(t *testing.T) {
	var inv StatementInvoice
	lines := map[invoiceLineKey]*StatementInvoiceLine{}
	key := invoiceLineKey{rateCardID: "card", price: math.MaxInt64/2 + 1}
	if err := inv.add(key, 1, lines); err != nil {
		t.Fatal(err)
	}
	if err := inv.add(key, 1, lines); err == nil {
		t.Fatal("amount overflow accepted")
	}
}

// A statement that yields no invoice line is refused, as is one at the
// provider's line limit plus one; exactly the limit is accepted.
func TestStatementInvoiceLineCountBounds(t *testing.T) {
	if _, err := TenantStatementInvoice(state.PlatformTenantStatement{Currency: "EUR"}); !errors.Is(err, ErrStatementNotInvoiceable) {
		t.Fatalf("empty statement err = %v", err)
	}
	build := func(n int) state.PlatformTenantStatement {
		statement := state.PlatformTenantStatement{Currency: "EUR"}
		for i := range n {
			statement.Lines = append(statement.Lines, guardTenantLine("", "t", 1, int64(i+1)))
			statement.AmountMillicents += int64(i + 1)
		}
		return statement
	}
	if _, err := TenantStatementInvoice(build(MaxStatementInvoiceLines)); err != nil {
		t.Fatalf("%d lines refused: %v", MaxStatementInvoiceLines, err)
	}
	if _, err := TenantStatementInvoice(build(MaxStatementInvoiceLines + 1)); !errors.Is(err, ErrStatementNotInvoiceable) {
		t.Fatalf("%d lines err = %v", MaxStatementInvoiceLines+1, err)
	}
}
