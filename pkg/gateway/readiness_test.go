// spec: §11

package gateway

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestReadyzProbe_All_EmptyReturnsTrue pins the empty-probe contract:
// when no signals are registered, the probe reports ready (the
// pre-split behaviour preserved for early-boot compatibility — see
// pkg/gateway/readiness.go::ReadyzProbe.All).
func TestReadyzProbe_All_EmptyReturnsTrue(t *testing.T) {
	var p ReadyzProbe
	ok, reason := p.All()
	if !ok {
		t.Errorf("empty probe All() = false, want true")
	}
	if reason != "" {
		t.Errorf("empty probe reason = %q, want \"\"", reason)
	}
}

// TestReadyzProbe_Register_DefaultsNotReady pins the opt-in semantics:
// every newly-registered signal starts at not-ready. The daemon
// flips each one to ready as components come up.
func TestReadyzProbe_Register_DefaultsNotReady(t *testing.T) {
	var p ReadyzProbe
	s := p.Register()
	ok, _ := s.Report()
	if ok {
		t.Errorf("freshly registered signal reports ready, want not-ready")
	}
	ok, _ = p.All()
	if ok {
		t.Errorf("probe with one not-ready signal reports ready")
	}
}

// TestReadyzProbe_All_FoldsSignals exercises the fan-in: every
// signal must be ready for All() to return true.
func TestReadyzProbe_All_FoldsSignals(t *testing.T) {
	var p ReadyzProbe
	s1 := p.Register()
	s2 := p.Register()
	// Both false → All false.
	ok, reason := p.All()
	if ok {
		t.Errorf("All() with two false signals returned true")
	}
	if reason == "" {
		t.Errorf("All() reason empty when signals are not ready")
	}
	// Flip one → All still false.
	s1.Set(true, "")
	ok, _ = p.All()
	if ok {
		t.Errorf("All() returned true with one signal still not-ready")
	}
	// Flip the other → All true.
	s2.Set(true, "")
	ok, reason = p.All()
	if !ok {
		t.Errorf("All() returned false with both signals ready")
	}
	if reason != "" {
		t.Errorf("All() reason = %q with all ready, want empty", reason)
	}
}

// TestReadyzProbe_All_ConcatsReasons verifies operator-visible
// reasons are joined when multiple signals fail.
func TestReadyzProbe_All_ConcatsReasons(t *testing.T) {
	var p ReadyzProbe
	s1 := p.Register()
	s2 := p.Register()
	s1.Set(false, "routing cache not primed")
	s2.Set(false, "pg ping failed: connection refused")
	ok, reason := p.All()
	if ok {
		t.Fatalf("All() = true, want false")
	}
	if !strings.Contains(reason, "routing cache not primed") {
		t.Errorf("reason missing first signal: %q", reason)
	}
	if !strings.Contains(reason, "pg ping failed") {
		t.Errorf("reason missing second signal: %q", reason)
	}
	if !strings.Contains(reason, "; ") {
		t.Errorf("reason not joined with \"; \": %q", reason)
	}
}

// TestReadyzProbe_ReadyFunc_StableUnderConcurrency verifies the
// returned ReadyFunc is safe for use on the /readyz hot path. The
// assertion is "no panic, no deadlock" — the bit can legitimately
// be in any state during a race between concurrent Set(true/false)
// calls, so we don't assert the value.
func TestReadyzProbe_ReadyFunc_StableUnderConcurrency(t *testing.T) {
	var p ReadyzProbe
	s := p.Register()
	rf := p.ReadyFunc()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			s.Set(false, "")
			s.Set(true, "")
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		// Discard the value — see comment above.
		_ = rf()
	}
	<-done
}

// TestReadyzProbe_RegisterSignal_FoldsIntoAll exercises the
// PR-B1 RegisterSignal path: a helper-constructed *ReadySignal
// (e.g. one returned by NewPGPingSignal or NewStalenessSignal) is
// added to the probe via RegisterSignal, and All() / ReadyFunc()
// fold it into the fan-in alongside signals added via Register().
//
// The mixed use case is the load-bearing one: gatewayd-internal's
// /readyz tighten (PR-B1) uses Register for manual signals
// (schedd-router, nodeCache) and RegisterSignal for the helper
// signals (PG ping, warm-hint staleness), because the helper
// constructors already allocate a *ReadySignal and the caller
// drives it via Stopper / Touch. Register() would build a
// duplicate placeholder and the All() would permanently fail /readyz.
func TestReadyzProbe_RegisterSignal_FoldsIntoAll(t *testing.T) {
	var p ReadyzProbe

	// Helper-constructed signal via NewStalenessSignal. The
	// returned signal is the same one the helper goroutine drives
	// (the goroutine flips true on touch, false on stale).
	external, _, _ := NewStalenessSignal(50 * time.Millisecond)
	p.RegisterSignal(external)

	// Manual signal via Register. Set(true, "") immediately because
	// in production this corresponds to a non-nil component.
	manual := p.Register()
	manual.Set(true, "")

	// Before the staleness signal is touched, it is "not yet ready"
	// (NewStalenessSignal's helper pre-arms true, but a race between
	// the touch path and the first tick can land with false; we
	// don't rely on the helper's pre-arm under -race).
	// Drive the signal to a known state by calling Set directly.
	external.Set(true, "")

	ready, reason := p.All()
	if !ready {
		t.Errorf("All() = false (reason=%q) — RegisterSignal-folder signal did not contribute true", reason)
	}

	// Now flip the external signal false. All() must report false.
	external.Set(false, "external failed")
	ready, reason = p.All()
	if ready {
		t.Errorf("All() = true after RegisterSignal-folder signal flipped false (reason=%q)", reason)
	}
	if reason != "external failed" {
		t.Errorf("All() reason = %q, want %q", reason, "external failed")
	}
}

// TestReadyzProbe_RegisterSignal_NilIsNoop asserts the nil-guard:
// callers do not need to check nil before RegisterSignal. A nil
// signal is a no-op (the probe's All() does not panic on a nil entry
// because RegisterSignal skips appending nil entries).
func TestReadyzProbe_RegisterSignal_NilIsNoop(t *testing.T) {
	var p ReadyzProbe
	p.RegisterSignal(nil) // must not panic
	ready, _ := p.All()
	if !ready {
		t.Errorf("All() after RegisterSignal(nil) = false (no signals registered; empty-probe contract is true)")
	}
}

// stubPinger satisfies the pinger interface used by NewPGPingSignal.
type stubPinger struct {
	err   error
	calls atomic.Int32 // incremented by the helper goroutine; read by the test
}

func (s *stubPinger) Ping(_ context.Context) error {
	s.calls.Add(1)
	return s.err
}

// TestNewPGPingSignal_FlipsOnSuccess verifies the helper flips the
// signal ready on the first successful ping (the immediate-ping path
// at pkg/gateway/readiness.go::NewPGPingSignal).
func TestNewPGPingSignal_FlipsOnSuccess(t *testing.T) {
	p := &stubPinger{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig, stop := NewPGPingSignal(ctx, p, 50*time.Millisecond)
	defer stop()
	// The initial ping fires synchronously; allow a tick to make
	// sure the goroutine has had a chance to run before we read.
	time.Sleep(100 * time.Millisecond)
	ok, _ := sig.Report()
	if !ok {
		t.Errorf("PG ping signal reports not-ready after successful ping")
	}
	if p.calls.Load() == 0 {
		t.Errorf("stubPinger.calls = 0, want ≥1 (the immediate ping)")
	}
}

// TestNewPGPingSignal_FlipsOnError verifies the helper flips the
// signal not-ready on a failing ping and surfaces the error in the
// reason.
func TestNewPGPingSignal_FlipsOnError(t *testing.T) {
	p := &stubPinger{err: errors.New("connection refused")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig, stop := NewPGPingSignal(ctx, p, 50*time.Millisecond)
	defer stop()
	time.Sleep(100 * time.Millisecond)
	ok, reason := sig.Report()
	if ok {
		t.Errorf("PG ping signal reports ready after erroring ping")
	}
	if !strings.Contains(reason, "connection refused") {
		t.Errorf("reason = %q, want substring \"connection refused\"", reason)
	}
}

// TestNewPGPingSignal_StopFlipsNotReady verifies the stopper hook
// flips the bit false (so a draining daemon doesn't claim ready).
func TestNewPGPingSignal_StopFlipsNotReady(t *testing.T) {
	p := &stubPinger{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig, stop := NewPGPingSignal(ctx, p, 50*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	stop()
	ok, reason := sig.Report()
	if ok {
		t.Errorf("PG ping signal still ready after stop()")
	}
	if reason != "pg ping stopped" {
		t.Errorf("reason after stop = %q, want \"pg ping stopped\"", reason)
	}
}

func TestNewPGPingSignal_StopIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, stop := NewPGPingSignal(ctx, &stubPinger{}, 50*time.Millisecond)
	stop()
	stop()
}

func TestNewDependencySignalTracksChecksAndStops(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig, stop := NewDependencySignal(ctx, "internal gateway", 40*time.Millisecond, func(context.Context) error {
		calls.Add(1)
		return nil
	})
	defer stop()
	time.Sleep(25 * time.Millisecond)
	if ready, reason := sig.Report(); !ready || reason != "" {
		t.Fatalf("dependency signal = (%v, %q), want ready", ready, reason)
	}
	if calls.Load() == 0 {
		t.Fatal("dependency check was not called immediately")
	}
	stop()
	stop()
	if ready, reason := sig.Report(); ready || reason != "internal gateway check stopped" {
		t.Fatalf("stopped dependency signal = (%v, %q), want stopped", ready, reason)
	}
}

func TestNewDependencySignalSurfacesFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig, stop := NewDependencySignal(ctx, "internal gateway", 40*time.Millisecond, func(context.Context) error {
		return errors.New("connection refused")
	})
	defer stop()
	time.Sleep(20 * time.Millisecond)
	ready, reason := sig.Report()
	if ready {
		t.Fatal("dependency signal reports ready after failed check")
	}
	if !strings.Contains(reason, "internal gateway check failed: connection refused") {
		t.Fatalf("dependency failure reason = %q", reason)
	}
}

// TestNewStalenessSignal_FreshTouchReady verifies the touch path
// keeps the signal ready while touches are recent. PR #1091
// review Finding 8: the pre-arm at NewStalenessSignal's tail
// was removed so the signal starts at (false, "no touch yet");
// the first tick is the canonical readiness flip.
func TestNewStalenessSignal_FreshTouchReady(t *testing.T) {
	sig, touch, stop := NewStalenessSignal(200 * time.Millisecond)
	defer stop()
	// No pre-arm: signal reports not-ready until the first tick
	// fires after Touch lands.
	touch()
	time.Sleep(50 * time.Millisecond)
	ok, _ := sig.Report()
	if !ok {
		t.Errorf("staleness signal flipped not-ready 50 ms after fresh touch")
	}
}

// TestNewStalenessSignal_StaleAfterWindow verifies the signal flips
// not-ready after `stale` elapses without a touch.
func TestNewStalenessSignal_StaleAfterWindow(t *testing.T) {
	sig, touch, stop := NewStalenessSignal(100 * time.Millisecond)
	defer stop()
	touch()
	time.Sleep(300 * time.Millisecond)
	ok, reason := sig.Report()
	if ok {
		t.Errorf("staleness signal still ready 300 ms after last touch (stale=100 ms)")
	}
	if reason != "stale" {
		t.Errorf("reason = %q, want \"stale\"", reason)
	}
	// Touching again flips it back to ready.
	touch()
	time.Sleep(50 * time.Millisecond)
	ok, _ = sig.Report()
	if !ok {
		t.Errorf("staleness signal did not recover after fresh touch")
	}
}
