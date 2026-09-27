package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	safeReleaseLeaseInterval = 10 * time.Second
	safeReleaseLeaseTTL      = 30 * time.Second
)

func safeReleaseWorkerLeaseOnce(ctx context.Context, loop *meter.Loop, store state.SafeReleaseWorkerLeaseStore, now time.Time) (bool, error) {
	if !loop.SafeReleaseReadiness(now).Healthy {
		return false, nil
	}
	if err := store.StampSafeReleaseWorkerLease(ctx, safeReleaseLeaseTTL); err != nil {
		return false, err
	}
	return true, nil
}

func safeReleaseWorkerLeaseLoop(ctx context.Context, loop *meter.Loop, store state.SafeReleaseWorkerLeaseStore, log *slog.Logger) {
	ticker := time.NewTicker(safeReleaseLeaseInterval)
	defer ticker.Stop()
	for {
		if _, err := safeReleaseWorkerLeaseOnce(ctx, loop, store, time.Now()); err != nil {
			log.Warn("safe release worker lease renewal failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
