package logdrain

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestDurableSenderNoDeliveryObservationBeforeQueueAck(t *testing.T) {
	q, err := NewQueue(QueueConfig{Root: t.TempDir(), DrainID: "drain-observation"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(Record{InstanceID: "vm-observation", Sequence: 1, Line: "log"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	item, ok, err := q.Next()
	if err != nil || !ok {
		t.Fatalf("queue next: %v %v", ok, err)
	}
	badCursor := filepath.Join(t.TempDir(), "missing", "cursor.json")
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		q.mu.Lock()
		q.cursorPath = badCursor
		q.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	delivered := 0
	sender, err := New(Config{Kind: KindHTTPJSON, TargetURL: endpoint.URL, DurableQueue: q, MaxAttempts: 1, OnDelivered: func(Record) { delivered++ }})
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.deliverDurable(context.Background(), item); err == nil {
		t.Fatal("broken queue ack succeeded")
	}
	if delivered != 0 || q.Stats().PendingRecords != 1 {
		t.Fatal("2xx before durable ack produced delivery authority")
	}
}
