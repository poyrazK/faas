package builderd

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	defaultWarmBuilderSweepInterval = time.Minute
	minWarmBuilderSweepInterval     = time.Second
)

type warmSnapshotCleanupKey struct {
	storageKey        string
	vmStateStorageKey string
	vmStatePath       string
	layerPath         string
}

func warmSnapshotCleanupKeyFor(snapshot WarmSnapshot) warmSnapshotCleanupKey {
	return warmSnapshotCleanupKey{
		storageKey:        snapshot.StorageKey,
		vmStateStorageKey: snapshot.VMStateStorageKey,
		vmStatePath:       snapshot.VMStatePath,
		layerPath:         snapshot.LayerPath,
	}
}

func (b *Builderd) enqueueWarmSnapshotCleanup(snapshot WarmSnapshot) {
	if b == nil || snapshot.StorageKey == "" {
		return
	}
	b.warmCleanupMu.Lock()
	defer b.warmCleanupMu.Unlock()
	if b.warmCleanupPending == nil {
		b.warmCleanupPending = make(map[warmSnapshotCleanupKey]WarmSnapshot)
	}
	b.warmCleanupPending[warmSnapshotCleanupKeyFor(snapshot)] = snapshot
}

func (b *Builderd) removeWarmSnapshotCleanup(snapshot WarmSnapshot) {
	if b == nil {
		return
	}
	b.warmCleanupMu.Lock()
	defer b.warmCleanupMu.Unlock()
	delete(b.warmCleanupPending, warmSnapshotCleanupKeyFor(snapshot))
}

// WarmBuilderSweepInterval returns a bounded cadence that notices an idle
// warm snapshot before it has been unused for another full idle window.
// Keeping the cadence derived from WarmIdle avoids another operator knob while
// preventing very short test or development windows from creating a hot loop.
func WarmBuilderSweepInterval(idle time.Duration) time.Duration {
	if idle <= 0 {
		idle = DefaultWarmIdle
	}
	interval := idle / 2
	if interval < minWarmBuilderSweepInterval {
		return minWarmBuilderSweepInterval
	}
	if interval > defaultWarmBuilderSweepInterval {
		return defaultWarmBuilderSweepInterval
	}
	return interval
}

// SweepExpiredWarmBuilder removes an idle retained snapshot from the
// lifecycle and its vmmd/local backing stores. The lifecycle is evicted before
// cleanup so no new build can restore a snapshot that has reached its idle
// deadline.
func (b *Builderd) SweepExpiredWarmBuilder(ctx context.Context, now time.Time) (bool, error) {
	if b == nil {
		return false, nil
	}
	b.warmOpMu.Lock()
	defer b.warmOpMu.Unlock()
	// A retained or running snapshot may be a newer generation that reuses
	// the same build-derived storage key. Wait until the warm slot is cold
	// before retrying old cleanup obligations so a retry cannot delete state
	// that an active restore or a newly captured snapshot still needs.
	var cleanupErr error
	if b.warm.State() == WarmCold {
		cleanupErr = b.retryWarmSnapshotCleanupLocked(ctx)
	}
	snapshot, expired := b.warm.ExpireSnapshot(now)
	if !expired {
		return false, cleanupErr
	}
	warmVM, ok := b.vm.(WarmVM)
	if !ok {
		return true, cleanupErr
	}
	return true, errors.Join(cleanupErr, b.cleanupWarmSnapshotLocked(ctx, warmVM, snapshot))
}

// CleanupWarmBuilder releases a retained warm snapshot during daemon
// shutdown. It runs after active build processing has drained, so a live
// builder cannot race deletion of its snapshot or retained drive.
func (b *Builderd) CleanupWarmBuilder(ctx context.Context) error {
	if b == nil {
		return nil
	}
	b.warmOpMu.Lock()
	defer b.warmOpMu.Unlock()
	var cleanupErr error
	snapshot, retained := b.warm.InvalidateSnapshot()
	warmVM, ok := b.vm.(WarmVM)
	if !ok {
		return nil
	}
	if retained {
		cleanupErr = b.cleanupWarmSnapshotLocked(ctx, warmVM, snapshot)
	}
	return errors.Join(cleanupErr, b.retryWarmSnapshotCleanupLocked(ctx))
}

// retryWarmSnapshotCleanupLocked retries cleanup failures that happened after
// the lifecycle had already forgotten a snapshot. The queue is deliberately
// in-memory: the warm snapshot itself is an optimization, and a later
// process restart cannot safely infer ownership from arbitrary storage keys.
// Cleanup is retried only while the warm slot is cold so an old failed delete
// cannot race a restore or a newer capture. The caller holds warmOpMu.
func (b *Builderd) retryWarmSnapshotCleanupLocked(ctx context.Context) error {
	if b.warm.State() != WarmCold {
		return nil
	}
	warmVM, ok := b.vm.(WarmVM)
	if !ok {
		return nil
	}

	b.warmCleanupMu.Lock()
	pending := make([]WarmSnapshot, 0, len(b.warmCleanupPending))
	for _, snapshot := range b.warmCleanupPending {
		pending = append(pending, snapshot)
	}
	b.warmCleanupMu.Unlock()

	var errs []error
	for _, snapshot := range pending {
		if err := b.cleanupWarmSnapshotLocked(ctx, warmVM, snapshot); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// WarmBuilderSweepLoop evicts retained builder state that has exceeded the
// configured idle window. The shutdown path also calls CleanupWarmBuilder so
// the final snapshot is released even when no periodic tick is due.
func WarmBuilderSweepLoop(ctx context.Context, b *Builderd, interval time.Duration, log *slog.Logger) {
	if b == nil {
		return
	}
	if interval <= 0 {
		interval = WarmBuilderSweepInterval(b.cfg.WarmIdle)
	}
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			expired, err := b.SweepExpiredWarmBuilder(ctx, now)
			if err != nil {
				log.Warn("builderd: warm snapshot sweep", "err", err)
				continue
			}
			if expired {
				log.Info("builderd: expired warm builder snapshot")
			}
		}
	}
}
