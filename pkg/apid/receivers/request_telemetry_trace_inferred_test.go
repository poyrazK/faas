package receivers

// adr: 1138

import (
	"context"
	"testing"

	"github.com/google/uuid"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// An inferred trace link is stored only with the trace id it marks.
func TestRequestTelemetryReceiverStoresTraceInferred(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	for _, tc := range []struct {
		name     string
		traceID  string
		inferred bool
		want     bool
	}{
		{"inferred link", traceID, true, true},
		{"propagated trace", traceID, false, false},
		{"mark without a trace id", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}
			r := NewRequestTelemetryReceiver(store, nil, nil, true)
			out := r.HandleOne(context.Background(), &apidpb.IncrementRequestTelemetryRequest{
				AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(),
				RouteTemplate: "other", Method: "GET", HttpStatus: 200, LatencyMs: 5, ReceivedAtUnixMs: 1,
				TraceId: tc.traceID, TraceInferred: tc.inferred,
			})
			if out.GetOutcome() != RtOutcomeInserted || len(store.inserted) != 1 {
				t.Fatalf("outcome %q, inserts %d", out.GetOutcome(), len(store.inserted))
			}
			if got := store.inserted[0].TraceInferred; got != tc.want {
				t.Fatalf("trace_inferred stored as %v, want %v", got, tc.want)
			}
		})
	}
}
