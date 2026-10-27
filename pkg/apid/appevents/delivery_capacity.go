package appevents

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func EventDeliveryReplayProblem(err error, fallback string) *api.Problem {
	if errors.Is(err, state.ErrEventDeliveryCapacity) {
		return api.NewProblem(http.StatusTooManyRequests, "event_delivery_capacity_exhausted", "Event delivery capacity exhausted", "retry recovery after pending deliveries drain")
	}
	if errors.Is(err, state.ErrDeadLetterNotReplayable) {
		return api.ErrDeadLetterNotReplayable()
	}
	return api.ErrInternal(fallback)
}
