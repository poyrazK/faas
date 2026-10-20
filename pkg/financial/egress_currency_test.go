package financial

import "testing"

// adr: 1430, 1642 — equal numeric rates in different currencies cannot fund
// each other; historical EUR contracts are never silently relabelled as USD.
func TestEgressFundingCurrency(t *testing.T) {
	book := EgressBook{RateMillicents: 1, RateQuantity: 1024}
	for _, tc := range []struct {
		currency string
		fits     bool
	}{{BillingCurrency, true}, {"EUR", false}, {"usd", false}, {"", false}} {
		t.Run(tc.currency, func(t *testing.T) {
			price := Price{Version: "retained", Meter: "egress", Currency: tc.currency, Unit: "interface_bytes", UnitQuantity: 1024, MillicentsPerUnit: 1}
			if got := EgressContractFits(price, book); got != tc.fits {
				t.Fatalf("currency %q: funding fits=%v, want %v", tc.currency, got, tc.fits)
			}
		})
	}
}
