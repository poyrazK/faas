package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

type safeReleaseReadiness interface {
	SafeReleaseReadiness(time.Time) meter.HealthStatus
}

type safeReleaseControlProbe interface {
	ProbeSafeRelease(context.Context) error
}

const (
	safeReleaseLeaseInterval = 10 * time.Second
	safeReleaseLeaseTTL      = 30 * time.Second
)

func safeReleaseWorkerLeaseOnce(ctx context.Context, loop safeReleaseReadiness, store state.SafeReleaseWorkerLeaseStore, probe safeReleaseControlProbe, now time.Time) (bool, error) {
	if !loop.SafeReleaseReadiness(now).Healthy {
		return false, nil
	}
	if probe == nil {
		return false, errors.New("safe release operator probe not configured")
	}
	if err := probe.ProbeSafeRelease(ctx); err != nil {
		return false, fmt.Errorf("probe APID safe release controls: %w", err)
	}
	if err := store.StampSafeReleaseWorkerLease(ctx, safeReleaseLeaseTTL); err != nil {
		return false, err
	}
	return true, nil
}

func safeReleaseWorkerLeaseLoop(ctx context.Context, loop *meter.Loop, store state.SafeReleaseWorkerLeaseStore, probe safeReleaseControlProbe, log *slog.Logger) {
	ticker := time.NewTicker(safeReleaseLeaseInterval)
	defer ticker.Stop()
	for {
		if _, err := safeReleaseWorkerLeaseOnce(ctx, loop, store, probe, time.Now()); err != nil {
			log.Warn("safe release worker lease renewal failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
