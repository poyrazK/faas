package mcpmanagement

import "github.com/onebox-faas/faas/pkg/api"

type analyticsTrafficView struct {
	Requests      int64   `json:"requests"`
	ErrorRequests int64   `json:"error_requests"`
	ErrorRatePct  float64 `json:"error_rate_pct"`
	ColdBoots     int64   `json:"cold_boots"`
	LatencyStatus string  `json:"latency_status"`
	P50MS         *int    `json:"p50_ms,omitempty"`
	P95MS         *int    `json:"p95_ms,omitempty"`
	P99MS         *int    `json:"p99_ms,omitempty"`
}

type analyticsMeasurementsView struct {
	MeasurementCoverage string `json:"measurement_coverage"`
	ColdRequestP95MS    *int   `json:"cold_request_p95_ms,omitempty"`
	WakeBootP95MS       *int   `json:"wake_boot_p95_ms,omitempty"`
	GuestExecutionP50MS *int   `json:"guest_execution_p50_ms,omitempty"`
	GuestExecutionP95MS *int   `json:"guest_execution_p95_ms,omitempty"`
	GuestCPUAvgMS       *int   `json:"guest_cpu_avg_ms,omitempty"`
	GuestCPUP95MS       *int   `json:"guest_cpu_p95_ms,omitempty"`
	GuestPeakRSSMaxMB   *int   `json:"guest_peak_rss_max_mb,omitempty"`
}

type analyticsRouteView struct {
	Route                  string `json:"route"`
	RouteModified          bool   `json:"route_modified"`
	Method                 string `json:"method"`
	MethodModified         bool   `json:"method_modified"`
	RouteFilterUsable      bool   `json:"route_filter_usable"`
	TimeseriesFilterUsable bool   `json:"timeseries_filter_usable"`
	analyticsTrafficView
	Measurements analyticsMeasurementsView `json:"measurements"`
}

type requestAnalyticsView struct {
	App                    string               `json:"app"`
	AppID                  string               `json:"app_id"`
	Scope                  string               `json:"scope"`
	Status                 string               `json:"status"`
	Since                  string               `json:"since"`
	From                   string               `json:"from"`
	Until                  string               `json:"until"`
	AsOf                   string               `json:"as_of"`
	WindowClamped          bool                 `json:"window_clamped"`
	Summary                analyticsTrafficView `json:"summary"`
	Routes                 []analyticsRouteView `json:"routes"`
	NativeRoutesLimit      int                  `json:"native_routes_limit"`
	NativeRouteCount       int                  `json:"native_returned_route_count"`
	ReturnedRouteCount     int                  `json:"returned_route_count"`
	NativeRoutesTruncated  bool                 `json:"native_routes_truncated"`
	NativeGroupsTruncated  bool                 `json:"native_groups_truncated"`
	RoutesTruncated        bool                 `json:"routes_truncated"`
	TextTruncated          bool                 `json:"text_truncated"`
	SourceCompleteness     string               `json:"source_completeness"`
	Freshness              string               `json:"freshness"`
	Basis                  string               `json:"basis"`
	ErrorBasis             string               `json:"error_basis"`
	MeasurementLimitations string               `json:"measurement_limitations"`
	Limitations            string               `json:"limitations"`
}

func projectAnalyticsTraffic(requests, errors, cold int64, rate float64, p50, p95, p99 int) (analyticsTrafficView, error) {
	v := analyticsTrafficView{Requests: requests, ErrorRequests: errors, ColdBoots: cold, ErrorRatePct: rate, LatencyStatus: "no_retained_requests"}
	if requests < 0 || errors < 0 || errors > requests || cold < 0 || cold > requests || !performanceRate(rate) || p50 < 0 || p95 < p50 || p99 < p95 || requests == 0 && (p99 != 0 || rate != 0) {
		return v, invalid("invalid_response", "native analytics has invalid traffic counts, rates or percentiles")
	}
	if requests > 0 {
		v.LatencyStatus, v.P50MS, v.P95MS, v.P99MS = "observed", &p50, &p95, &p99
	}
	return v, nil
}

func projectAnalyticsMeasurements(n api.RequestAnalyticsRoute) (analyticsMeasurementsView, error) {
	v := analyticsMeasurementsView{MeasurementCoverage: "not_reported", ColdRequestP95MS: n.ColdRequestP95MS, WakeBootP95MS: n.WakeBootP95MS, GuestExecutionP50MS: n.GuestExecutionP50MS,
		GuestExecutionP95MS: n.GuestExecutionP95MS, GuestCPUAvgMS: n.GuestCPUAvgMS, GuestCPUP95MS: n.GuestCPUP95MS, GuestPeakRSSMaxMB: n.GuestPeakRSSMaxMB}
	for _, value := range []*int{v.ColdRequestP95MS, v.WakeBootP95MS, v.GuestExecutionP50MS, v.GuestExecutionP95MS, v.GuestCPUAvgMS, v.GuestCPUP95MS, v.GuestPeakRSSMaxMB} {
		if value != nil && (*value < 0 || n.Requests <= 0) {
			return v, invalid("invalid_response", "native analytics has a negative measurement or measurements without retained requests")
		}
	}
	if v.GuestExecutionP50MS != nil && v.GuestExecutionP95MS != nil && *v.GuestExecutionP50MS > *v.GuestExecutionP95MS {
		return v, invalid("invalid_response", "native analytics guest execution percentiles are reversed")
	}
	return v, nil
}

func (m *management) projectRequestAnalytics(slug, appID string, n api.RequestAnalyticsResponse, limit int) (requestAnalyticsView, error) {
	v := requestAnalyticsView{App: slug, AppID: appID, Scope: "app_wide", Status: "observed", Since: n.Since, From: n.From, Until: n.Until, AsOf: n.AsOf, WindowClamped: n.WindowClamped,
		Routes: []analyticsRouteView{}, NativeRoutesLimit: n.RoutesLimit, NativeRouteCount: len(n.Routes), NativeRoutesTruncated: n.RoutesTruncated, NativeGroupsTruncated: n.GroupsTruncated, RoutesTruncated: len(n.Routes) > limit,
		SourceCompleteness: "unknown", Freshness: "unknown", Basis: "retained_requests_weighted_by_collapse_count", ErrorBasis: "http_status_gte_400",
		MeasurementLimitations: "Optional fields are native measurements: absent differs from observed zero. Route-level measurement counts/coverage are not reported. Retained values may be rounded/bucketed. Guest execution is runner wall time. CPU measures supported one-shot child processes including runtime startup, not all runtime modes; peak RSS is their rounded/bucketed high-water mark, not allocated VM/app memory. Cold request p95 includes handling; wake boot p95 measures the scheduler boot-start to RUNNING interval. These populations can differ; do not sum/subtract percentiles or infer resource/cost changes.",
		Limitations:            "App-wide retained route/method aggregates in native order, not all traffic or proof of health. Native plan/retention limits apply. Error requests include HTTP 4xx and 5xx, unlike 5xx-only debugger regression signals. Summary and route queries are sequential, not an atomic snapshot; counts need not reconcile. Native and projection limits are separate; omitted routes are not zero traffic. Route/method labels may be modified and collide. No cost/billing data, deployment provenance/comparisons, dimensions/consumer identities, dependency payloads, raw requests, spans, SQL, headers or bodies are exposed. App identity is checked before/after the native slug-scoped read. No request examples are fetched. Request discovery has independent windows, no method filter or historical until; do not manufacture selectors or claim cohort equivalence. Diagnostic consent grants no other environment/log or write access."}
	var err error
	v.Summary, err = projectAnalyticsTraffic(n.Requests, n.ErrorRequests, n.ColdBoots, n.ErrorRatePct, n.P50MS, n.P95MS, n.P99MS)
	if err != nil {
		return v, err
	}
	if n.RoutesLimit <= 0 || len(n.Routes) > n.RoutesLimit {
		return v, invalid("invalid_response", "native analytics route limit metadata is invalid")
	}
	for _, row := range n.Routes[:min(len(n.Routes), limit)] {
		traffic, err := projectAnalyticsTraffic(row.Requests, row.ErrorRequests, row.ColdBoots, row.ErrorRatePct, row.P50MS, row.P95MS, row.P99MS)
		if err != nil {
			return v, err
		}
		measurements, err := projectAnalyticsMeasurements(row)
		if err != nil {
			return v, err
		}
		text := func(s string) string { return m.requestText(s, &v.TextTruncated) }
		route, method := requestRoute(row.Route, text), text(row.Method)
		_, selectorErr := requestListOptions(requestsArgs{Route: row.Route})
		pairMethod, pairErr := analyticsTimeseriesFilter(row.Route, row.Method)
		v.Routes = append(v.Routes, analyticsRouteView{Route: route, RouteModified: route != row.Route, Method: method, MethodModified: method != row.Method,
			RouteFilterUsable: route != "" && route == row.Route && selectorErr == nil, TimeseriesFilterUsable: route != "" && route == row.Route && method == row.Method && pairMethod == method && pairErr == nil, analyticsTrafficView: traffic, Measurements: measurements})
	}
	v.ReturnedRouteCount = len(v.Routes)
	if n.Requests == 0 {
		v.Status = "no_retained_requests"
	}
	return v, nil
}
