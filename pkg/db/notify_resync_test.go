// adr: 1344
// notify_resync_test.go pins ADR-1344 §5 rule 3: pg_notify is not durable,
// so a SubscribeWithResync consumer is told to reload after the LISTEN
// connection is re-established and after one of its deliveries is dropped.

package db

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func quietHub() *notifyHub {
	return newNotifyHub(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func expectResync(t *testing.T, ch <-chan Notification) {
	t.Helper()
	select {
	case n := <-ch:
		if n.Channel != ResyncChannel {
			t.Fatalf("got %+v, want a resync notification", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no resync notification within 2s")
	}
}

// adr: 1344
func TestNotifyHubRequestResyncReachesOnlyResyncSubscribers(t *testing.T) {
	h := quietHub()
	resync := &hubSubscriber{channels: map[string]struct{}{"c": {}}, out: make(chan Notification, 4), resync: true}
	plain := &hubSubscriber{channels: map[string]struct{}{"c": {}}, out: make(chan Notification, 4)}
	h.subs[resync] = struct{}{}
	h.subs[plain] = struct{}{}

	h.requestResync()

	expectResync(t, resync.out)
	if len(plain.out) != 0 {
		t.Fatalf("plain subscriber received %d notifications, want 0", len(plain.out))
	}
}

// adr: 1344
// TestNotifyHubDroppedDeliveryQueuesResync: an overflowing resync subscriber
// loses the notification but is told to reload as soon as it has room, even
// if no further notification arrives for it.
func TestNotifyHubDroppedDeliveryQueuesResync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := quietHub()
	h.runCtx = ctx
	sub := &hubSubscriber{channels: map[string]struct{}{"c": {}}, out: make(chan Notification, 1), resync: true}
	h.subs[sub] = struct{}{}

	h.dispatch(Notification{Channel: "c", Payload: "first"})
	h.dispatch(Notification{Channel: "c", Payload: "dropped"})
	if sub.dropped.Load() != 1 {
		t.Fatalf("dropped=%d want 1", sub.dropped.Load())
	}

	recv(t, sub.out, "first")
	expectResync(t, sub.out)
}

// adr: 1344
// TestNotifyHubPendingResyncPrecedesNextDelivery: a queued resync is
// delivered ahead of the next notification, so the reload covers whatever
// was dropped before it.
func TestNotifyHubPendingResyncPrecedesNextDelivery(t *testing.T) {
	h := quietHub()
	sub := &hubSubscriber{channels: map[string]struct{}{"c": {}}, out: make(chan Notification, 2), resync: true}
	h.subs[sub] = struct{}{}
	h.mu.Lock()
	sub.resyncPending = true
	h.mu.Unlock()

	h.dispatch(Notification{Channel: "c", Payload: "next"})

	expectResync(t, sub.out)
	recv(t, sub.out, "next")
}

// adr: 1344
// TestNotifyHub_ResyncAfterBackendTerminated is the Postgres-backed check:
// killing the hub's LISTEN backend makes a resync subscriber reload, while a
// plain subscriber on the same hub sees no synthetic notification.
func TestNotifyHub_ResyncAfterBackendTerminated(t *testing.T) {
	t.Setenv(NotifyHubEnv, "")
	pool, ctx := hubTestPool(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	resyncCh, err := SubscribeWithResync(ctx, pool, []string{"hub_resync"}, log)
	if err != nil {
		t.Fatal(err)
	}
	plainCh, err := SubscribeWithReconnect(ctx, pool, []string{"hub_resync"}, log)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `
		SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		 WHERE datname = current_database() AND pid <> pg_backend_pid() AND query LIKE 'LISTEN %'`); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	select {
	case n := <-resyncCh:
		if n.Channel != ResyncChannel {
			t.Fatalf("first delivery after reconnect = %+v, want resync", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no resync within 10s of the LISTEN backend being terminated")
	}
	select {
	case n := <-plainCh:
		t.Fatalf("plain subscriber received %+v; resync must be opt-in", n)
	default:
	}
}
