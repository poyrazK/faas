package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RouteAdviceRouteStats, RouteAdviceConsumers and RouteAdviceThrottleExcess
// read retained debugger telemetry for the route advisor (ADR-955).
func (s *PgStore) RouteAdviceRouteStats(ctx context.Context, arg sqlc.RouteAdviceRouteStatsParams) ([]sqlc.RouteAdviceRouteStatsRow, error) {
	return s.appErrorsQueries().RouteAdviceRouteStats(ctx, s.pool, arg)
}

func (s *PgStore) RouteAdviceConsumers(ctx context.Context, arg sqlc.RouteAdviceConsumersParams) ([]sqlc.RouteAdviceConsumersRow, error) {
	return s.appErrorsQueries().RouteAdviceConsumers(ctx, s.pool, arg)
}

func (s *PgStore) RouteAdviceThrottleExcess(ctx context.Context, arg sqlc.RouteAdviceThrottleExcessParams) (int64, error) {
	return s.appErrorsQueries().RouteAdviceThrottleExcess(ctx, s.pool, arg)
}

func (m *MemStore) RouteAdviceRouteStats(_ context.Context, _ sqlc.RouteAdviceRouteStatsParams) ([]sqlc.RouteAdviceRouteStatsRow, error) {
	return nil, errMemStoreRequestTelemetry
}

func (m *MemStore) RouteAdviceConsumers(_ context.Context, _ sqlc.RouteAdviceConsumersParams) ([]sqlc.RouteAdviceConsumersRow, error) {
	return nil, errMemStoreRequestTelemetry
}

func (m *MemStore) RouteAdviceThrottleExcess(_ context.Context, _ sqlc.RouteAdviceThrottleExcessParams) (int64, error) {
	return 0, errMemStoreRequestTelemetry
}
