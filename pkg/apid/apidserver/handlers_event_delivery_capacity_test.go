package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEventDeliveryCapacityReplayProblems(t *testing.T) {
	err := &state.EventDeliveryCapacityError{Scope: "consumer"}
	for name, problem := range map[string]func(error, string) *api.Problem{"plain": apidserver.PlainReplayProblem, "keyed": apidserver.KeyedReplayProblem, "dead-letter": appevents.EventDeliveryReplayProblem} {
		p := problem(err, "invocation")
		if p.Status != http.StatusTooManyRequests || p.Code != "event_delivery_capacity_exhausted" {
			t.Fatalf("%s problem=%+v", name, p)
		}
	}
}
