package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestDesiredBurstInstances(t *testing.T) {
	tests := []struct {
		name     string
		inflight int64
		perVM    int
		max      int
		want     int
	}{
		{name: "empty", inflight: 0, perVM: 80, max: 20, want: 0},
		{name: "partial vm", inflight: 1, perVM: 80, max: 20, want: 1},
		{name: "exact vm", inflight: 80, perVM: 80, max: 20, want: 1},
		{name: "rounds up", inflight: 81, perVM: 80, max: 20, want: 2},
		{name: "scale cap", inflight: 2_000, perVM: 80, max: 20, want: 20},
		{name: "invalid bound", inflight: 100, perVM: 0, max: 20, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := desiredBurstInstances(tt.inflight, tt.perVM, tt.max); got != tt.want {
				t.Fatalf("desiredBurstInstances(%d, %d, %d) = %d, want %d", tt.inflight, tt.perVM, tt.max, got, tt.want)
			}
		})
	}
}

func TestBurstPressureBalancesRequestCount(t *testing.T) {
	var pressure burstPressure
	releaseOne := pressure.begin("app-1")
	releaseTwo := pressure.begin("app-1")
	state := pressure.state("app-1")
	if got := state.inflight.Load(); got != 2 {
		t.Fatalf("inflight after begin = %d, want 2", got)
	}
	releaseOne()
	releaseTwo()
	if got := state.inflight.Load(); got != 0 {
		t.Fatalf("inflight after release = %d, want 0", got)
	}
}

type burstTestBackend struct {
	*fakeBackend
	admitted chan int
}

func (b *burstTestBackend) AdmitBurst(_ context.Context, _ string, _ string, _ string, _ int, count int) (int, error) {
	for i := 0; i < count; i++ {
		b.AddTarget(Target{NodeID: "node-1", InstanceID: "burst-" + itoa(uint64(i+1))})
	}
	b.admitted <- count
	return count, nil
}

type blockingBurstBackend struct {
	*burstTestBackend
	started chan struct{}
	release chan struct{}
}

func (b *blockingBurstBackend) AdmitBurst(ctx context.Context, appID, scope, trigger string, maxConcurrency, count int) (int, error) {
	close(b.started)
	select {
	case <-b.release:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return b.burstTestBackend.AdmitBurst(ctx, appID, scope, trigger, maxConcurrency, count)
}

type burstSchedulerFake struct {
	calls int
}

func (s *burstSchedulerFake) AdmitInstance(context.Context, string, string, string, string) (string, string, string, string, int32, bool, int, error) {
	return "", "", "", "", 0, true, 0, nil
}

func (s *burstSchedulerFake) EnsureWake(context.Context, string, string) (string, string, string, string, int32, int, error) {
	return "", "", "", "", 0, 0, nil
}

func (s *burstSchedulerFake) AdmitMirrorInstance(context.Context, string, string, string) (string, string, error) {
	return "", "", nil
}

func (s *burstSchedulerFake) AdmitInstances(_ context.Context, _ string, _ string, _ string, count int, report func(string, string, string, string, int32, bool, int, error)) error {
	s.calls++
	for i := 0; i < count; i++ {
		id := "burst-" + itoa(uint64(i+1))
		report(id, "node-1", "", id, 0, false, 0, nil)
	}
	return nil
}

func TestPGBackendAdmitBurstUsesProductionBatchAdapter(t *testing.T) {
	fakeSched := &burstSchedulerFake{}
	b := NewPGBackend(nil, fakeSched, nil)

	admitted, err := b.AdmitBurst(context.Background(), "app-1", "", sched.TriggerGateway, 20, 100)
	if err != nil {
		t.Fatalf("AdmitBurst: %v", err)
	}
	if admitted != api.ScaleUpMaxBurstPerTick {
		t.Fatalf("admitted = %d, want %d", admitted, api.ScaleUpMaxBurstPerTick)
	}
	if fakeSched.calls != 1 {
		t.Fatalf("batch scheduler calls = %d, want 1", fakeSched.calls)
	}
	if got := b.HealthyCount("app-1"); got != api.ScaleUpMaxBurstPerTick {
		t.Fatalf("healthy targets = %d, want %d", got, api.ScaleUpMaxBurstPerTick)
	}
}

func TestMaybeBurstCapacityStartsOneDeduplicatedWorker(t *testing.T) {
	b := &burstTestBackend{
		fakeBackend: &fakeBackend{app: App{ID: "app-1", Plan: api.PlanScale}},
		admitted:    make(chan int, 1),
	}
	b.AddTarget(Target{NodeID: "node-1", InstanceID: "warm-1"})
	h := NewHandlerWith(b, NewMetrics(), nil)

	// Scale's per-VM bound is 80. 81 in-flight requests require a second
	// target, so the worker should request exactly one additional admission.
	state := h.burstPressure.state("app-1")
	state.inflight.Store(81)
	defer state.inflight.Store(0)
	h.maybeBurstCapacity(context.Background(), b.app, 20, 80)
	h.maybeBurstCapacity(context.Background(), b.app, 20, 80)
	select {
	case got := <-b.admitted:
		if got != 1 {
			t.Fatalf("burst admission count = %d, want 1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("burst admission worker did not run")
	}
	if got := b.HealthyCount("app-1"); got != 2 {
		t.Fatalf("healthy targets after burst = %d, want 2", got)
	}
}

func TestMaybeBurstCapacityWaitsForReadyTarget(t *testing.T) {
	b := &blockingBurstBackend{
		burstTestBackend: &burstTestBackend{
			fakeBackend: &fakeBackend{app: App{ID: "app-1", Plan: api.PlanScale}},
			admitted:    make(chan int, 1),
		},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	b.AddTarget(Target{NodeID: "node-1", InstanceID: "warm-1"})
	h := NewHandlerWith(b, NewMetrics(), nil)
	state := h.burstPressure.state("app-1")
	state.inflight.Store(81)
	defer state.inflight.Store(0)

	result := make(chan error, 1)
	go func() {
		_, err := h.maybeBurstCapacity(context.Background(), b.app, 20, 80)
		result <- err
	}()

	select {
	case <-b.started:
	case <-time.After(time.Second):
		t.Fatal("burst admission worker did not start")
	}
	select {
	case err := <-result:
		t.Fatalf("maybeBurstCapacity returned before target readiness: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(b.release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("maybeBurstCapacity: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("maybeBurstCapacity did not return after target became ready")
	}
}

func TestPGBackendAdmitBurstCapsLegacyAdapters(t *testing.T) {
	fakeSched := NewFakeScheduler("node-1")
	b := NewPGBackend(nil, fakeSched, nil)

	admitted, err := b.AdmitBurst(context.Background(), "app-1", "", sched.TriggerGateway, 20, 100)
	if err != nil {
		t.Fatalf("AdmitBurst: %v", err)
	}
	if admitted != api.ScaleUpMaxBurstPerTick {
		t.Fatalf("admitted = %d, want %d", admitted, api.ScaleUpMaxBurstPerTick)
	}
	if got := b.HealthyCount("app-1"); got != api.ScaleUpMaxBurstPerTick {
		t.Fatalf("healthy targets = %d, want %d", got, api.ScaleUpMaxBurstPerTick)
	}
}

// cappedBurstBackend models a successful first restore followed by a
// scheduler refusal to expand beyond the app or host limit.
type cappedBurstBackend struct {
	*fakeBackend
	burstCalls atomic.Int32
	release    chan struct{}
	burstErr   error
}

func (b *cappedBurstBackend) Admit(ctx context.Context, appID, deploymentID, scope, trigger string, maxConcurrency int) (string, WakeMethod, bool, error) {
	if b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return "", WakeMethodUnspecified, false, ctx.Err()
		}
	}
	return b.fakeBackend.Admit(ctx, appID, deploymentID, scope, trigger, maxConcurrency)
}

func (b *cappedBurstBackend) AdmitBurst(context.Context, string, string, string, int, int) (int, error) {
	b.burstCalls.Add(1)
	return 0, b.burstErr
}

func TestHandlerColdBurstRespectsAppInstanceCeiling(t *testing.T) {
	b := &cappedBurstBackend{
		fakeBackend: &fakeBackend{
			app:  App{ID: "app-1", Plan: api.PlanScale, MaxConcurrency: 1},
			host: "app.example.com", upstream: "node-1",
		},
		release: make(chan struct{}),
	}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})
	var once sync.Once
	release := func() { once.Do(func() { close(b.release) }) }
	t.Cleanup(release)
	results := make(chan int, 100)
	for i := 0; i < 100; i++ {
		go func() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil))
			results <- rec.Code
		}()
	}
	deadline := time.After(3 * time.Second)
	for h.gate.InflightWaiters(b.app.ID) != 100 {
		select {
		case <-deadline:
			t.Fatal("100 requests did not join the shared wake")
		case <-time.After(time.Millisecond):
		}
	}
	release()
	for i := 0; i < 100; i++ {
		select {
		case status := <-results:
			if status != http.StatusOK {
				t.Errorf("request returned %d, want 200", status)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("burst did not finish")
		}
	}
	if got := atomic.LoadInt32(b.Admits()); got != 1 {
		t.Errorf("cold admissions = %d, want one shared restore", got)
	}
	if got := b.burstCalls.Load(); got != 0 {
		t.Errorf("extra admissions = %d, want none at app ceiling", got)
	}
}

func TestBurstCapacityUsesExistingTargetsWhenExpansionStalls(t *testing.T) {
	backendErr := errors.New("scheduler RPC failed")
	for _, tt := range []struct {
		name     string
		healthy  bool
		admitErr error
		wantErr  error
	}{
		{name: "host full with ready target", healthy: true},
		{name: "no ready target", wantErr: errBurstCapacityStalled},
		{name: "scheduler failure with ready target", healthy: true, admitErr: backendErr},
		{name: "scheduler failure without ready target", admitErr: backendErr, wantErr: backendErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := &cappedBurstBackend{fakeBackend: &fakeBackend{app: App{ID: "app-1", Plan: api.PlanScale}}, burstErr: tt.admitErr}
			if tt.healthy {
				b.AddTarget(Target{NodeID: "node-1", InstanceID: "restored"})
			}
			h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			h.burstPressure.state(b.app.ID).inflight.Store(100)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := h.maybeBurstCapacity(ctx, b.app, 20, 80); !errors.Is(err, tt.wantErr) {
				t.Fatalf("maybeBurstCapacity = %v, want %v", err, tt.wantErr)
			}
			if got := b.burstCalls.Load(); got != 1 {
				t.Fatalf("admissions = %d, want one bounded attempt", got)
			}
		})
	}
}

func TestBurstCapacityClampsAppCeilingToPlan(t *testing.T) {
	for _, appLimit := range []int{0, 1, 100} {
		t.Run(itoa(uint64(appLimit)), func(t *testing.T) {
			b := &cappedBurstBackend{fakeBackend: &fakeBackend{app: App{ID: "app-1", MaxConcurrency: appLimit}}}
			b.AddTarget(Target{NodeID: "node-1", InstanceID: "ready"})
			h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			h.burstPressure.state(b.app.ID).inflight.Store(100)
			if _, err := h.maybeBurstCapacity(context.Background(), b.app, 1, 80); err != nil {
				t.Fatal(err)
			}
			if b.burstCalls.Load() != 0 {
				t.Fatal("attempted admission beyond plan ceiling")
			}
		})
	}
}
