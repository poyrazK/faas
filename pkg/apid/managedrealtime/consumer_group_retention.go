package managedrealtime

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"log/slog"
	"time"
)

func (s *Service) RunRealtimeConsumerGroupDeadRetention(ctx context.Context) {
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupRetentionStore)
	if !ok || !s.HistoryPreviewEnabled {
		return
	}
	log := s.Host.Logger()
	if log == nil {
		log = slog.Default()
	}
	Run := func() {
		pass, cancel := context.WithTimeout(ctx, time.Duration(api.RealtimeConsumerGroupDeadRetentionPassSeconds)*time.Second)
		defer cancel()
		if _, err := store.ExpireRealtimeConsumerGroupDeadLetters(pass); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("realtime dead-letter retention pass failed", "err", err)
		}
	}
	Run()
	tick := time.NewTicker(time.Duration(api.RealtimeConsumerGroupDeadRetentionScanSeconds) * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			Run()
		}
	}
}
