package appevents

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// QueueCapProblem is the limit problem a send gets when a replayed queue
// message finds its app at the plan's queue depth or its account at the
// plan's queue backlog bytes (ADR-20261101093812), or nil for any other
// error.
func QueueCapProblem(err error) *api.Problem {
	var depth *state.InvocationDepthError
	if errors.As(err, &depth) {
		return api.ErrPlanQueueDepth(depth.Limit, depth.Observed)
	}
	var backlog *state.InvocationBacklogBytesError
	if errors.As(err, &backlog) {
		return api.ErrPlanQueueBacklogBytes(backlog.Limit, backlog.Observed)
	}
	return nil
}

func EventDeliveryReplayProblem(err error, fallback string) *api.Problem {
	if problem := QueueCapProblem(err); problem != nil {
		return problem
	}
	if errors.Is(err, state.ErrEventDeliveryCapacity) {
		return api.NewProblem(http.StatusTooManyRequests, "event_delivery_capacity_exhausted", "Event delivery capacity exhausted", "retry recovery after pending deliveries drain")
	}
	if errors.Is(err, state.ErrDeadLetterNotReplayable) {
		return api.ErrDeadLetterNotReplayable()
	}
	if errors.Is(err, state.ErrKeyedReplayExpired) {
		return api.ErrDeadLetterKeyedReplayExpired()
	}
	return api.ErrInternal(fallback)
}
