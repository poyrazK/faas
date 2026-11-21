package imaged

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestExecutionTemplateGCProtectsCatalogAndReclaimsOnlyOldOrphans(t *testing.T) {
	for _, retired := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "retired"}[retired], func(t *testing.T) {
			root := t.TempDir()
			store := state.NewMemStore()
			now := time.Now().UTC()
			old := now.Add(-48 * time.Hour)
			key := state.SnapshotCaptureMemKey(uuid.Nil.String(), "", uuid.NewString())
			record := state.RuntimeSnapshotRecord{FCVersion: "1.7.0", Runtime: api.ExecutionRuntimeNode24, Architecture: "amd64", KernelDigest: strings.Repeat("a", 64), GuestExecutorDigest: strings.Repeat("b", 64), BaseImageDigest: strings.Repeat("c", 64), MemoryMB: 128, EphemeralDiskMB: 64, FormatVersion: 2, StorageKey: key, SnapshotDigest: strings.Repeat("d", 64), MemBytes: 128 << 20, VMStateBytes: 5, Sanitized: true, PayloadFree: true, State: state.RuntimeSnapshotStateReady, CreatedAt: old}
			record.CatalogKey = "execution-snapshots/v2/node24/amd64/firecracker-1.7.0/memory-128/disk-64/kernel-" + record.KernelDigest + "/executor-" + record.GuestExecutorDigest + "/base-" + record.BaseImageDigest
			if _, err := store.PublishRuntimeSnapshot(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			if retired {
				if err := store.RetireRuntimeSnapshot(t.Context(), record.CatalogKey, now); err != nil {
					t.Fatal(err)
				}
			}
			orphan := state.SnapshotCaptureMemKey(uuid.Nil.String(), "", uuid.NewString())
			fresh := state.SnapshotCaptureMemKey(uuid.Nil.String(), "", uuid.NewString())
			for _, mem := range []string{key, orphan, fresh} {
				snap := state.Snapshot{StorageKey: mem}
				for _, key := range []string{mem, state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)} {
					path := filepath.Join(root, key)
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("capture"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if mem != fresh {
					dir := filepath.Dir(filepath.Dir(filepath.Join(root, mem)))
					if err := os.Chtimes(dir, old, old); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Exercise the complete ordinary deployment sweep too, so it cannot
			// delete the platform directory before its catalog-aware collector.
			loop := &Loop{store: store, handler: &Handler{}, storageRoot: root, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			loop.removeLocalSnapshotOrphans(context.Background(), now)
			for _, mem := range []string{key, fresh} {
				if _, err := os.Stat(filepath.Join(root, mem)); err != nil {
					t.Fatal("retained template or recent capture lost", err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, orphan)); !os.IsNotExist(err) {
				t.Fatalf("old unreferenced capture not removed: %v", err)
			}
		})
	}
}
