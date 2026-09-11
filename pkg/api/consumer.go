package api

import (
	"fmt"
	"net/http"
)

const (
	ConsumerAuthModeOptional    = "optional"
	ConsumerAuthModeRequired    = "required"
	CodeConsumerAuthModeInvalid = "consumer_auth_mode_invalid"
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
