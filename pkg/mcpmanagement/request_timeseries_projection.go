package mcpmanagement

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type analyticsPointView struct {
	Start       string `json:"start"`
	End         string `json:"end"`
	WindowFrom  string `json:"window_from"`
	WindowUntil string `json:"window_until"`
	Partial     bool   `json:"partial"`
	ZeroFilled  bool   `json:"zero_filled"`
	analyticsTrafficView
}

type requestTimeseriesView struct {
	App                string               `json:"app"`
	AppID              string               `json:"app_id"`
	Scope              string               `json:"scope"`
	SeriesScope        string               `json:"series_scope"`
	Route              string               `json:"route,omitempty"`
	Method             string               `json:"method,omitempty"`
	Status             string               `json:"status"`
	Since              string               `json:"since"`
	From               string               `json:"from"`
	Until              string               `json:"until"`
	AsOf               string               `json:"as_of"`
	WindowClamped      bool                 `json:"window_clamped"`
	Bucket             string               `json:"bucket"`
	Points             []analyticsPointView `json:"points"`
	NativePointCount   int                  `json:"native_returned_point_count"`
	ReturnedPointCount int                  `json:"returned_point_count"`
	PointsTruncated    bool                 `json:"points_truncated"`
	Order              string               `json:"order"`
	SourceCompleteness string               `json:"source_completeness"`
	Freshness          string               `json:"freshness"`
	Basis              string               `json:"basis"`
	ErrorBasis         string               `json:"error_basis"`
	Limitations        string               `json:"limitations"`
}

func analyticsTimeseriesGrid(n api.RequestAnalyticsTimeseriesResponse) (from, until, first time.Time, count int, err error) {
	from, fe := time.Parse(time.RFC3339Nano, n.From)
	until, ue := time.Parse(time.RFC3339Nano, n.Until)
	if fe != nil || ue != nil || !from.Before(until) {
		return from, until, first, 0, invalid("invalid_response", "native timeseries has an invalid window")
	}
	from, until = from.UTC(), until.UTC()
	first = from.Truncate(time.Hour)
	// Match the native PostgreSQL generate_series end, including its
	// microsecond-exclusive boundary; do not invent a nanosecond bucket.
	last := until.Add(-time.Microsecond).Truncate(time.Hour)
	if !last.Before(first) {
		count = int((last.Unix()-first.Unix())/int64(time.Hour/time.Second)) + 1
	}
	return from, until, first, count, nil
}

func projectRequestTimeseries(slug, appID string, n api.RequestAnalyticsTimeseriesResponse, limit int) (requestTimeseriesView, error) {
	v := requestTimeseriesView{App: slug, AppID: appID, Scope: "app_wide", SeriesScope: "app_wide", Route: n.Route, Method: n.Method,
		Status: "no_retained_requests", Since: n.Since, From: n.From, Until: n.Until, AsOf: n.AsOf, WindowClamped: n.WindowClamped, Bucket: n.Bucket,
		Points: []analyticsPointView{}, NativePointCount: len(n.Points), PointsTruncated: len(n.Points) > limit, Order: "oldest_first",
		SourceCompleteness: "unknown", Freshness: "unknown", Basis: "retained_requests_weighted_by_collapse_count", ErrorBasis: "http_status_gte_400",
		Limitations: "Native UTC hourly grid, validated before prefix projection. Status describes all native points, including omitted buckets. Zero-filled buckets mean no retained requests, not zero incoming traffic, complete capture or health. Positive-count zero latency is observed; missing latency is omitted. Partial hours cover only window_from/window_until; do not compare their counts as full-hour rates. PostgreSQL microsecond precision can yield no buckets for a sub-microsecond window. Later points omitted by the output limit are not zero traffic. Counts/latency ranks use collapsed request weights, not independent samples. Errors include HTTP 4xx and 5xx. Do not average/sum percentiles or infer deployment causality, statistical significance or guest execution/CPU/memory data. Native plan/retention limits apply. Overview, trends and discovery are independent, not an atomic snapshot; discovery has neither method filtering nor historical until. No grouped dimensions/consumer identities, raw requests, spans, SQL, headers, bodies, costs or deployment payloads are exposed. No request examples are fetched and diagnostic consent grants no other environment/log/write access."}
	if n.Route != "" {
		v.SeriesScope = "route_method"
	}
	from, until, first, count, err := analyticsTimeseriesGrid(n)
	if err != nil {
		return v, err
	}
	if len(n.Points) != count {
		return v, invalid("invalid_response", "native timeseries has missing or extra hourly buckets")
	}
	if count == 0 {
		v.Status = "no_returned_buckets"
	}
	for i, row := range n.Points {
		start, err := time.Parse(time.RFC3339Nano, row.Start)
		if err != nil || !requestFilterText(row.Start, api.MCPAnalyticsTimeMaxBytes) || !start.Equal(first.Add(time.Duration(i)*time.Hour)) {
			return v, invalid("invalid_response", "native timeseries buckets are not a contiguous ordered UTC hourly grid")
		}
		traffic, err := projectAnalyticsTraffic(row.Requests, row.ErrorRequests, row.ColdBoots, row.ErrorRatePct, row.P50MS, row.P95MS, row.P99MS)
		if err != nil {
			return v, err
		}
		if row.Requests > 0 {
			v.Status = "observed"
		}
		if i < limit {
			v.Points = append(v.Points, projectAnalyticsPoint(start.UTC(), from, until, traffic))
		}
	}
	v.ReturnedPointCount = len(v.Points)
	return v, nil
}

func projectAnalyticsPoint(start, from, until time.Time, traffic analyticsTrafficView) analyticsPointView {
	end := start.Add(time.Hour)
	windowFrom, windowUntil := start, end
	if from.After(start) {
		windowFrom = from
	}
	if until.Before(end) {
		windowUntil = until
	}
	return analyticsPointView{Start: start.Format(time.RFC3339Nano), End: end.Format(time.RFC3339Nano), WindowFrom: windowFrom.Format(time.RFC3339Nano), WindowUntil: windowUntil.Format(time.RFC3339Nano),
		Partial: !windowFrom.Equal(start) || !windowUntil.Equal(end), ZeroFilled: traffic.Requests == 0, analyticsTrafficView: traffic}
}
