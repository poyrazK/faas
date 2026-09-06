package fcvm

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"strings"
	"testing"
)

func TestFailedCaptureCleanupSurvivesCancellationAndPreservesLegacy(t *testing.T) {
	for _, generated := range []bool{false, true} {
		key := state.SnapMemKey("dep")
		if generated {
			key = state.SnapshotCaptureMemKey("dep", "init", "failed")
		}
		t.Run(key, func(t *testing.T) {
			be, err := storage.NewLocalStorageBackend(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			pair := []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})}
			for _, part := range pair {
				if err := be.Put(context.Background(), part, strings.NewReader("snapshot")); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			v := NewJailerVMM(t.TempDir(), 0).WithStorage(be)
			v.cleanupFailedSnapshotCapture(ctx, SnapshotSpec{StorageKey: key})
			for _, part := range pair {
				rc, err := be.Get(context.Background(), part)
				if rc != nil {
					_ = rc.Close()
				}
				if generated && !storage.IsNotFound(err) {
					t.Fatalf("failed capture left behind: %s %v", part, err)
				}
				if !generated && err != nil {
					t.Fatalf("legacy capture removed: %v", err)
				}
			}
		})
	}
}
