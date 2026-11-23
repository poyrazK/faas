package gateway

import (
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
)

// App 4xx/5xx bodies leave the platform like any other response and are
// billed; responses the platform writes itself (a marked writer, or a
// problem carrying api.ErrorCodeHeader) and informational statuses are not.
func TestRecordEgress_StatusBilling(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		platform bool
		problem  bool
		want     uint64
	}{
		{name: "informational", status: 199},
		{name: "ok", status: 200, want: 100},
		{name: "app 404", status: 404, want: 100},
		{name: "app 500", status: 500, want: 100},
		{name: "app 502", status: 502, want: 100},
		{name: "marked platform 502", status: 502, platform: true},
		{name: "platform problem 504", status: 504, problem: true},
		{name: "platform problem 429", status: 429, problem: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{metrics: newMetric(t), egressSink: egresssink.NewEgressSink()}
			w := httptest.NewRecorder()
			if tc.problem {
				w.Header().Set(api.ErrorCodeHeader, "request_budget_exceeded")
			}
			rec := &statusRecorder{ResponseWriter: w, status: tc.status, Bytes: 100}
			if tc.platform {
				markPlatformResponse(rec)
			}
			h.recordEgress(rec, Target{InstanceID: "inst-1"}, App{ID: "app-1"})
			var got uint64
			for _, r := range h.egressSink.DrainRecords() {
				got += r.Bytes
			}
			if got != tc.want {
				t.Fatalf("billed bytes = %d, want %d", got, tc.want)
			}
		})
	}
}
