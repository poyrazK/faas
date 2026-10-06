package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) runRouteMonitorWorker(ctx context.Context) {
	ticker := time.NewTicker(api.RouteMonitorPollInterval)
	defer ticker.Stop()
	for {
		if _, err := s.drainRouteMonitors(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("production route monitor queue unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *server) drainRouteMonitors(ctx context.Context) (int, error) {
	worker, ok := s.store.(state.RouteMonitorWorkerStore)
	if !ok {
		return 0, nil
	}
	listCtx, cancel := context.WithTimeout(ctx, api.RouteCheckTimeout)
	targets, err := worker.ListDueRouteMonitors(listCtx)
	cancel()
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, target := range targets {
		if ctx.Err() != nil {
			return completed, ctx.Err()
		}
		checkCtx, cancel := context.WithTimeout(ctx, api.RouteCheckTimeout)
		done, err := worker.EvaluateRouteMonitor(checkCtx, target.AccountID, target.AppID)
		cancel()
		if err != nil && ctx.Err() == nil {
			s.log.Warn("production route monitor evaluation will retry", "app_id", target.AppID)
			retryCtx, retryCancel := context.WithTimeout(ctx, api.RouteCheckTimeout)
			_ = worker.DeferRouteMonitor(retryCtx, target)
			retryCancel()
		}
		if done {
			completed++
		}
	}
	return completed, nil
}
