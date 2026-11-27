package gateway

import (
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
)

// A discarded failed attempt's platform mark must not unbill the app's
// replayed response; a committed platform answer stays unbilled.
func TestRetryWriterPlatformMarkFollowsCommittedAttempt(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	rw := newRetryWriter(rec)
	writeForwarderProblem(rw, 503)
	rw.discard()
	rw.WriteHeader(200)
	_, _ = rw.Write([]byte("app body"))
	rw.finish()
	if rec.isPlatformResponse() {
		t.Fatal("replayed app response marked as platform after a discarded failure")
	}

	rec = &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	rw = newRetryWriter(rec)
	writeForwarderProblem(rw, 503)
	rw.finish()
	if !rec.isPlatformResponse() {
		t.Fatal("committed platform problem not marked")
	}
}

// After the app's status is on the wire, neither a platform mark nor an error
// code header set later unbills what the app already sent.
func TestPlatformMarkAfterCommitKeepsAppBytesBilled(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	rec.WriteHeader(200)
	_, _ = rec.Write([]byte("app body"))
	markPlatformResponse(rec)
	rec.Header().Set(api.ErrorCodeHeader, "app_unavailable")
	if rec.isPlatformResponse() {
		t.Fatal("app response unbilled by a platform write after commit")
	}
}

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
