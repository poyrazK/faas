package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// revisionPinSweepLoop retires zero-traffic deployments once their client
// compatibility window closes. Resolution also checks the deadline directly,
// so a delayed sweep can never serve an expired pin.
func revisionPinSweepLoop(ctx context.Context, store state.RevisionPinStore, log *slog.Logger) {
	if store == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		count, err := store.ExpireRevisionPins(ctx)
		if err != nil && ctx.Err() == nil {
			log.Warn("meterd: expire revision pins", "err", err)
		} else if count > 0 {
			log.Info("meterd: expired revision pins", "deployments", count)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
