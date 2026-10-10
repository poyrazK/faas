package gateway

// adr: 946

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

type wakeAheadClock struct{ t time.Time }

func (c *wakeAheadClock) now() time.Time { return c.t }
func (c *wakeAheadClock) after(d time.Duration) {
	c.t = c.t.Add(d)
}

type outcomeLog struct {
	mu  sync.Mutex
	got []string
}

func (o *outcomeLog) add(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.got = append(o.got, s)
}

func (o *outcomeLog) count(s string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, g := range o.got {
		if g == s {
			n++
		}
	}
	return n
}

func newTestLearner() (*WakeAheadLearner, *wakeAheadClock, *outcomeLog) {
	clock := &wakeAheadClock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	outcomes := &outcomeLog{}
	return NewWakeAheadLearner(clock.now, outcomes.add), clock, outcomes
}

// train records n caller wakes; calls lists the targets called after each
// wake with whether the call found the target parked.
func train(l *WakeAheadLearner, clock *wakeAheadClock, caller string, n int, calls map[string]bool) {
	for range n {
		l.ObserveWake(caller)
		clock.after(time.Second)
		for target, woken := range calls {
			l.ObserveCall(caller, target, woken)
		}
		clock.after(api.ServiceWakeAheadFollowWindow + time.Second)
	}
}

func TestWakeAheadLearnerPredicts(t *testing.T) {
	tests := []struct {
		name  string
		wakes int
		calls map[string]bool
		want  []string
	}{
		{"cold target called every wake", api.ServiceWakeAheadMinWakes, map[string]bool{"auth": true}, []string{"auth"}},
		{"too few wakes", api.ServiceWakeAheadMinWakes - 1, map[string]bool{"auth": true}, nil},
		{"target is usually warm already", api.ServiceWakeAheadMinWakes, map[string]bool{"auth": false}, nil},
		{"self calls are ignored", api.ServiceWakeAheadMinWakes, map[string]bool{"api": true}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, clock, _ := newTestLearner()
			train(l, clock, "api", tt.wakes, tt.calls)
			if got := l.Predict("api"); !slices.Equal(got, tt.want) {
				t.Fatalf("Predict = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWakeAheadLearnerNeedsCallShare(t *testing.T) {
	l, clock, _ := newTestLearner()
	// Called on 9 of 20 wakes: below the 50% call share.
	train(l, clock, "api", 9, map[string]bool{"auth": true})
	train(l, clock, "api", 11, nil)
	if got := l.Predict("api"); got != nil {
		t.Fatalf("Predict = %v, want none below the call share", got)
	}
	train(l, clock, "api", 4, map[string]bool{"auth": true})
	if got := l.Predict("api"); !slices.Equal(got, []string{"auth"}) {
		t.Fatalf("Predict = %v, want auth at 13 of 24 wakes", got)
	}
}

func TestWakeAheadLearnerWindowAndBursts(t *testing.T) {
	l, clock, _ := newTestLearner()
	if !l.ObserveWake("api") {
		t.Fatal("first wake must open a window")
	}
	clock.after(time.Second)
	if l.ObserveWake("api") {
		t.Fatal("a second request into the same cold app is the same wake")
	}
	clock.after(api.ServiceWakeAheadFollowWindow)
	l.ObserveCall("api", "auth", true) // outside the window
	clock.after(time.Second)
	if !l.ObserveWake("api") {
		t.Fatal("a wake after the window is a new wake")
	}
	c := l.callers["api"]
	if c.wakes != 1 || len(c.edges) != 0 {
		t.Fatalf("late call must not count: wakes=%v edges=%v", c.wakes, c.edges)
	}
}

func TestWakeAheadLearnerOrdersAndCapsTargets(t *testing.T) {
	l, clock, _ := newTestLearner()
	calls := map[string]bool{}
	for i := range api.ServiceWakeAheadMaxTargets + 2 {
		calls[fmt.Sprintf("svc-%d", i)] = true
	}
	train(l, clock, "api", api.ServiceWakeAheadMinWakes, calls)
	train(l, clock, "api", 2, map[string]bool{"svc-4": true})
	got := l.Predict("api")
	if len(got) != api.ServiceWakeAheadMaxTargets || got[0] != "svc-4" {
		t.Fatalf("Predict = %v, want %d targets led by the most called", got, api.ServiceWakeAheadMaxTargets)
	}
}

func TestWakeAheadLearnerKeepsPredictingWhenItWorks(t *testing.T) {
	l, clock, outcomes := newTestLearner()
	train(l, clock, "api", api.ServiceWakeAheadMinWakes, map[string]bool{"auth": true})
	// From now on every wake-ahead lands, so the call finds auth warm.
	for range 3 * api.ServiceWakeAheadMinWakes {
		l.ObserveWake("api")
		if !l.ClaimSpeculation("auth") {
			t.Fatal("claim must succeed once the previous one expired")
		}
		clock.after(time.Second)
		l.ObserveCall("api", "auth", false)
		clock.after(api.ServiceWakeAheadFollowWindow + time.Second)
	}
	if got := l.Predict("api"); !slices.Equal(got, []string{"auth"}) {
		t.Fatalf("a warm call caused by a wake-ahead must still count as a benefit, got %v", got)
	}
	if got := outcomes.count(WakeAheadUsed); got != 3*api.ServiceWakeAheadMinWakes {
		t.Fatalf("used outcomes = %d", got)
	}
}

func TestWakeAheadLearnerSpeculationLifecycle(t *testing.T) {
	l, clock, outcomes := newTestLearner()
	if !l.ClaimSpeculation("auth") || l.ClaimSpeculation("auth") {
		t.Fatal("a fresh claim must block a second one")
	}
	clock.after(api.ServiceWakeAheadFollowWindow + time.Second)
	if !l.ClaimSpeculation("auth") {
		t.Fatal("an expired claim must be retired")
	}
	if got := outcomes.count(WakeAheadUnused); got != 1 {
		t.Fatalf("unused outcomes = %d, want 1", got)
	}
	l.ReleaseSpeculation("auth")
	if !l.ClaimSpeculation("auth") {
		t.Fatal("a released claim must not block")
	}
	clock.after(time.Second)
	l.ObserveCall("api", "auth", true) // woken anyway: the wake-ahead did not help
	if got := outcomes.count(WakeAheadUsed); got != 0 {
		t.Fatalf("a call that still restored the target is not a use, got %d", got)
	}
}

func TestWakeAheadLearnerDecaysAndBounds(t *testing.T) {
	l, clock, _ := newTestLearner()
	train(l, clock, "api", api.ServiceWakeAheadDecayWakes, map[string]bool{"auth": true})
	if c := l.callers["api"]; c.wakes >= api.ServiceWakeAheadDecayWakes || c.edges["auth"].calls != c.wakes {
		t.Fatalf("statistics must halve at the decay point: %+v %+v", c, c.edges["auth"])
	}
	for i := range api.ServiceWakeAheadMaxApps + 1 {
		l.ObserveWake(fmt.Sprintf("caller-%d", i))
		clock.after(time.Millisecond)
	}
	if len(l.callers) > api.ServiceWakeAheadMaxApps {
		t.Fatalf("callers = %d, want at most %d", len(l.callers), api.ServiceWakeAheadMaxApps)
	}
	calls := map[string]bool{}
	for i := range api.ServiceWakeAheadMaxEdgesPerApp + 4 {
		calls[fmt.Sprintf("svc-%d", i)] = true
	}
	train(l, clock, "fan", 1, calls)
	if got := len(l.callers["fan"].edges); got > api.ServiceWakeAheadMaxEdgesPerApp {
		t.Fatalf("edges = %d, want at most %d", got, api.ServiceWakeAheadMaxEdgesPerApp)
	}
}

// wakeAheadBackend reports health per app on top of fakeBackend.
type wakeAheadBackend struct {
	*fakeBackend
	mu      sync.Mutex
	healthy map[string]int
}

func (b *wakeAheadBackend) HealthyCount(appID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.healthy[appID]
}

type wakeAheadFixture struct {
	h        *Handler
	backend  *wakeAheadBackend
	learner  *WakeAheadLearner
	clock    *wakeAheadClock
	enabled  bool
	resident int64
	wakeErr  error
	woken    chan string
	depths   chan int
}

func newWakeAheadFixture(t *testing.T) *wakeAheadFixture {
	t.Helper()
	f := &wakeAheadFixture{enabled: true, resident: 1000, woken: make(chan string, 8), depths: make(chan int, 8)}
	f.backend = &wakeAheadBackend{fakeBackend: &fakeBackend{app: App{ID: "api", AccountID: "acct", Plan: api.PlanPro}}, healthy: map[string]int{}}
	f.h = NewHandlerWith(f.backend, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	f.learner, f.clock, _ = newTestLearner()
	f.h.SetWakeAhead(WakeAheadConfig{
		Learner:   f.learner,
		Enabled:   func(context.Context, string) (bool, error) { return f.enabled, nil },
		Residency: func(context.Context) (int64, int64, error) { return f.resident, 10000, nil },
		Wake: func(ctx context.Context, appID string) error {
			depth, _ := ctx.Value(wakeAheadDepthKey{}).(int)
			f.depths <- depth
			f.woken <- appID
			return f.wakeErr
		},
	})
	train(f.learner, f.clock, "api", api.ServiceWakeAheadMinWakes, map[string]bool{"auth": true})
	return f
}

func (f *wakeAheadFixture) expectWake(t *testing.T) (string, int) {
	t.Helper()
	select {
	case target := <-f.woken:
		return target, <-f.depths
	case <-time.After(2 * time.Second):
		t.Fatal("no wake-ahead")
		return "", 0
	}
}

func (f *wakeAheadFixture) expectNoWake(t *testing.T) {
	t.Helper()
	select {
	case target := <-f.woken:
		t.Fatalf("unexpected wake-ahead of %s", target)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWakeAheadWakesMeasuredTargets(t *testing.T) {
	f := newWakeAheadFixture(t)
	f.h.noteColdWake(context.Background(), "api")
	if target, depth := f.expectWake(t); target != "auth" || depth != 1 {
		t.Fatalf("woke %s at depth %d, want auth at depth 1", target, depth)
	}
	if got := testutil.ToFloat64(f.h.metrics.serviceWakeAhead.WithLabelValues(WakeAheadStarted)); got != 1 {
		t.Fatalf("started = %v", got)
	}
}

func TestWakeAheadGuards(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*wakeAheadFixture) context.Context
		metric string
	}{
		{"caller did not opt in", func(f *wakeAheadFixture) context.Context { f.enabled = false; return context.Background() }, ""},
		{"fleet residency at the guard", func(f *wakeAheadFixture) context.Context {
			f.resident = 10000 * api.ServiceWakeAheadMaxResidentPercent / 100
			return context.Background()
		}, WakeAheadSkippedResidency},
		{"caller already running", func(f *wakeAheadFixture) context.Context { f.backend.healthy["api"] = 1; return context.Background() }, ""},
		{"target already running", func(f *wakeAheadFixture) context.Context { f.backend.healthy["auth"] = 1; return context.Background() }, ""},
		{"depth limit", func(*wakeAheadFixture) context.Context {
			return context.WithValue(context.Background(), wakeAheadDepthKey{}, api.ServiceWakeAheadMaxDepth)
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newWakeAheadFixture(t)
			ctx := tt.mutate(f)
			f.h.noteColdWake(ctx, "api")
			f.expectNoWake(t)
			if tt.metric != "" {
				deadline := time.Now().Add(time.Second)
				for testutil.ToFloat64(f.h.metrics.serviceWakeAhead.WithLabelValues(tt.metric)) != 1 && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
				if got := testutil.ToFloat64(f.h.metrics.serviceWakeAhead.WithLabelValues(tt.metric)); got != 1 {
					t.Fatalf("%s = %v, want 1", tt.metric, got)
				}
			}
		})
	}
}

func TestWakeAheadFailureReleasesClaim(t *testing.T) {
	f := newWakeAheadFixture(t)
	f.wakeErr = errors.New("no headroom")
	f.h.noteColdWake(context.Background(), "api")
	f.expectWake(t)
	deadline := time.Now().Add(time.Second)
	for testutil.ToFloat64(f.h.metrics.serviceWakeAhead.WithLabelValues(WakeAheadFailed)) != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !f.learner.ClaimSpeculation("auth") {
		t.Fatal("a failed wake-ahead must release its claim")
	}
}

func TestWakeAheadCachesTheOptIn(t *testing.T) {
	f := newWakeAheadFixture(t)
	calls := 0
	f.h.wakeAhead.cfg.Enabled = func(context.Context, string) (bool, error) { calls++; return true, nil }
	for range 3 {
		if !f.h.wakeAhead.cached(context.Background(), "api") {
			t.Fatal("opted-in caller must be allowed")
		}
	}
	if calls != 1 {
		t.Fatalf("setting reads = %d, want 1 within the cache TTL", calls)
	}
	f.h.wakeAhead.cfg.Residency = func(context.Context) (int64, int64, error) { return 0, 0, errors.New("db down") }
	if f.h.wakeAhead.cached(context.Background(), "") {
		t.Fatal("a failed residency read must not allow wake-ahead")
	}
}

func TestEnsureWakeAheadCapacityUsesItsTrigger(t *testing.T) {
	f := newWakeAheadFixture(t)
	if err := f.h.EnsureWakeAheadCapacity(context.Background(), App{ID: "auth", AccountID: "acct", Plan: api.PlanPro}); err != nil {
		t.Fatal(err)
	}
	if got := f.backend.lastAdmitTrigger; got != sched.TriggerServiceWakeAhead {
		t.Fatalf("trigger = %q, want %q", got, sched.TriggerServiceWakeAhead)
	}
}

func TestServiceProxyFeedsWakeAheadFromProductionCallers(t *testing.T) {
	var got []string
	p := &ServiceProxy{observeCall: func(caller, target string, woken bool) {
		got = append(got, fmt.Sprintf("%s>%s:%t", caller, target, woken))
	}}
	p.observeWakeAheadCall(ServiceCaller{AppID: "api"}, ServiceTarget{AppID: "auth"}, true)
	p.observeWakeAheadCall(ServiceCaller{AppID: "pr-7", PreviewOfSlug: "api"}, ServiceTarget{AppID: "auth"}, true)
	if !slices.Equal(got, []string{"api>auth:true"}) {
		t.Fatalf("observed %v, want only the production call", got)
	}
}
