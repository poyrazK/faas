// Tests for pkg/wire/readiness.go (issue #571).
//
// The daemon-level helpers mirror pkg/gateway/readiness.go's
// shape (same ReadySignal / ReadyzProbe / NewStalenessSignal /
// NewPGPingSignal contract). We test the new surface here, in
// isolation from pkg/gateway, so a refactor of pkg/wire does not
// regress the gatewayd probe family. The gateway-side tests in
// pkg/gateway/readiness_test.go pin the gateway's own copy.

package wire_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// fakePinger implements wire's pinger interface for unit tests
// without dragging pgxpool into the test import graph. The
// production wiring passes *pgxpool.Pool directly.
type fakePinger struct {
	mu      sync.Mutex
	err     error
	calls   int
	delay   time.Duration // sleep before responding
	lastCtx context.Context
}

func (f *fakePinger) Ping(ctx context.Context) error {
	f.mu.Lock()
	f.calls++
	f.lastCtx = ctx
	delay := f.delay
	err := f.err
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *fakePinger) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestReadyzProbe_ZeroStateIsReady(t *testing.T) {
	// Pre-split behaviour: a probe with no registered signals
	// returns ready=true. An early-boot scrape must not see a
	// spurious 503.
	var p wire.ReadyzProbe
	ready, reason := p.All()
	if !ready {
		t.Errorf("empty probe: ready = false, want true")
	}
	if reason != "" {
		t.Errorf("empty probe: reason = %q, want \"\"", reason)
	}
}

func TestReadyzProbe_SingleSignal(t *testing.T) {
	var p wire.ReadyzProbe
	s := p.Register()
	if ready, _ := p.All(); ready {
		t.Errorf("freshly registered signal reports ready=true; signals must opt IN")
	}
	s.Set(true, "")
	if ready, _ := p.All(); !ready {
		t.Errorf("after Set(true, \"\"): ready = false, want true")
	}
	s.Set(false, "not yet")
	if ready, reason := p.All(); ready {
		t.Errorf("after Set(false, \"not yet\"): ready = true, want false")
	} else if reason != "not yet" {
		t.Errorf("reason = %q, want %q", reason, "not yet")
	}
}

func TestReadyzProbe_FanIn(t *testing.T) {
	// Three signals: one ready, two not. All() must fold to
	// ready=false and concatenate the reasons.
	var p wire.ReadyzProbe
	a := p.Register()
	b := p.Register()
	c := p.Register()
	a.Set(true, "")
	b.Set(false, "b-down")
	c.Set(false, "c-down")
	ready, reason := p.All()
	if ready {
		t.Errorf("All() = true with two failing signals, want false")
	}
	if !strings.Contains(reason, "b-down") || !strings.Contains(reason, "c-down") {
		t.Errorf("reason = %q, missing one of b-down / c-down", reason)
	}
	if !strings.Contains(reason, "; ") {
		t.Errorf("reason = %q, want \"; \" separator between reasons", reason)
	}
}

func TestReadyzProbe_ReadyFuncIgnoresReason(t *testing.T) {
	var p wire.ReadyzProbe
	s := p.Register()
	if p.ReadyFunc()() {
		t.Errorf("ReadyFunc() = true on freshly registered signal")
	}
	s.Set(true, "")
	if !p.ReadyFunc()() {
		t.Errorf("ReadyFunc() = false after Set(true, \"\")")
	}
	s.Set(false, "down")
	if p.ReadyFunc()() {
		t.Errorf("ReadyFunc() = true after Set(false, \"down\")")
	}
}

func TestReadyzProbe_RegisterSignal_NilSafe(t *testing.T) {
	var p wire.ReadyzProbe
	p.RegisterSignal(nil, nil) // must not panic
	if ready, _ := p.All(); !ready {
		t.Errorf("RegisterSignal(nil) followed by no other registers: ready = false, want true (nil must be a no-op)")
	}
}

func TestReadyzProbe_RegisterSignal_FoldsExisting(t *testing.T) {
	var p wire.ReadyzProbe
	p.RegisterSignal(nil, nil) // nil-safe; must not panic
	// Real signal:
	sig := &wire.ReadySignal{}
	sig.Set(true, "")
	p.RegisterSignal(sig, nil)
	if !p.ReadyFunc()() {
		t.Errorf("RegisterSignal of ready signal: ready = false, want true")
	}
	sig.Set(false, "down")
	if p.ReadyFunc()() {
		t.Errorf("after Set(false): ready = true, want false")
	}
}

func TestReadyzProbe_ReasonFunc(t *testing.T) {
	var p wire.ReadyzProbe
	rfn := p.ReasonFunc()
	if got := rfn(); got != "" {
		t.Errorf("empty probe ReasonFunc = %q, want \"\"", got)
	}
	a := p.Register()
	a.Set(false, "component-down")
	if got := rfn(); !strings.Contains(got, "component-down") {
		t.Errorf("ReasonFunc = %q, want contains \"component-down\"", got)
	}
}

// TestReadySignal_ReportSetAtomicPair is the regression test
// for PR #1091 review Finding 6. The previous shape stored the
// ready bit in atomic.Bool and the reason string under a
// sync.RWMutex; Report() did `s.ready.Load(); s.lastReason()` —
// two separate operations. A Set() firing between the Load and
// the RLock could leave Report() returning a stale reason with
// a fresh ready bit (or vice versa).
//
// The fix bundles (ready, reason) into a single immutable
// readyState struct published via atomic.Pointer. Set is a single
// Store; Report is a single Load. A concurrent Report+Set must
// observe either the old state or the new state, never a torn
// pair. The test stresses the pair from two goroutines for
// several iterations and asserts every observed snapshot is
// internally consistent (one of two valid states).
func TestReadySignal_ReportSetAtomicPair(t *testing.T) {
	s := wire.NewReadySignalForTest(false, "v0")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	// Writer: flips between (false, "v0") and (true, "v1") every
	// iteration.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			s.Set(false, "v0")
			s.Set(true, "v1")
		}
	}()
	// Reader: 100 goroutines, each observing 1000 reports.
	const readers = 100
	const reads = 1000
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < reads; j++ {
				ready, reason := s.Report()
				switch {
				case ready && reason == "v1":
					// valid (true, "v1")
				case !ready && reason == "v0":
					// valid (false, "v0")
				default:
					t.Errorf("torn snapshot: ready=%v reason=%q (must be (true,\"v1\") or (false,\"v0\"))", ready, reason)
					return
				}
			}
		}()
	}
	// Let the race run for a short window.
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestNewStalenessSignal_StartsNotReady(t *testing.T) {
	// PR #1091 review Finding 7: NewStalenessSignal no longer
	// pre-arms the signal to ready at construction. The previous
	// pre-arm created a ≤cadence window where /readyz reported
	// ready before any real Touch had landed, contradicting the
	// "every component must opt IN" invariant. The signal now
	// starts at (false, "no touch yet") and the first tick is
	// the canonical readiness flip.
	s, _, stopper := wire.NewStalenessSignal(50 * time.Millisecond)
	defer stopper()
	if r, reason := s.Report(); r {
		t.Errorf("NewStalenessSignal: ready = true at construction (reason=%q), want false (no pre-arm)", reason)
	} else if reason != "no touch yet" {
		t.Errorf("NewStalenessSignal: reason = %q at construction, want \"no touch yet\"", reason)
	}
}

func TestNewStalenessSignal_TouchFlipsReady(t *testing.T) {
	s, touch, stopper := wire.NewStalenessSignal(50 * time.Millisecond)
	defer stopper()
	touch()
	if r, _ := s.Report(); !r {
		t.Errorf("after Touch: ready = false, want true")
	}
}

func TestNewStalenessSignal_StaleFlipsNotReady(t *testing.T) {
	// Cadence: half of stale; cap at 1s. 50ms stale → ~25ms cadence.
	// Wait long enough for at least one tick to flip stale.
	s, touch, stopper := wire.NewStalenessSignal(50 * time.Millisecond)
	defer stopper()
	touch()
	if r, _ := s.Report(); !r {
		t.Errorf("after Touch: ready = false, want true")
	}
	time.Sleep(150 * time.Millisecond)
	if r, reason := s.Report(); r {
		t.Errorf("after stale wait: ready = true, want false")
	} else if reason != "stale" {
		t.Errorf("reason = %q, want %q", reason, "stale")
	}
}

func TestNewStalenessSignal_StopperFlipsFalse(t *testing.T) {
	s, touch, stopper := wire.NewStalenessSignal(time.Second)
	touch()
	stopper()
	if r, reason := s.Report(); r {
		t.Errorf("after stopper: ready = true, want false")
	} else if reason != "shutting down" {
		t.Errorf("reason = %q, want %q", reason, "shutting down")
	}
}

func TestNewPGPingSignal_FirstPingSucceeds(t *testing.T) {
	pool := &fakePinger{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, stopper := wire.NewPGPingSignal(ctx, pool, 50*time.Millisecond)
	defer stopper()
	// First ping kicks off synchronously inside NewPGPingSignal.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if r, _ := s.Report(); r {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r, reason := s.Report(); !r {
		t.Errorf("after healthy ping: ready = false (reason=%q), want true", reason)
	}
	if pool.Calls() == 0 {
		t.Errorf("fakePinger.Ping never called")
	}
}

func TestNewPGPingSignal_PingFailureFlipsFalse(t *testing.T) {
	pool := &fakePinger{err: errors.New("connection refused")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, stopper := wire.NewPGPingSignal(ctx, pool, 30*time.Millisecond)
	defer stopper()
	// Wait for the signal to observe the failure.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if r, reason := s.Report(); !r && strings.Contains(reason, "connection refused") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	r, reason := s.Report()
	if r {
		t.Errorf("after connection-refused ping: ready = true, want false")
	}
	if !strings.Contains(reason, "connection refused") {
		t.Errorf("reason = %q, want contains \"connection refused\"", reason)
	}
}

func TestNewPGPingSignal_StopperFlipsFalse(t *testing.T) {
	pool := &fakePinger{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, stopper := wire.NewPGPingSignal(ctx, pool, time.Second)
	// Wait until first ping succeeds.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if r, _ := s.Report(); r {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	stopper()
	if r, reason := s.Report(); r {
		t.Errorf("after stopper: ready = true, want false")
	} else if reason != "pg ping stopped" {
		t.Errorf("reason = %q, want %q", reason, "pg ping stopped")
	}
}

func TestNewPGPingSignal_StopperIdempotent(t *testing.T) {
	pool := &fakePinger{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, stopper := wire.NewPGPingSignal(ctx, pool, time.Second)
	stopper()
	// Second call must not panic (sync.Once guards it).
	stopper()
}

// gatedPinger holds each Ping until released and records its deadline.
type gatedPinger struct {
	entered  chan time.Duration
	release  chan struct{}
	released sync.Once
}

func (g *gatedPinger) Ping(ctx context.Context) error {
	deadline, _ := ctx.Deadline()
	select {
	case g.entered <- time.Until(deadline):
	default:
	}
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestNewPGPingSignal_ZeroEveryDefaultsTo5s(t *testing.T) {
	// Passing every=0 must not panic or block. The first ping happens
	// immediately, bounded to every/2 = 2.5s. The ping is held so the
	// "not ready before the first ping completes" check cannot race the
	// goroutine (the release gate saw ready=true on a loaded runner).
	pool := &gatedPinger{entered: make(chan time.Duration, 1), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, stopper := wire.NewPGPingSignal(ctx, pool, 0)
	defer stopper()
	defer pool.released.Do(func() { close(pool.release) })
	var budget time.Duration
	select {
	case budget = <-pool.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("zero-every default: first ping did not start immediately")
	}
	if budget <= 2*time.Second || budget > 2500*time.Millisecond {
		t.Errorf("first ping budget = %s, want every/2 = 2.5s from the 5s default", budget)
	}
	if r, _ := s.Report(); r {
		t.Error("zero-every default: ready = true before the first ping completed, want false")
	}
	pool.released.Do(func() { close(pool.release) })
	deadline := time.Now().Add(2 * time.Second)
	for {
		if r, _ := s.Report(); r {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("zero-every default: ready stayed false after the first ping succeeded")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestControlMuxLite_HealthzReturns200(t *testing.T) {
	mux := http.NewServeMux()
	wire.ControlMuxLite(mux, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("/healthz code = %d, want 200", rr.Code)
	}
	if body := rr.Body.String(); body != "ok" {
		t.Errorf("/healthz body = %q, want \"ok\"", body)
	}
}

func TestControlReadyMuxLite_PreservesExistingHealthz(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	wire.ControlReadyMuxLite(mux, nil, nil)

	healthz := httptest.NewRecorder()
	mux.ServeHTTP(healthz, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if healthz.Code != http.StatusTeapot {
		t.Errorf("existing /healthz code = %d, want %d", healthz.Code, http.StatusTeapot)
	}

	readyz := httptest.NewRecorder()
	mux.ServeHTTP(readyz, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readyz.Code != http.StatusOK || readyz.Body.String() != "ready" {
		t.Errorf("/readyz = %d %q, want 200 ready", readyz.Code, readyz.Body.String())
	}
}

func TestControlMuxLite_ReadyzReturns200(t *testing.T) {
	mux := http.NewServeMux()
	wire.ControlMuxLite(mux, func() bool { return true }, nil)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("/readyz code = %d, want 200", rr.Code)
	}
	if body := rr.Body.String(); body != "ready" {
		t.Errorf("/readyz body = %q, want \"ready\"", body)
	}
}

func TestControlMuxLite_ReadyzReturns503(t *testing.T) {
	mux := http.NewServeMux()
	wire.ControlMuxLite(mux, func() bool { return false }, func() string { return "draining" })
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz code = %d, want 503", rr.Code)
	}
	if body := rr.Body.String(); body != "not-ready:draining" {
		t.Errorf("/readyz body = %q, want \"not-ready:draining\"", body)
	}
}

func TestControlMuxLite_NilMuxIsNoOp(t *testing.T) {
	// Must not panic on nil mux.
	wire.ControlMuxLite(nil, nil, nil)
}

func TestControlMuxLite_NilReadyFuncDegradesTo200(t *testing.T) {
	// Pre-split behaviour: nil readyFunc → always 200.
	mux := http.NewServeMux()
	wire.ControlMuxLite(mux, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("/readyz with nil readyFunc: code = %d, want 200 (pre-split degradation)", rr.Code)
	}
}

func TestControlMuxLite_ProbeEndToEnd(t *testing.T) {
	// Wire a real probe through ControlMuxLite — the canonical
	// daemon-side pattern. Three signals; flip two false; expect
	// 503 with both reasons.
	mux := http.NewServeMux()
	var p wire.ReadyzProbe
	a := p.Register()
	b := p.Register()
	c := p.Register()
	a.Set(true, "")
	b.Set(false, "b-down")
	c.Set(false, "c-down")
	wire.ControlMuxLite(mux, p.ReadyFunc(), p.ReasonFunc())

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz code = %d, want 503", rr.Code)
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, "not-ready:") {
		t.Errorf("/readyz body = %q, want prefix \"not-ready:\"", body)
	}
	for _, want := range []string{"b-down", "c-down"} {
		if !strings.Contains(body, want) {
			t.Errorf("/readyz body = %q, missing %q", body, want)
		}
	}
	// Flip all ready.
	a.Set(true, "")
	b.Set(true, "")
	c.Set(true, "")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("/readyz after all-flipped: code = %d, want 200", rr.Code)
	}
}
