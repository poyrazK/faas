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
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const requestAnalyticsRouteLimit = 50
const requestAnalyticsGroupLimit = 50
const requestAnalyticsDependencyRowLimit = 2000
const requestAnalyticsDependencyGroupLimit = 64
const requestAnalyticsDependencyOutputLimit = 10
const requestAnalyticsDeploymentLimit = 50

const requestAnalyticsRouteMaxLength = 256

var requestAnalyticsMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "PATCH": {},
	"DELETE": {}, "HEAD": {}, "OPTIONS": {},
}

var requestAnalyticsGroupBys = map[string]struct{}{
	"route": {}, "country": {}, "referrer_host": {}, "ua_family": {}, "status": {}, "consumer_id": {},
}

type requestAnalyticsDeploymentReader interface {
	RequestTelemetryAnalyticsByDeployment(context.Context, sqlc.RequestTelemetryAnalyticsByDeploymentParams) ([]sqlc.RequestTelemetryAnalyticsByDeploymentRow, error)
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

	groupBy, err := parseRequestAnalyticsGroupBy(r.URL.Query().Get("group_by"), "route")
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	response, err := s.requestAnalyticsResponse(r.Context(), app, acct, window, groupBy)
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
	groupBy, err := parseRequestAnalyticsGroupBy(r.URL.Query().Get("group_by"), "")
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	route, method, err := parseRequestAnalyticsRouteFilter(r.URL.Query().Get("route"), r.URL.Query().Get("method"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if groupBy != "" && groupBy != "route" && (route != "" || method != "") {
		api.WriteProblem(w, api.ErrValidation("route and method filters require group_by=route"))
		return
	}
	response, err := s.requestAnalyticsTimeseriesResponse(r.Context(), app, acct, window, groupBy, route, method)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("request analytics timeseries"))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func parseRequestAnalyticsGroupBy(raw, defaultValue string) (string, error) {
	if raw == "" {
		return defaultValue, nil
	}
	if _, ok := requestAnalyticsGroupBys[raw]; !ok {
		return "", fmt.Errorf("group_by must be one of route, country, referrer_host, ua_family, status, consumer_id")
	}
	return raw, nil
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

func (s *server) requestAnalyticsResponse(ctx context.Context, app state.App, acct state.Account, window requestAnalyticsWindow, groupBy string) (api.RequestAnalyticsResponse, error) {
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

	groupRows, err := s.store.RequestTelemetryAnalyticsByDimension(ctx, sqlc.RequestTelemetryAnalyticsByDimensionParams{
		AppID:        params.AppID,
		AccountID:    params.AccountID,
		ReceivedAt:   params.ReceivedAt,
		ReceivedAt_2: params.ReceivedAt_2,
		GroupBy:      groupBy,
		Limit:        requestAnalyticsGroupLimit,
	})
	if err != nil {
		return api.RequestAnalyticsResponse{}, err
	}

	groups := make([]api.RequestAnalyticsGroup, 0, len(groupRows))
	routes := make([]api.RequestAnalyticsRoute, 0, len(groupRows))
	groupsTruncated := false
	var otherRouteRequests int64
	for _, row := range groupRows {
		value := requestAnalyticsDimensionString(row.Dimension)
		method := requestAnalyticsDimensionString(row.Method)
		group := api.RequestAnalyticsGroup{
			Value:               value,
			Method:              method,
			Requests:            row.Requests,
			ErrorRequests:       row.ErrorRequests,
			ErrorRatePct:        requestErrorRatePct(row.Requests, row.ErrorRequests),
			ColdBoots:           row.ColdBoots,
			P50MS:               int(row.P50Ms),
			P95MS:               int(row.P95Ms),
			P99MS:               int(row.P99Ms),
			ColdRequestP95MS:    nullableAnalyticsInt(row.ColdRequestP95Ms),
			WakeBootP95MS:       nullableAnalyticsInt(row.WakeBootP95Ms),
			GuestExecutionP50MS: nullableAnalyticsInt(row.GuestExecutionP50Ms),
			GuestExecutionP95MS: nullableAnalyticsInt(row.GuestExecutionP95Ms),
		}
		groups = append(groups, group)
		if group.Value == "__other__" {
			groupsTruncated = true
			if groupBy == "route" {
				otherRouteRequests = group.Requests
			}
			continue
		}
		if groupBy == "route" {
			routes = append(routes, api.RequestAnalyticsRoute{
				Route:               group.Value,
				Method:              group.Method,
				Requests:            group.Requests,
				ErrorRequests:       group.ErrorRequests,
				ErrorRatePct:        group.ErrorRatePct,
				ColdBoots:           group.ColdBoots,
				P50MS:               group.P50MS,
				P95MS:               group.P95MS,
				P99MS:               group.P99MS,
				ColdRequestP95MS:    group.ColdRequestP95MS,
				WakeBootP95MS:       group.WakeBootP95MS,
				GuestExecutionP50MS: group.GuestExecutionP50MS,
				GuestExecutionP95MS: group.GuestExecutionP95MS,
			})
		}
	}
	dependenciesTruncated := false
	if groupBy == "route" && len(routes) > 0 {
		dependencyRows, err := s.store.ListRequestTelemetryDependencySpans(ctx, sqlc.ListRequestTelemetryDependencySpansParams{
			AppID:        params.AppID,
			AccountID:    params.AccountID,
			ReceivedAt:   params.ReceivedAt,
			ReceivedAt_2: params.ReceivedAt_2,
			Limit:        requestAnalyticsDependencyRowLimit + 1,
		})
		if err != nil {
			return api.RequestAnalyticsResponse{}, err
		}
		if len(dependencyRows) > requestAnalyticsDependencyRowLimit {
			dependenciesTruncated = true
			dependencyRows = dependencyRows[:requestAnalyticsDependencyRowLimit]
		}
		routeDependencies, aggregatedTruncated := buildRequestAnalyticsRouteDependencies(dependencyRows)
		dependenciesTruncated = dependenciesTruncated || aggregatedTruncated
		for i := range routes {
			key := requestAnalyticsRouteKey{route: routes[i].Route, method: routes[i].Method}
			if value, ok := routeDependencies[key]; ok {
				routes[i].DependencySamples = value.samples
				routes[i].DependencyRequests = value.requests
				routes[i].Dependencies = value.dependencies
			}
		}
	}

	var computeCost *api.RequestAnalyticsComputeCost
	var deploymentCosts *api.RequestAnalyticsDeploymentCostBreakdown
	if groupBy == "route" {
		// Usage and request analytics use the exact same bounded window. The
		// app's raw RAM-hours are valued at Gregale's current compute overage
		// rate, then distributed across the observed route request counts. This
		// intentionally excludes the account's shared included allowance and
		// any egress charge: it is a cost allocation estimate, not invoice math.
		usage, _, err := meter.BuildAppWindowSummary(ctx, s.store, acct.ID, app.ID, window.From, window.Until)
		if err != nil {
			return api.RequestAnalyticsResponse{}, err
		}
		estimatedMillicents := api.OverageMillicentsForBillableMBSeconds(usage.MBSeconds)
		requestCount, allocatedMillicents, otherRouteMillicents := allocateRequestAnalyticsRouteCost(routes, otherRouteRequests, estimatedMillicents)
		otherRouteRequestSharePct := 0.0
		if requestCount > 0 {
			otherRouteRequestSharePct = float64(otherRouteRequests) * 100 / float64(requestCount)
		}
		computeCost = &api.RequestAnalyticsComputeCost{
			EstimatedMillicents:       estimatedMillicents,
			AllocatedMillicents:       allocatedMillicents,
			UnallocatedMillicents:     estimatedMillicents - allocatedMillicents,
			OtherRouteMillicents:      otherRouteMillicents,
			OtherRouteRequests:        otherRouteRequests,
			OtherRouteRequestSharePct: otherRouteRequestSharePct,
			RateMillicentsPerGBHour:   api.OverageMillicentsPerGBHour,
			Currency:                  "EUR",
			AllocationMethod:          "request_share",
			Basis:                     "raw_ram_hours_at_current_overage_rate_before_allowance",
			RequestCount:              requestCount,
		}

		if deploymentStore, ok := s.store.(requestAnalyticsDeploymentReader); ok {
			deploymentRows, err := deploymentStore.RequestTelemetryAnalyticsByDeployment(ctx, sqlc.RequestTelemetryAnalyticsByDeploymentParams{
				AppID:        params.AppID,
				AccountID:    params.AccountID,
				ReceivedAt:   params.ReceivedAt,
				ReceivedAt_2: params.ReceivedAt_2,
				Limit:        requestAnalyticsDeploymentLimit,
			})
			if err != nil {
				return api.RequestAnalyticsResponse{}, err
			}
			var totalDeploymentRequests int64
			if len(deploymentRows) > 0 {
				totalDeploymentRequests = deploymentRows[0].TotalRequests
			}
			deploymentCounts := make([]int64, len(deploymentRows)+1)
			var listedRequests int64
			for i, row := range deploymentRows {
				deploymentCounts[i] = row.Requests
				listedRequests += row.Requests
			}
			if totalDeploymentRequests < listedRequests {
				totalDeploymentRequests = listedRequests
			}
			otherDeploymentRequests := totalDeploymentRequests - listedRequests
			deploymentCounts[len(deploymentRows)] = otherDeploymentRequests
			deploymentAllocations, deploymentRequestCount, deploymentAllocatedMillicents := allocateRequestShareMillicents(deploymentCounts, estimatedMillicents)
			deployments := make([]api.RequestAnalyticsDeploymentCost, 0, len(deploymentRows))
			for i, row := range deploymentRows {
				sharePct := 0.0
				if deploymentRequestCount > 0 {
					sharePct = float64(row.Requests) * 100 / float64(deploymentRequestCount)
				}
				deployments = append(deployments, api.RequestAnalyticsDeploymentCost{
					DeploymentID:                   row.DeploymentID,
					CommitSHA:                      requestAnalyticsDimensionString(row.CommitSha),
					DeploymentTag:                  requestAnalyticsDimensionString(row.DeploymentTag),
					DeploymentCreatedAt:            requestAnalyticsDimensionString(row.DeploymentCreatedAt),
					Requests:                       row.Requests,
					RequestSharePct:                sharePct,
					EstimatedComputeCostMillicents: deploymentAllocations[i],
				})
			}
			otherSharePct := 0.0
			if deploymentRequestCount > 0 {
				otherSharePct = float64(otherDeploymentRequests) * 100 / float64(deploymentRequestCount)
			}
			deploymentCosts = &api.RequestAnalyticsDeploymentCostBreakdown{
				EstimatedMillicents:   estimatedMillicents,
				AllocatedMillicents:   deploymentAllocatedMillicents,
				UnallocatedMillicents: estimatedMillicents - deploymentAllocatedMillicents,
				OtherMillicents:       deploymentAllocations[len(deploymentRows)],
				OtherRequests:         otherDeploymentRequests,
				OtherRequestSharePct:  otherSharePct,
				RequestCount:          deploymentRequestCount,
				Deployments:           deployments,
			}
		}
	}

	return api.RequestAnalyticsResponse{
		Slug:                  app.Slug,
		Since:                 echoDebugSince(window.RequestedSince, window.Since),
		From:                  window.From.Format(time.RFC3339Nano),
		Until:                 window.Until.Format(time.RFC3339Nano),
		WindowClamped:         window.WindowClamped,
		Requests:              summary.Requests,
		ErrorRequests:         summary.ErrorRequests,
		ErrorRatePct:          requestErrorRatePct(summary.Requests, summary.ErrorRequests),
		ColdBoots:             summary.ColdBoots,
		P50MS:                 int(summary.P50Ms),
		P95MS:                 int(summary.P95Ms),
		P99MS:                 int(summary.P99Ms),
		GroupBy:               groupBy,
		Groups:                groups,
		GroupsLimit:           requestAnalyticsGroupLimit,
		GroupsTruncated:       groupsTruncated,
		Routes:                routes,
		RoutesLimit:           requestAnalyticsRouteLimit,
		RoutesTruncated:       groupsTruncated && groupBy == "route",
		DependenciesTruncated: dependenciesTruncated,
		ComputeCost:           computeCost,
		DeploymentCosts:       deploymentCosts,
		AsOf:                  window.AsOf.Format(time.RFC3339Nano),
	}, nil
}

type requestAnalyticsRouteKey struct {
	route  string
	method string
}

type requestAnalyticsDependencySample struct {
	durationNanos  uint64
	exclusiveNanos uint64
	weight         int64
	isError        bool
}

type requestAnalyticsDependencyAggregate struct {
	dependencyType string
	dependencyKind string
	name           string
	samples        int64
	calls          int64
	errors         int64
	observations   []requestAnalyticsDependencySample
}

type requestAnalyticsRouteDependencyAggregate struct {
	requests int64
	samples  int64
	groups   map[string]*requestAnalyticsDependencyAggregate
}

type requestAnalyticsRouteDependencyResult struct {
	samples      int64
	requests     int64
	dependencies []api.RequestAnalyticsDependency
}

// buildRequestAnalyticsRouteDependencies turns retained, redacted span
// summaries into a bounded route/dependency rollup. It deliberately uses
// only spans with a platform-classified dependency identity; application
// spans do not masquerade as DB or outbound wait. The sample set is bounded
// by the caller's telemetry-row cap and per-route cardinality cap.
func buildRequestAnalyticsRouteDependencies(rows []sqlc.ListRequestTelemetryDependencySpansRow) (map[requestAnalyticsRouteKey]requestAnalyticsRouteDependencyResult, bool) {
	aggregates := make(map[requestAnalyticsRouteKey]*requestAnalyticsRouteDependencyAggregate)
	truncated := false
	for _, row := range rows {
		if row.Route == "" || row.Method == "" {
			continue
		}
		spans, spanTruncated := parseDebugEvidenceSpans(row.SpansSummary)
		truncated = truncated || spanTruncated
		if len(spans) == 0 {
			continue
		}
		key := requestAnalyticsRouteKey{route: row.Route, method: row.Method}
		routeAggregate := aggregates[key]
		if routeAggregate == nil {
			routeAggregate = &requestAnalyticsRouteDependencyAggregate{groups: make(map[string]*requestAnalyticsDependencyAggregate)}
			aggregates[key] = routeAggregate
		}
		weight := int64(row.Count)
		if weight < 1 {
			weight = 1
		}
		exclusives := debugDependencySpanExclusiveDurations(spans)
		hasDependency := false
		for index, span := range spans {
			if span.DependencyType == "" {
				continue
			}
			hasDependency = true
			routeAggregate.samples++
			segment := debugDependencySegment(span)
			segmentKey := debugDependencySegmentKey(segment)
			dependency := routeAggregate.groups[segmentKey]
			if dependency == nil {
				if len(routeAggregate.groups) >= requestAnalyticsDependencyGroupLimit {
					truncated = true
					continue
				}
				dependency = &requestAnalyticsDependencyAggregate{
					dependencyType: segment.Type,
					dependencyKind: segment.Kind,
					name:           segment.Name,
				}
				routeAggregate.groups[segmentKey] = dependency
			}
			duration := minDebugDependencyDuration(span.DurationNanos)
			exclusive := minDebugDependencyDuration(exclusives[index])
			isError := strings.EqualFold(span.Status, "error")
			dependency.samples++
			dependency.calls += weight
			if isError {
				dependency.errors += weight
			}
			dependency.observations = append(dependency.observations, requestAnalyticsDependencySample{
				durationNanos:  duration,
				exclusiveNanos: exclusive,
				weight:         weight,
				isError:        isError,
			})
		}
		if hasDependency {
			routeAggregate.requests += weight
		}
	}

	results := make(map[requestAnalyticsRouteKey]requestAnalyticsRouteDependencyResult, len(aggregates))
	for key, aggregate := range aggregates {
		dependencies := make([]api.RequestAnalyticsDependency, 0, len(aggregate.groups))
		for _, dependency := range aggregate.groups {
			p95 := requestAnalyticsDependencyPercentile(dependency.observations, 0.95, false)
			exclusiveP95 := requestAnalyticsDependencyPercentile(dependency.observations, 0.95, true)
			dependencies = append(dependencies, api.RequestAnalyticsDependency{
				Type:           dependency.dependencyType,
				Kind:           dependency.dependencyKind,
				Name:           dependency.name,
				Samples:        dependency.samples,
				Calls:          dependency.calls,
				ErrorCalls:     dependency.errors,
				P95MS:          p95,
				ExclusiveP95MS: exclusiveP95,
			})
		}
		sort.SliceStable(dependencies, func(i, j int) bool {
			if dependencies[i].ExclusiveP95MS != dependencies[j].ExclusiveP95MS {
				return dependencies[i].ExclusiveP95MS > dependencies[j].ExclusiveP95MS
			}
			if dependencies[i].P95MS != dependencies[j].P95MS {
				return dependencies[i].P95MS > dependencies[j].P95MS
			}
			if dependencies[i].Type != dependencies[j].Type {
				return dependencies[i].Type < dependencies[j].Type
			}
			if dependencies[i].Kind != dependencies[j].Kind {
				return dependencies[i].Kind < dependencies[j].Kind
			}
			return dependencies[i].Name < dependencies[j].Name
		})
		if len(dependencies) > requestAnalyticsDependencyOutputLimit {
			dependencies = dependencies[:requestAnalyticsDependencyOutputLimit]
			truncated = true
		}
		results[key] = requestAnalyticsRouteDependencyResult{
			samples:      aggregate.samples,
			requests:     aggregate.requests,
			dependencies: dependencies,
		}
	}
	return results, truncated
}

func requestAnalyticsDependencyPercentile(samples []requestAnalyticsDependencySample, quantile float64, exclusive bool) int64 {
	if len(samples) == 0 {
		return 0
	}
	sort.SliceStable(samples, func(i, j int) bool {
		if exclusive {
			return samples[i].exclusiveNanos < samples[j].exclusiveNanos
		}
		return samples[i].durationNanos < samples[j].durationNanos
	})
	var total int64
	for _, sample := range samples {
		total += sample.weight
	}
	target := int64(math.Ceil(float64(total) * quantile))
	if target < 1 {
		target = 1
	}
	var cumulative int64
	for _, sample := range samples {
		cumulative += sample.weight
		if cumulative >= target {
			duration := sample.durationNanos
			if exclusive {
				duration = sample.exclusiveNanos
			}
			return int64(duration / uint64(time.Millisecond))
		}
	}
	last := samples[len(samples)-1]
	if exclusive {
		return int64(last.exclusiveNanos / uint64(time.Millisecond))
	}
	return int64(last.durationNanos / uint64(time.Millisecond))
}

func (s *server) requestAnalyticsTimeseriesResponse(ctx context.Context, app state.App, acct state.Account, window requestAnalyticsWindow, groupBy, route, method string) (api.RequestAnalyticsTimeseriesResponse, error) {
	if groupBy != "" {
		return s.requestAnalyticsGroupedTimeseriesResponse(ctx, app, acct, window, groupBy, route, method)
	}
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

func (s *server) requestAnalyticsGroupedTimeseriesResponse(ctx context.Context, app state.App, acct state.Account, window requestAnalyticsWindow, groupBy, route, method string) (api.RequestAnalyticsTimeseriesResponse, error) {
	rows, err := s.store.RequestTelemetryAnalyticsTimeseriesGrouped(ctx, sqlc.RequestTelemetryAnalyticsTimeseriesGroupedParams{
		AppID:       stringToPgUUID(app.ID),
		AccountID:   stringToPgUUID(acct.ID),
		ReceivedAt:  pgtype.Timestamptz{Time: window.From, Valid: true},
		ReceivedAt2: pgtype.Timestamptz{Time: window.Until, Valid: true},
		GroupBy:     groupBy,
		Route:       route,
		Method:      method,
		Limit:       requestAnalyticsGroupLimit,
	})
	if err != nil {
		return api.RequestAnalyticsTimeseriesResponse{}, err
	}
	type seriesKey struct{ value, method string }
	seriesIndex := make(map[seriesKey]int)
	series := make([]api.RequestAnalyticsTimeseriesSeries, 0, requestAnalyticsGroupLimit+1)
	for _, row := range rows {
		value := requestAnalyticsDimensionString(row.Dimension)
		key := seriesKey{value: value, method: row.Method}
		idx, ok := seriesIndex[key]
		if !ok {
			series = append(series, api.RequestAnalyticsTimeseriesSeries{Value: value, Method: row.Method})
			idx = len(series) - 1
			seriesIndex[key] = idx
		}
		series[idx].Points = append(series[idx].Points, api.RequestAnalyticsTimeseriesPoint{
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
		GroupBy:       groupBy,
		Since:         echoDebugSince(window.RequestedSince, window.Since),
		From:          window.From.Format(time.RFC3339Nano),
		Until:         window.Until.Format(time.RFC3339Nano),
		WindowClamped: window.WindowClamped,
		Bucket:        "1h",
		Series:        series,
		AsOf:          window.AsOf.Format(time.RFC3339Nano),
	}, nil
}

func requestAnalyticsDimensionString(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	default:
		return fmt.Sprint(value)
	}
}

func nullableAnalyticsInt(value pgtype.Int4) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int32)
	return &converted
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
func (s *server) fetchDashboardRequestAnalytics(ctx context.Context, log *slog.Logger, app state.App, acct state.Account, groupBy, selectedRoute, selectedMethod string) *dashboard.RequestAnalyticsView {
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
	if groupBy != "route" {
		selectedRoute, selectedMethod = "", ""
	}
	response, err := s.requestAnalyticsResponse(ctx, app, acct, window, groupBy)
	if err != nil {
		log.Warn("dashboard renderAppDetail: request analytics", "account_id", acct.ID, "app_id", app.ID, "err", err)
		return nil
	}
	routes := make([]dashboard.RequestAnalyticsRouteView, 0, len(response.Routes))
	for _, route := range response.Routes {
		trendQuery := url.Values{}
		trendQuery.Set("analytics_route", route.Route)
		trendQuery.Set("analytics_method", route.Method)
		debugQuery := url.Values{}
		debugQuery.Set("since", response.Since)
		debugQuery.Set("route", route.Route)
		routes = append(routes, dashboard.RequestAnalyticsRouteView{
			Route:                   route.Route,
			Method:                  route.Method,
			Requests:                route.Requests,
			ErrorRequests:           route.ErrorRequests,
			ErrorRatePct:            route.ErrorRatePct,
			ColdBoots:               route.ColdBoots,
			P50MS:                   route.P50MS,
			P95MS:                   route.P95MS,
			P99MS:                   route.P99MS,
			ColdRequestP95MS:        intFromAnalyticsPointer(route.ColdRequestP95MS),
			ColdRequestP95Available: route.ColdRequestP95MS != nil,
			WakeBootP95MS:           intFromAnalyticsPointer(route.WakeBootP95MS),
			WakeBootP95Available:    route.WakeBootP95MS != nil,
			GuestExecutionP50MS:     intFromAnalyticsPointer(route.GuestExecutionP50MS),
			GuestExecutionP95MS:     intFromAnalyticsPointer(route.GuestExecutionP95MS),
			GuestExecutionAvailable: route.GuestExecutionP50MS != nil && route.GuestExecutionP95MS != nil,
			DependencySamples:       route.DependencySamples,
			DependencyRequests:      route.DependencyRequests,
			Dependencies:            route.Dependencies,
			EstimatedComputeCostEUR: millicentsAsEUR(route.EstimatedComputeCostMillicents),
			RequestSharePct:         route.RequestSharePct,
			TrendURL:                "/dashboard/apps/" + app.Slug + "?" + trendQuery.Encode(),
			DebugURL:                "/dashboard/apps/" + app.Slug + "/debug?" + debugQuery.Encode(),
		})
	}
	selectedQuery := url.Values{}
	if groupBy != "route" {
		selectedQuery.Set("analytics_by", groupBy)
	}
	if selectedRoute != "" && selectedMethod != "" {
		selectedQuery.Set("analytics_route", selectedRoute)
		selectedQuery.Set("analytics_method", selectedMethod)
	}
	seriesQuery := url.Values{}
	seriesQuery.Set("since", response.Since)
	if groupBy != "route" {
		seriesQuery.Set("group_by", groupBy)
	}
	if selectedRoute != "" && selectedMethod != "" {
		seriesQuery.Set("route", selectedRoute)
		seriesQuery.Set("method", selectedMethod)
	}
	view := &dashboard.RequestAnalyticsView{
		GroupBy:               response.GroupBy,
		Since:                 response.Since,
		From:                  response.From,
		Until:                 response.Until,
		WindowClamped:         response.WindowClamped,
		Requests:              response.Requests,
		ErrorRequests:         response.ErrorRequests,
		ErrorRatePct:          response.ErrorRatePct,
		ColdBoots:             response.ColdBoots,
		P50MS:                 response.P50MS,
		P95MS:                 response.P95MS,
		P99MS:                 response.P99MS,
		Routes:                routes,
		Groups:                requestAnalyticsGroupViews(response.Groups),
		GroupsLimit:           response.GroupsLimit,
		GroupsTruncated:       response.GroupsTruncated,
		RoutesLimit:           response.RoutesLimit,
		RoutesTruncated:       response.RoutesTruncated,
		DependenciesTruncated: response.DependenciesTruncated,
		ComputeCost:           requestAnalyticsComputeCostView(response.ComputeCost),
		DeploymentCosts:       requestAnalyticsDeploymentCostBreakdownView(response.DeploymentCosts),
		AsOf:                  response.AsOf,
		SelectedRoute:         selectedRoute,
		SelectedMethod:        selectedMethod,
		SelectedQuery:         selectedQuery.Encode(),
		TimeseriesURL:         "/v1/apps/" + app.Slug + "/analytics/timeseries?" + seriesQuery.Encode(),
	}
	series, err := s.requestAnalyticsTimeseriesResponse(ctx, app, acct, window, "", selectedRoute, selectedMethod)
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

func requestAnalyticsComputeCostView(cost *api.RequestAnalyticsComputeCost) *dashboard.RequestAnalyticsComputeCostView {
	if cost == nil {
		return nil
	}
	return &dashboard.RequestAnalyticsComputeCostView{
		EstimatedEUR:               millicentsAsEUR(cost.EstimatedMillicents),
		AllocatedEUR:               millicentsAsEUR(cost.AllocatedMillicents),
		UnallocatedEUR:             millicentsAsEUR(cost.UnallocatedMillicents),
		OtherRoutesEUR:             millicentsAsEUR(cost.OtherRouteMillicents),
		OtherRoutesRequests:        cost.OtherRouteRequests,
		OtherRoutesRequestSharePct: cost.OtherRouteRequestSharePct,
		RateEUR:                    millicentsAsEUR(cost.RateMillicentsPerGBHour),
		RequestCount:               cost.RequestCount,
	}
}

func requestAnalyticsDeploymentCostBreakdownView(cost *api.RequestAnalyticsDeploymentCostBreakdown) *dashboard.RequestAnalyticsDeploymentCostBreakdownView {
	if cost == nil {
		return nil
	}
	deployments := make([]dashboard.RequestAnalyticsDeploymentCostView, 0, len(cost.Deployments))
	for _, deployment := range cost.Deployments {
		revision := deployment.CommitSHA
		if revision == "" {
			revision = deployment.DeploymentID
		}
		deployments = append(deployments, dashboard.RequestAnalyticsDeploymentCostView{
			DeploymentID:    deployment.DeploymentID,
			Revision:        revision,
			Tag:             deployment.DeploymentTag,
			CreatedAt:       deployment.DeploymentCreatedAt,
			Requests:        deployment.Requests,
			RequestSharePct: deployment.RequestSharePct,
			EstimatedEUR:    millicentsAsEUR(deployment.EstimatedComputeCostMillicents),
		})
	}
	return &dashboard.RequestAnalyticsDeploymentCostBreakdownView{
		EstimatedEUR:   millicentsAsEUR(cost.EstimatedMillicents),
		AllocatedEUR:   millicentsAsEUR(cost.AllocatedMillicents),
		UnallocatedEUR: millicentsAsEUR(cost.UnallocatedMillicents),
		OtherEUR:       millicentsAsEUR(cost.OtherMillicents),
		OtherRequests:  cost.OtherRequests,
		OtherSharePct:  cost.OtherRequestSharePct,
		RequestCount:   cost.RequestCount,
		Deployments:    deployments,
	}
}

// One euro is 100,000 millicents (1/1000 of a cent). Integer formatting keeps
// these small estimates precise without floating-point rounding in templates.
func millicentsAsEUR(value int64) string {
	if value < 0 {
		return fmt.Sprintf("-%d.%05d", -(value / 100_000), -(value % 100_000))
	}
	return fmt.Sprintf("%d.%05d", value/100_000, value%100_000)
}

func requestAnalyticsGroupViews(groups []api.RequestAnalyticsGroup) []dashboard.RequestAnalyticsGroupView {
	views := make([]dashboard.RequestAnalyticsGroupView, 0, len(groups))
	for _, group := range groups {
		views = append(views, dashboard.RequestAnalyticsGroupView{
			Value: group.Value, Method: group.Method, Requests: group.Requests,
			ErrorRequests: group.ErrorRequests, ErrorRatePct: group.ErrorRatePct,
			ColdBoots: group.ColdBoots, P50MS: group.P50MS, P95MS: group.P95MS, P99MS: group.P99MS,
		})
	}
	return views
}

func intFromAnalyticsPointer(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
