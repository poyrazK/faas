package main

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"log/slog"
	"time"
)

func (s *server) runRealtimeReplayJobs(ctx context.Context) {
	store, ok := s.store.(state.RealtimeReplayJobStore)
	if !ok || !s.realtimeHistoryPreviewEnabled {
		return
	}
	log := s.log
	if log == nil {
		log = slog.Default()
	}
	tick := time.NewTicker(time.Duration(api.RealtimeReplayJobScanMilliseconds) * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			pass, cancel := context.WithTimeout(ctx, time.Duration(api.RealtimeReplayJobPassSeconds)*time.Second)
			err := store.RunRealtimeReplayJobs(pass)
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Warn("realtime replay job pass failed", "err", err)
			}
		}
	}
}
