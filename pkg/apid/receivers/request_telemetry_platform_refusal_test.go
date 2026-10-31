package receivers

// adr: 20261028110044

import (
	"context"
	"testing"

	"github.com/google/uuid"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A platform refusal mark is stored only on the 5xx it explains.
func TestRequestTelemetryReceiverStoresPlatformRefusal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int32
		refusal bool
		want    bool
	}{
		{"platform refusal", 503, true, true},
		{"application 5xx", 503, false, false},
		{"mark without a 5xx", 429, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}
			r := NewRequestTelemetryReceiver(store, nil, nil, true)
			out := r.HandleOne(context.Background(), &apidpb.IncrementRequestTelemetryRequest{
				AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(),
				RouteTemplate: "other", Method: "GET", HttpStatus: tc.status, LatencyMs: 5, ReceivedAtUnixMs: 1,
				PlatformRefusal: tc.refusal,
			})
			if out.GetOutcome() != RtOutcomeInserted || len(store.inserted) != 1 {
				t.Fatalf("outcome %q, inserts %d", out.GetOutcome(), len(store.inserted))
			}
			if got := store.inserted[0].PlatformRefusal; got != tc.want {
				t.Fatalf("platform_refusal stored as %v, want %v", got, tc.want)
			}
		})
	}
}
