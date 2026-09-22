package main

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

// eventually polls until cond holds or the deadline passes. The signal is
// flipped from a goroutine, so a bare read would be racy.
func eventually(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// TestReadyWhenClosed_StaysFalseUntilSubscribed is the regression test for
// the readiness half of the TestE2E_NormalPath_* investigation.
//
// watchInvalidations was launched as a bare goroutine with nothing observing
// it, while /readyz claimed to cover "the routing cache ... subscription".
// The daemon could therefore report ready — and a load balancer could route
// to it — before its pg_notify LISTEN for route invalidations existed.
//
// The ordering is the whole contract, and it is what a version that set the
// signal at goroutine entry would get wrong while still looking correct at
// the call site.
func TestReadyWhenClosed_StaysFalseUntilSubscribed(t *testing.T) {
	probe := &gateway.ReadyzProbe{}
	signal := probe.Register()
	subscribed := make(chan struct{})

	readyWhenClosed(t.Context(), signal, subscribed)

	// Not ready while the subscription is still being established. Give the
	// goroutine real time to misbehave: an eager Set would land here.
	time.Sleep(25 * time.Millisecond)
	if ready, _ := signal.Report(); ready {
		t.Fatal("signal reported ready before the subscription was established")
	}
	if ok, _ := probe.All(); ok {
		t.Fatal("/readyz would have returned 200 with route invalidations unsubscribed")
	}

	close(subscribed)

	if !eventually(t, time.Second, func() bool { ready, _ := signal.Report(); return ready }) {
		t.Fatal("signal never became ready after the subscription was established")
	}
	if !eventually(t, time.Second, func() bool { ok, _ := probe.All(); return ok }) {
		t.Fatal("/readyz never returned 200 after the subscription was established")
	}
}

// TestReadyWhenClosed_CancelledContextIsNotReadiness pins the fail-closed
// half: shutdown must not be mistaken for readiness. If a cancelled ctx
// flipped the signal, a daemon whose boot subscribe FAILED would start
// advertising itself as ready during teardown.
func TestReadyWhenClosed_CancelledContextIsNotReadiness(t *testing.T) {
	probe := &gateway.ReadyzProbe{}
	signal := probe.Register()
	ctx, cancel := context.WithCancel(context.Background())

	// Never closed — models watchInvalidations returning early because the
	// boot subscribe errored.
	readyWhenClosed(ctx, signal, make(chan struct{}))
	cancel()

	time.Sleep(25 * time.Millisecond)
	if ready, _ := signal.Report(); ready {
		t.Fatal("a cancelled context flipped the signal ready; shutdown is not readiness")
	}
	if ok, _ := probe.All(); ok {
		t.Fatal("/readyz returned 200 for a daemon that never subscribed")
	}
}
