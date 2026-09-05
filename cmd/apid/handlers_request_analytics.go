package main

// Customer-facing aggregated request analytics.
//
// GET /v1/apps/{slug}/analytics?since=24h&until=<RFC3339>
// GET /v1/apps/{slug}/analytics/timeseries?since=24h&until=<RFC3339>&route=GET%20%2Fusers&method=GET
//
// This is the historical analytics layer on top of request_telemetry. It
// intentionally returns aggregates only: the debugger remains the place for
// request-level identifiers and trace drill-down. The handler bounds the
// window by the plan's telemetry retention and the SQL queries weight the
// recorder's collapsed rows by count.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const requestAnalyticsRouteLimit = 50

const requestAnalyticsRouteMaxLength = 256

var requestAnalyticsMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "PATCH": {},
	"DELETE": {}, "HEAD": {}, "OPTIONS": {},
}

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

	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	window, err := parseRequestAnalyticsWindow(r, time.Now().UTC(), retention)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}

	response, err := s.requestAnalyticsResponse(r.Context(), app, acct, window)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("request analytics"))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// getAppRequestAnalyticsTimeseries serves the hourly, zero-filled series used
// by customer dashboards. It shares the overview's retention and window
// parser, so a chart can never read beyond the plan's retained telemetry.
func (s *server) getAppRequestAnalyticsTimeseries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("analytics", acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}

	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	window, err := parseRequestAnalyticsWindow(r, time.Now().UTC(), retention)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	route, method, err := parseRequestAnalyticsRouteFilter(r.URL.Query().Get("route"), r.URL.Query().Get("method"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	response, err := s.requestAnalyticsTimeseriesResponse(r.Context(), app, acct, window, route, method)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("request analytics timeseries"))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// parseRequestAnalyticsRouteFilter validates the exact route-label/method pair used
// to select a route-level series. Keeping the pair closed prevents ambiguous
// aggregates (for example, combining GET and POST latency distributions) and
// bounds the query-string value before it reaches the database.
func parseRequestAnalyticsRouteFilter(route, method string) (string, string, error) {
	if route == "" && method == "" {
		return "", "", nil
	}
	if route == "" || method == "" {
		return "", "", fmt.Errorf("route and method must be provided together")
	}
	if utf8.RuneCountInString(route) > requestAnalyticsRouteMaxLength {
		return "", "", fmt.Errorf("route must be at most %d characters", requestAnalyticsRouteMaxLength)
	}
	method = strings.ToUpper(method)
	if _, ok := requestAnalyticsMethods[method]; !ok {
		return "", "", fmt.Errorf("method must be one of GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
	}
	return route, method, nil
}

type requestAnalyticsWindow struct {
	From           time.Time
	Until          time.Time
	AsOf           time.Time
	Since          time.Duration
	RequestedSince string
	WindowClamped  bool
}

// parseRequestAnalyticsWindow accepts the legacy duration form (24h, 7d) and
// an RFC3339 timestamp for `since`, paired with an optional RFC3339 `until`.
// This keeps existing callers compatible while allowing date-picker clients
// to request an explicit half-open [since, until) range.
func parseRequestAnalyticsWindow(r *http.Request, now time.Time, retention time.Duration) (requestAnalyticsWindow, error) {
	now = now.UTC()
	until := now
	if raw := r.URL.Query().Get("until"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return requestAnalyticsWindow{}, fmt.Errorf("until must be RFC3339")
		}
		until = parsed.UTC()
		if until.After(now) {
			return requestAnalyticsWindow{}, fmt.Errorf("until must not be in the future")
		}
	}

	rawSince := r.URL.Query().Get("since")
	if rawSince == "" {
		rawSince = "24h"
	}
	from, err := analyticsSinceTime(rawSince, until)
	if err != nil {
		return requestAnalyticsWindow{}, err
	}
	if !from.Before(until) {
		return requestAnalyticsWindow{}, fmt.Errorf("since must be earlier than until")
	}

	windowClamped := false
	if retention > 0 {
		minimum := until.Add(-retention)
		if from.Before(minimum) {
			from = minimum
			windowClamped = true
		}
	}
	return requestAnalyticsWindow{
		From:           from,
		Until:          until,
		AsOf:           now,
		Since:          until.Sub(from),
		RequestedSince: rawSince,
		WindowClamped:  windowClamped,
	}, nil
}

func analyticsSinceTime(raw string, until time.Time) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed.UTC(), nil
	}
	dur, err := parseRequestAnalyticsDuration(raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("since must be a positive duration (for example 24h or 7d) or an RFC3339 timestamp")
	}
	return until.Add(-dur), nil
}

func parseRequestAnalyticsDuration(raw string) (time.Duration, error) {
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d, nil
	}
	if len(raw) > 1 && raw[len(raw)-1] == 'd' {
		n, err := strconv.ParseInt(raw[:len(raw)-1], 10, 64)
		if err == nil && n > 0 {
			d := time.Duration(n) * 24 * time.Hour
			if d > 0 {
				return d, nil
			}
		}
	}
	return 0, fmt.Errorf("invalid analytics duration")
}

func (s *server) requestAnalyticsResponse(ctx context.Context, app state.App, acct state.Account, window requestAnalyticsWindow) (api.RequestAnalyticsResponse, error) {
	params := sqlc.RequestTelemetryAnalyticsSummaryParams{
		AppID:        stringToPgUUID(app.ID),
		AccountID:    stringToPgUUID(acct.ID),
		ReceivedAt:   pgtype.Timestamptz{Time: window.From, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: window.Until, Valid: true},
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
		Since:           echoDebugSince(window.RequestedSince, window.Since),
		From:            window.From.Format(time.RFC3339Nano),
		Until:           window.Until.Format(time.RFC3339Nano),
		WindowClamped:   window.WindowClamped,
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
		AsOf:            window.AsOf.Format(time.RFC3339Nano),
	}, nil
}

func (s *server) requestAnalyticsTimeseriesResponse(ctx context.Context, app state.App, acct state.Account, window requestAnalyticsWindow, route, method string) (api.RequestAnalyticsTimeseriesResponse, error) {
	rows, err := s.store.RequestTelemetryAnalyticsTimeseries(ctx, sqlc.RequestTelemetryAnalyticsTimeseriesParams{
		AppID:       stringToPgUUID(app.ID),
		AccountID:   stringToPgUUID(acct.ID),
		ReceivedAt:  pgtype.Timestamptz{Time: window.From, Valid: true},
		ReceivedAt2: pgtype.Timestamptz{Time: window.Until, Valid: true},
		Route:       route,
		Method:      method,
	})
	if err != nil {
		return api.RequestAnalyticsTimeseriesResponse{}, err
	}
	points := make([]api.RequestAnalyticsTimeseriesPoint, 0, len(rows))
	for _, row := range rows {
		points = append(points, api.RequestAnalyticsTimeseriesPoint{
			Start:         timeFromPg(row.BucketStart),
			Requests:      row.Requests,
			ErrorRequests: row.ErrorRequests,
			ErrorRatePct:  requestErrorRatePct(row.Requests, row.ErrorRequests),
			ColdBoots:     row.ColdBoots,
			P50MS:         int(row.P50Ms),
			P95MS:         int(row.P95Ms),
			P99MS:         int(row.P99Ms),
		})
	}
	return api.RequestAnalyticsTimeseriesResponse{
		Slug:          app.Slug,
		Route:         route,
		Method:        method,
		Since:         echoDebugSince(window.RequestedSince, window.Since),
		From:          window.From.Format(time.RFC3339Nano),
		Until:         window.Until.Format(time.RFC3339Nano),
		WindowClamped: window.WindowClamped,
		Bucket:        "1h",
		Points:        points,
		AsOf:          window.AsOf.Format(time.RFC3339Nano),
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
func (s *server) fetchDashboardRequestAnalytics(ctx context.Context, log *slog.Logger, app state.App, acct state.Account, selectedRoute, selectedMethod string) *dashboard.RequestAnalyticsView {
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		return nil
	}
	now := time.Now().UTC()
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	window := requestAnalyticsWindow{
		From:           now.Add(-24 * time.Hour),
		Until:          now,
		AsOf:           now,
		Since:          24 * time.Hour,
		RequestedSince: "24h",
	}
	if retention > 0 && window.Since > retention {
		window.From = window.Until.Add(-retention)
		window.Since = retention
		window.WindowClamped = true
	}
	var err error
	response, err := s.requestAnalyticsResponse(ctx, app, acct, window)
	if err != nil {
		log.Warn("dashboard renderAppDetail: request analytics", "account_id", acct.ID, "app_id", app.ID, "err", err)
		return nil
	}
	routes := make([]dashboard.RequestAnalyticsRouteView, 0, len(response.Routes))
	for _, route := range response.Routes {
		trendQuery := url.Values{}
		trendQuery.Set("analytics_route", route.Route)
		trendQuery.Set("analytics_method", route.Method)
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
			TrendURL:      "/dashboard/apps/" + app.Slug + "?" + trendQuery.Encode(),
		})
	}
	selectedQuery := url.Values{}
	if selectedRoute != "" && selectedMethod != "" {
		selectedQuery.Set("analytics_route", selectedRoute)
		selectedQuery.Set("analytics_method", selectedMethod)
	}
	seriesQuery := url.Values{}
	seriesQuery.Set("since", response.Since)
	if selectedRoute != "" && selectedMethod != "" {
		seriesQuery.Set("route", selectedRoute)
		seriesQuery.Set("method", selectedMethod)
	}
	view := &dashboard.RequestAnalyticsView{
		Since:           response.Since,
		From:            response.From,
		Until:           response.Until,
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
		SelectedRoute:   selectedRoute,
		SelectedMethod:  selectedMethod,
		SelectedQuery:   selectedQuery.Encode(),
		TimeseriesURL:   "/v1/apps/" + app.Slug + "/analytics/timeseries?" + seriesQuery.Encode(),
	}
	series, err := s.requestAnalyticsTimeseriesResponse(ctx, app, acct, window, selectedRoute, selectedMethod)
	if err != nil {
		log.Warn("dashboard renderAppDetail: request analytics timeseries", "account_id", acct.ID, "app_id", app.ID, "err", err)
		return view
	}
	if len(series.Points) > 0 {
		latency := views.LatencySparklineView{}
		errorPoints := make([]appmetrics.SparklinePoint, 0, len(series.Points))
		coldBootPoints := make([]appmetrics.SparklinePoint, 0, len(series.Points))
		for _, point := range series.Points {
			at, parseErr := time.Parse(time.RFC3339Nano, point.Start)
			if parseErr != nil {
				continue
			}
			latency.P50 = append(latency.P50, appmetrics.SparklinePoint{Time: at, Value: float64(point.P50MS)})
			latency.P95 = append(latency.P95, appmetrics.SparklinePoint{Time: at, Value: float64(point.P95MS)})
			latency.P99 = append(latency.P99, appmetrics.SparklinePoint{Time: at, Value: float64(point.P99MS)})
			errorPoints = append(errorPoints, appmetrics.SparklinePoint{Time: at, Value: point.ErrorRatePct})
			coldBootPoints = append(coldBootPoints, appmetrics.SparklinePoint{Time: at, Value: requestErrorRatePct(point.Requests, point.ColdBoots)})
		}
		view.Bucket = series.Bucket
		view.LatencySparkline = latency
		view.LatencySparklineHTML = views.RenderLatencySparkline(latency, 480, 100)
		view.ErrorSparkline = errorPoints
		view.ErrorSparklineHTML = views.RenderErrorRateSparkline(errorPoints, 480, 100)
		view.ColdBootSparkline = coldBootPoints
		view.ColdBootSparklineHTML = views.RenderColdBootRateSparkline(coldBootPoints, 480, 100)
	}
	return view
}
