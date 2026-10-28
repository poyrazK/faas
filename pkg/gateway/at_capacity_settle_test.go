// adr: 20261027173012
package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// #5709: schedd answered at capacity for an instance the gateway could not
// route to yet, and the request got 429 "max_concurrency is 1; 0 already
// live". The gateway now waits briefly for that instance and serves it, or
// answers a retryable 503 that does not blame the customer's limit.
func TestAtCapacityWithoutRoutableTarget(t *testing.T) {
	for _, tc := range []struct {
		name       string
		appears    bool
		wantStatus int
	}{
		{name: "the slot holder becomes routable", appears: true, wantStatus: http.StatusOK},
		{name: "nothing becomes routable", wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			b.app.MaxConcurrency = 1
			b.atCapForCalls = 1
			if tc.appears {
				time.AfterFunc(200*time.Millisecond, func() {
					b.AddTarget(Target{NodeID: b.upstream, InstanceID: "booting-elsewhere", AddedAt: time.Now()})
				})
			}
			req := httptest.NewRequest(http.MethodGet, "http://"+b.host+"/", nil)
			req.Host = b.host
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.appears {
				return
			}
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != api.CodeCapacity || problem.Title != "App instance starting" || rec.Header().Get("Retry-After") != "1" {
				t.Fatalf("problem=%+v Retry-After=%q", problem, rec.Header().Get("Retry-After"))
			}
		})
	}
}

// A count the gateway can see is still the customer's limit.
func TestAtCapacityWithVisibleInstancesIsTheLimit(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.app.MaxConcurrency = 1
	b.AddTarget(Target{NodeID: b.upstream, InstanceID: "live-1", AddedAt: time.Now()})
	_, err := h.settleAtCapacity(t.Context(), b.app, api.MustLimitsFor(b.app.Plan), 1, func() PickResult { return PickResult{} })
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeAppConcurReached || problem.Observed == nil || *problem.Observed != 1 {
		t.Fatalf("err=%v, want app_concurrency_reached observing 1", err)
	}
}
