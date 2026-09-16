// sse_fanin_test.go pins the cross-process-to-in-process bridge
// contract. Without this test, a refactor of the fan-in (e.g. swapping
// db.SubscribeWithReconnect for a per-request Subscribe) would
// silently break the dashboard's "live updates" UX because no public
// test exercises the path. The fake subscribe seam lets us feed
// synthetic notifications without a live Postgres.
package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
)

// TestSSEFanIn_RepublishesToBroadcaster locks the load-bearing
// invariant: every pg_notify payload that arrives on the subscribe
// channel lands on the broadcaster under the same channel name. We
// drive the fan-in with a fake subscribe that hands it a buffer of
// three notifications; we subscribe to the broadcaster on each
// channel; we assert one Event per channel.
func TestSSEFanIn_RepublishesToBroadcaster(t *testing.T) {
	bc := events.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Pre-attach a subscriber to each channel so PublishTopic
	// fans out (the dashboard reads from the broadcaster in the
	// production shape; the test mirrors that).
	appsCh, cancelApps := bc.Subscribe(db.NotifyAppChanged)
	defer cancelApps()
	depsCh, cancelDeps := bc.Subscribe(db.NotifyDeploymentChanged)
	defer cancelDeps()
	doneCh, cancelDone := bc.Subscribe(db.NotifyInvocationDone)
	defer cancelDone()

	frames := []db.Notification{
		{Channel: db.NotifyAppChanged, Payload: `{"app_id":"a1"}`},
		{Channel: db.NotifyDeploymentChanged, Payload: `{"deployment_id":"d1"}`},
		{Channel: db.NotifyInvocationDone, Payload: `{"invocation_id":"i1","state":"completed"}`},
	}

	// Fake subscribe seam: returns a channel that hands the fan-in
	// each frame in order then closes. Mirrors the outer channel
	// shape of db.SubscribeWithReconnect so the fan-in's "inner
	// closed" path is not triggered.
	out := make(chan db.Notification, len(frames))
	for _, f := range frames {
		out <- f
	}
	close(out)
	subscribe := func(ctx context.Context, _ *slog.Logger) (<-chan db.Notification, error) {
		return out, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		sseFanIn(ctx, log, nil, bc, subscribe)
		close(done)
	}()

	// Drain each subscriber and assert one frame each.
	gotApps := <-appsCh
	if gotApps.Topic != db.NotifyAppChanged || string(gotApps.Payload) != `{"app_id":"a1"}` {
		t.Errorf("appsCh = (%q, %s), want (app_changed, {app_id:a1})", gotApps.Topic, gotApps.Payload)
	}
	gotDeps := <-depsCh
	if gotDeps.Topic != db.NotifyDeploymentChanged || string(gotDeps.Payload) != `{"deployment_id":"d1"}` {
		t.Errorf("depsCh = (%q, %s), want (deployment_changed, {deployment_id:d1})", gotDeps.Topic, gotDeps.Payload)
	}
	gotDone := <-doneCh
	if gotDone.Topic != db.NotifyInvocationDone || string(gotDone.Payload) != `{"invocation_id":"i1","state":"completed"}` {
		t.Errorf("doneCh = (%q, %s)", gotDone.Topic, gotDone.Payload)
	}

	// Cancel the context and confirm the goroutine exits. Subscribe
	// closed the inner channel; the fan-in's contract on an
	// unexpected close is "log + return", so a context cancel races
	// with the close. Either path exits the goroutine.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sseFanIn did not exit within 2s of ctx cancel")
	}
}

// TestSSEFanIn_NilBroadcasterReturns confirms the defensive guard:
// nil broadcaster exits immediately instead of panicking. The
// production constructor in newServerWithDeps guarantees a non-nil
// broadcaster; this test pins the contract so a future refactor
// doesn't drop the guard.
func TestSSEFanIn_NilBroadcasterReturns(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscribe := func(ctx context.Context, _ *slog.Logger) (<-chan db.Notification, error) {
		t.Fatal("subscribe should not be called when broadcaster is nil")
		return nil, nil
	}
	sseFanIn(ctx, log, nil, nil, subscribe)
	// No assertion needed — reaching here without panic or
	// subscribe call is the contract.
}

// TestSSEFanIn_SubscribeErrorLogsAndReturns: when the production
// subscribe fails at boot (e.g. Postgres unreachable), the fan-in
// must not loop or panic. The test confirms the function returns
// after logging the failure.
func TestSSEFanIn_SubscribeErrorLogsAndReturns(t *testing.T) {
	bc := events.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscribe := func(ctx context.Context, _ *slog.Logger) (<-chan db.Notification, error) {
		return nil, errFakeSubscribe
	}
	sseFanIn(ctx, log, nil, bc, subscribe)
}

// errFakeSubscribe is a sentinel used to assert the fan-in exits on
// initial-subscribe failure.
var errFakeSubscribe = &fakeErr{m: "fake subscribe failed"}

type fakeErr struct{ m string }

func (e *fakeErr) Error() string { return e.m }

// Compile-time guard: sseChannels must contain the channels Move 3
// promises to wire. If a future PR drops one, the test fails before
// the production wiring lands.
func TestSSEChannels_Contract(t *testing.T) {
	want := map[string]bool{
		db.NotifyAppChanged:             true,
		db.NotifyDeploymentChanged:      true,
		db.NotifyInstanceChanged:        true,
		db.NotifyCronFired:              true,
		db.NotifyQuotaWarning:           true,
		db.NotifyBillingPastDue:         true,
		db.NotifyInvocationDone:         true,
		db.NotifyDebugRegressionChanged: true,
		db.NotifyStatelessAdvisory:      true,
	}
	got := map[string]bool{}
	for _, ch := range sseChannels {
		got[ch] = true
	}
	for ch := range want {
		if !got[ch] {
			t.Errorf("sseChannels missing %q (Move 3 contract)", ch)
		}
	}
	if len(got) != len(want) {
		t.Errorf("sseChannels has %d entries, want %d (extra: %v)", len(got), len(want), got)
	}
}
