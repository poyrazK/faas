package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RouteAdviceRouteStats, RouteAdviceConsumers and RouteAdviceThrottleExcess
// read retained debugger telemetry for the route advisor (ADR-976), each
// bounded by api.RouteAnalyticsQueryTimeout (ADR-20261026143210).
func (s *PgStore) RouteAdviceRouteStats(ctx context.Context, arg sqlc.RouteAdviceRouteStatsParams) ([]sqlc.RouteAdviceRouteStatsRow, error) {
	return routeAnalyticsRead(ctx, s, func(db sqlc.DBTX) ([]sqlc.RouteAdviceRouteStatsRow, error) {
		return s.appErrorsQueries().RouteAdviceRouteStats(ctx, db, arg)
	})
}

func (s *PgStore) RouteAdviceConsumers(ctx context.Context, arg sqlc.RouteAdviceConsumersParams) ([]sqlc.RouteAdviceConsumersRow, error) {
	return routeAnalyticsRead(ctx, s, func(db sqlc.DBTX) ([]sqlc.RouteAdviceConsumersRow, error) {
		return s.appErrorsQueries().RouteAdviceConsumers(ctx, db, arg)
	})
}

func (s *PgStore) RouteAdviceThrottleExcess(ctx context.Context, arg sqlc.RouteAdviceThrottleExcessParams) (int64, error) {
	return routeAnalyticsRead(ctx, s, func(db sqlc.DBTX) (int64, error) {
		return s.appErrorsQueries().RouteAdviceThrottleExcess(ctx, db, arg)
	})
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
