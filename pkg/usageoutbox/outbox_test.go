package usageoutbox

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testEvent(id string) Event {
	return Event{EventID: id, AccountID: "account", AppID: "app", WindowStart: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC), RequestCount: 1, BillableUnits: 1}
}

func BenchmarkEnqueueGroupFsync(b *testing.B) {
	q, err := Open(b.TempDir(), 1<<30)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		event := testEvent("")
		for pb.Next() {
			event.EventID = uuid.NewString()
			if err := q.Enqueue(event); err != nil {
				b.Error(err)
			}
		}
	})
}

func TestRestartReplaysOnlyUnacknowledgedEvents(t *testing.T) {
	root := t.TempDir()
	q, err := Open(root, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(testEvent("first")); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(testEvent("second")); err != nil {
		t.Fatal(err)
	}
	first, ok, err := q.Next()
	if err != nil || !ok || first.Event.EventID != "first" {
		t.Fatalf("first=%+v ok=%t err=%v", first, ok, err)
	}
	// Simulate apid commit followed by gateway death before acknowledgement.
	q, err = Open(root, 4096)
	if err != nil {
		t.Fatal(err)
	}
	first, ok, err = q.Next()
	if err != nil || !ok || first.Event.EventID != "first" {
		t.Fatalf("replay=%+v ok=%t err=%v", first, ok, err)
	}
	if err := q.Ack(first); err != nil {
		t.Fatal(err)
	}
	q, err = Open(root, 4096)
	if err != nil {
		t.Fatal(err)
	}
	second, ok, err := q.Next()
	if err != nil || !ok || second.Event.EventID != "second" {
		t.Fatalf("second=%+v ok=%t err=%v", second, ok, err)
	}
	if err := q.Ack(second); err != nil {
		t.Fatal(err)
	}
	if got := q.Stats().PendingRecords; got != 0 {
		t.Fatalf("pending=%d", got)
	}
	q, err = Open(root, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := q.Next(); err != nil || ok {
		t.Fatalf("empty replay ok=%t err=%v", ok, err)
	}
}

func TestRestartPreservesOptionalAuditEvidence(t *testing.T) {
	root := t.TempDir()
	q, err := Open(root, 4096)
	if err != nil {
		t.Fatal(err)
	}
	event := testEvent(uuid.NewString())
	event.Audit = &AuditEvidence{RouteTemplate: "POST /payments/{id}", Method: "POST", HTTPStatus: 201,
		SourceIP: "203.0.113.42", OccurredAt: time.Now().UTC()}
	if err := q.Enqueue(event); err != nil {
		t.Fatal(err)
	}
	q, err = Open(root, 4096)
	if err != nil {
		t.Fatal(err)
	}
	item, ok, err := q.Next()
	if err != nil || !ok || item.Event.Audit == nil || item.Event.Audit.SourceIP != "203.0.113.42" {
		t.Fatalf("replayed audit item=%+v ok=%v err=%v", item, ok, err)
	}
}

func TestConcurrentGroupCommitPersistsEveryEvent(t *testing.T) {
	root := t.TempDir()
	q, err := Open(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	const count = 100
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- q.Enqueue(testEvent(uuid.NewString())) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	q, err = Open(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got := q.Stats().PendingRecords; got != count {
		t.Fatalf("durable events=%d, want %d", got, count)
	}
	seen := make(map[string]bool, count)
	for i := 0; i < count; i++ {
		item, ok, err := q.Next()
		if err != nil || !ok {
			t.Fatalf("next %d ok=%t err=%v", i, ok, err)
		}
		if seen[item.Event.EventID] {
			t.Fatalf("duplicate event %s", item.Event.EventID)
		}
		seen[item.Event.EventID] = true
		if err := q.Ack(item); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFullOutboxKeepsEarlierFact(t *testing.T) {
	q, err := Open(t.TempDir(), 180)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(testEvent("first")); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(testEvent("second")); !errors.Is(err, ErrFull) {
		t.Fatalf("second enqueue=%v", err)
	}
	item, ok, err := q.Next()
	if err != nil || !ok || item.Event.EventID != "first" {
		t.Fatalf("head=%+v ok=%t err=%v", item, ok, err)
	}
}

func TestTornFinalAppendIsTruncatedButCorruptCompleteRecordFails(t *testing.T) {
	root := t.TempDir()
	q, err := Open(root, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(testEvent("first")); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(root, "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"event_id\":"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	q, err = Open(root, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if got := q.Stats().PendingRecords; got != 1 {
		t.Fatalf("pending=%d", got)
	}
	if err := os.WriteFile(filepath.Join(root, "events.jsonl"), []byte("not-json\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, 4096); err == nil {
		t.Fatal("corrupt complete event was accepted")
	}
}
