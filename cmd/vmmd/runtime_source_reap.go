package main

// adr: 435. Sealed sources remain node-local and outside ephemeral jail roots.

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func vmmdRuntimeSourceRoot(backend storage.StorageBackend) string {
	root := envOr("FAAS_STORAGE_ROOT", "/srv/fc")
	if cache := storage.AsCacheBackend(backend); cache != nil {
		root = cache.Root()
	}
	return filepath.Join(root, ".vmmd-runtime-sources")
}

func vmmdRuntimeSourceSweep(store state.Store, root string, log *slog.Logger) func(context.Context) {
	if store == nil {
		return nil
	}
	isLive := vmmdRuntimeSourceLiveness(store)
	return func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		rep, err := fcvm.ReapOrphanedRuntimeSources(ctx, fcvm.RuntimeSourceReapOptions{Root: root, IsLive: isLive})
		if err != nil {
			log.Warn("vmmd: orphan runtime source sweep failed", "err", err)
		}
		if rep.Reaped > 0 || rep.Failed > 0 || rep.SkippedUnknown > 0 {
			log.Info("vmmd: orphan runtime source sweep", "scanned", rep.Scanned, "reaped", rep.Reaped,
				"reclaimed_logical_bytes", rep.ReclaimedLogicalBytes, "failed", rep.Failed,
				"skipped_live", rep.SkippedLive, "skipped_unknown", rep.SkippedUnknown)
		}
	}
}

func vmmdRuntimeSourceLiveness(store state.Store) fcvm.LiveInstanceFunc {
	return func(ctx context.Context, id string) (bool, error) {
		instance, err := store.InstanceByID(ctx, id)
		if errors.Is(err, state.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !state.State(instance.State).Valid() {
			return false, errors.New("vmmd: unknown durable instance state")
		}
		return state.IsLive(instance.State), nil
	}
}
