// Tests for cmd/meterd/readiness.go (issue #571 PR-A2).
//
// Loop.Health dereferences loop.cfg (SampleInterval etc.),
// so a zero-value *meter.Loop with nil cfg panics. Tests
// here use BuildReadinessProbe(nil) for the negative path and
// skip the end-to-end positive path — the production wiring
// passes a fully-initialized loop built via meter.NewLoop, and
// loop.Health's contract is pinned by pkg/meter's own tests.

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestBuildReadinessProbe_NilLoop(t *testing.T) {
	// Nil loop (test path / config error) surfaces "meterd
	// loop nil" as the failing reason.
	p := BuildReadinessProbe(nil)
	defer p.Drain("", nil)
	if p == nil {
		t.Fatal("BuildReadinessProbe(nil) returned nil probe")
	}
	// Wait for the first adapter tick to evaluate.
	time.Sleep(50 * time.Millisecond)
	r, reason := p.All()
	if r {
		t.Errorf("nil loop: All() = true, want false (reason=%q)", reason)
	}
	if !strings.Contains(reason, "loop nil") {
		t.Errorf("reason = %q, want contains \"loop nil\"", reason)
	}
}

func TestBuildReadinessProbe_DrainFlipsFalse(t *testing.T) {
	// Drain() flips every signal to (false, "draining") and
	// fires every registered stopper (Finding 4 from PR #1091
	// review — the helper goroutine must exit before the
	// signal flip is observed). After Drain, /readyz must
	// see 503 with the "draining" reason in the body.
	p := BuildReadinessProbe(nil)
	p.Drain("", nil)
	r, reason := p.All()
	if r {
		t.Errorf("after Drain: All() = true, want false")
	}
	if !strings.Contains(reason, "draining") {
		t.Errorf("reason = %q, want contains \"draining\"", reason)
	}
}

func TestBuildReadinessProbe_EndToEndViaControlMuxLite_NilLoop(t *testing.T) {
	// Wire the probe through ControlMuxLite with a nil loop
	// (the panic-prone path). The /readyz body must surface
	// the failing reason with the canonical "not-ready:"
	// prefix.
	p := BuildReadinessProbe(nil)
	defer p.Drain("", nil)
	time.Sleep(50 * time.Millisecond)
	mux := http.NewServeMux()
	wire.ControlMuxLite(mux, p.ReadyFunc(), p.ReasonFunc())

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz code = %d, want 503 (loop nil)", rr.Code)
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, "not-ready:") {
		t.Errorf("/readyz body = %q, want prefix \"not-ready:\"", body)
	}
	if !strings.Contains(body, "loop nil") {
		t.Errorf("/readyz body = %q, want contains \"loop nil\"", body)
	}
}

func TestBuildReadinessProbe_EndToDrainOnShutdown(t *testing.T) {
	// After Drain(), /readyz must return 503 with reason
	// "draining" so the §12 "Fleet readiness" panel surfaces
	// the drain window. Drain is the canonical post-RunAndShutdown
	// hook (replaces the per-daemon `defer stop()` shape from
	// PR-A2 commit 9).
	p := BuildReadinessProbe(nil)
	p.Drain("", nil)
	mux := http.NewServeMux()
	wire.ControlMuxLite(mux, p.ReadyFunc(), p.ReasonFunc())
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz code = %d, want 503 (draining)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "draining") {
		t.Errorf("/readyz body = %q, want contains \"draining\"", rr.Body.String())
	}
}

type fakeDatabasePinger struct{ err error }

func (f fakeDatabasePinger) Ping(context.Context) error { return f.err }

// TestRegisterDatabaseReadiness is issue #571: an unreachable database flips
// meterd's /readyz with a pg reason; a reachable one leaves the signal ready.
func TestRegisterDatabaseReadiness(t *testing.T) {
	for name, tc := range map[string]struct {
		err   error
		ready bool
	}{
		"reachable":   {ready: true},
		"unreachable": {err: errors.New("connection refused")},
	} {
		t.Run(name, func(t *testing.T) {
			p := &wire.ReadyzProbe{}
			registerDatabaseReadiness(t.Context(), p, fakeDatabasePinger{err: tc.err})
			defer p.Drain("", nil)
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if r, reason := p.All(); r == tc.ready && (r || strings.Contains(reason, "pg ping failed")) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			r, reason := p.All()
			if r != tc.ready {
				t.Fatalf("ready = %v (reason %q), want %v", r, reason, tc.ready)
			}
			if !tc.ready && !strings.Contains(reason, "pg ping failed") {
				t.Fatalf("reason = %q, want a pg ping failure", reason)
			}
		})
	}
}
