package billing

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// MinorUnit is how an amount is written in a currency: Exponent decimal
// places, in multiples of Step minor units (ADR-20261019100330). Gregale
// keeps ISO 4217 minor units; a provider may write some currencies
// differently.
type MinorUnit struct {
	Exponent int
	Step     int64
}

// ISOMinorUnit is currency's ISO 4217 minor unit.
func ISOMinorUnit(currency string) (MinorUnit, error) {
	exp, ok := api.CurrencyMinorExponent(currency)
	if !ok {
		return MinorUnit{}, ErrInvoiceCurrencyUnsupported
	}
	return MinorUnit{Exponent: exp, Step: 1}, nil
}

// Stripe writes these currencies without decimals, ISK and UGX as two-decimal
// amounts ending in 00, and three-decimal amounts ending in 0.
var (
	stripeZeroDecimal  = []string{"BIF", "CLP", "DJF", "GNF", "JPY", "KMF", "KRW", "MGA", "PYG", "RWF", "VND", "VUV", "XAF", "XOF", "XPF"}
	stripeWholeHundred = []string{"ISK", "UGX"}
	stripeThreeDecimal = []string{"BHD", "JOD", "KWD", "OMR", "TND"}
)

// StripeMinorUnit is how Stripe writes amounts in currency.
func StripeMinorUnit(currency string) (MinorUnit, error) {
	iso, err := ISOMinorUnit(currency)
	switch {
	case err != nil:
		return MinorUnit{}, err
	case slices.Contains(stripeZeroDecimal, currency):
		return MinorUnit{Exponent: 0, Step: 1}, nil
	case slices.Contains(stripeWholeHundred, currency):
		return MinorUnit{Exponent: 2, Step: 100}, nil
	case slices.Contains(stripeThreeDecimal, currency):
		return MinorUnit{Exponent: 3, Step: 10}, nil
	}
	return iso, nil
}

func (u MinorUnit) step() int64 {
	if u.Step < 1 {
		return 1
	}
	return u.Step
}

// FromMillicents converts millicents to u, rounding half away from zero to
// a multiple of Step.
func (u MinorUnit) FromMillicents(millicents int64) int64 {
	return roundHalfAway(millicents, pow10(5-u.Exponent)*u.step()) * u.step()
}

// ToISOMinor converts an amount written in u to the ISO minor unit with
// isoExponent decimals, rounding half away from zero.
func (u MinorUnit) ToISOMinor(amount int64, isoExponent int) int64 {
	if isoExponent >= u.Exponent {
		return amount * pow10(isoExponent-u.Exponent)
	}
	return roundHalfAway(amount, pow10(u.Exponent-isoExponent))
}

func pow10(n int) int64 {
	out := int64(1)
	for ; n > 0; n-- {
		out *= 10
	}
	return out
}

func roundHalfAway(value, divisor int64) int64 {
	if value < 0 {
		return -((-value + divisor/2) / divisor)
	}
	return (value + divisor/2) / divisor
}
