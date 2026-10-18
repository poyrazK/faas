// adr: 1344 — node liveness lease keeper (§4, P3 phase 1).
package nodelease

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/state"
)

type fakeStore struct {
	epoch int64
	err   error
	calls int
}

func (f *fakeStore) RenewNodeLease(_ context.Context, _, _ string, ttl time.Duration) (state.NodeLease, error) {
	f.calls++
	if f.err != nil {
		return state.NodeLease{}, f.err
	}
	return state.NodeLease{Epoch: f.epoch, ExpiresAt: time.Unix(0, 0).Add(ttl)}, nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestKeeperFenceWindow(t *testing.T) {
	store := &fakeStore{epoch: 3}
	k, err := New(store, "node-1", "host/boot", quiet())
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Unix(1_000, 0)
	k.now = func() time.Time { return clock }
	reg := prometheus.NewRegistry()
	k.RegisterMetrics(reg, "schedd")

	if k.WouldFence() {
		t.Fatal("a node that never held its lease reports fenced")
	}
	if err := k.Renew(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(k.metrics.epoch); got != 3 {
		t.Fatalf("epoch gauge = %v, want 3", got)
	}

	// Renewals fail from here on; the fence point is TTL - margin after the
	// last successful reply.
	store.err = errors.New("database unreachable")
	clock = clock.Add(DefaultTTL - DefaultFenceMargin)
	_ = k.Renew(context.Background())
	if k.WouldFence() {
		t.Fatal("fenced at exactly TTL - margin; the window is exclusive")
	}
	clock = clock.Add(time.Second)
	_ = k.Renew(context.Background())
	if !k.WouldFence() {
		t.Fatal("not fenced after TTL - margin without a renewal")
	}
	if got := testutil.ToFloat64(k.metrics.renewals.WithLabelValues("error")); got != 2 {
		t.Fatalf("error renewals = %v, want 2", got)
	}

	// A renewal that succeeds again clears the fence.
	store.err = nil
	if err := k.Renew(context.Background()); err != nil {
		t.Fatal(err)
	}
	if k.WouldFence() {
		t.Fatal("still fenced after a successful renewal")
	}
}

func TestKeeperCountsAHeldLease(t *testing.T) {
	store := &fakeStore{err: state.ErrNodeLeaseHeld}
	k, err := New(store, "node-1", "host/boot", quiet())
	if err != nil {
		t.Fatal(err)
	}
	k.RegisterMetrics(prometheus.NewRegistry(), "schedd")
	if err := k.Renew(context.Background()); !errors.Is(err, state.ErrNodeLeaseHeld) {
		t.Fatalf("Renew = %v, want ErrNodeLeaseHeld", err)
	}
	if got := testutil.ToFloat64(k.metrics.renewals.WithLabelValues("held")); got != 1 {
		t.Fatalf("held renewals = %v, want 1", got)
	}
	if k.WouldFence() {
		t.Fatal("a node that never held the lease reports fenced")
	}
}

func TestKeeperTimingMustLeaveTwoRenewalsBeforeTheFence(t *testing.T) {
	k, err := New(&fakeStore{}, "node-1", "h", quiet())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ttl, interval, margin time.Duration
		ok                    bool
	}{
		{30 * time.Second, 10 * time.Second, 5 * time.Second, true},
		{30 * time.Second, 15 * time.Second, 5 * time.Second, false},
		{10 * time.Second, 2 * time.Second, 10 * time.Second, false},
		{30 * time.Second, 10 * time.Second, 0, false},
	} {
		if _, err := k.WithTiming(tc.ttl, tc.interval, tc.margin); (err == nil) != tc.ok {
			t.Errorf("WithTiming(%s, %s, %s) err = %v, want ok=%v", tc.ttl, tc.interval, tc.margin, err, tc.ok)
		}
	}
	if _, err := New(nil, "node-1", "h", quiet()); err == nil {
		t.Error("New accepted a nil store")
	}
}

func TestHolderIsNonEmpty(t *testing.T) {
	if Holder() == "" {
		t.Fatal("Holder() is empty")
	}
}
