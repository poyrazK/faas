package billing

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019100330 — statements invoice in any ISO 4217 currency with at
// most three decimals, and Stripe's representation is converted both ways.
func TestCurrencyMinorUnits(t *testing.T) {
	for _, tc := range []struct {
		currency            string
		millicents          int64
		iso, stripe         int64
		stripeErr, isoValid bool
	}{
		{currency: "EUR", millicents: 1_234_567, iso: 1235, stripe: 1235, isoValid: true},
		{currency: "JPY", millicents: 15_050_000, iso: 151, stripe: 151, isoValid: true},
		{currency: "KWD", millicents: 1_234_567, iso: 12346, stripe: 12350, isoValid: true},
		{currency: "ISK", millicents: 15_050_000, iso: 151, stripe: 15100, isoValid: true},
		{currency: "MGA", millicents: 15_050_000, iso: 15050, stripe: 151, isoValid: true},
		{currency: "CLF", millicents: 100_000, stripeErr: true},
	} {
		iso, err := ISOMinorUnit(tc.currency)
		if (err == nil) != tc.isoValid {
			t.Fatalf("%s: ISO err = %v", tc.currency, err)
		}
		stripe, err := StripeMinorUnit(tc.currency)
		if tc.stripeErr {
			if !errors.Is(err, ErrInvoiceCurrencyUnsupported) {
				t.Fatalf("%s: Stripe err = %v", tc.currency, err)
			}
			continue
		}
		if got := iso.FromMillicents(tc.millicents); got != tc.iso {
			t.Errorf("%s ISO = %d, want %d", tc.currency, got, tc.iso)
		}
		if got := stripe.FromMillicents(tc.millicents); got != tc.stripe {
			t.Errorf("%s Stripe = %d, want %d", tc.currency, got, tc.stripe)
		}
		if back := stripe.ToISOMinor(tc.stripe, iso.Exponent); back != iso.FromMillicents(stripeMillicents(stripe, tc.stripe)) {
			t.Errorf("%s Stripe→ISO = %d", tc.currency, back)
		}
	}
}

// stripeMillicents is the millicents a Stripe amount stands for.
func stripeMillicents(u MinorUnit, amount int64) int64 { return amount * pow10(5-u.Exponent) }

// adr: 20261019100330 — a JPY statement invoices in whole yen, and rewriting
// it for Stripe's ISK-style or three-decimal units keeps the total exact.
func TestStatementInvoiceInProviderUnits(t *testing.T) {
	statement := state.PlatformTenantStatement{Currency: "JPY", AmountMillicents: 250_050_000,
		Lines: []state.PlatformTenantStatementLine{
			{PlatformTenantRateCardID: "c1", BillableUnits: 3, PriceMillicentsPerUnit: 50_000},
			{PlatformTenantRateCardID: "c2", BillableUnits: 1, PriceMillicentsPerUnit: 249_900_000},
		}}
	inv, err := TenantStatementInvoice(statement)
	if err != nil {
		t.Fatal(err)
	}
	if inv.TotalMinor != 2501 || inv.Minor.Exponent != 0 {
		t.Fatalf("JPY invoice = %+v", inv)
	}
	var sum int64
	for _, line := range inv.Lines {
		sum += line.AmountMinor
	}
	if sum+inv.RoundingMinor != inv.TotalMinor {
		t.Fatalf("items %d + rounding %d != total %d", sum, inv.RoundingMinor, inv.TotalMinor)
	}
	stripe := inv.In(MinorUnit{Exponent: 2, Step: 100})
	sum = 0
	for _, line := range stripe.Lines {
		if line.AmountMinor%100 != 0 {
			t.Fatalf("line %d is not whole", line.AmountMinor)
		}
		sum += line.AmountMinor
	}
	if stripe.TotalMinor != 250100 || sum+stripe.RoundingMinor != stripe.TotalMinor {
		t.Fatalf("Stripe-unit invoice = %+v", stripe)
	}
}
