// adr: 631 — reclaim unowned layer clones and retry failed teardowns.
package main

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// layerCloneReapInterval and pendingCleanupRetryInterval pace vmmd's
// background clone reclamation and teardown retry (ADR-631).
const (
	layerCloneReapInterval      = 10 * time.Minute
	pendingCleanupRetryInterval = 2 * time.Minute
)

type instanceReader interface {
	InstanceByID(ctx context.Context, id string) (state.Instance, error)
}

// durableInstanceLive is the durable-state gate shared by every sweep: a row
// that is genuinely gone is not live; any other error is unknown and must not
// authorise resource removal.
func durableInstanceLive(store instanceReader) fcvm.LiveInstanceFunc {
	return func(ctx context.Context, instanceID string) (bool, error) {
		ins, err := store.InstanceByID(ctx, instanceID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		return state.IsLive(ins.State), nil
	}
}

// instanceOwnership is the Manager's in-process view (live, waking, or a
// retained failed teardown).
type instanceOwnership interface {
	HasInstanceOwnership(instance string) bool
}

// layerCloneOwnershipGate reports an instance as live whenever anything still
// owns it: this Manager, a resource-journal record (verified recovery owns
// those, ADR-477), or durable state. Only a clone that nothing owns may be
// reclaimed by its name.
func layerCloneOwnershipGate(durable fcvm.LiveInstanceFunc, mgr instanceOwnership, journal *fcvm.ResourceJournal) fcvm.LiveInstanceFunc {
	return func(ctx context.Context, instanceID string) (bool, error) {
		if mgr != nil && mgr.HasInstanceOwnership(instanceID) {
			return true, nil
		}
		owned, err := journal.Owns(instanceID)
		if err != nil {
			return false, err
		}
		if owned {
			return true, nil
		}
		return durable(ctx, instanceID)
	}
}

// layerCloneFlatDirs is where host-path drives live, and therefore where their
// clones are created (beside the source). The legacy kernel path names it.
func layerCloneFlatDirs(kernelPath string) []string {
	if kernelPath == "" {
		return nil
	}
	if dir := filepath.Dir(kernelPath); dir != "." && dir != "/" {
		return []string{dir}
	}
	return nil
}

func reapLayerClones(ctx context.Context, log *slog.Logger, root string, flatDirs []string, isLive fcvm.LiveInstanceFunc) {
	rep, err := fcvm.ReapOrphanedLayerClones(ctx, fcvm.LayerCloneReapOptions{
		Root:     root,
		FlatDirs: flatDirs,
		IsLive:   isLive,
		Log:      log,
	})
	if err != nil {
		log.Warn("vmmd: orphan layer clone reap failed", "err", err)
		return
	}
	if rep.Scanned > 0 {
		log.Info("vmmd: orphan layer clone reap complete",
			"scanned", rep.Scanned, "reaped", rep.Reaped,
			"reclaimed_logical_bytes", rep.ReclaimedLogicalBytes,
			"skipped_live", rep.SkippedLive,
			"skipped_young", rep.SkippedYoung,
			"skipped_unknown", rep.SkippedUnknown,
			"failed", rep.Failed)
	}
}

type pendingCleanupRetrier interface {
	RetryPendingCleanups(ctx context.Context) (completed, pending int)
}

// runLayerCloneMaintenance reclaims unowned clones on startup and every
// layerCloneReapInterval, and retries retained teardowns every
// pendingCleanupRetryInterval, until ctx ends. An empty root disables the
// clone sweep (no node-local cache); the retry runs regardless.
func runLayerCloneMaintenance(ctx context.Context, log *slog.Logger, root string, flatDirs []string, isLive fcvm.LiveInstanceFunc, retrier pendingCleanupRetrier) {
	sweep := func() {
		if root != "" || len(flatDirs) > 0 {
			reapLayerClones(ctx, log, root, flatDirs, isLive)
		}
	}
	sweep()
	reap := time.NewTicker(layerCloneReapInterval)
	defer reap.Stop()
	retry := time.NewTicker(pendingCleanupRetryInterval)
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-reap.C:
			sweep()
		case <-retry.C:
			if completed, pending := retrier.RetryPendingCleanups(ctx); completed > 0 || pending > 0 {
				log.Info("vmmd: pending teardown retry", "completed", completed, "still_pending", pending)
			}
		}
	}
}
