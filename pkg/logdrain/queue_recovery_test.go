package logdrain

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// docs/ops/log-drain-health.md: each drain owns a durable outbox and resumes
// at its first unacknowledged record after a restart or storage failure.
func TestQueueAcceptsProductionDrainIDs(t *testing.T) {
	for _, id := range []string{"00000000-0000-4000-8000-000000000001", "drain-0", "x-drain"} {
		t.Run(id, func(t *testing.T) {
			root := t.TempDir()
			q, err := NewQueue(QueueConfig{Root: root, DrainID: id})
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Dir(q.recordsPath) != filepath.Join(root, id) {
				t.Fatalf("queue escaped per-drain directory: %s", q.recordsPath)
			}
		})
	}
}

func TestQueueRejectsNonComponentDrainIDs(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../drain", "a/b", `a\b`, "a\x00b", strings.Repeat("a", maxQueueIDLen+1)} {
		t.Run(id, func(t *testing.T) {
			if _, err := NewQueue(QueueConfig{Root: t.TempDir(), DrainID: id}); err == nil {
				t.Fatalf("accepted invalid drain ID %q", id)
			}
		})
	}
}

func TestQueueCursorFailureKeepsHeadRetryable(t *testing.T) {
	for _, operation := range []string{"ack", "dead letter", "attempt"} {
		t.Run(operation, func(t *testing.T) {
			q, err := NewQueue(QueueConfig{Root: t.TempDir(), DrainID: "drain-1"})
			if err != nil {
				t.Fatal(err)
			}
			for sequence := uint64(1); sequence <= 2; sequence++ {
				if err := q.Enqueue(Record{InstanceID: "vm-1", Sequence: sequence, Line: "keep me"}, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			item, ok, err := q.Next()
			if err != nil || !ok {
				t.Fatalf("Next: %v, %v", ok, err)
			}
			cursorPath := q.cursorPath
			q.cursorPath = filepath.Join(t.TempDir(), "missing", "cursor.json")
			switch operation {
			case "ack":
				err = q.Ack(item)
			case "dead letter":
				err = q.DeadLetter(item, 3, errors.New("provider unavailable"))
			case "attempt":
				err = q.MarkAttempt(item, 1)
			}
			q.cursorPath = cursorPath
			if err == nil {
				t.Fatal("expected cursor write failure")
			}
			next, ok, err := q.Next()
			if err != nil || !ok || next.Record.Sequence != 1 || next.Attempts != 0 {
				t.Fatalf("failed cursor write changed head: %+v, %v, %v", next, ok, err)
			}
			if q.LastSequences()["vm-1"] != 0 || q.Stats().PendingRecords != 2 {
				t.Fatalf("failed cursor write changed source cursor or backlog: %v, %+v", q.LastSequences(), q.Stats())
			}
			if err := q.Ack(item); err != nil {
				t.Fatalf("retry after storage recovers: %v", err)
			}
			resumed, err := NewQueue(QueueConfig{Root: queueRoot(q), DrainID: "drain-1"})
			if err != nil {
				t.Fatal(err)
			}
			next, ok, err = resumed.Next()
			if err != nil || !ok || next.Record.Sequence != 2 || resumed.Stats().PendingRecords != 1 {
				t.Fatalf("recovered queue lost next record: %+v, %v, %v", next, ok, err)
			}
		})
	}
}

func TestQueueRecoversCompactionBeforeCursorCommit(t *testing.T) {
	q, err := NewQueue(QueueConfig{Root: t.TempDir(), DrainID: "drain-1"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1, 0).UTC()
	for sequence := uint64(1); sequence <= 2; sequence++ {
		if err := q.Enqueue(Record{InstanceID: "vm-1", Sequence: sequence, Line: "equal-sized"}, at); err != nil {
			t.Fatal(err)
		}
	}
	item, ok, err := q.Next()
	if err != nil || !ok {
		t.Fatalf("Next: %v, %v", ok, err)
	}
	if err := q.Ack(item); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(q.recordsPath)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after replacing records.jsonl with its unacknowledged
	// suffix but before resetting the persisted cursor. Here the old offset
	// equals the new file's size, so a size-only recovery test loses the tail.
	if err := os.Rename(q.recordsPath, q.recordsPath+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(q.recordsPath, data[item.nextOffset:], 0o600); err != nil {
		t.Fatal(err)
	}
	resumed, err := NewQueue(QueueConfig{Root: queueRoot(q), DrainID: "drain-1"})
	if err != nil {
		t.Fatal(err)
	}
	next, ok, err := resumed.Next()
	if err != nil || !ok || next.Record.Sequence != 2 || resumed.Stats().PendingRecords != 1 {
		t.Fatalf("compaction recovery lost unacknowledged record: %+v, %v, %v", next, ok, err)
	}
}
