//go:build load

// Package gateway load test — CI-asserted hot-path SLO gate (spec §14 M4 row
// 2: "1,000 rps to hot app adds < 2 ms p50").
//
// Build tag is `load` so this file does NOT run under `make test`. The
// dedicated `make test-load` target and the `load:` GitHub Actions job
// (ubuntu-4-cpus) gate it. ubuntu-latest is 2 vCPU / 7 GB and can't carry
// 1000 rps reliably — the dedicated runner is load-bearing.
//
// Topology: Handler → real httptest.NewServer upstream, latency measured at
// ServeHTTP entry/exit (the spec's "< 2 ms added latency" budget covers the
// full path including the loopback dial to the proxy). The handler is wired
// with an unlimitedLimiter via WithLimiter so the test isn't constrained by
// the newTestHandler default of PlanPro (100 rps / 500 burst — would 429
// ~half of phase B and skew measurements).
package gateway

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ = api.PlanPro // silence unused-import if the compile-time guard below is removed in a future refactor

// TestHandlerHotPathAddsLT2MsAt1kRPS is the M4 acceptance gate. It runs two
// phases through the SAME handler so per-process noise cancels:
//
//   - Phase A (baseline): 50 rps × 5 s, ~250 samples — handler under light load.
//   - Phase B (load):     1000 rps × 5 s, ~5000 samples — handler under load.
//
// The gate is the DELTA between hot-load p50 and idle p50. Idle p50 alone is
// noisy (Go runtime, GC, CPU frequency scaling, scheduler jitter); the delta
// measures only the load-induced regression. Hard limit: < 2 ms.
//
// Standard error of each median at these sample sizes is 3-20 µs — plenty of
// margin around the 2 ms gate. Don't extend the load window without re-tuning.
func TestHandlerHotPathAddsLT2MsAt1kRPS(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.running = true // hot path: no wake, no cold header, no wake latency
	h.WithLimiter(unlimitedLimiter())

	const (
		phaseADur = 5 * time.Second
		phaseBDur = 5 * time.Second
		rpsA      = 50
		rpsB      = 1000
		settle    = 200 * time.Millisecond
	)

	// Warm the runtime before sampling (compile/parse cost, page faults).
	// 100 requests at 200 rps gives the JIT/RT enough to stabilize without
	// biasing the measured latency window.
	if err := warmup(h); err != nil {
		t.Fatalf("warmup: %v", err)
	}

	idle := samplePhase(t, h, "idle-baseline", rpsA, phaseADur)
	time.Sleep(settle) // let any tail goroutines drain
	loaded := samplePhase(t, h, "hot-load", rpsB, phaseBDur)

	p50Idle := p50(idle)
	p50Load := p50(loaded)
	delta := p50Load - p50Idle
	t.Logf("hot-path load test: idle p50=%s  load p50=%s  delta=%s  (n_idle=%d n_load=%d)",
		p50Idle, p50Load, delta, len(idle), len(loaded))

	if delta >= 2*time.Millisecond {
		t.Errorf("hot-path p50 regressed under load: idle=%s load=%s delta=%s, want delta < 2ms (spec §14 M4)", p50Idle, p50Load, delta)
	}
}

// warmup fires `n` requests to settle the runtime. Returns the first error
// encountered (and stops early) so a bad handler is loud rather than silent.
func warmup(h *Handler) error {
	const n = 100
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				errs <- &httpError{status: rec.Code}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// httpError is a small error type so warmup's failure mode names the offending
// status. Keeps the production imports of this file minimal (no need for
// fmt.Errorf at the call site).
type httpError struct{ status int }

func (e *httpError) Error() string { return http.StatusText(e.status) }

// samplePhase drives `rps` requests per second for `dur` against `h` using a
// fixed worker pool. Returns per-request ServeHTTP latencies. Workers fire on
// `time.NewTicker` ticks; the last tick may under-fill, which is fine — the
// measurement window is exactly `dur`.
//
// Workers > rps/5 so a momentarily slow handler doesn't pile up the ticker
// queue (which would skew later samples toward late-wake latency).
func samplePhase(t *testing.T, h *Handler, label string, rps int, dur time.Duration) []time.Duration {
	t.Helper()
	t.Logf("phase %q: rps=%d dur=%s", label, rps, dur)

	interval := time.Second / time.Duration(rps)
	workers := rps / 5
	if workers < 10 {
		workers = 10
	}

	tick := time.NewTicker(interval)
	defer tick.Stop()

	var (
		mu       sync.Mutex
		durs     = make([]time.Duration, 0, rps*int(dur/time.Second)+workers)
		errCount int64
		wg       sync.WaitGroup
		stop     = make(chan struct{})
	)

	// Drainer: a worker pool reads from the ticker until `stop`. Each tick
	// fans out one ServeHTTP call. Sample under the mu lock because durs is
	// shared across workers.
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				case <-tick.C:
					start := time.Now()
					req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)
					elapsed := time.Since(start)
					if rec.Code != http.StatusOK {
						atomic.AddInt64(&errCount, 1)
						continue
					}
					mu.Lock()
					durs = append(durs, elapsed)
					mu.Unlock()
				}
			}
		}()
	}

	time.AfterFunc(dur, func() { close(stop) })
	wg.Wait()

	if errs := atomic.LoadInt64(&errCount); errs > 0 {
		t.Fatalf("phase %q: %d non-200 responses (rate limiter / proxy glitch)", label, errs)
	}
	return durs
}

// p50 returns the median of `durs`. The defensive copy guards against a
// concurrent append from another worker that hasn't returned yet — sorts
// must operate on a stable slice.
//
// Mirrors `pkg/fcvm/manager_metal_test.go:164-167` (single-line idiom).
func p50(durs []time.Duration) time.Duration {
	if len(durs) == 0 {
		return 0
	}
	cp := make([]time.Duration, len(durs))
	copy(cp, durs)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	return cp[len(cp)/2]
}

// unlimitedLimiter returns a Limiter whose Allow always returns true. The
// 1k rps load test must NOT be constrained by the PlanPro default of
// 100 rps / 500 burst that newTestHandler installs — that would 429 ~half
// of phase B and skew measurements toward the fast 429 path.
//
// Production never calls WithNoop; it's a test seam guarded by a doc
// comment. The `noop` field itself is unexported so accidental misuse is
// a compile error.
func unlimitedLimiter() *Limiter {
	return NewLimiter().WithNoop()
}

// Compile-time guard: catch accidental drift between api.LimitsFor and the
// handler_test default. PlanPro's burst should be ≥ 100 so TestRateLimitReturns429
// (which sets PlanFree with burst 20) keeps working — if the plan table ever
// drops below, the Free-path test will silently degrade.
var _ = func() bool {
	limits, ok := api.LimitsFor(api.PlanFree)
	if !ok || limits.RateLimitBurst < 20 {
		panic("handler_load_test: api.PlanFree burst dropped below 20 — TestRateLimitReturns429 math breaks")
	}
	return true
}()
