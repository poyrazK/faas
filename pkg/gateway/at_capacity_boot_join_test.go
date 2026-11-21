package gateway

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type bootReportingBackend struct {
	Backend
	booting atomic.Bool
}

func (b *bootReportingBackend) BootInProgress(string) bool { return b.booting.Load() }

// Prod 2026-11-15 (app sitoded-link): a burst on a parked app reached both
// gateways. The one that did not start the wake got schedd at-capacity, waited
// the flat 2 s settle, and answered 503 "App instance starting" to ~11
// requests while the other gateway's boot became routable ~1 s later. A
// request whose slot is held by an announced boot now waits for that boot, up
// to the app's cold-wake budget.
func TestAtCapacityWaitsForABootInProgress(t *testing.T) {
	for _, tc := range []struct {
		name      string
		booting   bool
		settleAt  time.Duration // when the boot stops being in progress (0: never)
		routable  time.Duration // when a target becomes pickable (0: never)
		wantOK    bool
		maxElapse time.Duration
	}{
		{name: "boot in progress becomes routable after the flat wait", booting: true, routable: 2600 * time.Millisecond, wantOK: true, maxElapse: 4 * time.Second},
		{name: "no boot in progress keeps the flat wait", routable: 2600 * time.Millisecond, maxElapse: 2500 * time.Millisecond},
		{name: "a boot that fails stops the wait", booting: true, settleAt: 2300 * time.Millisecond, maxElapse: 2800 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			b.app.MaxConcurrency = 1
			backend := &bootReportingBackend{Backend: h.backend}
			backend.booting.Store(tc.booting)
			h.backend = backend
			start := time.Now()
			if tc.settleAt > 0 {
				time.AfterFunc(tc.settleAt, func() { backend.booting.Store(false) })
			}
			pick := func() PickResult {
				if tc.routable > 0 && time.Since(start) >= tc.routable {
					return PickResult{OK: true, Target: Target{InstanceID: "booted-elsewhere"}}
				}
				return PickResult{}
			}
			got, err := h.settleAtCapacity(t.Context(), b.app, "", api.MustLimitsFor(b.app.Plan), 2, pick)
			elapsed := time.Since(start)
			if tc.wantOK {
				if err != nil || !got.OK {
					t.Fatalf("settle = %+v, %v after %s; want the booted instance", got, err, elapsed)
				}
			} else if !errors.Is(err, ErrInstanceSlotsSettling) {
				t.Fatalf("settle err = %v after %s, want ErrInstanceSlotsSettling", err, elapsed)
			}
			if elapsed > tc.maxElapse {
				t.Fatalf("settle took %s, want at most %s", elapsed, tc.maxElapse)
			}
		})
	}
}

func TestBootTrackerForgetsSettledAndStaleBoots(t *testing.T) {
	var tr bootTracker
	now := time.Now()
	tr.note("app", "i1", now)
	if !tr.inProgress("app", now) {
		t.Fatal("announced boot not in progress")
	}
	tr.settle("app", "i1")
	if tr.inProgress("app", now) {
		t.Fatal("settled boot still in progress")
	}
	tr.note("app", "i2", now.Add(-bootTrackerMaxAge-time.Second))
	if tr.inProgress("app", now) {
		t.Fatal("a boot announced past the max age still counts")
	}
}
