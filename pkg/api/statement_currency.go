package api

// Customers price their own APIs in any ISO 4217 currency with at most three
// decimal places (ADR-20261019100330). Statements keep millicents, 1/100000
// of the main unit; invoices and payment amounts use the currency's minor
// unit.

// currencyExponents lists the ISO 4217 currencies whose minor unit is not
// one hundredth of the main unit. CLF and UYW have four decimals, which
// millicents cannot hold exactly enough to invoice, so they are refused.
var currencyExponents = map[string]int{
	"BIF": 0, "CLP": 0, "DJF": 0, "GNF": 0, "ISK": 0, "JPY": 0, "KMF": 0, "KRW": 0, "PYG": 0, "RWF": 0,
	"UGX": 0, "UYI": 0, "VND": 0, "VUV": 0, "XAF": 0, "XOF": 0, "XPF": 0,
	"BHD": 3, "IQD": 3, "JOD": 3, "KWD": 3, "LYD": 3, "OMR": 3, "TND": 3,
	"CLF": 4, "UYW": 4,
}

// CurrencyMinorExponent is the number of decimal places of currency's minor
// unit, and false for a code statements cannot invoice.
func CurrencyMinorExponent(currency string) (int, bool) {
	exp, listed := currencyExponents[currency]
	if !listed {
		exp = 2
	}
	return exp, len(currency) == 3 && exp <= 3
}

// MinorToMillicents converts an amount in currency's minor unit to
// millicents.
func MinorToMillicents(currency string, minor int64) int64 {
	exp, _ := CurrencyMinorExponent(currency)
	for ; exp < 5; exp++ {
		minor *= 10
	}
	return minor
}
