// PR scale-out readiness — heartbeat tests for WatchEvictions. The
// production subscription is db.SubscribeWithReconnect against a live
// Postgres; these tests inject a fake subscribe func that returns a
// caller-controlled channel so the heartbeat goroutine's lifetime
// can be driven synchronously without standing up a daemon.
//
// Mirrors the pattern in pkg/gateway/idle_test.go (now fn injection)
// and pkg/gateway/cert_expiry_test.go (ticker-driven tests). The
// heartbeat goroutine itself is the load-bearing piece; the
// subscribe-channel is a seam that lets the test toggle the loop
// on/off without a real `LISTEN compute_node_changed`.

package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// fakeSubscribe is the test seam for WatchEvictions — see
// subscribeFunc in nodecache.go. The returned channel is closed by
// the test to drive WatchEvictions's "subscriber closed" return path.
//
// Channels and logger arguments are intentionally not recorded: the
// tests only care about (channel, err) and the heartbeat's gauge
// state. Recording them would be speculative instrumentation that
// has not earned its keep (PR #407 review findings S4+S5).
type fakeSubscribe struct {
	mu  sync.Mutex
	ch  chan db.Notification
	err error
}

func newFakeSubscribe() *fakeSubscribe { return &fakeSubscribe{ch: make(chan db.Notification, 4)} }

// subscribe is the function-shape adapter for the nodeCache.subscribe
// field. The fakeSubscribe controls whether Subscribe returns the
// channel or an error.
func (f *fakeSubscribe) subscribe(_ context.Context, _ *pgxpool.Pool, _ []string, _ *slog.Logger) (<-chan db.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.ch, nil
}

func (f *fakeSubscribe) closeCh() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ch != nil {
		// Drain any unread notifications before close so the
		// main loop's "ok" branch fires the close path, not
		// an accidental panic in a separate writer.
		close(f.ch)
		f.ch = nil
	}
}

// makeNodeCacheForTest builds a nodeCache wired to the supplied
// fakeSubscribe. Direct construction (rather than newNodeCache) so we
// don't trigger gateway.SetNodeResolver's package-level side effect
// (CLAUDE.md: tests don't mutate package-level state).
//
// metrics=nil is allowed; WatchEvictions skips the heartbeat goroutine
// in that case (TestNodeCacheWatchEvictionsHeartbeat_NilMetricsIsNoOp).
//
// If onHeartbeatStopped is non-nil, it is wired to nodeCache's
// heartbeatStopped field so the test can deterministically wait for
// the heartbeat goroutine's exit via sync.WaitGroup (PR #407 review
// finding S2: replace time.Sleep with WaitGroup).
func makeNodeCacheForTest(f *fakeSubscribe, metrics *gateway.Metrics, onHeartbeatStopped func()) *nodeCache {
	return &nodeCache{
		cache:            gateway.NewNodeClientCache(nil, nil),
		log:              testLogger(),
		metrics:          metrics,
		subscribe:        f.subscribe,
		heartbeatStopped: onHeartbeatStopped,
	}
}

// valueOf reads the gauge via Gather() — the same path operators see
// at /metrics scrape. Mirrors TestComputeNodeChangedSubscriberAliveRegisters
// in pkg/gateway/metrics_test.go.
func gaugeValue(t *testing.T, m *gateway.Metrics) float64 {
	t.Helper()
	fams, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, fam := range fams {
		if fam.GetName() == "gateway_compute_node_changed_subscriber_alive" {
			for _, mt := range fam.GetMetric() {
				return mt.GetGauge().GetValue()
			}
			return 0
		}
	}
	return 0
}

// waitForHeartbeat blocks until the heartbeat goroutine signals exit
// via the WaitGroup, or fails the test after a generous deadline. The
// exit hook is wired by makeNodeCacheForTest; production never
// installs it. Tests that don't expect a heartbeat goroutine to
// launch (NotStartedOnSubscribeFailure, NilMetricsIsNoOp) skip this
// entirely.
func waitForHeartbeat(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat goroutine did not exit within 2s")
	}
}

// TestNodeCacheWatchEvictionsHeartbeat_IncrementsAlive — happy path:
// a short heartbeat interval drives the gauge monotonically upward.
// First tick fires after one interval (mirrors
// StartCertExpiryRefresher's no-boot-bump behavior).
func TestNodeCacheWatchEvictionsHeartbeat_IncrementsAlive(t *testing.T) {
	fs := newFakeSubscribe()
	m := gateway.NewMetrics()
	heartbeatWG := &sync.WaitGroup{}
	heartbeatWG.Add(1) // heartbeat goroutine Done()s on exit
	nc := makeNodeCacheForTest(fs, m, heartbeatWG.Done)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	interval := 50 * time.Millisecond
	done := make(chan struct{})
	go func() {
		nc.watchEvictionsWithInterval(ctx, nil, interval)
		close(done)
	}()

	// Wait for the heartbeat to have ticked at least twice.
	deadline := time.Now().Add(time.Second)
	var got float64
	for time.Now().Before(deadline) {
		got = gaugeValue(t, m)
		if got >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got < 2 {
		t.Errorf("gauge = %v, want >= 2 within 1s with %v interval", got, interval)
	}

	cancel()
	fs.closeCh()
	<-done
	waitForHeartbeat(t, heartbeatWG)

	// Gauge must NOT climb after the heartbeat exits.
	gotAfter := gaugeValue(t, m)
	time.Sleep(100 * time.Millisecond)
	if got := gaugeValue(t, m); got != gotAfter {
		t.Errorf("gauge climbed after heartbeat exit: before=%v after=%v", gotAfter, got)
	}
}

// TestNodeCacheWatchEvictionsHeartbeat_StopsOnCtxCancel — ctx cancel
// must end the heartbeat goroutine (and WatchEvictions) cleanly.
// Gauge stops climbing once the goroutine has exited.
func TestNodeCacheWatchEvictionsHeartbeat_StopsOnCtxCancel(t *testing.T) {
	fs := newFakeSubscribe()
	m := gateway.NewMetrics()
	heartbeatWG := &sync.WaitGroup{}
	heartbeatWG.Add(1)
	nc := makeNodeCacheForTest(fs, m, heartbeatWG.Done)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		nc.watchEvictionsWithInterval(ctx, nil, 40*time.Millisecond)
		close(done)
	}()

	// Wait for at least one tick.
	deadline := time.Now().Add(time.Second)
	var v1 float64
	for time.Now().Before(deadline) {
		v1 = gaugeValue(t, m)
		if v1 >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if v1 < 1 {
		t.Fatalf("pre-cancel gauge = %v, want >= 1", v1)
	}

	cancel()
	<-done
	waitForHeartbeat(t, heartbeatWG)

	v2 := gaugeValue(t, m)
	time.Sleep(100 * time.Millisecond)
	v3 := gaugeValue(t, m)
	if v2 != v3 {
		t.Errorf("gauge climbed after cancel: v2=%v v3=%v", v2, v3)
	}
}

// TestNodeCacheWatchEvictionsHeartbeat_StopsOnSubscriberClose — when
// the subscribe-fake's channel closes (mid-flight Reconnect failure
// in production), WatchEvictions returns and the heartbeat goroutine
// must stop. The gauge freezes; an alert rule fires "stale".
func TestNodeCacheWatchEvictionsHeartbeat_StopsOnSubscriberClose(t *testing.T) {
	fs := newFakeSubscribe()
	m := gateway.NewMetrics()
	heartbeatWG := &sync.WaitGroup{}
	heartbeatWG.Add(1)
	nc := makeNodeCacheForTest(fs, m, heartbeatWG.Done)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		nc.watchEvictionsWithInterval(ctx, nil, 40*time.Millisecond)
		close(done)
	}()

	// Wait for at least one tick.
	deadline := time.Now().Add(time.Second)
	var v1 float64
	for time.Now().Before(deadline) {
		v1 = gaugeValue(t, m)
		if v1 >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if v1 < 1 {
		t.Fatalf("pre-close gauge = %v, want >= 1", v1)
	}

	fs.closeCh()
	<-done
	waitForHeartbeat(t, heartbeatWG)

	v2 := gaugeValue(t, m)
	time.Sleep(100 * time.Millisecond)
	v3 := gaugeValue(t, m)
	if v2 != v3 {
		t.Errorf("gauge climbed after subscriber close: v2=%v v3=%v", v2, v3)
	}
}

// TestNodeCacheWatchEvictionsHeartbeat_NotStartedOnSubscribeFailure
// — when the initial subscribe returns a non-nil error, the heartbeat
// goroutine MUST NOT start. The gauge stays at 0; the alert rule
// fires. This pins the "first-subscribe failure → no heartbeat"
// semantics the plan calls for.
//
// heartbeatWG is intentionally not installed in makeNodeCacheForTest:
// if the goroutine is never launched, wg.Wait() would block forever.
// The test instead asserts the gauge stays at 0 (no heartbeat ticks)
// and that WatchEvictions returned via the done channel.
func TestNodeCacheWatchEvictionsHeartbeat_NotStartedOnSubscribeFailure(t *testing.T) {
	fs := newFakeSubscribe()
	fs.err = errors.New("subscribe boom")
	m := gateway.NewMetrics()
	nc := makeNodeCacheForTest(fs, m, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		nc.watchEvictionsWithInterval(ctx, nil, 40*time.Millisecond)
		close(done)
	}()

	// Wait for WatchEvictions to return from the subscribe-failure path.
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WatchEvictions did not return after subscribe failure")
	}

	// Hold here briefly to catch any spurious late heartbeat launch.
	time.Sleep(100 * time.Millisecond)

	if got := gaugeValue(t, m); got != 0 {
		t.Errorf("gauge = %v after subscribe-failure; want 0 (no heartbeat started)", got)
	}
}

// TestNodeCacheWatchEvictionsHeartbeat_NilMetricsIsNoOp — when
// metrics is nil, WatchEvictions must not panic (the heartbeat
// goroutine is skipped via the n.metrics != nil guard). Subscribe
// succeeds; the main loop runs; the only assertion is "no panic".
// The fake channel stays open so WatchEvictions returns on ctx
// cancel only.
func TestNodeCacheWatchEvictionsHeartbeat_NilMetricsIsNoOp(t *testing.T) {
	fs := newFakeSubscribe()
	nc := makeNodeCacheForTest(fs, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("WatchEvictions panicked with nil metrics: %v", r)
			}
			close(done)
		}()
		nc.watchEvictionsWithInterval(ctx, nil, 20*time.Millisecond)
	}()

	select {
	case <-done:
		// returned, no panic
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WatchEvictions did not return after ctx timeout")
	}
}
