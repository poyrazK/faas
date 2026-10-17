package api

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/financial"
)

// Gregale bills every plan fee, overage, credit, refund, invoice and budget in
// one currency (ADR-1430). Money stays integer cents or millicents of it.
const (
	// BillingCurrency is the ISO 4217 code of every Gregale charge.
	BillingCurrency = financial.BillingCurrency
	// BillingCurrencyLower is the lowercase form billing providers use on
	// the wire.
	BillingCurrencyLower = financial.BillingCurrencyLower
	// BillingCurrencySymbol prefixes formatted amounts.
	BillingCurrencySymbol = "$"
)

// FormatBillingCents renders integer cents as "$X.YY"; negative amounts keep
// the sign before the symbol.
func FormatBillingCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s%s%d.%02d", sign, BillingCurrencySymbol, cents/100, cents%100)
}
