package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RouteAwakeMinutes and RouteAwakeRouteMinutes read billed running time and
// retained request telemetry for the route awake report (ADR-1187). Every
// awake read is bounded by api.RouteAnalyticsQueryTimeout (ADR-20261026143210).
func (s *PgStore) RouteAwakeMinutes(ctx context.Context, arg sqlc.RouteAwakeMinutesParams) ([]sqlc.RouteAwakeMinutesRow, error) {
	return routeAnalyticsRead(ctx, s, func(db sqlc.DBTX) ([]sqlc.RouteAwakeMinutesRow, error) {
		return s.appErrorsQueries().RouteAwakeMinutes(ctx, db, arg)
	})
}

func (s *PgStore) RouteAwakeRouteMinutes(ctx context.Context, arg sqlc.RouteAwakeRouteMinutesParams) ([]sqlc.RouteAwakeRouteMinutesRow, error) {
	return routeAnalyticsRead(ctx, s, func(db sqlc.DBTX) ([]sqlc.RouteAwakeRouteMinutesRow, error) {
		return s.appErrorsQueries().RouteAwakeRouteMinutes(ctx, db, arg)
	})
}

// RouteAwakeSourceMinutes reads route probe and prewarm events, the awake
// causes that are not customer requests (ADR-1199).
func (s *PgStore) RouteAwakeSourceMinutes(ctx context.Context, arg sqlc.RouteAwakeSourceMinutesParams) ([]sqlc.RouteAwakeSourceMinutesRow, error) {
	return routeAnalyticsRead(ctx, s, func(db sqlc.DBTX) ([]sqlc.RouteAwakeSourceMinutesRow, error) {
		return s.appErrorsQueries().RouteAwakeSourceMinutes(ctx, db, arg)
	})
}

func (m *MemStore) RouteAwakeSourceMinutes(_ context.Context, _ sqlc.RouteAwakeSourceMinutesParams) ([]sqlc.RouteAwakeSourceMinutesRow, error) {
	return nil, errMemStoreRequestTelemetry
}

func (m *MemStore) RouteAwakeMinutes(_ context.Context, _ sqlc.RouteAwakeMinutesParams) ([]sqlc.RouteAwakeMinutesRow, error) {
	return nil, errMemStoreRequestTelemetry
}

func (m *MemStore) RouteAwakeRouteMinutes(_ context.Context, _ sqlc.RouteAwakeRouteMinutesParams) ([]sqlc.RouteAwakeRouteMinutesRow, error) {
	return nil, errMemStoreRequestTelemetry
}

// RouteAwakeWorkMinutes reads the deliveries schedd made to the app: cron,
// queue, delayed task, async invoke, webhook, replay and event-source work
// (ADR-20261022184757).
func (s *PgStore) RouteAwakeWorkMinutes(ctx context.Context, arg sqlc.RouteAwakeWorkMinutesParams) ([]sqlc.RouteAwakeWorkMinutesRow, error) {
	return routeAnalyticsRead(ctx, s, func(db sqlc.DBTX) ([]sqlc.RouteAwakeWorkMinutesRow, error) {
		return s.appErrorsQueries().RouteAwakeWorkMinutes(ctx, db, arg)
	})
}

func (m *MemStore) RouteAwakeWorkMinutes(_ context.Context, _ sqlc.RouteAwakeWorkMinutesParams) ([]sqlc.RouteAwakeWorkMinutesRow, error) {
	return nil, errMemStoreRequestTelemetry
}
