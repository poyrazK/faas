package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// guestEventPublishBudget bounds one answered in-guest publish. guest-init
// waits 4 s for the verdict, so vmmd answers well inside that.
const guestEventPublishBudget = 3 * time.Second

// guestEventsPausedRetrySeconds is the Retry-After for a paused account,
// as POST /v1/events:publish answers while the events flag is off.
const guestEventsPausedRetrySeconds = 30

// errGuestEventPublishUnavailable: this vmmd has nowhere to persist the event.
var errGuestEventPublishUnavailable = errors.New("in-guest event publish is unavailable")

// guestEventRateLimitedError refuses a publish over the account's rate.
type guestEventRateLimitedError struct {
	perSecond int
	wait      time.Duration
}

func (e guestEventRateLimitedError) Error() string {
	return fmt.Sprintf("event publish rate limited: the account publishes at most %d events per second; retry the same event after %s",
		e.perSecond, e.wait.Round(time.Millisecond))
}

// guestEventPublishVerdict turns a publish result into the answer guest-init
// returns to the app (ADR-20261031102000). Refusals the app can act on keep
// their own status; anything else is a retryable 503.
func guestEventPublishVerdict(err error) api.EventPublishVerdict {
	var limited guestEventRateLimitedError
	var syntax *json.SyntaxError
	var mistyped *json.UnmarshalTypeError
	switch {
	case err == nil:
		return api.EventPublishVerdict{Status: http.StatusAccepted}
	case errors.Is(err, errGuestEventsPaused):
		return api.EventPublishVerdict{Status: http.StatusServiceUnavailable, Code: api.CodeEventsPaused, RetryAfterSeconds: guestEventsPausedRetrySeconds}
	case errors.As(err, &limited):
		return api.EventPublishVerdict{Status: http.StatusTooManyRequests, Code: api.CodeEventPublishRateLimited,
			RetryAfterSeconds: max(1, int(math.Ceil(limited.wait.Seconds())))}
	case errors.Is(err, state.ErrEventSchemaRequired), errors.Is(err, state.ErrEventSchemaUnknown), errors.Is(err, state.ErrEventDataInvalid):
		return api.EventPublishVerdict{Status: http.StatusUnprocessableEntity, Code: api.CodeValidation}
	case errors.Is(err, events.ErrPublishInvalid), errors.As(err, &syntax), errors.As(err, &mistyped):
		return api.EventPublishVerdict{Status: http.StatusBadRequest, Code: "invalid_event"}
	default:
		return api.EventPublishVerdict{Status: http.StatusServiceUnavailable, Code: "event_publish_unavailable", RetryAfterSeconds: 1}
	}
}
