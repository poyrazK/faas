package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func busyRefusal(w http.ResponseWriter, r *http.Request) {
	markNodeBusy(r.Context())
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = io.WriteString(w, `{"code":"concurrency_throttled"}`)
}

// prod 2026-11-19: with overflow=queue, a node's instance-busy refusal
// reached the client at once as 429. It is now retried within the queue wait.
func TestNodeBusyRefusalIsQueuedThenServed(t *testing.T) {
	calls := 0
	var targets []string
	forward := func(w http.ResponseWriter, r *http.Request, tgt Target) {
		calls++
		targets = append(targets, tgt.InstanceID)
		if calls < 3 {
			busyRefusal(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}
	repick := func(prev Target) (Target, bool) { return Target{InstanceID: "other-" + prev.InstanceID}, true }
	rec := httptest.NewRecorder()
	waitOutNodeBusy(2*time.Second, forward, repick)(rec, httptest.NewRequest(http.MethodGet, "/", nil), Target{InstanceID: "a"})
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" || calls != 3 {
		t.Fatalf("status=%d body=%q calls=%d; want the queued request served", rec.Code, rec.Body.String(), calls)
	}
	if rec.Header().Get("Retry-After") != "" {
		t.Fatalf("a discarded refusal leaked its headers: %v", rec.Header())
	}
	if targets[1] != "other-a" {
		t.Fatalf("targets=%v; want the retry to prefer another instance", targets)
	}
}

func TestNodeBusyRefusalIsRelayedAfterTheQueueWait(t *testing.T) {
	calls := 0
	forward := func(w http.ResponseWriter, r *http.Request, _ Target) {
		calls++
		busyRefusal(w, r)
	}
	rec := httptest.NewRecorder()
	start := time.Now()
	waitOutNodeBusy(150*time.Millisecond, forward, func(Target) (Target, bool) { return Target{}, false })(rec, httptest.NewRequest(http.MethodGet, "/", nil), Target{})
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "concurrency_throttled") || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("status=%d body=%q header=%v; want the refusal relayed unchanged", rec.Code, rec.Body.String(), rec.Header())
	}
	if calls < 2 || time.Since(start) > time.Second {
		t.Fatalf("calls=%d after %s; want retries bounded by the queue wait", calls, time.Since(start))
	}
}

func TestNodeBusyWaitDoesNotReplayAnUnreplayableBody(t *testing.T) {
	calls := 0
	forward := func(w http.ResponseWriter, r *http.Request, _ Target) {
		calls++
		busyRefusal(w, r)
	}
	req := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(strings.NewReader("payload")))
	req.GetBody = nil
	rec := httptest.NewRecorder()
	waitOutNodeBusy(time.Second, forward, func(Target) (Target, bool) { return Target{}, false })(rec, req, Target{})
	if calls != 1 || rec.Code != http.StatusTooManyRequests {
		t.Fatalf("calls=%d status=%d; want one attempt for a body that cannot be replayed", calls, rec.Code)
	}
}

func TestNodeBusyWaitPassesOtherResponsesThrough(t *testing.T) {
	forward := func(w http.ResponseWriter, _ *http.Request, _ Target) {
		w.Header().Set("X-App", "1")
		w.WriteHeader(http.StatusTooManyRequests) // the app's own 429
		_, _ = io.WriteString(w, "slow down")
	}
	rec := httptest.NewRecorder()
	waitOutNodeBusy(time.Second, forward, func(Target) (Target, bool) { return Target{}, false })(rec, httptest.NewRequest(http.MethodGet, "/", nil), Target{})
	if rec.Code != http.StatusTooManyRequests || rec.Body.String() != "slow down" || rec.Header().Get("X-App") != "1" {
		t.Fatalf("status=%d body=%q; want the app's own 429 unchanged", rec.Code, rec.Body.String())
	}
}
