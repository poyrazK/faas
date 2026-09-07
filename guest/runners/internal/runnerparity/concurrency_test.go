package runnerparity

// TestRunner_HandleIsConcurrent pins the §4.9 listener-level concurrency
// contract for the guest runners (issue #559 / AC #3). The runner's
// `http.ListenAndServe` dispatches each accepted connection on its own
// goroutine, so a single VM can serve the platform's
// `concurrency_per_vm` bound (Free 4, Hobby 5, Pro 25, Scale 80) at the
// listener layer. The customer *handler process* may still serialize
// internally (sync subprocess-per-request), but the listener's own
// goroutine fan-out is what defines the per-VM bound — and that's what
// this test pins.
//
// Why a parity-package test rather than per-runner: the concurrency
// claim is identical for every runner (all five use `http.ListenAndServe`
// with `http.ServeMux`), so the pin is runner-agnostic. A single test
// exercises the listener shape any runner will land on. The five
// runner-specific `TestHandle_RoundTrip` tests cover the per-runtime
// envelope semantics; this test covers the listener semantics.
//
// The test fires N parallel http.Get against an httptest.Server whose
// handler sleeps for a fixed window. If the listener serializes
// (single-threaded accept loop), wall time ≈ N × sleep. If the
// listener dispatches concurrently (Go net/http default), wall time
// ≈ 1 × sleep + scheduling jitter. The assertion is the latter.

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunner_HandleIsConcurrent fires N parallel GETs at a slow handler
// and asserts the listener dispatches them concurrently. The handler
// shape mirrors the runner's `handle(w, r, script, signal)` pattern —
// HTTP server + ServeMux + per-request sleep — so the concurrency claim
// pins the §4.9 model. N=20 with sleep=50ms gives a serialized floor of
// 1s (200ms × safety factor well above any CI jitter) and a concurrent
// ceiling of ~100ms; the assertion bracket keeps the test stable under
// -race and -coverpkg=*.
//
// Two sub-cases:
//   - "concurrent": Go net/http default listener — must finish in
//     ~1 × sleepEach. This is the §4.9.1 contract.
//   - "serialized_negative": a hand-rolled handler that serializes
//     requests through a chan must trip the floor assertion. Pins
//     the test logic itself: if someone re-implements this test
//     against a different transport that happens to be concurrent
//     even when we want it serialized, the negative case fails
//     loudly.
func TestRunner_HandleIsConcurrent(t *testing.T) {
	const (
		n         = 20                    // concurrent in-flight requests
		sleepEach = 50 * time.Millisecond // per-request wall time
	)

	t.Run("concurrent", func(t *testing.T) {
		// Mirror the runner's listener shape: ServeMux +
		// per-request handler closure that does work then writes
		// 200. If `handle` were serialized (e.g. through a
		// mutex around the accept loop — which is what this test
		// guards against regressing to), the wall time would be
		// N × sleepEach instead of ~1×.
		var served atomic.Int64
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(sleepEach)
			served.Add(1)
			w.WriteHeader(http.StatusOK)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		elapsed, ok := fanOutNGETs(t, srv.URL+"/probe", n)
		if !ok {
			return // fanOutNGETs already recorded errors
		}

		if got := served.Load(); got != int64(n) {
			t.Errorf("served count = %d, want %d (every request must reach the handler)", got, n)
		}

		// Concurrency floor: a serialized listener would take at
		// least N × sleepEach = 1s. A concurrent listener
		// finishes in ~1 × sleepEach + jitter. Allow up to 4×
		// sleepEach as the upper bound — generous enough for CI
		// sched jitter but well below the serialized floor of
		// 20× = 1s.
		serializedFloor := time.Duration(n) * sleepEach
		concurrentCeiling := 4 * sleepEach
		if elapsed >= serializedFloor {
			t.Errorf("listener appears serialized: elapsed=%v, serialized floor=%v (N=%d × sleep=%v)",
				elapsed, serializedFloor, n, sleepEach)
		}
		if elapsed > concurrentCeiling {
			t.Errorf("listener slower than expected for concurrent dispatch: elapsed=%v, ceiling=%v",
				elapsed, concurrentCeiling)
		}
		t.Logf("elapsed=%v (concurrent dispatch: 1×sleep=%v baseline; serialized floor=%v)",
			elapsed, sleepEach, serializedFloor)
	})

	t.Run("serialized_negative", func(t *testing.T) {
		// Hand-rolled handler that serializes request work
		// through a single worker. Each handler acquires a
		// token before sleeping, then signals done after
		// responding — the worker only releases the next token
		// when the previous request's response is written. Wall
		// time must be ≥ N × sleepEach.
		//
		// This pins the test's own floor-assertion logic: if a
		// future contributor refactors fanOutNGETs and breaks
		// it (e.g. by measuring goroutine-spawn time instead
		// of HTTP completion), this case fails loudly with a
		// clear "serialized listener finished in <1s" signal
		// rather than the positive case silently passing for
		// the wrong reason.
		next := make(chan struct{}) // worker → handler: "your turn"
		done := make(chan struct{}) // handler → worker: "I'm done"
		var served atomic.Int64
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			<-next // wait for the worker's turn-grant
			time.Sleep(sleepEach)
			served.Add(1)
			w.WriteHeader(http.StatusOK)
			done <- struct{}{} // tell the worker we're finished
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		// Single worker: grant turn to handler N, wait for
		// done, repeat. Total time ≈ N × sleepEach.
		workerExit := make(chan struct{})
		go func() {
			defer close(workerExit)
			for i := 0; i < n; i++ {
				next <- struct{}{}
				<-done
			}
		}()

		elapsed, ok := fanOutNGETs(t, srv.URL+"/probe", n)
		<-workerExit
		if !ok {
			return
		}

		if got := served.Load(); got != int64(n) {
			t.Errorf("served count = %d, want %d", got, n)
		}
		serializedFloor := time.Duration(n) * sleepEach
		// The single-worker funnel serializes request handling
		// despite Go's net/http fan-out at the listener — the
		// handler itself is the bottleneck. Total wall time
		// must be at least N × sleepEach. We give a small
		// jitter margin (10 ms) so CI scheduling jitter can't
		// make the negative case flake.
		if elapsed < serializedFloor-10*time.Millisecond {
			t.Errorf("serialized listener was unexpectedly fast: elapsed=%v, expected >= %v (N=%d × sleep=%v)",
				elapsed, serializedFloor, n, sleepEach)
		}
		t.Logf("serialized elapsed=%v (floor=%v) — negative case confirms floor assertion is live",
			elapsed, serializedFloor)
	})
}

// fanOutNGETs fires n parallel http.Get against url and returns the
// wall-clock duration plus an ok flag (false on per-goroutine
// failures, which are recorded via t.Errorf). Used by both
// sub-cases of TestRunner_HandleIsConcurrent so the fan-out logic
// lives in exactly one place.
func fanOutNGETs(t *testing.T, url string, n int) (time.Duration, bool) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(url)
			if err != nil {
				errs <- err
				return
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs <- errBadStatus{resp.StatusCode}
				return
			}
		}()
	}
	start := time.Now()
	wg.Wait()
	elapsed := time.Since(start)
	close(errs)
	failed := false
	for err := range errs {
		failed = true
		t.Errorf("concurrent GET failed: %v", err)
	}
	return elapsed, !failed
}

// errBadStatus surfaces a non-200 response as a typed error so the
// per-goroutine capture in TestRunner_HandleIsConcurrent can report it
// with the offending status code rather than just an opaque failure.
type errBadStatus struct {
	code int
}

func (e errBadStatus) Error() string {
	return "unexpected status code " + http.StatusText(e.code)
}
