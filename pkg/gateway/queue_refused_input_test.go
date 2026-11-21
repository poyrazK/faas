package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type fixedStatusSynthDispatcher struct {
	fakeSynthDispatcher
	code int
}

func (f *fixedStatusSynthDispatcher) InvokeWithStatus(_ context.Context, _ string, inv state.Invocation) (state.Invocation, int, error) {
	inv.State = state.InvocationFailed
	inv.Result = json.RawMessage(`{"error":"bad order"}`)
	return inv, f.code, nil
}

// prod 2026-11-19: a queue message whose handler answered 400 was retried to
// max_attempts like a 503, although docs/dead-letters.md says a refused input
// is dead-lettered. 408 and 429 stay retryable; broker records without a
// durable invocation keep their attempt budget.
func TestQueueMessageRefusedWith4xxIsDeadLetteredAtOnce(t *testing.T) {
	for _, tc := range []struct {
		code    int
		durable bool
		want    string
	}{
		{http.StatusBadRequest, true, "dead_letter"},
		{http.StatusNotFound, true, "dead_letter"},
		{http.StatusTooManyRequests, true, batchDispatchStatusRetry},
		{http.StatusRequestTimeout, true, batchDispatchStatusRetry},
		{http.StatusServiceUnavailable, true, batchDispatchStatusRetry},
		{http.StatusBadRequest, false, batchDispatchStatusRetry},
	} {
		srv := NewSynthServer("/tmp/faas-synth-refused-test.sock", &fixedStatusSynthDispatcher{code: tc.code}, nil)
		req := batchDeliveryRequest()
		rec := req.Records[0]
		if tc.durable {
			rec.InvocationID = "11111111-1111-4111-8111-111111111111"
			rec.ItemIdentifier = rec.InvocationID
			rec.InvocationAttempt = 1
		}
		got := srv.dispatchBatchRecord(context.Background(), req, rec)
		if got.Status != tc.want {
			t.Errorf("code %d durable=%v: status %q, want %q (%s)", tc.code, tc.durable, got.Status, tc.want, got.Error)
		}
		// prod 2026-11-20: a refusal read "max_attempts" after one attempt.
		if wantCode := map[bool]string{true: "function_refused_input", false: "function_state_failed"}[tc.want == "dead_letter"]; got.Code != wantCode {
			t.Errorf("code %d durable=%v: result code %q, want %q", tc.code, tc.durable, got.Code, wantCode)
		}
	}
}
