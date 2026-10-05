package fcvm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Complete local memory writeback before returning a usable capture. moveOut's
// cross-filesystem copy closes without syncing; otherwise the first restore's
// bind-source permission sync also flushes the entire new memory image.
func publishLocalSnapshotMemory(source, destination string, syncFile func(string) error) (int64, error) {
	size, err := moveOut(source, destination)
	if err != nil {
		return 0, err
	}
	if err := syncFile(destination); err != nil {
		return 0, fmt.Errorf("sync published memory: %w", err)
	}
	return size, nil
}

func syncLocalSnapshotMemory(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("published snapshot memory must be a regular file")
	}
	if err == nil {
		err = f.Sync()
	}
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	return syncResourceParent(path)
}

func (v *JailerVMM) cleanupFailedSnapshotCapture(ctx context.Context, spec SnapshotSpec) {
	if v.storage == nil || !state.IsSnapshotCaptureKey(spec.StorageKey) {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	snap := state.Snapshot{StorageKey: spec.StorageKey}
	for _, key := range []string{spec.StorageKey, state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap)} {
		if key == "" {
			continue
		}
		if err := v.storage.Delete(cleanup, key); err != nil {
			slog.Default().Warn("vmm: remove failed snapshot capture", "key", key, "err", err)
		}
	}
}
