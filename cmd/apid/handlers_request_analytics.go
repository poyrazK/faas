package main

// Customer-facing aggregated request analytics.
//
// GET /v1/apps/{slug}/analytics?since=24h
//
// This is the historical analytics layer on top of request_telemetry. It
// intentionally returns aggregates only: the debugger remains the place for
// request-level identifiers and trace drill-down. The handler bounds the
// window by the plan's telemetry retention and the SQL queries weight the
// recorder's collapsed rows by count.

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const requestAnalyticsRouteLimit = 50

// getAppRequestAnalytics serves the bounded, aggregated request analytics
// overview for one app. The route is gated by the same paid telemetry
// entitlement as the customer debugger, but exposes no request IDs or trace
// payloads and therefore uses the normal read-surface auth chain.
func (s *server) getAppRequestAnalytics(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("analytics", acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}

	sinceRaw := r.URL.Query().Get("since")
	sinceDur := parseDebugSinceFromString(sinceRaw, 24*time.Hour)
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	windowClamped := retention > 0 && sinceDur > retention
	if windowClamped {
		sinceDur = retention
	}

	now := time.Now().UTC()
	response, err := s.requestAnalyticsResponse(r.Context(), app, acct, sinceRaw, sinceDur, windowClamped, now)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("request analytics"))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) requestAnalyticsResponse(ctx context.Context, app state.App, acct state.Account, sinceRaw string, sinceDur time.Duration, windowClamped bool, now time.Time) (api.RequestAnalyticsResponse, error) {
	params := sqlc.RequestTelemetryAnalyticsSummaryParams{
		AppID:        stringToPgUUID(app.ID),
		AccountID:    stringToPgUUID(acct.ID),
		ReceivedAt:   pgtype.Timestamptz{Time: now.Add(-sinceDur), Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: now, Valid: true},
	}
	summary, err := s.store.RequestTelemetryAnalyticsSummary(ctx, params)
	if err != nil {
		return api.RequestAnalyticsResponse{}, err
	}

	routeRows, err := s.store.RequestTelemetryAnalyticsByRoute(ctx, sqlc.RequestTelemetryAnalyticsByRouteParams{
		AppID:        params.AppID,
		AccountID:    params.AccountID,
		ReceivedAt:   params.ReceivedAt,
		ReceivedAt_2: params.ReceivedAt_2,
		Limit:        requestAnalyticsRouteLimit + 1,
	})
	if err != nil {
		return api.RequestAnalyticsResponse{}, err
	}

	routesTruncated := len(routeRows) > requestAnalyticsRouteLimit
	if routesTruncated {
		routeRows = routeRows[:requestAnalyticsRouteLimit]
	}
	routes := make([]api.RequestAnalyticsRoute, 0, len(routeRows))
	for _, row := range routeRows {
		routes = append(routes, api.RequestAnalyticsRoute{
			Route:         row.Route,
			Method:        row.Method,
			Requests:      row.Requests,
			ErrorRequests: row.ErrorRequests,
			ErrorRatePct:  requestErrorRatePct(row.Requests, row.ErrorRequests),
			ColdBoots:     row.ColdBoots,
			P50MS:         int(row.P50Ms),
			P95MS:         int(row.P95Ms),
			P99MS:         int(row.P99Ms),
		})
	}

	return api.RequestAnalyticsResponse{
		Slug:            app.Slug,
		Since:           echoDebugSince(sinceRaw, sinceDur),
		Until:           now.Format(time.RFC3339Nano),
		WindowClamped:   windowClamped,
		Requests:        summary.Requests,
		ErrorRequests:   summary.ErrorRequests,
		ErrorRatePct:    requestErrorRatePct(summary.Requests, summary.ErrorRequests),
		ColdBoots:       summary.ColdBoots,
		P50MS:           int(summary.P50Ms),
		P95MS:           int(summary.P95Ms),
		P99MS:           int(summary.P99Ms),
		Routes:          routes,
		RoutesLimit:     requestAnalyticsRouteLimit,
		RoutesTruncated: routesTruncated,
		AsOf:            now.Format(time.RFC3339Nano),
	}, nil
}

func requestErrorRatePct(requests, errors int64) float64 {
	if requests <= 0 {
		return 0
	}
	return float64(errors) * 100 / float64(requests)
}

// fetchDashboardRequestAnalytics projects the same aggregate query used by
// the public endpoint into the app-detail template. It is intentionally
// best-effort: the live metrics/SLO panels should remain usable during a
// transient request_telemetry read failure.
func (s *server) fetchDashboardRequestAnalytics(ctx context.Context, log *slog.Logger, app state.App, acct state.Account) *dashboard.RequestAnalyticsView {
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		return nil
	}
	now := time.Now().UTC()
	response, err := s.requestAnalyticsResponse(ctx, app, acct, "24h", 24*time.Hour, false, now)
	if err != nil {
		log.Warn("dashboard renderAppDetail: request analytics", "account_id", acct.ID, "app_id", app.ID, "err", err)
		return nil
	}
	routes := make([]dashboard.RequestAnalyticsRouteView, 0, len(response.Routes))
	for _, route := range response.Routes {
		routes = append(routes, dashboard.RequestAnalyticsRouteView{
			Route:         route.Route,
			Method:        route.Method,
			Requests:      route.Requests,
			ErrorRequests: route.ErrorRequests,
			ErrorRatePct:  route.ErrorRatePct,
			ColdBoots:     route.ColdBoots,
			P50MS:         route.P50MS,
			P95MS:         route.P95MS,
			P99MS:         route.P99MS,
		})
	}
	return &dashboard.RequestAnalyticsView{
		Since:           response.Since,
		WindowClamped:   response.WindowClamped,
		Requests:        response.Requests,
		ErrorRequests:   response.ErrorRequests,
		ErrorRatePct:    response.ErrorRatePct,
		ColdBoots:       response.ColdBoots,
		P50MS:           response.P50MS,
		P95MS:           response.P95MS,
		P99MS:           response.P99MS,
		Routes:          routes,
		RoutesLimit:     response.RoutesLimit,
		RoutesTruncated: response.RoutesTruncated,
		AsOf:            response.AsOf,
	}
}
