package logdrain

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDurableSenderShutdownDoesNotSpendUnsentRetry(t *testing.T) {
	q, err := NewQueue(QueueConfig{Root: t.TempDir(), DrainID: "drain-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(Record{InstanceID: "vm-1", Sequence: 1, Line: "retry me"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	item, ok, err := q.Next()
	if err != nil || !ok {
		t.Fatalf("Next: %v, %v", ok, err)
	}
	if err := q.MarkAttempt(item, 1); err != nil {
		t.Fatal(err)
	}
	item, _, err = q.Next()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sender, err := New(Config{
		Kind: KindHTTPJSON, TargetURL: "https://logs.example.test", DurableQueue: q, MaxAttempts: 2,
		OnRetry: func(Record, int) { cancel() },
	})
	if err != nil {
		t.Fatal(err)
	}
	sender.deliverDurable(ctx, item)
	resumed, err := NewQueue(QueueConfig{Root: queueRoot(q), DrainID: "drain-1"})
	if err != nil {
		t.Fatal(err)
	}
	item, ok, err = resumed.Next()
	if err != nil || !ok || item.Attempts != 1 {
		t.Fatalf("shutdown spent unsent retry: %+v, %v, %v", item, ok, err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(provider.Close)
	sender, err = New(Config{Kind: KindHTTPJSON, TargetURL: provider.URL, DurableQueue: resumed, MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	sender.deliverDurable(context.Background(), item)
	if stats := resumed.Stats(); stats.PendingRecords != 0 || stats.DeadLetterTotal != 0 {
		t.Fatalf("restarted sender did not deliver final retry: %+v", stats)
	}
}
