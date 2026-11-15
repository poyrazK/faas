package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A body cut at the plan cap after its 200 went out is recorded as 502 with
// the full latency, not as the 200 the client never finished receiving (prod
// CLI e2e 2026-11-15 #10).
func TestObservedStatus_BodyCutAfterCommitIs502(t *testing.T) {
	tests := []struct {
		name          string
		writes        []string
		wantStatus    int
		wantTruncated bool
	}{
		{"under the cap keeps 200", []string{"aaaa", "bbbb"}, http.StatusOK, false},
		{"past the cap after commit is 502", []string{"aaaa", "bbbb", "cccc"}, http.StatusBadGateway, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
			req := httptest.NewRequest(http.MethodGet, "/big", nil)
			start := time.Now().Add(-3 * time.Second)
			req = req.WithContext(withResponseRecorder(WithStartTime(req.Context(), start), rec))
			rec.WriteHeader(http.StatusOK)
			cw := &capWriter{ResponseWriter: rec, cap: 10, disabled: &atomic.Bool{}, onCap: func() {
				t.Fatal("onCap ran after the status was committed")
			}}
			for _, w := range tt.writes {
				_, _ = cw.Write([]byte(w))
			}
			if got := observedStatus(req, rec.status); got != tt.wantStatus {
				t.Fatalf("observedStatus = %d, want %d", got, tt.wantStatus)
			}
			truncated := truncatedResponse(req)
			if (truncated != nil) != tt.wantTruncated {
				t.Fatalf("truncatedResponse = %v, want truncated=%v", truncated, tt.wantTruncated)
			}
			if tt.wantTruncated && truncated.status != http.StatusOK {
				t.Fatalf("sent status = %d, want the 200 that went out", truncated.status)
			}
			if got := observedLatency(req, observedStatus(req, rec.status), time.Since(start)); got < 3*time.Second {
				t.Fatalf("latency = %v, want the full elapsed", got)
			}
		})
	}
}
