package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestVMMDLogArchiveSink_EnqueueDrainsToSpool(t *testing.T) {
	root := t.TempDir()
	spool := logarchive.NewSpool(filepath.Join(root, "archive"), 1<<20)
	sink := newVMMDLogArchiveSink(spool, logarchive.NewMetrics(nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	sink.Enqueue("instance-1", logbuf.Line{
		Seq:       7,
		Stream:    "stderr",
		Line:      "hello from vmmd",
		WrittenAt: time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC),
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := sink.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	files := spool.FilesSnapshot()
	if len(files) != 1 {
		t.Fatalf("spool files = %d, want 1", len(files))
	}
	body, err := os.ReadFile(files[0].Path)
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	if !strings.Contains(string(body), `"seq":7`) || !strings.Contains(string(body), "hello from vmmd") {
		t.Fatalf("spool body = %s", body)
	}
}

// Prod hunt #3: the archive only received ring evictions, so a parked
// instance whose logs fit in the ring had nothing to read with
// `gregale logs --archive`. Retired lines must reach the spool after any
// evictions queued before them.
func TestVMMDLogArchiveSink_RetireSpoolsRetainedLinesInOrder(t *testing.T) {
	root := t.TempDir()
	spool := logarchive.NewSpool(filepath.Join(root, "archive"), 1<<20)
	sink := newVMMDLogArchiveSink(spool, logarchive.NewMetrics(nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	at := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	sink.Enqueue("instance-1", logbuf.Line{Seq: 1, Stream: "stdout", Line: "evicted", WrittenAt: at})
	sink.Retire("instance-1", []logbuf.Line{
		{Seq: 2, Stream: "stdout", Line: "retained one", WrittenAt: at},
		{Seq: 3, Stream: "stderr", Line: "retained two", WrittenAt: at},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := sink.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	files := spool.FilesSnapshot()
	if len(files) != 1 {
		t.Fatalf("spool files = %d, want 1", len(files))
	}
	body, err := os.ReadFile(files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	first, second, third := strings.Index(got, "evicted"), strings.Index(got, "retained one"), strings.Index(got, "retained two")
	if first < 0 || second < 0 || third < 0 || first > second || second > third {
		t.Fatalf("spool body out of order or incomplete:\n%s", got)
	}
	if sink.queuedBytes != 0 {
		t.Fatalf("queuedBytes = %d after drain, want 0", sink.queuedBytes)
	}
}

func retiredTestLines(at time.Time, from, n int) []logbuf.Line {
	lines := make([]logbuf.Line, 0, n)
	for i := 0; i < n; i++ {
		lines = append(lines, logbuf.Line{Seq: int64(from + i), Stream: "stdout", Line: fmt.Sprintf("retired %d", from+i), WrittenAt: at})
	}
	return lines
}

func spooledBody(t *testing.T, spool *logarchive.Spool) string {
	t.Helper()
	var all strings.Builder
	for _, f := range spool.FilesSnapshot() {
		body, err := os.ReadFile(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(body)
	}
	return all.String()
}

// Logs/traces audit 2026-10-31 A12: during a mass teardown the queue's
// byte cap was reached and a retiring ring's whole batch was dropped. Retire
// now waits for the worker to free room and queues the batch in order.
func TestVMMDLogArchiveSink_RetireWaitsForQueueRoom(t *testing.T) {
	spool := logarchive.NewSpool(filepath.Join(t.TempDir(), "archive"), 1<<20)
	sink := newVMMDLogArchiveSink(spool, logarchive.NewMetrics(nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	sink.retireWait = time.Minute
	// Stand in for queued work the worker has not reached yet.
	sink.mu.Lock()
	sink.queuedBytes = sink.maxQueuedBytes
	sink.mu.Unlock()
	at := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	retired := make(chan struct{})
	go func() {
		sink.Retire("instance-1", retiredTestLines(at, 1, 3))
		close(retired)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		sink.mu.Lock()
		waiting := sink.waiters
		sink.mu.Unlock()
		if waiting == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Retire did not wait for queue room")
		}
		time.Sleep(time.Millisecond)
	}
	sink.release(sink.maxQueuedBytes)
	select {
	case <-retired:
	case <-time.After(5 * time.Second):
		t.Fatal("Retire still waiting after the worker freed room")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sink.Close(ctx); err != nil {
		t.Fatal(err)
	}
	got := spooledBody(t, spool)
	for i := 1; i <= 3; i++ {
		if !strings.Contains(got, fmt.Sprintf("retired %d", i)) {
			t.Fatalf("retired line %d missing from the spool:\n%s", i, got)
		}
	}
	if dropped := testutil.ToFloat64(sink.dropped); dropped != 0 {
		t.Fatalf("dropped = %v, want 0", dropped)
	}
}

// A12: when the queue stays full past retireWait, or the batch is larger
// than the whole queue, the retired lines go straight to the spool; only
// what the spool refuses is dropped, and every dropped line is counted.
func TestVMMDLogArchiveSink_RetireSpillsWhenQueueStaysFull(t *testing.T) {
	at := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		spoolMax  int64
		pinFull   bool
		maxQueued int64
		dropped   float64
	}{
		{name: "queue stays full", spoolMax: 1 << 20, pinFull: true, maxQueued: vmmdLogArchiveQueueBytes},
		{name: "batch larger than the queue", spoolMax: 1 << 20, maxQueued: 8},
		{name: "spool refuses", spoolMax: 64, pinFull: true, maxQueued: vmmdLogArchiveQueueBytes, dropped: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spool := logarchive.NewSpool(filepath.Join(t.TempDir(), "archive"), tc.spoolMax)
			sink := newVMMDLogArchiveSink(spool, logarchive.NewMetrics(nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
			sink.retireWait = 20 * time.Millisecond
			sink.maxQueuedBytes = tc.maxQueued
			if tc.pinFull {
				sink.mu.Lock()
				sink.queuedBytes = sink.maxQueuedBytes
				sink.mu.Unlock()
			}
			sink.Retire("instance-1", retiredTestLines(at, 1, 3))
			if dropped := testutil.ToFloat64(sink.dropped); dropped != tc.dropped {
				t.Fatalf("dropped = %v, want %v", dropped, tc.dropped)
			}
			if tc.pinFull {
				sink.release(sink.maxQueuedBytes)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := sink.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if got := spooledBody(t, spool); tc.dropped == 0 && strings.Count(got, "retired ") != 3 {
				t.Fatalf("spool holds %d retired lines, want 3:\n%s", strings.Count(got, "retired "), got)
			}
		})
	}
}

// A ring retired after the archive closed cannot be spooled any more; its
// lines are counted as dropped instead of vanishing silently.
func TestVMMDLogArchiveSink_RetireAfterCloseCountsDroppedLines(t *testing.T) {
	spool := logarchive.NewSpool(filepath.Join(t.TempDir(), "archive"), 1<<20)
	sink := newVMMDLogArchiveSink(spool, logarchive.NewMetrics(nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sink.Close(ctx); err != nil {
		t.Fatal(err)
	}
	sink.Retire("instance-1", retiredTestLines(time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC), 1, 2))
	if dropped := testutil.ToFloat64(sink.dropped); dropped != 2 {
		t.Fatalf("dropped = %v, want 2", dropped)
	}
}
