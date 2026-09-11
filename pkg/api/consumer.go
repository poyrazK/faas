package api

import (
	"fmt"
	"net/http"
)

const (
	ConsumerAuthModeOptional    = "optional"
	ConsumerAuthModeRequired    = "required"
	CodeConsumerAuthModeInvalid = "consumer_auth_mode_invalid"
	CodeConsumerKeyRequired     = "consumer_key_required"
	CodeConsumerKeyInvalid      = "consumer_key_invalid"
	CodeConsumerKeyInactive     = "consumer_key_inactive"
	CodeConsumerScopeMissing    = "consumer_scope_missing"
)

func ErrInvalidConsumerAuthMode(mode string) *Problem {
	return NewProblem(422, CodeConsumerAuthModeInvalid, "Invalid consumer_auth_mode",
		"consumer_auth_mode must be 'optional' or 'required'; got "+mode)
}

func ErrConsumerKeysNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodeConsumerKeysNotAllowed,
		"Consumer keys unavailable on this plan",
		fmt.Sprintf("the %s plan does not include consumer keys; upgrade to Hobby or above to authenticate your API customers.", p)).
		WithLimit(0, 0).WithDocs(docsBase + "/plans#consumer-keys")
}

func ErrConsumerKeyQuota(p Plan, scope string, limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanConsumerKeyQuotaReached,
		"Consumer key limit reached",
		fmt.Sprintf("%s plan caps consumer keys at %d per %s; you have %d. Revoke one to add another.",
			p, limit, PlanQuotaScopeDisplayName(scope), observed)).
		WithLimit(int64(limit), int64(observed)).WithDocs(docsBase + "/plans#consumer-keys")
}

// The consumer-key authentication problems are intentionally distinct from
// operator API-key errors. End-customer clients can use these stable codes to
// decide whether to prompt for a key, rotate an inactive key, or change the
// requested method's scope without parsing gateway prose.

func ErrConsumerKeyRequired() *Problem {
	return NewProblem(http.StatusUnauthorized, CodeConsumerKeyRequired,
		"Consumer key required", "this app requires Authorization: Bearer <consumer-key>")
}

func ErrConsumerKeyInvalid() *Problem {
	return NewProblem(http.StatusUnauthorized, CodeConsumerKeyInvalid,
		"Invalid consumer key", "the presented consumer key is not valid for this app")
}

func ErrConsumerKeyInactive() *Problem {
	return NewProblem(http.StatusUnauthorized, CodeConsumerKeyInactive,
		"Inactive consumer key", "the presented consumer key has been revoked or expired")
}

func ErrConsumerScopeMissing(method string) *Problem {
	detail := "the consumer key does not grant access to this request method"
	if method != "" {
		detail = "the consumer key does not grant access to " + method + " requests"
	}
	return NewProblem(http.StatusForbidden, CodeConsumerScopeMissing,
		"Consumer scope missing", detail)
}
