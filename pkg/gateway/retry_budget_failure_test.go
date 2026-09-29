// adr: 375
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// An earlier successful window must not permit a request whose original
// observation failed. The original is served once; only its replay is refused.
type failingOriginalBudget struct{ spends int }

func (*failingOriginalBudget) ObserveOriginal(context.Context, string, time.Duration) error {
	return errors.New("unavailable")
}
func (b *failingOriginalBudget) AllowRetry(context.Context, string, int, int) (bool, error) {
	b.spends++
	return true, nil
}
func (*failingOriginalBudget) BackendID() string { return "test-shared-backend" }

func TestRetryObservationFailureServesOriginalOnce(t *testing.T) {
	backend := &failingOriginalBudget{}
	budget, err := NewSharedRetryBudget(backend)
	if err != nil {
		t.Fatal(err)
	}
	obs := &recordingObserver{}
	count := 0
	runWithRetry(httptest.NewRecorder(), replayableRequest(http.MethodGet, ""), Target{InstanceID: "dead"}, testPolicy(),
		func(Target) {}, func(w http.ResponseWriter, r *http.Request, target Target) { count++; deadTargetAttempt(w, r, target) },
		func() (Target, bool) { return Target{InstanceID: "next"}, true }, obs, retryBudgetAdmission{budget: budget, scope: "app"})
	if count != 1 || backend.spends != 0 {
		t.Fatalf("forwards=%d retry spends=%d", count, backend.spends)
	}
	if len(obs.exhausted) != 1 || obs.exhausted[0] != RetrySkipAggregate {
		t.Fatalf("exhausted=%v", obs.exhausted)
	}
}
