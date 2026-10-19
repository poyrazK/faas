package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Route insights (ADR-1198): candidate apps, insight upserts and resolution,
// and the per-app listing. A trigger on route_insights enqueues webhooks.
func (s *PgStore) RouteInsightCandidateApps(ctx context.Context, arg sqlc.RouteInsightCandidateAppsParams) ([]sqlc.RouteInsightCandidateAppsRow, error) {
	return s.appErrorsQueries().RouteInsightCandidateApps(ctx, s.pool, arg)
}

func (s *PgStore) UpsertRouteInsight(ctx context.Context, arg sqlc.UpsertRouteInsightParams) error {
	return s.appErrorsQueries().UpsertRouteInsight(ctx, s.pool, arg)
}

func (s *PgStore) ResolveStaleRouteInsights(ctx context.Context, arg sqlc.ResolveStaleRouteInsightsParams) (int64, error) {
	return s.appErrorsQueries().ResolveStaleRouteInsights(ctx, s.pool, arg)
}

func (s *PgStore) ListRouteInsights(ctx context.Context, arg sqlc.ListRouteInsightsParams) ([]sqlc.ListRouteInsightsRow, error) {
	return s.appErrorsQueries().ListRouteInsights(ctx, s.pool, arg)
}

// MemStore has no retained telemetry, so it has no insights either.
func (m *MemStore) RouteInsightCandidateApps(context.Context, sqlc.RouteInsightCandidateAppsParams) ([]sqlc.RouteInsightCandidateAppsRow, error) {
	return nil, errMemStoreRequestTelemetry
}

func (m *MemStore) UpsertRouteInsight(context.Context, sqlc.UpsertRouteInsightParams) error {
	return errMemStoreRequestTelemetry
}

func (m *MemStore) ResolveStaleRouteInsights(context.Context, sqlc.ResolveStaleRouteInsightsParams) (int64, error) {
	return 0, errMemStoreRequestTelemetry
}

func (m *MemStore) ListRouteInsights(context.Context, sqlc.ListRouteInsightsParams) ([]sqlc.ListRouteInsightsRow, error) {
	return nil, errMemStoreRequestTelemetry
}
