package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// adr: 20261109120000 — a long-lived response (an upgrade, a marked
// long-lived forward, or an event stream) is recorded with its time to
// headers, not its lifetime; an ordinary response keeps its full duration.
func TestObservedLatencyUsesTimeToHeadersForLongLivedResponses(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute)
	elapsed := 10 * time.Minute
	for _, tc := range []struct {
		name        string
		status      int
		contentType string
		longLived   bool
		want        time.Duration
	}{
		{name: "a WebSocket upgrade", status: http.StatusSwitchingProtocols, longLived: true, want: 80 * time.Millisecond},
		{name: "an event stream", status: http.StatusOK, contentType: "text/event-stream; charset=utf-8", want: 80 * time.Millisecond},
		{name: "a marked long-lived forward", status: http.StatusOK, contentType: "application/grpc", longLived: true, want: 80 * time.Millisecond},
		{name: "an ordinary slow response", status: http.StatusOK, contentType: "application/json", want: elapsed},
		{name: "an event stream that failed", status: http.StatusServiceUnavailable, contentType: "text/event-stream", want: elapsed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &statusRecorder{ContentType: tc.contentType, headerAt: start.Add(80 * time.Millisecond)}
			rec.trafficResponseLongLived = func(int) bool { return tc.longLived }
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r = r.WithContext(withResponseRecorder(WithStartTime(r.Context(), start), rec))
			if got := observedLatency(r, tc.status, elapsed); got != tc.want {
				t.Fatalf("latency = %v, want %v", got, tc.want)
			}
		})
	}
	// Without a recorder, or before headers, the elapsed time stands.
	if got := observedLatency(httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK, elapsed); got != elapsed {
		t.Fatalf("latency without a recorder = %v", got)
	}
}
