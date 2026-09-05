package imaged

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"strings"
)

func (h *Handler) deleteSnapshotPair(ctx context.Context, snap state.Snapshot) {
	be, err := h.storageFor()
	if err != nil {
		h.log.Warn("imaged: snapshot cleanup backend", "err", err)
		return
	}
	for _, key := range []string{snap.StorageKey, state.SnapshotVMStateKey(snap)} {
		if err := be.Delete(ctx, key); err != nil {
			h.log.Warn("imaged: remove unused snapshot", "key", key, "err", err)
		}
	}
}

func (h *Handler) cleanupSnapshotCaptures(ctx context.Context, be storage.StorageBackend, depID string) {
	if inventory, ok := h.store.(state.SnapshotArtifactStore); ok {
		keys, err := inventory.SnapshotStorageKeys(ctx, depID)
		if err != nil {
			h.log.Warn("imaged: list recorded snapshot captures", "deployment", depID, "err", err)
		} else {
			for _, key := range keys {
				if state.IsSnapshotCaptureKey(key) {
					h.deleteSnapshotPair(ctx, state.Snapshot{StorageKey: key})
				}
			}
		}
	}
	lister, ok := be.(storage.LocalArtifactLister)
	if !ok {
		return
	}
	prefix := "snap/" + depID + "/"
	keys, err := lister.List(ctx, prefix)
	if err != nil {
		h.log.Warn("imaged: list snapshot captures", "deployment", depID, "err", err)
		return
	}
	for _, key := range keys {
		// The backend may return broader listings; constrain destructive cleanup
		// to this deployment's generation namespace.
		memKey := strings.TrimSuffix(key, "/vmstate")
		if memKey != key {
			memKey += "/mem"
		}
		if !strings.HasPrefix(key, prefix) || !state.IsSnapshotCaptureKey(memKey) {
			continue
		}
		if err := be.Delete(ctx, key); err != nil {
			h.log.Warn("imaged: remove snapshot capture", "key", key, "err", err)
		}
	}
}
