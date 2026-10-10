// adr: 950
package gateway

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// wakeAheadBackend is a multi-app Backend: Admit marks an app healthy and
// records the trigger. hold, when set for an app, blocks that app's Admit
// until released so a test can observe what happens while it restores.
type wakeAheadBackend struct {
	mu       sync.Mutex
	healthy  map[string]bool
	triggers map[string][]string
	admitted chan string
	hold     map[string]chan struct{}
}

func newWakeAheadBackend() *wakeAheadBackend {
	return &wakeAheadBackend{
		healthy:  map[string]bool{},
		triggers: map[string][]string{},
		admitted: make(chan string, 64),
		hold:     map[string]chan struct{}{},
	}
}

func (b *wakeAheadBackend) Lookup(context.Context, string) (App, bool) { return App{}, false }
func (b *wakeAheadBackend) Pick(string) PickResult                     { return PickResult{} }
func (b *wakeAheadBackend) LookupMirrorRules(context.Context, string) ([]MirrorRuleRow, bool) {
	return nil, false
}
func (b *wakeAheadBackend) ScheduleMirror(context.Context, string, string, string) (string, string, error) {
	return "", "", nil
}

func (b *wakeAheadBackend) HealthyCount(appID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.healthy[appID] {
		return 1
	}
	return 0
}

func (b *wakeAheadBackend) Admit(ctx context.Context, appID, _, _, trigger string, _ int) (string, WakeMethod, bool, error) {
	b.mu.Lock()
	b.triggers[appID] = append(b.triggers[appID], trigger)
	hold := b.hold[appID]
	b.mu.Unlock()
	b.admitted <- appID
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return "", WakeMethodUnspecified, false, ctx.Err()
		}
	}
	b.mu.Lock()
	b.healthy[appID] = true
	b.mu.Unlock()
	return "wake-" + appID, WakeMethodSnapshotRestore, false, nil
}

func (b *wakeAheadBackend) triggersFor(appID string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.triggers[appID]...)
}

func wakeAheadApp(id string) App {
	return App{ID: id, AccountID: "acct", Plan: api.PlanPro}
}

// staticWakeAheadPlanner maps a caller to its dependencies and counts calls.
type staticWakeAheadPlanner struct {
	mu    sync.Mutex
	deps  map[string][]string
	calls map[string]int
}

func (p *staticWakeAheadPlanner) plan(_ context.Context, callerAppID string) (ServiceWakeAheadPlan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[callerAppID]++
	var plan ServiceWakeAheadPlan
	for _, id := range p.deps[callerAppID] {
		plan.Targets = append(plan.Targets, wakeAheadApp(id))
	}
	return plan, nil
}

func (p *staticWakeAheadPlanner) callsFor(appID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[appID]
}

func newWakeAheadHandler(b *wakeAheadBackend, deps map[string][]string) (*Handler, *staticWakeAheadPlanner, *Metrics) {
	planner := &staticWakeAheadPlanner{deps: deps, calls: map[string]int{}}
	m := NewMetrics()
	h := NewHandlerWith(b, m, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithServiceWakeAhead(planner.plan)
	return h, planner, m
}

func waitAdmitted(t *testing.T, b *wakeAheadBackend, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case got := <-b.admitted:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("app %s was never admitted", want)
		}
	}
}

func waitHealthy(t *testing.T, b *wakeAheadBackend, ids ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for _, id := range ids {
		for b.HealthyCount(id) == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("app %s never became healthy", id)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// ADR-950 core contract: a cold chain public-api → auth → billing restores
// every hop while the caller is still being admitted, not one after another.
func TestServiceWakeAheadRestoresChainInParallel(t *testing.T) {
	b := newWakeAheadBackend()
	release := make(chan struct{})
	b.hold["public-api"] = release
	h, _, m := newWakeAheadHandler(b, map[string][]string{
		"public-api": {"auth"},
		"auth":       {"billing"},
	})

	done := make(chan error, 1)
	go func() { done <- h.EnsureServiceCapacity(context.Background(), wakeAheadApp("public-api")) }()

	// public-api's admission is held, yet both downstream hops restore.
	waitAdmitted(t, b, "public-api")
	waitHealthy(t, b, "auth", "billing")
	if b.HealthyCount("public-api") != 0 {
		t.Fatal("caller finished before its dependencies; the chain was not parallel")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("caller wake: %v", err)
	}

	for _, id := range []string{"auth", "billing"} {
		if got := b.triggersFor(id); len(got) != 1 || got[0] != sched.TriggerServiceWakeAhead {
			t.Fatalf("%s admit triggers = %v, want one %q", id, got, sched.TriggerServiceWakeAhead)
		}
	}
	if got := b.triggersFor("public-api"); len(got) != 1 || got[0] != sched.TriggerServiceMesh {
		t.Fatalf("caller admit triggers = %v, want one %q", got, sched.TriggerServiceMesh)
	}
	// The outcome is recorded after each restore returns, which can trail the
	// target becoming healthy.
	deadline := time.Now().Add(5 * time.Second)
	for testutil.ToFloat64(m.serviceWakeAhead.WithLabelValues(string(ServiceWakeAheadRestored))) != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("restored = %v, want 2", testutil.ToFloat64(m.serviceWakeAhead.WithLabelValues(string(ServiceWakeAheadRestored))))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Only request-driven wakes are evidence that callees are about to be called.
// A floor or prewarm wake must not fan out.
func TestServiceWakeAheadIgnoresNonRequestTriggers(t *testing.T) {
	for _, trigger := range []string{sched.TriggerPrewarm, sched.TriggerAppWake, sched.TriggerMirror} {
		t.Run(trigger, func(t *testing.T) {
			b := newWakeAheadBackend()
			h, planner, _ := newWakeAheadHandler(b, map[string][]string{"api": {"auth"}})
			if _, _, _, err := h.ensureCapacity(context.Background(), "api", "acct", "", 1, api.PlanPro, 0, trigger); err != nil {
				t.Fatalf("wake: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
			if calls := planner.callsFor("api"); calls != 0 {
				t.Fatalf("planner called %d times for trigger %q", calls, trigger)
			}
			if got := b.triggersFor("auth"); len(got) != 0 {
				t.Fatalf("auth admitted %v for trigger %q", got, trigger)
			}
		})
	}
}

// A dependency that already has a healthy replica is not admitted again, and
// a warm caller never reaches the wake leader at all.
func TestServiceWakeAheadSkipsWarmTargets(t *testing.T) {
	b := newWakeAheadBackend()
	b.healthy["auth"] = true
	h, planner, m := newWakeAheadHandler(b, map[string][]string{"api": {"auth"}})
	if err := h.EnsureServiceCapacity(context.Background(), wakeAheadApp("api")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for testutil.ToFloat64(m.serviceWakeAhead.WithLabelValues(string(ServiceWakeAheadAlreadyWarm))) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("already_warm was never recorded")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := b.triggersFor("auth"); len(got) != 0 {
		t.Fatalf("warm dependency admitted %v", got)
	}

	// The caller is now warm: a second call short-circuits before the gate.
	if err := h.EnsureServiceCapacity(context.Background(), wakeAheadApp("api")); err != nil {
		t.Fatal(err)
	}
	if calls := planner.callsFor("api"); calls != 1 {
		t.Fatalf("planner calls = %d, want 1 (warm caller must not re-plan)", calls)
	}
}

// When the node's in-flight cap is full, wake-ahead is skipped rather than
// queued, and the caller's own wake still proceeds.
func TestServiceWakeAheadSkipsWhenSaturated(t *testing.T) {
	b := newWakeAheadBackend()
	h, planner, m := newWakeAheadHandler(b, map[string][]string{"api": {"auth"}})
	for range cap(h.wakeAhead.slots) {
		h.wakeAhead.slots <- struct{}{}
	}
	if err := h.EnsureServiceCapacity(context.Background(), wakeAheadApp("api")); err != nil {
		t.Fatalf("caller wake must not depend on wake-ahead capacity: %v", err)
	}
	if b.HealthyCount("api") != 1 {
		t.Fatal("caller was not admitted")
	}
	if planner.callsFor("api") != 0 {
		t.Fatal("planner ran without a free slot")
	}
	if got := testutil.ToFloat64(m.serviceWakeAhead.WithLabelValues(string(ServiceWakeAheadSaturated))); got != 1 {
		t.Fatalf("saturated = %v, want 1", got)
	}
}

// nil disables wake-ahead and restores ADR-196 on-demand behaviour.
func TestServiceWakeAheadNilPlannerDisables(t *testing.T) {
	b := newWakeAheadBackend()
	h, _, _ := newWakeAheadHandler(b, map[string][]string{"api": {"auth"}})
	h.WithServiceWakeAhead(nil)
	if err := h.EnsureServiceCapacity(context.Background(), wakeAheadApp("api")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if got := b.triggersFor("auth"); len(got) != 0 {
		t.Fatalf("auth admitted %v with wake-ahead disabled", got)
	}
}
