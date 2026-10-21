package billing

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func charged(n int64) *int64 { return &n }

// adr: 1502
func TestAppStatementInvoiceLinesAndRounding(t *testing.T) {
	minute := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	five := int64(5)
	cards := []state.APIConsumerRateCard{{ID: "tiered", Tiers: []state.APIConsumerRateCardTier{{UpTo: &five, PriceMillicentsPerUnit: 0}, {PriceMillicentsPerUnit: 600}}}}
	statement := state.APIConsumerUsageStatement{Currency: "EUR", AmountMillicents: 600*3 + 600*4, Buckets: []state.APIConsumerUsageStatementBucket{
		// 10 units on an allowance card: 7 free, 3 charged at 600.
		{WindowStart: minute, BillableUnits: 10, RateCardID: "flat", PriceMillicentsPerUnit: 600, ChargedUnits: charged(3), AmountMillicents: 1800},
		// 9 units on a ladder: 5 free, 4 at 600.
		{WindowStart: minute.Add(time.Minute), BillableUnits: 9, RateCardID: "tiered", TierUnits: []int64{5, 4}, AmountMillicents: 2400},
	}}
	inv, err := AppStatementInvoice(statement, cards)
	if err != nil {
		t.Fatal(err)
	}
	type key struct {
		card  string
		price int64
	}
	got := map[key]StatementInvoiceLine{}
	for _, line := range inv.Lines {
		got[key{line.RateCardID, line.PriceMillicentsPerUnit}] = line
	}
	if len(inv.Lines) != 4 || got[key{"flat", 0}].Units != 7 || got[key{"flat", 600}].AmountMinor != 2 ||
		got[key{"tiered", 0}].Units != 5 || got[key{"tiered", 600}].AmountMinor != 2 {
		t.Fatalf("lines = %+v", inv.Lines)
	}
	// 1.8 and 2.4 cents round to 2 and 2; the statement's 4.2 cents rounds to 4.
	if inv.TotalMinor != 4 || inv.RoundingMinor != 0 {
		t.Fatalf("total %d rounding %d, want 4 and 0", inv.TotalMinor, inv.RoundingMinor)
	}
	statement.Buckets[1].TierUnits = []int64{5, 4, 1}
	if _, err := AppStatementInvoice(statement, cards); !errors.Is(err, ErrStatementNotInvoiceable) {
		t.Fatalf("ladder mismatch err = %v", err)
	}
}

// adr: 1502
func TestTenantStatementInvoiceRoundingAndCurrency(t *testing.T) {
	line := func(card string, units, price int64) state.PlatformTenantStatementLine {
		return state.PlatformTenantStatementLine{AppID: "a", PlatformTenantRateCardID: card, BillableUnits: units,
			PriceMillicentsPerUnit: price, AmountMillicents: units * price, Currency: "EUR"}
	}
	statement := state.PlatformTenantStatement{Currency: "EUR", AmountMillicents: 1600,
		Lines: []state.PlatformTenantStatementLine{line("t1", 2, 300), line("t2", 2, 500)}}
	inv, err := TenantStatementInvoice(statement)
	if err != nil {
		t.Fatal(err)
	}
	// 0.6 and 1.0 cents round to 1 and 1; 1.6 cents rounds to 2, so no rounding item.
	if inv.TotalMinor != 2 || inv.RoundingMinor != 0 || len(inv.Lines) != 2 {
		t.Fatalf("invoice = %+v", inv)
	}
	statement.Lines = []state.PlatformTenantStatementLine{line("t1", 1, 600), line("t2", 1, 600)}
	statement.AmountMillicents = 1200
	if inv, err = TenantStatementInvoice(statement); err != nil || inv.TotalMinor != 1 || inv.RoundingMinor != -1 {
		t.Fatalf("rounding invoice = %+v err=%v, want total 1 with a -1 rounding item", inv, err)
	}
	statement.Currency = "CLF"
	if _, err := TenantStatementInvoice(statement); !errors.Is(err, ErrInvoiceCurrencyUnsupported) {
		t.Fatalf("four-decimal CLF err = %v", err)
	}
	statement.Currency, statement.AmountMillicents = "EUR", 999
	if _, err := TenantStatementInvoice(statement); !errors.Is(err, ErrStatementNotInvoiceable) {
		t.Fatalf("total mismatch err = %v", err)
	}
	if got := (MinorUnit{Exponent: 2, Step: 1}).FromMillicents(-1500); got != -2 {
		t.Fatalf("FromMillicents(-1500) = %d, want -2", got)
	}
}

// adr: 20261018212918
func TestProductControlStatementInvoiceLinesAndRounding(t *testing.T) {
	line := func(feature, revision, plan string, price, succeeded, failed, waived int64) api.ProductBillingLine {
		return api.ProductBillingLine{Feature: feature, Revision: revision, Plan: plan, UnitPriceMillicents: price,
			Starts: succeeded + failed + waived, Succeeded: succeeded, Failed: failed, Waived: waived, AmountMillicents: succeeded * price}
	}
	doc := api.ProductBillingDocument{Kind: "statement", Currency: "EUR", AmountMillicents: 3*1500 + 1500, Snapshot: &api.ProductBillingReview{Lines: []api.ProductBillingLine{
		line("export", "rev-a", "starter", 1500, 2, 4, 1),
		line("export", "rev-b", "starter", 1500, 1, 0, 0), // same price, newer catalog revision: shares the line
		line("export", "rev-b", "pro", 1500, 1, 0, 0),
		line("validate", "rev-b", "pro", 0, 7, 0, 0),
		line("export", "rev-b", "pro", 9000, 0, 3, 0), // failures only: not invoiced
	}}}
	inv, err := ProductControlStatementInvoice(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := []StatementInvoiceLine{
		{Label: "included successful validate runs on plan pro", Units: 7},
		{Label: "successful export runs on plan pro", Units: 1, PriceMillicentsPerUnit: 1500, AmountMillicents: 1500, AmountMinor: 2},
		{Label: "successful export runs on plan starter", Units: 3, PriceMillicentsPerUnit: 1500, AmountMillicents: 4500, AmountMinor: 5},
	}
	if len(inv.Lines) != len(want) {
		t.Fatalf("lines = %+v", inv.Lines)
	}
	for i := range want {
		if inv.Lines[i] != want[i] {
			t.Fatalf("line %d = %+v, want %+v", i, inv.Lines[i], want[i])
		}
	}
	// 1.5 and 4.5 cents round to 2 and 5; the 6-cent total needs a -1 rounding item.
	if inv.Currency != "EUR" || inv.TotalMinor != 6 || inv.RoundingMinor != -1 {
		t.Fatalf("invoice = %+v", inv)
	}
	credit := api.ProductBillingDocument{Kind: "credit_note", Currency: "EUR", AmountMillicents: -1500}
	if _, err := ProductControlStatementInvoice(credit); !errors.Is(err, ErrStatementNotInvoiceable) {
		t.Fatalf("credit note err = %v", err)
	}
	if _, err := ProductControlStatementInvoice(api.ProductBillingDocument{Kind: "statement", Currency: "EUR"}); !errors.Is(err, ErrStatementNotInvoiceable) {
		t.Fatalf("statement without snapshot err = %v", err)
	}
	mismatch := doc
	mismatch.AmountMillicents++
	if _, err := ProductControlStatementInvoice(mismatch); !errors.Is(err, ErrStatementNotInvoiceable) {
		t.Fatalf("total mismatch err = %v", err)
	}
	empty := api.ProductBillingDocument{Kind: "statement", Currency: "EUR", Snapshot: &api.ProductBillingReview{Lines: []api.ProductBillingLine{line("export", "rev-a", "starter", 1500, 0, 2, 0)}}}
	if _, err := ProductControlStatementInvoice(empty); !errors.Is(err, ErrStatementNotInvoiceable) {
		t.Fatalf("statement without successes err = %v", err)
	}
	doc.Currency = "CLF"
	if _, err := ProductControlStatementInvoice(doc); !errors.Is(err, ErrInvoiceCurrencyUnsupported) {
		t.Fatalf("four-decimal CLF err = %v", err)
	}
}

// adr: 20261021154511 — included starts are admitted at price 0, so their
// successes get a no-charge line beside the paid overage of the same
// feature, and a month inside the included starts is not invoiced.
func TestProductControlStatementInvoiceIncludedStarts(t *testing.T) {
	included := api.ProductBillingLine{Feature: "export", Plan: "starter", Starts: 100, Succeeded: 96, Failed: 4}
	overage := api.ProductBillingLine{Feature: "export", Plan: "starter", UnitPriceMillicents: 20000, Starts: 12, Succeeded: 10, Failed: 2, AmountMillicents: 200000}
	doc := api.ProductBillingDocument{Kind: "statement", Currency: "EUR", AmountMillicents: 200000,
		Snapshot: &api.ProductBillingReview{Lines: []api.ProductBillingLine{included, overage}}}
	inv, err := ProductControlStatementInvoice(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := []StatementInvoiceLine{
		{Label: "included successful export runs on plan starter", Units: 96},
		{Label: "successful export runs on plan starter", Units: 10, PriceMillicentsPerUnit: 20000, AmountMillicents: 200000, AmountMinor: 200},
	}
	if len(inv.Lines) != len(want) || inv.Lines[0] != want[0] || inv.Lines[1] != want[1] || inv.TotalMinor != 200 || inv.RoundingMinor != 0 {
		t.Fatalf("invoice = %+v", inv)
	}
	doc.AmountMillicents, doc.Snapshot.Lines = 0, []api.ProductBillingLine{included}
	if _, err := ProductControlStatementInvoice(doc); !errors.Is(err, ErrStatementNotInvoiceable) {
		t.Fatalf("statement charging nothing err = %v", err)
	}
}
