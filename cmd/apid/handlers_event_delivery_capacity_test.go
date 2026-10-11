package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"testing"
)

func TestEventDeliveryCapacityReplayProblems(t *testing.T) {
	err := &state.EventDeliveryCapacityError{Scope: "consumer"}
	for name, problem := range map[string]func(error, string) *api.Problem{"plain": plainReplayProblem, "keyed": keyedReplayProblem, "dead-letter": eventDeliveryReplayProblem} {
		p := problem(err, "invocation")
		if p.Status != http.StatusTooManyRequests || p.Code != "event_delivery_capacity_exhausted" {
			t.Fatalf("%s problem=%+v", name, p)
		}
	}
}
