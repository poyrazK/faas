package mcpmanagement

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type requestTimeseriesArgs struct {
	App    string `json:"app" jsonschema:"Existing app slug with app-wide diagnostic consent"`
	Since  string `json:"since,omitempty" jsonschema:"Positive duration or whole days, or RFC3339 start timestamp; at most 64 bytes. Native default 24h and retention cap apply."`
	Until  string `json:"until,omitempty" jsonschema:"Optional RFC3339 exclusive historical end at or before now; at most 64 bytes"`
	Route  string `json:"route,omitempty" jsonschema:"Exact unchanged native route label, at most 256 bytes, without query, fragment, controls or credentials. Native method prefixes are preserved. Supply together with method; omit both for app-wide series."`
	Method string `json:"method,omitempty" jsonschema:"GET, POST, PUT, PATCH, DELETE, HEAD or OPTIONS; normalized to uppercase. Required with route."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum oldest-first hourly point prefix from 1 to 168; default 48. Output bound only; no native pagination, window change or query limit."`
}

func analyticsTimeseriesFilter(route, method string) (string, error) {
	if route == "" && method == "" {
		return "", nil
	}
	if route == "" || method == "" || strings.TrimSpace(route) == "" || !requestFilterText(route, api.MCPRequestFieldMaxBytes) || strings.ContainsAny(route, "?#") {
		return "", invalid("invalid_argument", "supply both exact route and method; route must be at most 256 bytes without query, fragment or controls")
	}
	method = strings.ToUpper(method)
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return method, nil
	default:
		return "", invalid("invalid_argument", "method must be GET, POST, PUT, PATCH, DELETE, HEAD or OPTIONS")
	}
}

func (m *management) analyticsTimeseriesSelection(args requestTimeseriesArgs) (api.AppRequestAnalyticsTimeseriesOptions, int, error) {
	var opts api.AppRequestAnalyticsTimeseriesOptions
	window, _, err := analyticsSelection(requestAnalyticsArgs{App: args.App, Since: args.Since, Until: args.Until})
	if err != nil {
		return opts, 0, err
	}
	method, err := analyticsTimeseriesFilter(args.Route, args.Method)
	if err != nil {
		return opts, 0, err
	}
	if m.clean(args.Route) != args.Route {
		return opts, 0, invalid("invalid_argument", "route selectors must be unchanged and must not contain credentials")
	}
	opts.Since, opts.Until, opts.Route, opts.Method = window.Since, window.Until, args.Route, method
	limit := args.Limit
	if limit == 0 {
		limit = api.MCPAnalyticsPointsDefault
	}
	if limit < 1 || limit > api.MCPAnalyticsPointsMax {
		return opts, 0, invalid("invalid_argument", "limit must be between 1 and 168")
	}
	return opts, limit, nil
}

func (m *management) getRequestTimeseries(ctx context.Context, args requestTimeseriesArgs) (any, error) {
	opts, limit, err := m.analyticsTimeseriesSelection(args)
	if err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	n, err := m.client.GetAppRequestAnalyticsTimeseries(ctx, args.App, opts)
	if err != nil {
		return nil, err
	}
	if n.Route != opts.Route || n.Method != opts.Method || n.GroupBy != "" || len(n.Series) != 0 || n.Bucket != "1h" {
		return nil, invalid("invalid_response", "native timeseries changed the exact route/method selection, grouping or hourly bucket")
	}
	window := analyticsWindowMetadata{n.Slug, n.Since, n.From, n.Until, n.AsOf, n.WindowClamped}
	if err := validateAnalyticsTimeRange(args.App, api.AppRequestAnalyticsOptions{Since: opts.Since, Until: opts.Until}, window); err != nil {
		return nil, err
	}
	v, err := projectRequestTimeseries(args.App, app.ID, n, limit)
	if err != nil {
		return nil, err
	}
	return m.performanceResult(performanceArgs{App: args.App}, app.ID, v), nil
}

const requestTimeseriesGuidance = "Use gregale_get_request_analytics_timeseries to inspect when retained traffic, HTTP 4xx/5xx errors, cold boots or weighted p50/p95/p99 changed. Omit route and method together for an app-wide series; otherwise supply both exact unchanged labels. A timeseries_filter_usable overview pair can be copied unchanged, including native method prefixes such as GET /orders; never strip prefixes or reconstruct modified labels. Method input is normalized to uppercase. Preserve the native historical from/until, since, as_of, window_clamped and native/returned point counts. Points are the oldest-first bounded prefix; points_truncated means later buckets are omitted, not zero traffic. Each UTC hourly start/end has clipped window_from/window_until and partial flags; do not treat partial hours as full-hour volume or compare their counts as rates. Native SQL has microsecond precision; sub-microsecond windows may have no returned buckets. A zero_filled bucket means no retained requests, not complete capture or healthy traffic; its latency percentiles are absent. Positive-count observed zero latency remains valid. Status describes the full native series, including omitted points, not only the projected prefix. Capture completeness and freshness are unknown. Never average/sum hourly percentiles into a window percentile or infer deployment causality or statistical significance. The native timeseries supplies no guest CPU/memory/execution measurements. Overview, trends and discovery are independent reads, not an atomic snapshot; discovery has no historical until or method filter. Fetching the prompt/trends collects no diagnostics or request examples, grants no extra environment/log/write access and performs no probes, replay, model requests or changes."
