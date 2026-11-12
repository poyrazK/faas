package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// adr: 20261110090000 — on production (2026-11-09) a synthetic check that
// gave up after 30 s and a client that gave up after 10 s were both recorded
// as 200, so an abandoned request counted as a success. A request whose
// client left before any response was written is recorded as 499; a written
// response keeps its status, and a budget timeout is not a client abort.
func TestObservedStatusRecordsAbandonedRequestsAs499(t *testing.T) {
	cancelled := func() context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	expired := func() context.Context {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		t.Cleanup(cancel)
		return ctx
	}
	for _, tc := range []struct {
		name        string
		ctx         context.Context
		wroteHeader bool
		status      int
		want        int
	}{
		{"client left before any response", cancelled(), false, http.StatusOK, statusClientClosedRequest},
		{"client left after the response began", cancelled(), true, http.StatusOK, http.StatusOK},
		{"request still open", context.Background(), false, http.StatusOK, http.StatusOK},
		{"request budget ran out", expired(), false, http.StatusGatewayTimeout, http.StatusGatewayTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &statusRecorder{status: http.StatusOK, wroteHeader: tc.wroteHeader}
			r := httptest.NewRequest(http.MethodGet, "/slow", nil).WithContext(withResponseRecorder(tc.ctx, rec))
			if got := observedStatus(r, tc.status); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
	if got := observedStatus(httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK); got != http.StatusOK {
		t.Fatalf("status without a recorder = %d", got)
	}
	if statusClassBucket(statusClientClosedRequest) != "4xx" {
		t.Fatalf("499 must count as a client error, got class %q", statusClassBucket(statusClientClosedRequest))
	}
}
