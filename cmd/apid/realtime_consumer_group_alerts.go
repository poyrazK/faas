package main

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"log/slog"
	"time"
)

func (s *server) runRealtimeConsumerGroupAlerts(ctx context.Context) {
	store, ok := s.store.(state.RealtimeConsumerGroupAlertStore)
	if !ok || !s.realtimeHistoryPreviewEnabled {
		return
	}
	log := s.log
	if log == nil {
		log = slog.Default()
	}
	run := func() {
		pass, cancel := context.WithTimeout(ctx, time.Duration(api.RealtimeConsumerGroupAlertPassSeconds)*time.Second)
		defer cancel()
		if _, err := store.EvaluateRealtimeConsumerGroupAlerts(pass); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("realtime consumer group lag alert scan failed", "err", err)
		}
	}
	run()
	tick := time.NewTicker(time.Duration(api.RealtimeConsumerGroupAlertScanSeconds) * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			run()
		}
	}
}
