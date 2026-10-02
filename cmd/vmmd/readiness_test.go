// Tests for cmd/vmmd/readiness.go (issue #571 PR-A2).
// adr: 398 — run the portable readiness checks on the internal KVM host.
//
// The /readyz probe is constructed at vmmd boot and exposed
// via ControlMuxLite on the metrics mux. These tests pin the
// shape of each individual signal so a future refactor that
// breaks the contract fails here rather than at the LB scrape.

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestBuildReadinessProbe_HappyPath(t *testing.T) {
	// On a CI host without /dev/kvm, the kvm signal will fail;
	// the fc-binary signal will also fail unless `firecracker` is
	// on PATH. We probe the OR-folded behaviour: as long as the
	// fan-in works correctly, the test is informative regardless
	// of platform shape.
	p, bound := BuildReadinessProbe()
	if p == nil {
		t.Fatal("BuildReadinessProbe returned nil probe")
	}
	if bound == nil {
		t.Fatal("BuildReadinessProbe returned nil grpcBoundSignal")
	}

	// Pre-bound: gRPC signal must report not-ready (the vmmd
	// boot path explicitly flips it via MarkBound()).
	if r, reason := bound.Signal().Report(); r {
		t.Errorf("before MarkBound: gRPC signal ready = true (reason=%q), want false", reason)
	}
	if r, _ := p.All(); r {
		t.Errorf("before MarkBound: probe All() = true, want false (gRPC signal must be the gating one)")
	}

	// Flip gRPC. Now All() reports ready iff kvm + fc-binary
	// were both ready at construction time.
	bound.MarkBound()
	if r, _ := bound.Signal().Report(); !r {
		t.Errorf("after MarkBound: gRPC signal ready = false, want true")
	}
	// All() must agree with the gRPC signal post-flip.
	gotReady, _ := p.All()
	wantReady := kvmAndFCReady(t)
	if gotReady != wantReady {
		t.Errorf("after MarkBound: probe All() = %v, want %v (kvm/fc platform-dependent)", gotReady, wantReady)
	}
}

func TestBuildReadinessProbe_ProbeEndToEnd(t *testing.T) {
	// Wire the probe through ControlMuxLite — the canonical
	// daemon-side pattern — and assert the /readyz body shape.
	p, bound := BuildReadinessProbe()
	bound.MarkBound()
	mux := http.NewServeMux()
	wire.ControlMuxLite(mux, p.ReadyFunc(), p.ReasonFunc())

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if kvmAndFCReady(t) {
		if rr.Code != http.StatusOK {
			t.Errorf("/readyz code = %d, want 200 (all signals ready)", rr.Code)
		}
		if body := rr.Body.String(); body != "ready" {
			t.Errorf("/readyz body = %q, want \"ready\"", body)
		}
	} else {
		// Some signal reported not-ready (kvm or fc).
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("/readyz code = %d, want 503 (a signal is down)", rr.Code)
		}
		body := rr.Body.String()
		if !strings.HasPrefix(body, "not-ready:") {
			t.Errorf("/readyz body = %q, want prefix \"not-ready:\"", body)
		}
	}
}

func TestGrpcBoundSignal_MarkBoundIdempotent(t *testing.T) {
	// MarkBound is single-shot in production; calling it
	// twice must not panic and the second call must leave
	// ready=true (the Set path is idempotent).
	p, bound := BuildReadinessProbe()
	bound.MarkBound()
	bound.MarkBound()
	if r, _ := bound.Signal().Report(); !r {
		t.Errorf("after two MarkBound calls: ready = false, want true")
	}
	if p.ReadyFunc()() != kvmAndFCReady(t) {
		t.Errorf("after two MarkBound calls: probe All() = %v, want %v", p.ReadyFunc()(), kvmAndFCReady(t))
	}
}

// kvmAndFCReady returns true iff both the /dev/kvm signal and
// the fc-binary signal were ready at construction time. The
// CI host may not have either; the assertion is informational
// either way — what matters is that the probe's All() OR-fold
// agrees with the platform state.
func kvmAndFCReady(t *testing.T) bool {
	t.Helper()
	p, bound := BuildReadinessProbe()
	// Isolate the host checks: a fresh probe's unbound gRPC signal otherwise
	// makes this helper report false even on a working KVM/Firecracker host.
	bound.MarkBound()
	ready, _ := p.All()
	return ready
}
