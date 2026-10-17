package billing

import (
	"errors"
	"testing"
	"time"

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
	statement.Currency = "JPY"
	if _, err := TenantStatementInvoice(statement); !errors.Is(err, ErrInvoiceCurrencyUnsupported) {
		t.Fatalf("JPY err = %v", err)
	}
	statement.Currency, statement.AmountMillicents = "EUR", 999
	if _, err := TenantStatementInvoice(statement); !errors.Is(err, ErrStatementNotInvoiceable) {
		t.Fatalf("total mismatch err = %v", err)
	}
	if got := millicentsToCents(-1500); got != -2 {
		t.Fatalf("millicentsToCents(-1500) = %d, want -2", got)
	}
}
