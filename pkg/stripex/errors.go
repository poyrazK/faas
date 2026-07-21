package stripex

import (
	"errors"

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
//	errors.Is(err, ErrNoAPIKey)         -> "no-api-key"
//	errors.Is(err, ErrNegativeQuantity) -> "negative-quantity"
//	any other error                    -> "other"
//
// The errors.Is branches catch the two errors pushUsageRecordSDK
// synthesizes before the SDK is invoked (no apiKey, negative quantity)
// — these never become *stripe.Error, so they need their own labels.
// They are matched via the sentinels declared at usage.go:17-30,
// not by string-fragment, so adding context to the wrapped message
// (account id, qty) does not change classification.
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
	// the network. Match by sentinel (declared at usage.go) so the
	// wrapped message can carry diagnostic context (account id, qty)
	// without changing the classification. Sentinels are added when a
	// new pre-SDK failure mode is introduced.
	if errors.Is(err, ErrNoAPIKey) {
		return labelNoAPIKey
	}
	if errors.Is(err, ErrNegativeQuantity) {
		return labelNegativeQuantity
	}

	// SDK errors — unwrap with errors.As. pushUsageRecordSDKWithID
	// returns the wrapped *stripe.Error via fmt.Errorf with %w at
	// usage.go:92.
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

// PushResultLabels returns the closed set of result labels ClassifyPushError
// may emit, in stable order. The set is the canonical list for the
// `_stripe_push_duration_seconds` and `meterd_ops_total{op="stripe",code}`
// label tuples — pkg/wire pre-instantiates every label here at registry
// construction time so the histogram's HELP/TYPE lines appear in
// `/metrics` from the moment the daemon boots, even before the first
// push. Without pre-instantiation, Prometheus' default exposition skips
// histograms with zero observed label tuples, which would make the
// dashboard's panel render as "no data" until at least one push
// happened — a real-world ops hazard.
//
// Adding a new label requires editing this function AND the dashboard's
// panel config; do not extend ClassifyPushError's switch arms without
// also adding the label here. Documented in ADR-024 §"Consequences".
func PushResultLabels() []string {
	return []string{
		labelOK,
		labelNoAPIKey,
		labelNegativeQuantity,
		labelAPIConnection,
		labelAPIError,
		labelAuthError,
		labelPermission,
		labelCardError,
		labelInvalidRequest,
		labelRateLimit,
		labelOther,
	}
}
