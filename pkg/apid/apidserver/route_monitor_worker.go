package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routejobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) runRouteMonitorWorker(ctx context.Context) {
	ticker := time.NewTicker(api.RouteMonitorPollInterval)
	defer ticker.Stop()
	for {
		if _, err := s.drainRouteMonitors(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("production route monitor queue unavailable")
			s.routeJobMetrics.QueueUnavailable(routejobs.WorkerMonitor)
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
	return routejobs.MonitorDrain{
		Queue: worker, AfterEvaluate: s.applyRouteMonitorRollback, Metrics: s.routeJobMetrics, Log: s.log,
		Released: func(ctx context.Context, target state.RouteMonitorTarget) bool {
			return s.routeCapabilityReleased(ctx, routeMonitorCapability, target.AccountID, target.AppID)
		},
	}.Run(ctx)
}
