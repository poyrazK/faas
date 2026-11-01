package logdrain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The queues of one node share a byte budget: one drain's backlog stops
// being accepted once the node is full, acknowledged bytes come back, and a
// stopped drain releases what it held.
func TestQueueSharedSpoolBudget(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	line := strings.Repeat("x", 200)
	budget := NewSpoolBudget(1000)
	a, err := NewQueue(QueueConfig{Root: root, DrainID: "drain-a", Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewQueue(QueueConfig{Root: root, DrainID: "drain-b", Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	seq := uint64(0)
	enqueue := func(q *Queue) error {
		seq++
		return q.Enqueue(Record{AppID: "app-1", InstanceID: "i-1", Sequence: seq, Line: line}, at)
	}
	for {
		if err := enqueue(a); err != nil {
			if !errors.Is(err, ErrQueueFull) {
				t.Fatalf("enqueue a: %v", err)
			}
			break
		}
	}
	if used := budget.Used(); used != a.Stats().PendingBytes || used > 1000 || used == 0 {
		t.Fatalf("budget used = %d, queue a pending = %d", used, a.Stats().PendingBytes)
	}
	if err := enqueue(b); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("enqueue into b on a full node = %v, want ErrQueueFull", err)
	}

	item, ok, err := a.Next()
	if err != nil || !ok {
		t.Fatalf("Next = %v, %v", ok, err)
	}
	if err := a.Ack(item); err != nil {
		t.Fatal(err)
	}
	if err := enqueue(b); err != nil {
		t.Fatalf("enqueue into b after an ack freed space: %v", err)
	}
	if want := a.Stats().PendingBytes + b.Stats().PendingBytes; budget.Used() != want {
		t.Fatalf("budget used = %d, want the queues' pending %d", budget.Used(), want)
	}

	a.Release()
	if want := b.Stats().PendingBytes; budget.Used() != want {
		t.Fatalf("budget used after releasing a = %d, want %d", budget.Used(), want)
	}
	// Reopening a drain with a backlog charges it again.
	reopened, err := NewQueue(QueueConfig{Root: root, DrainID: "drain-a", Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	if want := reopened.Stats().PendingBytes + b.Stats().PendingBytes; budget.Used() != want {
		t.Fatalf("budget used after reopening a = %d, want %d", budget.Used(), want)
	}
}
