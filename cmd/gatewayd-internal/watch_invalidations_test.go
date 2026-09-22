package main

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

// TestWatchInvalidations_FailedSubscribeLeavesReadinessClosed pins the
// fail-closed half of the /readyz invalidation signal.
//
// watchInvalidations announces readiness by closing `subscribed`. The whole
// contract is that it does so ONLY after SubscribeWithReconnect has
// established the LISTEN. When the boot subscribe fails, the channel must
// stay open so /readyz keeps reporting 503: a gateway that cannot hear route
// changes serves a frozen routing table, which is worse than being drained
// out of rotation.
//
// Getting this backwards is silent and dangerous — a broken gateway would
// advertise itself ready and the load balancer would route real traffic to
// it. The function had no test at all before this.
//
// A nil pool is the cheapest way to force the failure: SubscribeWithReconnect
// rejects it outright, exercising the early-return path without needing
// Postgres.
func TestWatchInvalidations_FailedSubscribeLeavesReadinessClosed(t *testing.T) {
	subscribed := make(chan struct{})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	done := make(chan struct{})
	go func() {
		defer close(done)
		watchInvalidations(t.Context(), nil, &fakeInvalidator{}, log, subscribed)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watchInvalidations did not return after a failed subscribe")
	}

	select {
	case <-subscribed:
		t.Fatal("readiness was announced despite the subscribe failing; /readyz would report 200 for a gateway that cannot hear route changes")
	default:
	}
}

// TestWatchInvalidations_NilReadinessChannelIsTolerated pins the seam every
// existing caller relies on: a test double or a future caller that does not
// care about readiness passes nil, and must not panic on the close.
func TestWatchInvalidations_NilReadinessChannelIsTolerated(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	done := make(chan struct{})
	go func() {
		defer close(done)
		watchInvalidations(t.Context(), nil, &fakeInvalidator{}, log, nil)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watchInvalidations did not return with a nil readiness channel")
	}
}
