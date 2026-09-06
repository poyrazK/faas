package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type initialCapacityScheduler struct {
	burstSchedulerFake
	desired int
}

func (s *initialCapacityScheduler) EnsureWakeCapacity(_ context.Context, _, _ string, desired int, report func(string, string, string, string, int32, int)) error {
	s.desired = desired
	report("one", "node", "dep", "wake-one", WireWakeRestore, 8080)
	report("two", "node", "dep", "wake-two", WireWakeRestore, 8080)
	return nil
}

func TestInitialCapacityCachesEveryReturnedTarget(t *testing.T) {
	scheduler := &initialCapacityScheduler{}
	b := NewPGBackend(nil, scheduler, nil)
	wake, method, atCap, err := b.EnsureWarmCapacity(context.Background(), "app", "", "gateway", 2)
	if err != nil || atCap || wake != "wake-one" || method != WakeMethodSnapshotRestore {
		t.Fatalf("capacity result=%s,%v,%v,%v", wake, method, atCap, err)
	}
	if scheduler.desired != 2 || b.HealthyCount("app") != 2 {
		t.Fatalf("desired=%d healthy=%d", scheduler.desired, b.HealthyCount("app"))
	}
	first, second := b.Pick("app"), b.Pick("app")
	if first.Target.InstanceID == second.Target.InstanceID {
		t.Fatal("sibling target not available to round-robin")
	}
}

func TestInitialWakeDemandUsesQueuedPressureAndBounds(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), nil)
	for _, tc := range []struct {
		pressure      int64
		maximum, want int
	}{{0, 20, 1}, {1, 20, 1}, {80, 20, 1}, {81, 20, 2}, {100, 1, 1}, {10000, 20, api.ScaleUpMaxBurstPerTick}} {
		h.burstPressure.state("app").inflight.Store(tc.pressure)
		if got := h.initialWakeDemand("app", tc.maximum, api.PlanScale); got != tc.want {
			t.Errorf("pressure=%d max=%d got=%d want=%d", tc.pressure, tc.maximum, got, tc.want)
		}
	}
}

type blockedInitialCapacity struct {
	started chan struct{}
	finish  chan struct{}
}

func (b *blockedInitialCapacity) EnsureWarmCapacity(ctx context.Context, _, _, _ string, _ int) (string, WakeMethod, bool, error) {
	close(b.started)
	select {
	case <-b.finish:
		return "wake", WakeMethodSnapshotRestore, false, nil
	case <-ctx.Done():
		return "", WakeMethodUnspecified, false, ctx.Err()
	}
}

func TestInitialCapacityGenerationPreventsDuplicateExpansion(t *testing.T) {
	b := &burstTestBackend{fakeBackend: &fakeBackend{app: App{ID: "app", Plan: api.PlanScale}}, admitted: make(chan int, 1)}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.burstPressure.state("app").inflight.Store(100)
	initial := &blockedInitialCapacity{started: make(chan struct{}), finish: make(chan struct{})}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { _, _, _, err := h.ensureInitialWarm(ctx, initial, "app", "", "gateway", 2); done <- err }()
	<-initial.started
	b.AddTarget(Target{NodeID: "node", InstanceID: "first"}) // First RUNNING notify arrives before batch completion.
	waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer waitCancel()
	if _, err := h.maybeBurstCapacity(waitCtx, b.app, 3, 80); err == nil {
		t.Error("capacity waiter did not join initial generation")
	}
	select {
	case <-b.admitted:
		t.Error("started duplicate expansion during initial batch")
	default:
	}
	b.AddTarget(Target{NodeID: "node", InstanceID: "second"})
	close(initial.finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := h.maybeBurstCapacity(ctx, b.app, 3, 80); err != nil {
		t.Fatal(err)
	}
}

type capturingInitialBackend struct {
	*fakeBackend
	desired int
}

func (b *capturingInitialBackend) EnsureWarmCapacity(_ context.Context, _, _, _ string, desired int) (string, WakeMethod, bool, error) {
	b.desired = desired
	for i := 0; i < desired; i++ {
		b.AddTarget(Target{NodeID: "node", InstanceID: itoa(uint64(i + 1)), WakeID: "initial-" + itoa(uint64(i+1))})
	}
	return "initial-1", WakeMethodSnapshotRestore, false, nil
}

func TestHandlerPassesInitialPressureWithinAppCap(t *testing.T) {
	for _, maximum := range []int{1, 3} {
		t.Run(itoa(uint64(maximum)), func(t *testing.T) {
			b := &capturingInitialBackend{fakeBackend: &fakeBackend{app: App{ID: "app", Plan: api.PlanScale, MaxConcurrency: maximum}, host: "app.example.com", upstream: "node"}}
			h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			h.burstPressure.state("app").inflight.Store(100)
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil))
			if rec.Code != http.StatusOK || b.desired != min(2, maximum) {
				t.Fatalf("status=%d desired=%d body=%s", rec.Code, b.desired, rec.Body.String())
			}
		})
	}
}

func TestInitialCapacityWakeHeaderFollowsSelectedSibling(t *testing.T) {
	b := &capturingInitialBackend{fakeBackend: &fakeBackend{app: App{ID: "app", Plan: api.PlanScale, MaxConcurrency: 3}, host: "app.example.com", upstream: "node"}}
	b.nextIdx.Store(1) // Select the sibling, not the primary returned by EnsureWarmCapacity.
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.burstPressure.state("app").inflight.Store(100)
	var selected Target
	h.WithForwarding(func(target Target) http.Handler {
		selected = target
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})
	cold := httptest.NewRecorder()
	h.ServeHTTP(cold, httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil))
	if cold.Code != http.StatusOK || selected.InstanceID != "2" || cold.Header().Get("x-faas-wake-id") != "initial-2" {
		t.Fatalf("cold status=%d instance=%s wake=%s", cold.Code, selected.InstanceID, cold.Header().Get("x-faas-wake-id"))
	}
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil))
	if warm.Code != http.StatusOK || warm.Header().Get("x-faas-wake-id") != "" {
		t.Fatalf("warm status=%d wake=%s", warm.Code, warm.Header().Get("x-faas-wake-id"))
	}
}
