package api

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/financial"
)

// Gregale bills every plan fee, overage, credit, refund, invoice and budget in
// one currency (ADR-1501). Money stays integer cents or millicents of it.
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

// FormatBillingMillicents renders millicents as "$X.YY", keeping sub-cent
// digits only when the amount has them ("$0.00125").
func FormatBillingMillicents(millicents int64) string {
	sign := ""
	if millicents < 0 {
		sign, millicents = "-", -millicents
	}
	perUnit := 100 * MillicentsPerCent
	fraction := strings.TrimRight(fmt.Sprintf("%05d", millicents%perUnit), "0")
	for len(fraction) < 2 {
		fraction += "0"
	}
	return fmt.Sprintf("%s%s%d.%s", sign, BillingCurrencySymbol, millicents/perUnit, fraction)
}
