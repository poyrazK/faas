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
