package stripex

import (
	"errors"
	"strings"

	stripe "github.com/stripe/stripe-go"
)

// Push error classification — maps a Stripe push failure to a short,
// stable Prometheus label suitable for the meterd dashboard. The label
// set is closed and matches the bucket count the §14 M7 dashboard
// panels are designed around; a new bucket requires a dashboard
// revision, not a code change.
//
// The classifier is the seam between stripex (which knows about
// *stripe.Error) and pkg/wire / pkg/meter (which only know about
// labels). Lives in stripex so the SDK-coupled part stays there; the
// pusher just calls stripex.ClassifyPushError(err) and observes the
// returned string.
//
// Mapping table:
//
//	nil                                -> "ok"
//	*stripe.Error{Type: APIConnection} -> "api-connection"
//	*stripe.Error{Type: API}           -> "api-error"
//	*stripe.Error{Type: Authentication}-> "auth-error"
//	*stripe.Error{Type: Permission}    -> "permission"
//	*stripe.Error{Type: Card}          -> "card-error"
//	*stripe.Error{Type: InvalidRequest}-> "invalid-request"
//	*stripe.Error{Type: RateLimit}     -> "rate-limit"
//	*stripe.Error{Type: …} (other)     -> "other"
//	any other error containing "apiKey" -> "no-api-key"
//	any other error containing "negative" -> "negative-quantity"
//	any other error                    -> "other"
//
// The string-match branches catch the two errors pushUsageRecordSDK
// synthesizes before the SDK is invoked (no apiKey, negative quantity)
// — these never become *stripe.Error, so they need their own labels.
//
// "ok" is intentionally returned for nil so the pusher can write a
// uniform:
//
//	code := stripex.ClassifyPushError(err)
//	ops.ObserveCode("stripe", code, dur)
//	ops.StripePushDuration(code).Observe(...)
//
// with no separate success branch — code=="" would mean "skip", which
// is a different semantic the dashboard labels differently.
const (
	labelOK               = "ok"
	labelNoAPIKey         = "no-api-key"
	labelNegativeQuantity = "negative-quantity"
	labelOther            = "other"
	labelAPIConnection    = "api-connection"
	labelAPIError         = "api-error"
	labelAuthError        = "auth-error"
	labelPermission       = "permission"
	labelCardError        = "card-error"
	labelInvalidRequest   = "invalid-request"
	labelRateLimit        = "rate-limit"
)

func ClassifyPushError(err error) string {
	if err == nil {
		return labelOK
	}

	// Pre-SDK errors — pushUsageRecordSDK raises these before touching
	// the network. Detect them by string fragment rather than type
	// assertion because they're fmt.Errorf-wrapped, not exported types.
	// The fragments match the format strings at usage.go:52,59.
	msg := err.Error()
	if strings.Contains(msg, "apiKey") {
		return labelNoAPIKey
	}
	if strings.Contains(msg, "negative") {
		return labelNegativeQuantity
	}

	// SDK errors — unwrap with errors.As. pushUsageRecordSDKWithID
	// returns the wrapped *stripe.Error via fmt.Errorf with %w at
	// usage.go:72.
	var se *stripe.Error
	if !errors.As(err, &se) {
		return labelOther
	}

	switch se.Type {
	case stripe.ErrorTypeAPIConnection:
		return labelAPIConnection
	case stripe.ErrorTypeAPI:
		return labelAPIError
	case stripe.ErrorTypeAuthentication:
		return labelAuthError
	case stripe.ErrorTypePermission:
		return labelPermission
	case stripe.ErrorTypeCard:
		return labelCardError
	case stripe.ErrorTypeInvalidRequest:
		return labelInvalidRequest
	case stripe.ErrorTypeRateLimit:
		return labelRateLimit
	default:
		return labelOther
	}
}
