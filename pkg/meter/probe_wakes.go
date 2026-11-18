package meter

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// platformProbeUnbilledSeconds reads, for one minute, each instance's
// seconds inside a wake a baseline health probe alone caused
// (ADR-20261116120000 §3): from the wake's start to the probe's end plus
// api.ProtectionProbeUnbilledTail, which covers schedd's park grace, one
// reaper tick and the park. A store without the ledger bills as before.
func platformProbeUnbilledSeconds(ctx context.Context, store any, minute time.Time) (map[string]int64, error) {
	ledger, ok := store.(state.PlatformProbeWakeStore)
	if !ok {
		return nil, nil
	}
	seconds, err := ledger.PlatformProbeUnbilledSeconds(ctx, minute, minute.Add(time.Minute), api.ProtectionProbeUnbilledTail)
	if err != nil {
		return nil, fmt.Errorf("meter: load platform probe wakes: %w", err)
	}
	return seconds, nil
}

// PlatformProbeWakeRetentionLoop runs PlatformProbeWakeRetentionOnce every
// interval until ctx is done.
func PlatformProbeWakeRetentionLoop(ctx context.Context, ledger state.PlatformProbeWakeStore, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = DefaultRetentionInterval
	}
	if log == nil {
		log = slog.Default()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if n, err := PlatformProbeWakeRetentionOnce(ctx, ledger, now); err != nil {
				log.Warn("platform probe wake retention tick", "rows_deleted", n, "err", err)
			} else if n > 0 {
				log.Info("platform probe wake retention tick ok", "rows_deleted", n)
			}
		}
	}
}

// PlatformProbeWakeRetentionOnce deletes probe wakes older than
// api.PlatformProbeWakeRetention, at most MaxRetentionBatches batches.
func PlatformProbeWakeRetentionOnce(ctx context.Context, ledger state.PlatformProbeWakeStore, now time.Time) (int64, error) {
	cutoff := now.Add(-api.PlatformProbeWakeRetention)
	var total int64
	for range MaxRetentionBatches {
		n, err := ledger.DeletePlatformProbeWakesBefore(ctx, cutoff, api.PlatformProbeWakeDeleteBatch)
		total += n
		if err != nil {
			return total, fmt.Errorf("platform probe wake retention (deleted so far %d): %w", total, err)
		}
		if n < api.PlatformProbeWakeDeleteBatch {
			return total, nil
		}
	}
	return total, nil
}
