package builderd

import (
	"context"
	"log/slog"
	"time"
)

const (
	defaultWarmBuilderSweepInterval = time.Minute
	minWarmBuilderSweepInterval     = time.Second
)

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
	snapshot, expired := b.ExpireWarmBuilder(now)
	if !expired {
		return false, nil
	}
	warmVM, ok := b.vm.(WarmVM)
	if !ok {
		return true, nil
	}
	return true, b.cleanupWarmSnapshot(ctx, warmVM, snapshot)
}

// CleanupWarmBuilder releases a retained warm snapshot during daemon
// shutdown. It runs after active build processing has drained, so a live
// builder cannot race deletion of its snapshot or retained drive.
func (b *Builderd) CleanupWarmBuilder(ctx context.Context) error {
	if b == nil {
		return nil
	}
	snapshot, retained := b.InvalidateWarmBuilder()
	if !retained {
		return nil
	}
	warmVM, ok := b.vm.(WarmVM)
	if !ok {
		return nil
	}
	return b.cleanupWarmSnapshot(ctx, warmVM, snapshot)
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
