package api

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Hunt #10: a cold scenario run failed because one chaos PUT met a
// connection reset. Transport failures are retried; API answers are not.
func TestScenarioChaosRetriesTransportFailures(t *testing.T) {
	old := scenarioControlRetryDelay
	scenarioControlRetryDelay = time.Millisecond
	t.Cleanup(func() { scenarioControlRetryDelay = old })

	var calls atomic.Int32
	reject := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if reject {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"title":"Invalid chaos plan","status":400,"code":"validation"}`))
			return
		}
		if n == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rules_installed":1,"generation":"g2"}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "fp_test_x")

	got, err := c.InjectScenarioTestChaos(t.Context(), "run", InjectScenarioTestChaosRequest{DurationMS: 1000})
	if err != nil || calls.Load() != 2 || got.RulesInstalled != 1 {
		t.Fatalf("inject = %+v, %v after %d calls; want success on the retry", got, err, calls.Load())
	}

	calls.Store(0)
	reject = true
	if _, err := c.InjectScenarioTestChaos(t.Context(), "run", InjectScenarioTestChaosRequest{DurationMS: 1000}); err == nil || calls.Load() != 1 {
		t.Fatalf("a 400 was retried: err=%v calls=%d", err, calls.Load())
	}
}
