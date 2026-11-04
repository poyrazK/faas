package githubd

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// wakingWebhookStore has no deliveries; it reports each claim and wakes the
// worker the way PGWebhookStore does after an enqueue.
type wakingWebhookStore struct {
	queued chan struct{}
	claims chan struct{}
}

func (s *wakingWebhookStore) Enqueue(context.Context, WebhookDelivery) (bool, error) {
	return true, nil
}

func (s *wakingWebhookStore) Claim(context.Context) (WebhookDelivery, error) {
	s.claims <- struct{}{}
	return WebhookDelivery{}, ErrNoWebhookDelivery
}

func (s *wakingWebhookStore) Complete(context.Context, string) error { return nil }

func (s *wakingWebhookStore) Fail(context.Context, string, string, time.Time, bool) error {
	return nil
}

func (s *wakingWebhookStore) Prune(context.Context, time.Time) error { return nil }

func (s *wakingWebhookStore) Queued() <-chan struct{} { return s.queued }

// TestWebhookWorkerWaitsForQueuedDeliveries pins the idle loop: a worker
// whose store wakes it on enqueue claims once at start, then waits for the
// wake instead of claiming every 500 ms.
func TestWebhookWorkerWaitsForQueuedDeliveries(t *testing.T) {
	store := &wakingWebhookStore{queued: make(chan struct{}, 1), claims: make(chan struct{}, 8)}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		RunWebhookDeliveryWorker(ctx, store, &Service{}, slog.Default())
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()
	claimed := func(within time.Duration) bool {
		select {
		case <-store.claims:
			return true
		case <-time.After(within):
			return false
		}
	}
	if !claimed(5 * time.Second) {
		t.Fatal("worker made no claim at start")
	}
	if claimed(webhookPollEvery + 300*time.Millisecond) {
		t.Fatal("idle worker claimed again on the 500 ms poll despite its store's wake")
	}
	store.queued <- struct{}{}
	if !claimed(2 * time.Second) {
		t.Fatal("a queued delivery did not wake the worker")
	}
}
