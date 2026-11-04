package apidserver

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/analyticsrollup"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// requestAnalyticsTotals returns the window's totals and its top groups.
// When the app's rollups cover the window they answer the counts and
// latencies (ADR-20261103113952), and the raw rows add only their sampled
// measures, within RequestAnalyticsSampledMeasuresTimeout. Otherwise both
// come from the raw rows.
func (s *server) requestAnalyticsTotals(ctx context.Context, app state.App, acct state.Account, window requestAnalyticsWindow, groupBy string, params sqlc.RequestTelemetryAnalyticsSummaryParams) (sqlc.RequestTelemetryAnalyticsSummaryRow, []sqlc.RequestTelemetryAnalyticsByDimensionRow, error) {
	byDimension := sqlc.RequestTelemetryAnalyticsByDimensionParams{
		AppID: params.AppID, AccountID: params.AccountID,
		ReceivedAt: params.ReceivedAt, ReceivedAt_2: params.ReceivedAt_2,
		GroupBy: groupBy, Limit: requestAnalyticsGroupLimit,
	}
	if reader, ok := analyticsrollup.Covers(ctx, s.store, app.ID, window.From); ok {
		q := analyticsrollup.Query{AppID: app.ID, AccountID: acct.ID, From: window.From, Until: window.Until, GroupBy: groupBy, Limit: requestAnalyticsGroupLimit}
		groups, err := analyticsrollup.Read(ctx, reader, q)
		if err != nil {
			return sqlc.RequestTelemetryAnalyticsSummaryRow{}, nil, err
		}
		totals := groups
		if groupBy != "route" {
			// Each request is in one route row, so the route rows folded
			// into a single group are the window's totals.
			q.GroupBy, q.Limit = "route", 0
			if totals, err = analyticsrollup.Read(ctx, reader, q); err != nil {
				return sqlc.RequestTelemetryAnalyticsSummaryRow{}, nil, err
			}
		}
		summary := analyticsrollup.Summary(totals)
		return summary, analyticsrollup.ByDimension(groups, s.requestAnalyticsSampledMeasures(ctx, byDimension)), nil
	}
	summary, err := s.store.RequestTelemetryAnalyticsSummary(ctx, params)
	if err != nil {
		return sqlc.RequestTelemetryAnalyticsSummaryRow{}, nil, err
	}
	rows, err := s.store.RequestTelemetryAnalyticsByDimension(ctx, byDimension)
	return summary, rows, err
}

// requestAnalyticsSampledMeasures reads the raw groups for the measures
// only sampled rows carry. They are optional in the response, so a slow
// read leaves them out instead of failing the analytics.
func (s *server) requestAnalyticsSampledMeasures(ctx context.Context, arg sqlc.RequestTelemetryAnalyticsByDimensionParams) []sqlc.RequestTelemetryAnalyticsByDimensionRow {
	sampledCtx, cancel := context.WithTimeout(ctx, api.RequestAnalyticsSampledMeasuresTimeout)
	defer cancel()
	rows, err := s.store.RequestTelemetryAnalyticsByDimension(sampledCtx, arg)
	if err != nil {
		if ctx.Err() == nil && s.log != nil {
			timedOut := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, state.ErrRouteAnalyticsTimeout)
			s.log.Warn("request analytics sampled measures left out", "timed_out", timedOut, "err", err)
		}
		return nil
	}
	return rows
}

func (s *server) requestAnalyticsTimeseriesRows(ctx context.Context, app state.App, acct state.Account, window requestAnalyticsWindow, route, method string) ([]sqlc.RequestTelemetryAnalyticsTimeseriesRow, error) {
	if reader, ok := analyticsrollup.Covers(ctx, s.store, app.ID, window.From); ok {
		groups, err := analyticsrollup.Read(ctx, reader, analyticsrollup.Query{
			AppID: app.ID, AccountID: acct.ID, From: window.From, Until: window.Until,
			GroupBy: "route", Route: route, Method: method, Hourly: true,
		})
		if err != nil {
			return nil, err
		}
		return analyticsrollup.Timeseries(groups, window.From, window.Until), nil
	}
	return s.store.RequestTelemetryAnalyticsTimeseries(ctx, sqlc.RequestTelemetryAnalyticsTimeseriesParams{
		AppID:       stringToPgUUID(app.ID),
		AccountID:   stringToPgUUID(acct.ID),
		ReceivedAt:  pgtype.Timestamptz{Time: window.From, Valid: true},
		ReceivedAt2: pgtype.Timestamptz{Time: window.Until, Valid: true},
		Route:       route,
		Method:      method,
	})
}

func (s *server) requestAnalyticsGroupedTimeseriesRows(ctx context.Context, app state.App, acct state.Account, window requestAnalyticsWindow, groupBy, route, method string) ([]sqlc.RequestTelemetryAnalyticsTimeseriesGroupedRow, error) {
	if analyticsrollup.SupportsFilter(groupBy, route, method) {
		if reader, ok := analyticsrollup.Covers(ctx, s.store, app.ID, window.From); ok {
			groups, err := analyticsrollup.Read(ctx, reader, analyticsrollup.Query{
				AppID: app.ID, AccountID: acct.ID, From: window.From, Until: window.Until,
				GroupBy: groupBy, Route: route, Method: method, Limit: requestAnalyticsGroupLimit, Hourly: true,
			})
			if err != nil {
				return nil, err
			}
			return analyticsrollup.TimeseriesGrouped(groups, window.From, window.Until), nil
		}
	}
	return s.store.RequestTelemetryAnalyticsTimeseriesGrouped(ctx, sqlc.RequestTelemetryAnalyticsTimeseriesGroupedParams{
		AppID:       stringToPgUUID(app.ID),
		AccountID:   stringToPgUUID(acct.ID),
		ReceivedAt:  pgtype.Timestamptz{Time: window.From, Valid: true},
		ReceivedAt2: pgtype.Timestamptz{Time: window.Until, Valid: true},
		GroupBy:     groupBy,
		Route:       route,
		Method:      method,
		Limit:       requestAnalyticsGroupLimit,
	})
}
