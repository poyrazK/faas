package logdrain

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueueResumesPendingRecordAndSourceCursorAfterRestart(t *testing.T) {
	root := t.TempDir()
	record := Record{AppID: "app-1", InstanceID: "instance-1", Sequence: 7, Line: "hello"}
	enqueuedAt := time.Date(2026, 9, 11, 18, 0, 0, 0, time.UTC)

	queue, err := NewQueue(QueueConfig{Root: root, DrainID: "drain-1", MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	if err := queue.Enqueue(record, enqueuedAt); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	item, ok, err := queue.Next()
	if err != nil || !ok || item.Record != record || !item.EnqueuedAt.Equal(enqueuedAt) {
		t.Fatalf("Next = (%+v, %v, %v), want queued record", item, ok, err)
	}
	if err := queue.MarkAttempt(item, 2); err != nil {
		t.Fatalf("MarkAttempt: %v", err)
	}

	queue, err = NewQueue(QueueConfig{Root: root, DrainID: "drain-1", MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("NewQueue after restart: %v", err)
	}
	item, ok, err = queue.Next()
	if err != nil || !ok || item.Attempts != 2 {
		t.Fatalf("Next after restart = (%+v, %v, %v), want attempts=2", item, ok, err)
	}
	if err := queue.Ack(item); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if _, ok, err := queue.Next(); err != nil || ok {
		t.Fatalf("Next after ack = (%v, %v), want empty", ok, err)
	}
	if got := queue.LastSequences()[record.InstanceID]; got != int64(record.Sequence) {
		t.Fatalf("LastSequences = %v, want %d", got, record.Sequence)
	}
	if stats := queue.Stats(); stats.PendingRecords != 0 || stats.PendingBytes != 0 {
		t.Fatalf("Stats after ack = %+v, want empty", stats)
	}

	queue, err = NewQueue(QueueConfig{Root: root, DrainID: "drain-1", MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("NewQueue second restart: %v", err)
	}
	if err := queue.Enqueue(record, enqueuedAt); err != nil {
		t.Fatalf("duplicate Enqueue: %v", err)
	}
	if stats := queue.Stats(); stats.PendingRecords != 0 {
		t.Fatalf("duplicate enqueue Stats = %+v, want empty", stats)
	}
}

func TestQueueDeadLettersAndBoundsDeadLetterFile(t *testing.T) {
	root := t.TempDir()
	queue, err := NewQueue(QueueConfig{
		Root: root, DrainID: "drain-1", MaxBytes: 1 << 20, DeadLetterMaxBytes: 512,
	})
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	for sequence := uint64(1); sequence <= 2; sequence++ {
		record := Record{InstanceID: "instance-1", Sequence: sequence, Line: "dead"}
		if err := queue.Enqueue(record, time.Now().UTC()); err != nil {
			t.Fatalf("Enqueue(%d): %v", sequence, err)
		}
		item, ok, err := queue.Next()
		if err != nil || !ok {
			t.Fatalf("Next(%d) = (%v, %v)", sequence, ok, err)
		}
		if err := queue.DeadLetter(item, 3, errors.New("endpoint failed")); err != nil {
			t.Fatalf("DeadLetter(%d): %v", sequence, err)
		}
	}
	stats := queue.Stats()
	if stats.PendingRecords != 0 || stats.DeadLetterTotal != 1 {
		t.Fatalf("Stats after dead letters = %+v, want empty active queue and one retained letter", stats)
	}
	deadPath := filepath.Join(root, "drain-1", "dead-letters.jsonl")
	if info, err := os.Stat(deadPath); err != nil || info.Size() > 512 {
		t.Fatalf("dead-letter file = (%v, %v), want bounded newest entry", info, err)
	}
}

func TestQueueRejectsFullAndInvalidIDs(t *testing.T) {
	if _, err := NewQueue(QueueConfig{Root: t.TempDir(), DrainID: "../escape"}); err == nil {
		t.Fatal("NewQueue accepted path traversal id")
	}
	queue, err := NewQueue(QueueConfig{Root: t.TempDir(), DrainID: "drain-1", MaxBytes: 1})
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	if err := queue.Enqueue(Record{Line: "record"}, time.Now().UTC()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("Enqueue full = %v, want ErrQueueFull", err)
	}
}

func TestQueueCompactsAcknowledgedPrefix(t *testing.T) {
	root := t.TempDir()
	queue, err := NewQueue(QueueConfig{Root: root, DrainID: "drain-1", MaxBytes: 4 << 20})
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	for sequence := uint64(1); sequence <= 300; sequence++ {
		if err := queue.Enqueue(Record{InstanceID: "instance-1", Sequence: sequence, Line: strings.Repeat("x", 8192)}, time.Now().UTC()); err != nil {
			t.Fatalf("Enqueue(%d): %v", sequence, err)
		}
	}
	initialInfo, err := os.Stat(queue.recordsPath)
	if err != nil {
		t.Fatalf("stat initial queue: %v", err)
	}
	compacted := false
	for sequence := 1; sequence <= 200; sequence++ {
		item, ok, err := queue.Next()
		if err != nil || !ok || item.Record.Sequence != uint64(sequence) {
			t.Fatalf("Next(%d) = (%+v, %v, %v)", sequence, item, ok, err)
		}
		if err := queue.Ack(item); err != nil {
			t.Fatalf("Ack(%d): %v", sequence, err)
		}
		if queue.state.Offset == 0 {
			compacted = true
			compactedInfo, err := os.Stat(queue.recordsPath)
			if err != nil {
				t.Fatalf("stat compacted queue: %v", err)
			}
			if compactedInfo.Size() >= initialInfo.Size() {
				t.Fatalf("compacted queue size = %d, initial size = %d", compactedInfo.Size(), initialInfo.Size())
			}
		}
	}
	if !compacted {
		t.Fatal("queue never compacted its acknowledged prefix")
	}
	resumed, err := NewQueue(QueueConfig{Root: root, DrainID: "drain-1", MaxBytes: 4 << 20})
	if err != nil {
		t.Fatalf("NewQueue after compaction: %v", err)
	}
	item, ok, err := resumed.Next()
	if err != nil || !ok || item.Record.Sequence != 201 {
		t.Fatalf("Next after compaction = (%+v, %v, %v), want sequence 201", item, ok, err)
	}
}
