package fcvm

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"log/slog"
	"time"
)

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
