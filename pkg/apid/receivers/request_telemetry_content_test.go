package receivers

// adr: 1188

import (
	"context"
	"testing"

	"github.com/google/uuid"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRequestTelemetryReceiverStoresKnownContentOutcomes(t *testing.T) {
	for sent, want := range map[string]string{
		"same_300":                    "same_300",
		"":                            "",
		"same_42":                     "", // an outcome this apid does not know drops to empty
		"<script>bad":                 "",
		api.ContentOutcomeUncacheable: api.ContentOutcomeUncacheable,
	} {
		store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}
		r := NewRequestTelemetryReceiver(store, nil, nil, true)
		out := r.HandleOne(context.Background(), &apidpb.IncrementRequestTelemetryRequest{
			AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(),
			RouteTemplate: "GET /catalog", Method: "GET", HttpStatus: 200, LatencyMs: 5, ReceivedAtUnixMs: 1,
			ContentOutcome: sent,
		})
		if out.GetOutcome() != RtOutcomeInserted || len(store.inserted) != 1 {
			t.Fatalf("%q: outcome %q, inserts %d", sent, out.GetOutcome(), len(store.inserted))
		}
		if got := store.inserted[0].ContentOutcome; got != want {
			t.Fatalf("%q stored as %q, want %q", sent, got, want)
		}
	}
}
