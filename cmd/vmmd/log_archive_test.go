package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/logarchive"
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
