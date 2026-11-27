package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Observe audit 2026-11-21 (findings 8 and 15): 429s the gateway answered for
// lack of app capacity were invisible to customer SLOs, error rates and outage
// detection, which counted only 5xx.
func TestWakeCapacityRefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"drop overflow", &WakeConcurrencyDropError{}, true},
		{"queue full", fmt.Errorf("wake: %w", ErrConcurrencyQueueFull), true},
		{"plan concurrency", api.NewProblem(http.StatusTooManyRequests, api.CodeAppConcurReached, "t", "d"), true},
		{"queue timeout is a 503", ErrConcurrencyQueueWaitTimeout, false},
		{"fleet capacity is a platform 503", api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "t", "d"), false},
		{"other error", errors.New("boom"), false},
	} {
		if got := wakeCapacityRefusal(tc.err); got != tc.want {
			t.Errorf("%s: wakeCapacityRefusal = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCapacityRefusalForSLO(t *testing.T) {
	newReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		return r.WithContext(withCapacityRefusalMark(WithFirstByteRecorder(r.Context(), &firstByteRecorder{})))
	}
	r := newReq()
	if capacityRefusalForSLO(r, http.StatusTooManyRequests) {
		t.Fatal("unmarked 429 (a rate limit) counted as a capacity refusal")
	}
	markCapacityRefused(r)
	if !capacityRefusalForSLO(r, http.StatusTooManyRequests) {
		t.Fatal("marked capacity 429 not counted")
	}
	if capacityRefusalForSLO(r, http.StatusServiceUnavailable) {
		t.Fatal("a 503 is counted by the platform error counter, not as a capacity refusal")
	}
	answered := newReq()
	markCapacityRefused(answered)
	answered.Context().Value(firstByteAtKey{}).(*firstByteRecorder).record(time.Now())
	if capacityRefusalForSLO(answered, http.StatusTooManyRequests) {
		t.Fatal("a 429 the guest answered itself counted as a gateway capacity refusal")
	}
	markCapacityRefused(nil) // no request: ignored
}

func TestWriteRequestWakeErrorMarksCapacityRefusal(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(withCapacityRefusalMark(r.Context()))
	w := httptest.NewRecorder()
	writeRequestWakeError(w, r, ErrConcurrencyQueueFull)
	if w.Code != http.StatusTooManyRequests || !capacityRefusalForSLO(r, w.Code) {
		t.Fatalf("status %d, counted %v; want a counted 429", w.Code, capacityRefusalForSLO(r, w.Code))
	}
}
