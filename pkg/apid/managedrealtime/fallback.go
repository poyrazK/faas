package managedrealtime

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) RunManagedRealtimeFallbacks(ctx context.Context) {
	store, ok := s.Host.Store().(state.ManagedRealtimeNotificationStore)
	if !ok {
		return
	}
	log := s.Host.Logger()
	if log == nil {
		log = slog.Default()
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	run := func() {
		passCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if _, err := store.DrainManagedRealtimeFallbacks(passCtx, 128); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("managed realtime notification fallback pass failed", "err", err)
		}
	}
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			run()
		}
	}
}
