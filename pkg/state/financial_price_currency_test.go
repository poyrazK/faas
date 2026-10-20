// spec: ADR-1430 — price snapshots written in EUR before the USD switch stay
// valid history; new snapshots must use the billing currency.
package state

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// rc.279's rollout rolled back because meterd never became ready: production
// held October egress snapshots priced in EUR, ListFinancialPriceSnapshots
// rejected them, and every pricing capture tick failed.
func TestStoredFinancialPriceAcceptsPreSwitchCurrency(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	snapshot := func(currency string) FinancialPriceSnapshot {
		return FinancialPriceSnapshot{
			AccountID:     "b3ff5267-c44b-4f8c-b557-d7ff4f400001",
			PeriodStart:   start,
			PeriodEnd:     start.AddDate(0, 1, 0),
			EffectiveFrom: time.Date(2026, 10, 9, 12, 56, 0, 0, time.UTC),
			Plan:          api.PlanFree,
			DeliveryMode:  "off",
			Price: financial.Price{
				Version: "financial-v1-0e5f097559a2850c23099bba5f57bf1f4a7940fcb93d40778c3ec863c75a4161",
				Meter:   "egress", Currency: currency, Unit: "interface_bytes", UnitQuantity: 1,
			},
		}
	}
	for _, tc := range []struct {
		currency       string
		stored, newRow bool
	}{
		{financial.BillingCurrency, true, true},
		{financial.LegacyBillingCurrency, true, false},
		{"GBP", false, false},
		{"", false, false},
	} {
		p := snapshot(tc.currency)
		if got := validStoredFinancialPrice(p); got != tc.stored {
			t.Errorf("stored %q snapshot valid = %v, want %v", tc.currency, got, tc.stored)
		}
		if got := validFinancialPrice(p); got != tc.newRow {
			t.Errorf("new %q snapshot valid = %v, want %v", tc.currency, got, tc.newRow)
		}
	}
	// The legacy currency does not relax any other rule.
	broken := snapshot(financial.LegacyBillingCurrency)
	broken.PeriodEnd = start.AddDate(0, 2, 0)
	if validStoredFinancialPrice(broken) {
		t.Error("a pre-switch snapshot spanning two months was accepted")
	}
}
