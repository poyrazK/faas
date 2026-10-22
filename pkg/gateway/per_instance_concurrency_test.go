// spec: §4.9.1
// adr: 20261022203523

package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// countingAuditor counts audit rows by kind.
type countingAuditor struct {
	mu    sync.Mutex
	kinds map[string]int
}

func (a *countingAuditor) Emit(_ context.Context, kind string, _ *string, _ map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.kinds[kind]++
}

func (a *countingAuditor) count(kind string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.kinds[kind]
}

// perInstanceBurstBackend admits burst instances with unique IDs, up to the
// ceiling the gateway passes, the way schedd's ledger caps them.
type perInstanceBurstBackend struct {
	*fakeBackend
	next     atomic.Int32
	admitted atomic.Int32
}

func (b *perInstanceBurstBackend) AdmitBurst(_ context.Context, appID, _, _ string, maxConcurrency, count int) (int, error) {
	admitted := 0
	for ; admitted < count && b.HealthyCount(appID) < maxConcurrency; admitted++ {
		b.AddTarget(Target{NodeID: "node-1", InstanceID: "burst-" + itoa(uint64(b.next.Add(1)))})
	}
	b.admitted.Add(int32(admitted))
	return admitted, nil
}

// TestPerInstanceConcurrencyScalesOutABurst reproduces Hunt 12 H12-4: a
// gunicorn --workers 2 app on Scale with max_concurrency 3 received eight
// concurrent slow requests, and all of them queued inside one guest because
// the gateway admitted up to the plan's 80 per VM and asked for
// ceil(8/80) = 1 instance. Declaring per_instance_concurrency 2 must spread
// six concurrent requests over three instances, two each.
func TestPerInstanceConcurrencyScalesOutABurst(t *testing.T) {
	const requests = 6
	b := &perInstanceBurstBackend{fakeBackend: &fakeBackend{
		app:  App{ID: "app-1", Plan: api.PlanScale, MaxConcurrency: 3, PerInstanceConcurrency: 2},
		host: "app.example.com",
	}}
	b.AddTarget(Target{NodeID: "node-1", InstanceID: "warm-1"})
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	audit := &countingAuditor{kinds: map[string]int{}}
	h.WithVMConcurrencyAudit(audit)

	var mu sync.Mutex
	inflight, peak := map[string]int{}, map[string]int{}
	entered := make(chan struct{}, requests)
	release := make(chan struct{})
	h.WithForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			inflight[target.InstanceID]++
			peak[target.InstanceID] = max(peak[target.InstanceID], inflight[target.InstanceID])
			mu.Unlock()
			entered <- struct{}{}
			<-release
			mu.Lock()
			inflight[target.InstanceID]--
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		})
	})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	codes := make(chan int, requests)
	for range requests {
		go func() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil))
			codes <- rec.Code
		}()
	}
	// Three instances at two requests each hold all six at once; one
	// instance at its declared two would leave four waiting.
	for i := range requests {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d requests reached an instance; burst admissions=%d", i, requests, b.admitted.Load())
		}
	}
	mu.Lock()
	for instance, n := range peak {
		if n > 2 {
			t.Errorf("instance %s served %d requests at once, want at most per_instance_concurrency 2", instance, n)
		}
	}
	instances := len(peak)
	mu.Unlock()
	if instances != 3 {
		t.Errorf("requests reached %d instances, want 3 (max_concurrency)", instances)
	}
	releaseOnce.Do(func() { close(release) })
	for range requests {
		if code := <-codes; code != http.StatusOK {
			t.Errorf("request returned %d, want 200", code)
		}
	}
	if got := b.HealthyCount("app-1"); got > 3 {
		t.Errorf("healthy targets = %d, want at most the app ceiling 3", got)
	}
	// Waiting for a declared slot is normal operation, not the platform's
	// per-VM threshold, so it writes no audit rows.
	if got := audit.count("vm.inflight_threshold_reached"); got != 0 {
		t.Errorf("vm.inflight_threshold_reached rows = %d, want 0 for a declared per-instance limit", got)
	}
}

func TestEffectiveVMConcurrencyLimitUsesDeclaredPerInstanceConcurrency(t *testing.T) {
	scale := api.MustLimitsFor(api.PlanScale)
	for _, tc := range []struct {
		name     string
		declared int
		want     int
	}{
		{name: "unset keeps the plan bound", declared: 0, want: 80},
		{name: "declared", declared: 2, want: 2},
		{name: "one", declared: 1, want: 1},
		{name: "equal to the bound", declared: 80, want: 80},
		{name: "above the bound after a downgrade", declared: 81, want: 80},
		{name: "negative", declared: -3, want: 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := App{Plan: api.PlanScale, PerInstanceConcurrency: tc.declared}
			if got := effectiveVMConcurrencyLimit(app, scale); got != tc.want {
				t.Fatalf("effectiveVMConcurrencyLimit(%d) = %d, want %d", tc.declared, got, tc.want)
			}
		})
	}
}

func TestConcurrencyAdmissionPolicyWaitForPerInstanceConcurrency(t *testing.T) {
	for _, tc := range []struct {
		name        string
		app         App
		wantWait    time.Duration
		wantWaiters int
	}{
		{name: "plan default", app: App{Plan: api.PlanScale}, wantWait: api.ConcurrencyQueuePaidDefaultWait, wantWaiters: api.ConcurrencyQueueScaleDefaultDepth},
		{name: "free plan default", app: App{Plan: api.PlanFree}, wantWait: api.ConcurrencyQueueFreeDefaultWait, wantWaiters: api.ConcurrencyQueueFreeDefaultDepth},
		{name: "declared waits for a slot", app: App{Plan: api.PlanScale, PerInstanceConcurrency: 2}, wantWait: api.PerInstanceConcurrencyQueueDefaultWait, wantWaiters: api.ConcurrencyQueueScaleDefaultDepth},
		{name: "declared on free", app: App{Plan: api.PlanFree, PerInstanceConcurrency: 1}, wantWait: api.PerInstanceConcurrencyQueueDefaultWait, wantWaiters: api.ConcurrencyQueueFreeDefaultDepth},
		{name: "explicit wait wins", app: App{Plan: api.PlanScale, PerInstanceConcurrency: 2, MaxQueueWaitMS: 750}, wantWait: 750 * time.Millisecond, wantWaiters: api.ConcurrencyQueueScaleDefaultDepth},
		{name: "explicit depth kept", app: App{Plan: api.PlanScale, PerInstanceConcurrency: 2, MaxQueueDepth: 12}, wantWait: api.PerInstanceConcurrencyQueueDefaultWait, wantWaiters: 12},
		{name: "drop never waits", app: App{Plan: api.PlanScale, PerInstanceConcurrency: 2, ConcurrencyOverflow: api.ConcurrencyOverflowDrop}, wantWait: api.ConcurrencyQueuePaidDefaultWait, wantWaiters: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := concurrencyAdmissionPolicy(tc.app)
			if got.MaxWait != tc.wantWait || got.MaxWaiters != tc.wantWaiters {
				t.Fatalf("policy = wait %v waiters %d, want wait %v waiters %d", got.MaxWait, got.MaxWaiters, tc.wantWait, tc.wantWaiters)
			}
		})
	}
}
