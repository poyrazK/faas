package mcpmanagement

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type performanceArgs struct {
	App   string `json:"app" jsonschema:"Existing app slug with app-wide diagnostic consent"`
	Since string `json:"since,omitempty" jsonschema:"Positive duration or whole days, e.g. 1h or 3d; at most 64 bytes. Native default 24h and plan retention limits apply."`
}

type performanceWindow struct {
	App                   string `json:"app"`
	AppID                 string `json:"app_id"`
	Scope                 string `json:"scope"`
	Since                 string `json:"since"`
	WindowStart           string `json:"window_start"`
	WindowEnd             string `json:"window_end"`
	RetentionClamped      bool   `json:"retention_clamped"`
	RetentionClampedBasis string `json:"retention_clamped_basis"`
}

func registerPerformanceDiagnostics(s *mcp.Server, m *management) {
	register(s, m, "gregale_get_profiles", "Read retained sampled CPU for an exact deployment, required runtime and explicit start/end, optionally an exact native profile route label. Requires app-wide diagnostic consent. Returns a bounded native-order function prefix and collection/attribution summaries; no flamegraphs, file paths, source links or raw stacks. Sampled CPU is not VM/billed CPU or request latency. No collection, enablement or writes.", m.getProfiles)
	register(s, m, "gregale_get_wake_timeline", "Inspect one bounded oldest-first stage timeline for an exact wake ID. Requires app-wide diagnostic consent. Projects selected startup timings, admission presence, method/tier/trigger and closed restore fallback details; no raw event data or infrastructure IDs. Missing stages stay unknown. Explicit native keyset continuations preserve equal-time events; older APIs remain bounded reads with no manufactured cursor. No automatic paging, collection, probes or writes.", m.getWakeTimeline)
	register(s, m, "gregale_get_wake_history", "Inspect bounded retained instance wake observations: triggers, boot/restore method, tier, readiness and admission concurrency/capacity evidence. Native queued_count is an admission ledger reading, not waiting queue length. Requires app-wide diagnostic consent. Native keyset support filters the requested window before paging and returns explicit next_arguments; older APIs retain latest-100 candidate compatibility limits. Missing/zero fields and empty history remain uncertain. No timeline/request enrichment, collection, probes or writes.", m.getWakeHistory)
	register(s, m, "gregale_list_error_groups", "List recurring error groups (fingerprints) last seen in a window, by default the last 24h. Counts are each group's stored history, not window totals. Page with next_arguments. Excludes messages and payloads; requires diagnostic consent.", m.listErrorGroups)
	register(s, m, "gregale_list_error_requests", "List one bounded page of public request IDs linked to an exact error fingerprint. Requires diagnostic consent. This independent retained fingerprint history has no summary time-window filter. Request evidence availability is not checked; use the returned lookup_identifier for an explicit follow-up. Excludes messages, headers and infrastructure/deployment payloads. No automatic evidence reads or writes.", m.listErrorRequests)
	register(s, m, "gregale_get_request_analytics_timeseries", "Inspect historical UTC hourly retained request traffic, HTTP 4xx/5xx errors, cold boots and weighted p50/p95/p99. App-wide or an exact unchanged route/method pair. Requires diagnostic consent; preserves effective windows, partial hours, zero-filled gaps and oldest-first output limits. Empty buckets do not prove health. No guest/resource measurements, collection, probes, replay or writes.", m.getRequestTimeseries)
	register(s, m, "gregale_get_request_analytics", "Read a bounded app-wide route/method analytics overview: retained traffic, HTTP 4xx/5xx errors, cold boots, latency percentiles and optional native guest execution/CPU/RSS measurements. Requires diagnostic consent remotely. Supports duration/day or absolute since and optional historical until. Preserves native window/retention/output limits and missing measurements; sample coverage remains unknown. Excludes billing, deployment, consumer/dimension and dependency payloads. Read-only; empty evidence does not prove health.", m.getRequestAnalytics)
	register(s, m, "gregale_list_regressions", "Read a bounded inbox of native-visible latency/error regression observations, deployment scopes, detection timestamps and triage state. Requires app-wide diagnostic consent remotely. Native ordering/visibility and one-hour default detection lookback apply. Suspected dependencies are hypotheses; SQL/query text is excluded. Read-only; empty evidence does not prove health.", m.listRegressions)
	register(s, m, "gregale_list_fleet_regressions", "Inspect native-visible regressions for one to five explicitly selected apps with separate per-app diagnostic consent. Preserves each app's effective lookback, native order, deployment scopes and evidence limits. Returns per-app code/status errors and partial/unavailable results; failed or empty reads do not prove health. Bounds per-app output and read time; no automatic app discovery, ranking, paging, subscriptions or writes.", m.listFleetRegressions)
	register(s, m, "gregale_compare_deployments", "Compare retained per-route latency percentiles and represented request counts for two exact app deployments. Requires app-wide diagnostic consent remotely and native window metadata. Read-only telemetry query; no replay or mutations. Missing samples, source completeness and traffic-mix uncertainty remain explicit.", m.compareDeployments)
	register(s, m, "gregale_get_debug_coverage", "Inspect trace, span, wake and guest evidence within retained telemetry for one app. Percentages refer only to retained represented requests, not all traffic or full capture. Remote access requires diagnostic consent; native plan/retention limits apply.", m.getDebugCoverage)
	register(s, m, "gregale_get_dependency_latency", "Inspect a bounded projection of sampled dependency latency percentiles, error rates, parent-child impact edges, representative request lookups and split-window regression signals. App-wide diagnostic consent is required remotely. Preserves sampling/completeness flags; excludes SQL/query patterns, raw spans, separate trace IDs and deployment comparisons. Edge durations describe child span samples, not full request latency. Empty evidence is not proof of health.", m.getDependencyLatency)
	register(s, m, "gregale_get_critical_paths", "Inspect bounded ordered critical-path patterns reconstructed from retained sampled spans, weighted latency/error trends and representative request lookup identifiers. Requires app-wide diagnostic consent remotely. Preserves source/projection limits; excludes raw spans, SQL and separate trace IDs. Regression compares time-window halves, not deployments; empty evidence is not proof of health.", m.getCriticalPaths)
	s.AddPrompt(&mcp.Prompt{Name: "gregale_performance_diagnosis", Description: "Read-only app performance review using the regression inbox, retained coverage, dependency trends, critical paths and request evidence.", Arguments: []*mcp.PromptArgument{{Name: "app", Required: true, Description: "Existing app with diagnostic consent"}, {Name: "since", Description: "Positive lookback duration/day alias; regression/comparison default 1h, other aggregates 24h; retention caps apply"}}}, m.getPerformancePrompt)
}

func validatePerformanceArgs(args performanceArgs) error {
	if err := validateSegment(args.App, "app"); err != nil {
		return err
	}
	if args.Since != "" {
		_, err := requestSince(args.Since)
		return err
	}
	return nil
}

func (m *management) getDebugCoverage(ctx context.Context, args performanceArgs) (any, error) {
	if err := validatePerformanceArgs(args); err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	native, err := m.client.GetAppDebugCoverage(ctx, args.App, args.Since)
	if err != nil {
		return nil, err
	}
	window, err := validatePerformanceWindow(args, app.ID, native.AppID, native.Since, native.WindowStart, native.WindowEnd, nil)
	if err != nil {
		return nil, err
	}
	view, err := projectDebugCoverage(window, native)
	if err != nil {
		return nil, err
	}
	return m.performanceResult(args, app.ID, view), nil
}

func (m *management) getDependencyLatency(ctx context.Context, args performanceArgs) (any, error) {
	if err := validatePerformanceArgs(args); err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	native, err := m.client.GetAppDebugDependencyLatency(ctx, args.App, args.Since)
	if err != nil {
		return nil, err
	}
	window, err := validatePerformanceWindow(args, app.ID, native.AppID, native.Since, native.WindowStart, native.WindowEnd, &native.RetentionClamped)
	if err != nil {
		return nil, err
	}
	view, err := m.projectDependencyLatency(window, native)
	if err != nil {
		return nil, err
	}
	return m.performanceResult(args, app.ID, view), nil
}

func (m *management) performanceResult(args performanceArgs, appID string, value any) *guardedRead {
	return &guardedRead{value: value, check: func(ctx context.Context) error {
		app, err := m.requestApp(ctx, args.App)
		if err == nil && app.ID != appID {
			return invalid("request_access_denied", "app identity changed during performance inspection")
		}
		return err
	}}
}

func validatePerformanceWindow(args performanceArgs, appID, responseAppID, since, startRaw, endRaw string, clamped *bool) (performanceWindow, error) {
	window := performanceWindow{App: args.App, AppID: appID, Scope: "app_wide", Since: since, WindowStart: startRaw, WindowEnd: endRaw, RetentionClampedBasis: "requested_and_effective_lookback"}
	effective, err := requestSince(since)
	start, startErr := time.Parse(time.RFC3339Nano, startRaw)
	end, endErr := time.Parse(time.RFC3339Nano, endRaw)
	if appID == "" || appID != responseAppID || err != nil || startErr != nil || endErr != nil || !start.Before(end) || end.Sub(start) != effective {
		return window, invalid("invalid_response", "native performance response has inconsistent app or window metadata")
	}
	requested := 24 * time.Hour
	if args.Since != "" {
		requested, err = requestSince(args.Since)
		if err != nil {
			return window, err
		}
	}
	if effective > requested {
		return window, invalid("invalid_response", "native performance response widened the requested window")
	}
	window.RetentionClamped = effective < requested
	if clamped != nil {
		window.RetentionClampedBasis = "native_response"
		window.RetentionClamped = *clamped
		if effective < requested && !*clamped {
			return window, invalid("invalid_response", "native performance window changed without retention clamping")
		}
	}
	return window, nil
}
