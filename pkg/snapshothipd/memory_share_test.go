package snapshothipd

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// A job is queued once while pending, the pass gets the job's memory and
// layer keys, and a disabled runner never queues.
func TestMemorySharingQueuesEachSnapshotOnce(t *testing.T) {
	calls := make(chan []string, 4)
	orig := shareSnapshotMemory
	shareSnapshotMemory = func(_ context.Context, _ *storage.MemoryShareIndex, _ storage.StorageBackend, memKey string, layerKeys []string) (storage.MemoryShareResult, error) {
		calls <- append([]string{memKey}, layerKeys...)
		return storage.MemoryShareResult{SharedBytes: 4096}, nil
	}
	t.Cleanup(func() { shareSnapshotMemory = orig })

	r := (&Runner{log: slog.New(slog.NewTextHandler(io.Discard, nil))}).WithMemorySharing(true)
	job := state.SnapshotReplicaJob{SnapshotID: "s1", StorageKey: "snap/d/captures/c/v2/mem", LayerStorageKeys: []string{"apps/a/d.ext4"}}
	r.offerMemoryShare(job)
	r.offerMemoryShare(job) // still pending: not queued twice
	if n := len(r.memory.jobs); n != 1 {
		t.Fatalf("queued %d jobs, want 1", n)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go r.runMemorySharing(ctx)
	select {
	case got := <-calls:
		if len(got) != 2 || got[0] != job.StorageKey || got[1] != "apps/a/d.ext4" {
			t.Fatalf("pass got %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("memory sharing pass did not run")
	}

	off := (&Runner{}).WithMemorySharing(false)
	off.offerMemoryShare(job)
	if off.memory != nil {
		t.Fatal("disabled runner has a memory sharer")
	}
}
