package main

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func eventDeliveryReplayProblem(err error, fallback string) *api.Problem {
	if errors.Is(err, state.ErrEventDeliveryCapacity) {
		return api.NewProblem(http.StatusTooManyRequests, "event_delivery_capacity_exhausted", "Event delivery capacity exhausted", "retry recovery after pending deliveries drain")
	}
	return api.ErrInternal(fallback)
}
