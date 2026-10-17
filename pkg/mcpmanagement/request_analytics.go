package mcpmanagement

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type requestAnalyticsArgs struct {
	App   string `json:"app" jsonschema:"Existing app slug with app-wide diagnostic consent"`
	Since string `json:"since,omitempty" jsonschema:"Positive duration or whole days, or RFC3339 start timestamp; at most 64 bytes. Native default 24h; native retention cap applies."`
	Until string `json:"until,omitempty" jsonschema:"Optional RFC3339 exclusive window end at or before now; at most 64 bytes"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum projected route rows from 1 to 50; default 20. Output bound only, not native pagination or a query limit."`
}

func analyticsSelection(args requestAnalyticsArgs) (api.AppRequestAnalyticsOptions, int, error) {
	opts := api.AppRequestAnalyticsOptions{Since: args.Since, Until: args.Until, GroupBy: "route"}
	if err := validateSegment(args.App, "app"); err != nil {
		return opts, 0, err
	}
	if !requestFilterText(args.Since, api.MCPAnalyticsTimeMaxBytes) || !requestFilterText(args.Until, api.MCPAnalyticsTimeMaxBytes) {
		return opts, 0, invalid("invalid_argument", "analytics timestamps/lookbacks must be at most 64 bytes without control characters")
	}
	end := time.Now()
	if args.Until != "" {
		until, err := time.Parse(time.RFC3339Nano, args.Until)
		if err != nil || until.After(end) {
			return opts, 0, invalid("invalid_argument", "until must be an RFC3339 timestamp at or before now")
		}
		end, opts.Until = until, until.UTC().Format(time.RFC3339Nano)
	}
	if args.Since != "" {
		if start, err := time.Parse(time.RFC3339Nano, args.Since); err == nil {
			if !start.Before(end) {
				return opts, 0, invalid("invalid_argument", "since must be earlier than the analytics window end")
			}
			opts.Since = start.UTC().Format(time.RFC3339Nano)
		} else if _, err := requestSince(args.Since); err != nil {
			return opts, 0, invalid("invalid_argument", "since must be a positive duration/day lookback or RFC3339 start timestamp")
		}
	}
	limit := args.Limit
	if limit == 0 {
		limit = api.MCPAnalyticsRoutesDefault
	}
	if limit < 1 || limit > api.MCPAnalyticsRoutesMax {
		return opts, 0, invalid("invalid_argument", "limit must be between 1 and 50")
	}
	return opts, limit, nil
}

func (m *management) getRequestAnalytics(ctx context.Context, args requestAnalyticsArgs) (any, error) {
	opts, limit, err := analyticsSelection(args)
	if err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	native, err := m.client.GetAppRequestAnalyticsOpts(ctx, args.App, opts)
	if err != nil {
		return nil, err
	}
	if err := validateAnalyticsWindow(args.App, opts, native); err != nil {
		return nil, err
	}
	view, err := m.projectRequestAnalytics(args.App, app.ID, native, limit)
	if err != nil {
		return nil, err
	}
	return m.performanceResult(performanceArgs{App: args.App}, app.ID, view), nil
}

type analyticsWindowMetadata struct {
	Slug, Since, From, Until, AsOf string
	WindowClamped                  bool
}

func validateAnalyticsWindow(slug string, opts api.AppRequestAnalyticsOptions, n api.RequestAnalyticsResponse) error {
	if n.GroupBy != "route" {
		return invalid("invalid_response", "native analytics has unexpected grouping")
	}
	return validateAnalyticsTimeRange(slug, opts, analyticsWindowMetadata{n.Slug, n.Since, n.From, n.Until, n.AsOf, n.WindowClamped})
}

func validateAnalyticsTimeRange(slug string, opts api.AppRequestAnalyticsOptions, n analyticsWindowMetadata) error {
	errInvalid := invalid("invalid_response", "native analytics has inconsistent app, grouping or window metadata")
	if n.Slug != slug || !requestFilterText(n.Since, api.MCPAnalyticsTimeMaxBytes) || !requestFilterText(n.From, api.MCPAnalyticsTimeMaxBytes) || !requestFilterText(n.Until, api.MCPAnalyticsTimeMaxBytes) || !requestFilterText(n.AsOf, api.MCPAnalyticsTimeMaxBytes) {
		return errInvalid
	}
	from, fe := time.Parse(time.RFC3339Nano, n.From)
	until, ue := time.Parse(time.RFC3339Nano, n.Until)
	asOf, ae := time.Parse(time.RFC3339Nano, n.AsOf)
	effective, se := requestSince(n.Since)
	if fe != nil || ue != nil || ae != nil || se != nil || !from.Before(until) || until.After(asOf) || until.Sub(from) != effective {
		return errInvalid
	}
	if opts.Until != "" {
		requested, err := time.Parse(time.RFC3339Nano, opts.Until)
		if err != nil || !until.Equal(requested) {
			return errInvalid
		}
	}
	requestedFrom, err := time.Parse(time.RFC3339Nano, opts.Since)
	if err != nil {
		lookback := 24 * time.Hour
		if opts.Since != "" {
			lookback, err = requestSince(opts.Since)
			if err != nil {
				return errInvalid
			}
		}
		requestedFrom = until.Add(-lookback)
	}
	if !requestedFrom.Before(until) || from.Before(requestedFrom) || from.After(requestedFrom) != n.WindowClamped {
		return errInvalid
	}
	return nil
}

const requestAnalyticsGuidance = "Use gregale_get_request_analytics for a bounded route/method overview before choosing request investigations. Report its effective from/until, since, as_of, native window_clamped, source_completeness unknown and native/projection/text route limits. It accepts duration/day or RFC3339 since and optional historical until; do not pass absolute timestamps to duration-only performance tools. Analytics error_requests includes HTTP status >=400 (4xx and 5xx), unlike 5xx-only debugger regression signals. Counts/latency percentiles are weighted retained request aggregates, not full traffic capture or independent samples. Empty results or absent measurements do not prove health. Optional measurement fields preserve observed zero separately from absence; measurement_coverage not_reported means route-level measurement counts/coverage are unknown. Guest execution is runner wall time; child CPU includes one-shot runtime startup and may exceed wall time; peak RSS is a rounded/bucketed child-process high-water mark, not app/VM memory or allocated capacity. Cold request p95 includes handling and wake boot p95 measures the scheduler boot interval; do not subtract or sum percentiles from different populations. Native retained values may be rounded/bucketed. A route_filter_usable label can be used with supported gregale_list_requests filters only as an independent read with its own returned window. Discovery supports neither a method filter nor historical until, so do not claim to reproduce this analytics route/method cohort or historical range. Never rewrite a modified label or remove method prefixes to manufacture a filter; obtain the original safe selector from the user. Do not infer a deployment, billing amount, healthy baseline or causal explanation, or collect, replay, probe or apply changes. Analytics retrieval grants no additional environment/log/write permissions and fetches no request examples."
