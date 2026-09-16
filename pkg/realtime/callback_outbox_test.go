package realtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testCallbackEvent() Event {
	return Event{
		ID:                "evt_outbox_1",
		Type:              EventMessage,
		EndpointID:        "endpoint-1",
		AppID:             "app-1",
		AccountID:         "account-1",
		ConnectionID:      "connection-1",
		Sequence:          7,
		Data:              []byte("hello"),
		At:                time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC),
		CallbackURL:       "https://example.com/callback",
		CallbackPath:      "/realtime/message",
		CallbackAuthToken: "callback-secret",
	}
}

func newTestCallbackOutbox(t *testing.T, cfg CallbackOutboxConfig) *CallbackOutbox {
	t.Helper()
	if cfg.Root == "" {
		cfg.Root = t.TempDir()
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 1 << 20
	}
	if cfg.RetryInterval == 0 {
		cfg.RetryInterval = time.Millisecond
	}
	queue, err := NewCallbackOutbox(cfg)
	if err != nil {
		t.Fatalf("NewCallbackOutbox: %v", err)
	}
	return queue
}

func TestCallbackOutboxPersistsPendingEventAndPrivateFields(t *testing.T) {
	root := t.TempDir()
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root})
	event := testCallbackEvent()
	claimed, err := queue.EnqueueAndClaim(event)
	if err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	}
	if err := queue.Fail(event.ID); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	restarted := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root})
	time.Sleep(3 * time.Millisecond)
	replayed, ok, err := restarted.ClaimNext()
	if err != nil || !ok {
		t.Fatalf("ClaimNext after restart = (%+v, %v, %v), want event", replayed, ok, err)
	}
	if replayed.ID != event.ID || replayed.Type != EventMessage || string(replayed.Data) != "hello" ||
		replayed.CallbackURL != event.CallbackURL || replayed.CallbackPath != event.CallbackPath ||
		replayed.CallbackAuthToken != event.CallbackAuthToken {
		t.Fatalf("replayed event = %+v, want private callback fields preserved", replayed)
	}
}

func TestCallbackOutboxAckRemovesPendingEvent(t *testing.T) {
	root := t.TempDir()
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root})
	event := testCallbackEvent()
	claimed, err := queue.EnqueueAndClaim(event)
	if err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	}
	if err := queue.Ack(event.ID); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if stats := queue.Stats(); stats.Pending != 0 || stats.PendingBytes != 0 {
		t.Fatalf("Stats after ack = %+v, want empty", stats)
	}
	if _, err := os.Stat(filepath.Join(root, event.ID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending file stat = %v, want not found", err)
	}
}

func TestCallbackOutboxDeadLettersAfterBoundedFailures(t *testing.T) {
	root := t.TempDir()
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root, MaxAttempts: 2})
	event := testCallbackEvent()
	for attempt := 1; attempt <= 2; attempt++ {
		if attempt > 1 {
			time.Sleep(3 * time.Millisecond)
		}
		claimed, err := queue.EnqueueAndClaim(event)
		if err != nil || !claimed {
			t.Fatalf("attempt %d EnqueueAndClaim = (%v, %v), want claimed", attempt, claimed, err)
		}
		if err := queue.Fail(event.ID); err != nil {
			t.Fatalf("attempt %d Fail: %v", attempt, err)
		}
	}
	stats := queue.Stats()
	if stats.Pending != 0 || stats.DeadLetterTotal != 1 {
		t.Fatalf("Stats after dead letter = %+v, want no pending and one dead letter", stats)
	}
	if _, err := os.Stat(filepath.Join(root, "dead", event.ID+".json")); err != nil {
		t.Fatalf("dead letter stat: %v", err)
	}
}

func TestCallbackOutboxRunReplaysPendingEvents(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{MaxAttempts: 2})
	event := testCallbackEvent()
	if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	} else {
		queue.Release(event.ID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	delivered := make(chan Event, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- queue.Run(ctx, func(_ context.Context, event Event) error {
			delivered <- event
			cancel()
			return nil
		})
	}()
	select {
	case got := <-delivered:
		if got.ID != event.ID {
			t.Fatalf("delivered event ID = %q, want %q", got.ID, event.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for outbox replay")
	}
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestCallbackOutboxRejectsFullPayload(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{MaxBytes: 1})
	if _, err := queue.EnqueueAndClaim(testCallbackEvent()); !errors.Is(err, ErrCallbackOutboxFull) {
		t.Fatalf("EnqueueAndClaim full = %v, want ErrCallbackOutboxFull", err)
	}
}

func TestCallbackOutboxSupportsDisconnectEvents(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	event := testCallbackEvent()
	event.Type = EventDisconnect
	if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim disconnect = (%v, %v), want claimed", claimed, err)
	}
	if err := queue.Ack(event.ID); err != nil {
		t.Fatalf("Ack disconnect: %v", err)
	}
}

func TestManagerStatsIncludesCallbackOutboxCounters(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	event := testCallbackEvent()
	if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	}
	queue.Release(event.ID)
	m := NewManager(Config{}, HTTPHooks{DurableQueue: queue})
	defer m.Close()
	stats := m.Stats()
	if stats.CallbackPending != 1 || stats.CallbackPendingBytes == 0 || stats.CallbackDeadLetters != 0 {
		t.Fatalf("Manager Stats = %+v, want pending callback counters", stats)
	}
}
