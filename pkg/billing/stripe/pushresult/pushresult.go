// Package pushresult is the closed set of result labels for Stripe usage
// pushes (ADR-024). It imports only the standard library, so pkg/wire can
// pre-create the push histogram's series without linking stripe-go, whose
// package init runs `uname -a` in every binary that links it.
// stripe.ClassifyPushError maps a push error onto these labels.
package pushresult

const (
	OK               = "ok"
	NoAPIKey         = "no-api-key"
	NegativeQuantity = "negative-quantity"
	Other            = "other"
	APIConnection    = "api-connection"
	APIError         = "api-error"
	AuthError        = "auth-error"
	Permission       = "permission"
	CardError        = "card-error"
	InvalidRequest   = "invalid-request"
	RateLimit        = "rate-limit"
)

// Labels returns every label in the closed set. Adding a label also needs
// a stripe.ClassifyPushError arm and the dashboard panel (ADR-024
// §"Consequences").
func Labels() []string {
	return []string{
		OK,
		NoAPIKey,
		NegativeQuantity,
		APIConnection,
		APIError,
		AuthError,
		Permission,
		CardError,
		InvalidRequest,
		RateLimit,
		Other,
	}
}
