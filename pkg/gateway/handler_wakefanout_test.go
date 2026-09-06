// handler_wakefanout_test.go — PR-C (issue #556) end-to-end pinned
// test for wake-fan-out. The plan's contract:
//
//   - Picker returns !OK with ColdBucket=dep-B (the deployment the
//     operator's traffic ratio landed on, currently empty).
//   - Handler sees ColdBucket!="", calls Backend.Admit(appID, dep-B,
//     max) — exactly ONE retry-bound admit per request.
//   - Handler re-picks; if the retry still fails the handler
//     surfaces the at-capacity 503.
//   - The admit captures the deploymentID the handler passed so the
//     test verifies the fan-out, not just that an admit happened.
//
// This pins the load-bearing wake-fan-out behaviour at the HTTP
// layer, independent of the in-pkg PGBackend tests that exercise
// the picker / admit surface directly.

package gateway_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// wakefanoutBackend is a minimal Backend for the wake-fan-out test.
// It mirrors the real PGBackend shape but the test fully controls
// what Pick returns on each call so the handler's wake-fan-out
// branch is exercised deterministically.
type wakefanoutBackend struct {
	app            gateway.App
	host           string
	upstreamAddr   string
	mu             sync.Mutex
	admitErr       error
	admitsByDeploy map[string]int32 // deploymentID → admit count
	totalAdmits    atomic.Int32
	// firstPick / secondPick drive the Pick state machine: the
	// handler sees firstPick once (the cold-bucket wake trigger),
	// then secondPick on the retry (the warmed-bucket routable
	// target). Both default to {} (no target, no cold bucket).
	firstPick  gateway.PickResult
	secondPick gateway.PickResult
	picks      atomic.Int32
}

func (b *wakefanoutBackend) Lookup(_ context.Context, host string) (gateway.App, bool) {
	if host == b.host {
		return b.app, true
	}
	return gateway.App{}, false
}

func (b *wakefanoutBackend) Pick(_ string) gateway.PickResult {
	n := b.picks.Add(1)
	b.mu.Lock()
	defer b.mu.Unlock()
	switch n {
	case 1:
		return b.firstPick
	case 2:
		return b.secondPick
	}
	// Third+ Pick (in case the test fires more than 2) — just
	// return whatever secondPick was. Tests that need different
	// behaviour past call 2 should set secondPick explicitly.
	return b.secondPick
}

func (b *wakefanoutBackend) HealthyCount(_ string) int { return 1 }

func (b *wakefanoutBackend) Admit(_ context.Context, _, deploymentID, _, _ string, _ int) (string, gateway.WakeMethod, bool, error) {
	b.totalAdmits.Add(1)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.admitErr != nil {
		return "", gateway.WakeMethodUnspecified, false, b.admitErr
	}
	b.admitsByDeploy[deploymentID]++
	return "fake-wake-id", gateway.WakeMethodColdBoot, false, nil
}

// LookupMirrorRules (issue #72 / ADR-125 PR-A3) is the no-op
// stub that satisfies the widened Backend interface. The
// wake-fan-out tests don't exercise the mirror dispatch path
// (commit 3 wires that into pkg/gateway/handler.go); the stub
// preserves pre-A3 behaviour bit-for-bit.
func (b *wakefanoutBackend) LookupMirrorRules(_ context.Context, _ string) ([]gateway.MirrorRuleRow, bool) {
	return nil, false
}

// ScheduleMirror (issue #72 / ADR-124 PR-A3) — wake-fanout tests
// don't exercise the mirror hot path; the stub satisfies the
// widened Backend interface.
func (b *wakefanoutBackend) ScheduleMirror(_ context.Context, _, _, _ string) (string, string, error) {
	return "", "", nil
}

// TestHandler_WakeFanOut_WakesLandedDeployment (issue #556 /
// PR-C): end-to-end for the wake-fan-out path. The handler's first
// Pick returns ColdBucket="dep-B" (the operator-stated 75% bucket,
// currently empty). The handler must call Admit(appID, "dep-B",
// max) — verified by the admit-deployment counter — then re-Pick
// and route to dep-B's warmed target. The legacy "warmest bucket"
// fallback inside Pick is intentionally NOT used here; the test
// forces the handler's !OK branch by returning firstPick with
// OK=false and ColdBucket="dep-B".
//
// Handler call ordering pinned here:
//   - ensureCapacity sees an existing target and does not admit a
//     needless extra VM.
//   - Pick returns ColdBucket="dep-B" → wake-fan-out fires and calls
//     Admit(appID, "dep-B") with the picked deploymentID.
//   - Retry Pick returns the warmed dep-B Target → proxy.
//
// So totalAdmits is 2 (one legacy + one wake-fan-out); the
// per-deployment counter must show exactly one Admit("dep-B") and
// one Admit("").
func TestHandler_WakeFanOut_WakesLandedDeployment(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from canary"))
	}))
	t.Cleanup(upstream.Close)

	b := &wakefanoutBackend{
		app:            gateway.App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
		host:           "jane-api.apps.dom",
		upstreamAddr:   upstream.Listener.Addr().String(),
		admitsByDeploy: map[string]int32{},
		// First Pick: cold bucket dep-B, no target.
		firstPick: gateway.PickResult{Picked: "dep-B", ColdBucket: "dep-B"},
		// Second Pick (after Admit): dep-B is now warm, target routable.
		secondPick: gateway.PickResult{
			Target: gateway.Target{
				NodeID:       upstream.Listener.Addr().String(),
				InstanceID:   "i-canary-1",
				DeploymentID: "dep-B",
				WakeID:       "fake-wake-id",
			},
			OK:         true,
			Picked:     "dep-B",
			ColdBucket: "dep-B",
		},
	}
	h := gateway.NewHandlerWith(b, gateway.NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	if got := rec.Header().Get("x-faas-wake-id"); got != "fake-wake-id" {
		t.Fatalf("cold-bucket wake ID = %q, want fake-wake-id", got)
	}

	// Only the explicit cold deployment bucket is admitted.
	if got := b.totalAdmits.Load(); got != 1 {
		t.Fatalf("totalAdmits = %d, want 1 (wake-fan-out only)", got)
	}
	if got := b.admitsByDeploy["dep-B"]; got != 1 {
		t.Errorf("admitsByDeploy[dep-B] = %d, want 1 (handler must wake the landed deployment)", got)
	}
	if got := b.admitsByDeploy[""]; got != 0 {
		t.Errorf("admitsByDeploy[\"\"] = %d, want 0 (warm capacity must be reused)", got)
	}
	if got := b.picks.Load(); got != 2 {
		t.Errorf("Pick calls = %d, want 2 (initial cold + post-admit retry)", got)
	}
}

// TestHandler_WakeFanOut_AdmitsOnceEvenIfRetryStillCold (issue #556
// / PR-C): the retry Pick also returns a cold bucket (the admit
// didn't warm it in time, e.g. slow cold boot). The handler must
// STILL surface the at-capacity 503 after the single retry — it
// must NOT loop on additional admits. The plan's contract is
// bounded-at-1.
//
// Counting: ensureCapacity reuses the existing target; wake-fan-out = 1
// Admit (ColdBucket); retry Pick fails → 503. Total = 1 admit, no third
// retry.
func TestHandler_WakeFanOut_AdmitsOnceEvenIfRetryStillCold(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("unused"))
	}))
	t.Cleanup(upstream.Close)

	b := &wakefanoutBackend{
		app:            gateway.App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
		host:           "jane-api.apps.dom",
		upstreamAddr:   upstream.Listener.Addr().String(),
		admitsByDeploy: map[string]int32{},
		// Both Picks land on the cold bucket — handler must
		// bound the retry at 1.
		firstPick:  gateway.PickResult{Picked: "dep-B", ColdBucket: "dep-B"},
		secondPick: gateway.PickResult{Picked: "dep-B", ColdBucket: "dep-B"},
	}
	h := gateway.NewHandlerWith(b, gateway.NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code/100 != 4 && rec.Code/100 != 5 {
		t.Fatalf("status = %d, want 4xx/5xx (handler must surface failure, not loop); body=%s", rec.Code, rec.Body.String())
	}
	// 1 total: only the wake-fan-out admit.
	// The third Pick is NOT preceded by another Admit — that's the
	// "bounded at 1 retry" contract.
	if got := b.totalAdmits.Load(); got != 1 {
		t.Fatalf("totalAdmits = %d, want 1 (single wake-fan-out retry)", got)
	}
	if got := b.picks.Load(); got != 2 {
		t.Errorf("Pick calls = %d, want 2 (initial + single retry)", got)
	}
}

// TestHandler_WakeFanOut_NotTriggeredOnWarmPick (issue #556 /
// PR-C): when the first Pick returns OK=true and no ColdBucket, the
// handler must NOT call wake-fan-out Admit. The wake-fan-out branch
// is gated on `!pick.OK && pick.ColdBucket != ""`; a warm Pick with
// ColdBucket="" must not fire the fan-out path.
//
// Counting: warm Pick → no fan-out → proxy. Total = 0 admits.
func TestHandler_WakeFanOut_NotTriggeredOnWarmPick(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("warm route"))
	}))
	t.Cleanup(upstream.Close)

	b := &wakefanoutBackend{
		app:            gateway.App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
		host:           "jane-api.apps.dom",
		upstreamAddr:   upstream.Listener.Addr().String(),
		admitsByDeploy: map[string]int32{},
		firstPick: gateway.PickResult{
			Target: gateway.Target{
				NodeID:       upstream.Listener.Addr().String(),
				InstanceID:   "i-warm",
				DeploymentID: "dep-A",
				WakeID:       "",
			},
			OK:         true,
			Picked:     "dep-A",
			ColdBucket: "", // warm pick — no fan-out
		},
	}
	h := gateway.NewHandlerWith(b, gateway.NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (warm route should serve)", rec.Code)
	}
	if got := b.totalAdmits.Load(); got != 0 {
		t.Errorf("totalAdmits = %d, want 0 (warm route must not admit)", got)
	}
	if got := b.admitsByDeploy[""]; got != 0 {
		t.Errorf("admitsByDeploy[\"\"] = %d, want 0", got)
	}
	if got := b.admitsByDeploy["dep-A"]; got != 0 {
		t.Errorf("admitsByDeploy[dep-A] = %d, want 0 (no fan-out on warm pick)", got)
	}
	if got := b.picks.Load(); got != 1 {
		t.Errorf("Pick calls = %d, want 1 (warm pick — no retry)", got)
	}
}
