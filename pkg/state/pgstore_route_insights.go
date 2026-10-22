package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RouteInsightDetection is one route insight that became active within an
// alert window.
type RouteInsightDetection struct {
	Kind, Method, Route, Message string
	DetectedAt                   time.Time
}

// RouteInsightAlertStore reads what the route_insights_detected alert metric
// counts (ADR-20261020161310); meterd's evaluator type-asserts it.
type RouteInsightAlertStore interface {
	// RouteInsightsDetectedSince returns up to limit of the app's active
	// insights that became active at or after since, newest first, and how
	// many there are in all.
	RouteInsightsDetectedSince(ctx context.Context, appID string, since time.Time, limit int) ([]RouteInsightDetection, int64, error)
}

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

// GetRouteInsight returns ErrNotFound when the app has no such insight.
func (s *PgStore) GetRouteInsight(ctx context.Context, arg sqlc.GetRouteInsightParams) (sqlc.GetRouteInsightRow, error) {
	row, err := s.appErrorsQueries().GetRouteInsight(ctx, s.pool, arg)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrNotFound
	}
	return row, err
}

func (s *PgStore) MarkRouteInsightsFixed(ctx context.Context, arg sqlc.MarkRouteInsightsFixedParams) (int64, error) {
	return s.appErrorsQueries().MarkRouteInsightsFixed(ctx, s.pool, arg)
}

func (s *PgStore) RouteInsightsDetectedSince(ctx context.Context, appID string, since time.Time, limit int) ([]RouteInsightDetection, int64, error) {
	rows, err := s.appErrorsQueries().RouteInsightsDetectedSince(ctx, s.pool, sqlc.RouteInsightsDetectedSinceParams{
		AppID: uuidToPgtype(appID), Since: pgTimestamptzUTC(since), RowLimit: int32(limit),
	})
	if err != nil || len(rows) == 0 {
		return nil, 0, err
	}
	out := make([]RouteInsightDetection, 0, len(rows))
	for _, r := range rows {
		out = append(out, RouteInsightDetection{Kind: r.Kind, Method: r.Method, Route: r.Route, Message: r.Message, DetectedAt: r.FirstDetectedAt.Time.UTC()})
	}
	return out, rows[0].Total, nil
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

func (m *MemStore) GetRouteInsight(context.Context, sqlc.GetRouteInsightParams) (sqlc.GetRouteInsightRow, error) {
	return sqlc.GetRouteInsightRow{}, ErrNotFound
}

func (m *MemStore) MarkRouteInsightsFixed(context.Context, sqlc.MarkRouteInsightsFixedParams) (int64, error) {
	return 0, errMemStoreRequestTelemetry
}

// RouteInsightsDetectedSince reports none: MemStore never records insights.
func (m *MemStore) RouteInsightsDetectedSince(context.Context, string, time.Time, int) ([]RouteInsightDetection, int64, error) {
	return nil, 0, nil
}
