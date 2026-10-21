package financial

// BillingCurrency is the ISO 4217 code of every Gregale charge, credit,
// refund, invoice and budget (ADR-1430). pkg/api re-exports it; it lives here
// because pkg/api imports this package.
const (
	BillingCurrency      = "USD"
	BillingCurrencyLower = "usd"
	// LegacyBillingCurrency is the currency records written before ADR-1430
	// carry. Those records stay valid history; nothing new is written in it.
	LegacyBillingCurrency = "EUR"
)
