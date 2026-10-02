package main

import (
	"context"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
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

func TestVMMDArchiveSinkRequiresCapturedOwner(t *testing.T) {
	store := state.NewMemStore()
	fx := conformance.Seed(t, store)
	instance, err := store.CreateInstance(t.Context(), fx.App.ID, fx.Deployment.ID, string(state.StateRunning), 128, fx.Node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	spool := logarchive.NewSpool(t.TempDir(), 1<<20)
	sink := newVMMDLogArchiveSink(spool, nil, nil)
	sink.catalog, sink.nodeID, sink.bindings = store, fx.Node.ID, make(map[string]state.RuntimeLogArchiveBinding)
	line := logbuf.Line{Seq: 1, Stream: "stdout", WrittenAt: time.Now(), Line: "owned"}
	sink.Enqueue(instance.ID, line)
	wr := fcvm.WakeRequest{Instance: instance.ID, AppID: fx.App.ID, AccountID: fx.Account.ID, DeploymentID: fx.Deployment.ID}
	if err := sink.Capture(t.Context(), wr); err != nil {
		t.Fatal(err)
	}
	sink.Enqueue(instance.ID, line)
	wr.AccountID = "other"
	if err := sink.Capture(t.Context(), wr); err == nil {
		t.Fatal("mismatched RPC owner accepted")
	}
	line.Seq = 2
	line.Line = "unowned"
	sink.Enqueue(instance.ID, line)
	if err := sink.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	files := spool.FilesSnapshot()
	if len(files) != 1 {
		t.Fatalf("files=%+v", files)
	}
	raw, err := os.ReadFile(files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"msg":"owned"`) != 1 || strings.Contains(string(raw), "unowned") {
		t.Fatalf("uncaptured lines spooled: %s", raw)
	}
}
