package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RouteAwakeMinutes and RouteAwakeRouteMinutes read billed running time and
// retained request telemetry for the route awake report (ADR-1187).
func (s *PgStore) RouteAwakeMinutes(ctx context.Context, arg sqlc.RouteAwakeMinutesParams) ([]sqlc.RouteAwakeMinutesRow, error) {
	return s.appErrorsQueries().RouteAwakeMinutes(ctx, s.pool, arg)
}

func (s *PgStore) RouteAwakeRouteMinutes(ctx context.Context, arg sqlc.RouteAwakeRouteMinutesParams) ([]sqlc.RouteAwakeRouteMinutesRow, error) {
	return s.appErrorsQueries().RouteAwakeRouteMinutes(ctx, s.pool, arg)
}

// RouteAwakeSourceMinutes reads route probe and prewarm events, the awake
// causes that are not customer requests (ADR-1199).
func (s *PgStore) RouteAwakeSourceMinutes(ctx context.Context, arg sqlc.RouteAwakeSourceMinutesParams) ([]sqlc.RouteAwakeSourceMinutesRow, error) {
	return s.appErrorsQueries().RouteAwakeSourceMinutes(ctx, s.pool, arg)
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
