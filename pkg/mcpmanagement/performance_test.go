// adr: 1620
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func performanceFixtureCoverage(appID string) api.DebugCoverageResponse {
	return api.DebugCoverageResponse{AppID: appID, Since: "1h", WindowStart: "2026-10-17T10:00:00Z", WindowEnd: "2026-10-17T11:00:00Z", PlanRetentionDays: 3,
		TelemetryRows: 2, RepresentedRequests: 10, ErrorRequests: 7,
		TraceLinked: api.DebugCoverageSignal{Rows: 1, Requests: 7, RatePct: 70}, SpanEvidence: api.DebugCoverageSignal{Rows: 1, Requests: 7, RatePct: 70},
		OldestTelemetryAt: "2026-10-17T10:20:00Z", LatestTelemetryAt: "2026-10-17T10:59:00Z"}
}

func performanceFixtureDependencies(appID string) api.DebugDependencyLatencyResponse {
	return api.DebugDependencyLatencyResponse{AppID: appID, Since: "1h", WindowStart: "2026-10-17T10:00:01Z", WindowEnd: "2026-10-17T11:00:01Z", Complete: false, Truncated: true,
		TelemetryRows: 2, RepresentedRequests: 10, SpanSamples: 4,
		Dependencies: []api.DebugDependencyLatencyItem{{Type: "binding", Kind: "postgres", Name: "orders " + testToken, Calls: 20, ErrorCalls: 14, ErrorRatePct: 70, P50MS: 50, P95MS: 50, P99MS: 50, ExclusiveP95MS: 40, BaselineP95MS: 20, CurrentP95MS: 50, P95DeltaMS: 30, RegressionFactor: 2.5, Regression: true, CurrentErrorRatePct: 100, ErrorRateDeltaPct: 100, FailureRegression: true,
			Queries: []api.DebugDependencyQueryItem{{Pattern: "excluded-sql-query"}}}},
		Edges:                []api.DebugDependencyImpactEdge{dependencyImpactFixtureEdge()},
		DeploymentComparison: &api.DebugDependencyDeploymentComparison{CurrentDeploymentID: "excluded-deployment"}}
}

func performanceFixtureHook(w http.ResponseWriter, r *http.Request, appID string) bool {
	switch r.URL.Path {
	case "/v1/apps/alpha/debug/coverage":
		_ = json.NewEncoder(w).Encode(performanceFixtureCoverage(appID))
		return true
	case "/v1/apps/alpha/debug/dependencies":
		_ = json.NewEncoder(w).Encode(performanceFixtureDependencies(appID))
		return true
	}
	return false
}

func emptyPerformanceCoverage(appID string) api.DebugCoverageResponse {
	c := performanceFixtureCoverage(appID)
	c.TelemetryRows, c.RepresentedRequests, c.ErrorRequests = 0, 0, 0
	c.TraceLinked, c.SpanEvidence = api.DebugCoverageSignal{}, api.DebugCoverageSignal{}
	c.OldestTelemetryAt, c.LatestTelemetryAt = "", ""
	return c
}

func emptyPerformanceDependencies(appID string) api.DebugDependencyLatencyResponse {
	d := performanceFixtureDependencies(appID)
	d.TelemetryRows, d.RepresentedRequests, d.SpanSamples = 0, 0, 0
	d.Dependencies, d.Edges = nil, nil
	d.Complete, d.Truncated = true, false
	return d
}

func TestPerformanceConsentProjectionAndPrompt(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "denied", "approved", "replaced"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				var reads atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						t.Error("performance diagnosis wrote native state")
						w.WriteHeader(405)
						return
					}
					switch r.URL.Path {
					case "/v1/apps/alpha":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					case "/v1/apps":
						_ = json.NewEncoder(w).Encode([]api.AppResponse{{ID: "app-1", Slug: "alpha"}, {ID: "future-app", Slug: "future"}})
					case "/v1/apps/future":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "future-app", Slug: "future"})
					default:
						reads.Add(1)
						if r.URL.Query().Get("since") != "1h" {
							t.Error("lookback not forwarded")
						}
						if !performanceFixtureHook(w, r, "app-1") {
							t.Errorf("unexpected performance read %s", r.URL.Path)
							w.WriteHeader(404)
						}
					}
				}))
				defer b.Close()
				opts := Options{APIBase: b.URL}
				if mode != "local" {
					opts.Environments = &EnvironmentConsent{}
					if mode == "approved" {
						opts.Environments.RequestReadApps = map[string]string{"alpha": "app-1"}
					}
					if mode == "replaced" {
						opts.Environments.RequestReadApps = map[string]string{"alpha": "old-app"}
					}
				}
				denied := mode == "denied" || mode == "replaced"
				cs, ctx := connectConfiguredTestServer(t, b, opts, version)
				for _, tool := range []string{"gregale_get_debug_coverage", "gregale_get_dependency_latency"} {
					out := callTool(t, ctx, cs, tool, performanceArgs{App: "alpha", Since: "1h"}, denied)
					if denied {
						if string(out["code"]) != `"request_access_denied"` || reads.Load() != 0 {
							t.Fatal("performance evidence read before consent")
						}
						continue
					}
					encoded, _ := json.Marshal(out)
					if strings.Contains(string(encoded), "excluded-") || strings.Contains(string(encoded), `"queries"`) || strings.Contains(string(encoded), `"trace_id"`) {
						t.Fatalf("excluded performance data leaked: %s", encoded)
					}
					if string(out["scope"]) != `"app_wide"` {
						t.Fatal("scope not explicit")
					}
					if tool == "gregale_get_debug_coverage" {
						if string(out["collection_completeness"]) != `"unknown"` || !strings.Contains(string(out["trace_linked"]), "70") {
							t.Fatalf("invented capture denominator: %s", out)
						}
					} else {
						if string(out["complete"]) != "false" || string(out["source_truncated"]) != "true" || !strings.Contains(string(out["dependencies"]), "[REDACTED]") {
							t.Fatalf("lost sampling or redaction: %s", out)
						}
						var edges []dependencyImpactEdgeView
						_ = json.Unmarshal(out["edges"], &edges)
						if len(edges) != 1 || len(edges[0].Exemplars) != 2 || edges[0].Exemplars[0].LookupIdentifier != "row-1" || edges[0].Exemplars[0].DurationBasis != "child_span_sample" || edges[0].Exemplars[0].Count != 7 || edges[0].Calls != 20 || !edges[0].LabelsModified || string(out["source_completeness"]) != `"unknown"` || string(out["returned_edge_count"]) != "1" {
							t.Fatal("dependency relationships, representative lookup or sampling limits lost")
						}
					}
				}
				before := reads.Load()
				prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_performance_diagnosis", Arguments: map[string]string{"app": "alpha", "since": "1h"}})
				if (err != nil) != denied {
					t.Fatalf("prompt=%+v err=%v", prompt, err)
				}
				if !denied {
					for _, needle := range []string{"gregale_get_debug_coverage", "gregale_get_dependency_latency", "gregale_list_requests", "independent observations", "collection completeness remains unknown", "Do not request SQL", "JSON Pointers", "uncalibrated label", "Unsupported findings", "selected span-chain", "not full request latency", "native_retained_may_be_bucketed", "guest_failure", "/regression/observation", "no_matching_observation", "two explicit deployment IDs", "dependency_comparison", "per-side sample counts", "inspect_dependency_change", "child_span_sample", "labels_modified", "synthetic", "gregale_get_request_analytics", "HTTP status >=400", "measurement_coverage not_reported", "neither a method filter nor historical until", "route_filter_usable", "gregale_get_request_analytics_timeseries", "partial hours", "timeseries_filter_usable", "Never average/sum", "gregale_list_error_groups", "gregale_list_error_requests", "inclusive group last-seen", "not selected-window totals", "evidence_availability not_checked", "gregale_get_wake_history", "keyset_started_at_instance_id_v1", "native_window_echo", "read_succeeded", "latest 100 instance candidates", "zero_or_not_reported", "not_echoed_by_native", "missing timestamps are unverified", "source_status not_reported", "admission concurrency ledger", "gregale_get_wake_timeline", "timestamp_only_not_lossless", "exclusive RFC3339 lower bound", "missing stages do not establish", "never subtract, sum", "native_retained_request_wake_id", "timeline_availability not_checked", "invalid_native_wake", "association_scope retained_row", "keyset_at_id_v1", "native_upgrade_required", "page_complete", "gregale_get_profiles", "native_nonzero_sample_entries", "whole deployment/runtime/window", "inclusive total CPU", "Recorded failed uploads"} {
						if !strings.Contains(prompt.Messages[0].Content.(*mcp.TextContent).Text, needle) {
							t.Errorf("prompt omitted %s", needle)
						}
					}
				}
				if reads.Load() != before {
					t.Fatal("prompt fetched performance evidence")
				}
				p := completionParams("app", "", nil)
				p.Ref.Name = "gregale_performance_diagnosis"
				res, err := cs.Complete(ctx, p)
				want := 1
				if mode == "local" {
					want = 2
				}
				if denied {
					want = 0
				}
				if err != nil || len(res.Completion.Values) != want {
					t.Fatalf("performance completion grew consent: %+v %v", res, err)
				}
				if mode == "approved" {
					callTool(t, ctx, cs, "gregale_get_debug_coverage", performanceArgs{App: "future", Since: "1h"}, true)
				}
			})
		}
	}
}

func TestPerformanceEmptyClampedAndTruncated(t *testing.T) {
	for _, mode := range []string{"empty", "clamped", "bounded", "complete"} {
		t.Run(mode, func(t *testing.T) {
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha" {
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					return
				}
				c, d := performanceFixtureCoverage("app-1"), performanceFixtureDependencies("app-1")
				if mode == "empty" {
					c.TelemetryRows, c.RepresentedRequests, c.ErrorRequests = 0, 0, 0
					c.TraceLinked, c.SpanEvidence = api.DebugCoverageSignal{}, api.DebugCoverageSignal{}
					c.OldestTelemetryAt, c.LatestTelemetryAt = "", ""
					d.TelemetryRows, d.RepresentedRequests, d.SpanSamples = 0, 0, 0
					d.Dependencies, d.Edges = nil, nil
					d.Complete, d.Truncated = true, false
				}
				if mode == "clamped" {
					c.Since, c.WindowStart = "30m", "2026-10-17T10:30:00Z"
					c.OldestTelemetryAt = ""
					d.Since, d.WindowStart = "30m", "2026-10-17T10:30:01Z"
					d.RetentionClamped = true
					d.Edges[0].Exemplars[1].ReceivedAt = "2026-10-17T10:40:00Z"
				}
				if mode == "bounded" {
					d.Complete, d.Truncated = true, false
					dep := d.Dependencies[0]
					dep.Name = strings.Repeat("x", api.MCPRequestFieldMaxBytes-4) + testToken + strings.Repeat("z", 30)
					d.Dependencies = make([]api.DebugDependencyLatencyItem, api.MCPPerformanceDependenciesMax+1)
					for i := range d.Dependencies {
						d.Dependencies[i] = dep
					}
				}
				if mode == "complete" {
					d.Complete, d.Truncated = true, false
				}
				if strings.HasSuffix(r.URL.Path, "/coverage") {
					_ = json.NewEncoder(w).Encode(c)
				} else {
					_ = json.NewEncoder(w).Encode(d)
				}
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			c := callTool(t, ctx, cs, "gregale_get_debug_coverage", performanceArgs{App: "alpha", Since: "1h"}, false)
			d := callTool(t, ctx, cs, "gregale_get_dependency_latency", performanceArgs{App: "alpha", Since: "1h"}, false)
			if string(c["collection_completeness"]) != `"unknown"` {
				t.Fatal("claimed global capture completeness")
			}
			if mode == "empty" && (string(c["status"]) != `"no_retained_telemetry"` || string(d["status"]) != `"no_retained_spans"` || string(d["dependencies"]) != "[]") {
				t.Fatal("empty evidence presented as health")
			}
			if mode == "clamped" {
				if string(c["retention_clamped"]) != "true" || string(c["retention_clamped_basis"]) != `"requested_and_effective_lookback"` || string(d["retention_clamped_basis"]) != `"native_response"` {
					t.Fatal("lost clamp provenance")
				}
			}
			if mode == "bounded" {
				var deps []dependencyLatencyView
				_ = json.Unmarshal(d["dependencies"], &deps)
				if len(deps) != api.MCPPerformanceDependenciesMax || string(d["dependencies_truncated"]) != "true" || string(d["text_truncated"]) != "true" || string(d["complete"]) != "false" || strings.Contains(deps[0].Name, "fp_test") {
					t.Fatal("unsafe or unbounded dependency projection")
				}
			}
			if mode == "complete" && string(d["complete"]) != "true" {
				t.Fatal("lost native bounded-read completeness")
			}
		})
	}
}

func TestPerformanceNativeErrorsAndInvalidResponses(t *testing.T) {
	for _, mode := range []string{"plan_gated", "not_retained", "replaced", "wrong_app", "bad_window", "bad_counts", "bad_rate", "false_clamp", "negative_latency"} {
		t.Run(mode, func(t *testing.T) {
			var changed atomic.Bool
			var reads atomic.Int32
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha" {
					id := "app-1"
					if changed.Load() {
						id = "replacement"
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
					return
				}
				reads.Add(1)
				if status := map[string]int{"plan_gated": 403, "not_retained": 404}[mode]; status != 0 {
					api.WriteProblem(w, api.NewProblem(status, "native_performance_error", "Unavailable", "native evidence denied"))
					return
				}
				c, d := performanceFixtureCoverage("app-1"), performanceFixtureDependencies("app-1")
				switch mode {
				case "replaced":
					changed.Store(true)
				case "wrong_app":
					c.AppID, d.AppID = "other-app", "other-app"
				case "bad_window":
					c.WindowEnd, d.WindowEnd = "invalid", "invalid"
				case "bad_counts":
					c.ErrorRequests = 100
					d.SpanSamples = -1
				case "bad_rate":
					c.TraceLinked.RatePct = 101
					d.Dependencies[0].ErrorRatePct = 101
				case "false_clamp":
					c.Since = "2h"
					d.Since, d.WindowStart = "30m", "2026-10-17T10:30:01Z"
				case "negative_latency":
					c.SpanEvidence.Requests = 100
					d.Dependencies[0].P95MS = -1
				}
				if strings.HasSuffix(r.URL.Path, "/coverage") {
					_ = json.NewEncoder(w).Encode(c)
				} else {
					_ = json.NewEncoder(w).Encode(d)
				}
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			for _, tool := range []string{"gregale_get_debug_coverage", "gregale_get_dependency_latency"} {
				changed.Store(false)
				before := reads.Load()
				out := callTool(t, ctx, cs, tool, performanceArgs{App: "alpha", Since: "1h"}, true)
				if reads.Load() != before+1 {
					t.Fatal("native error caused fallback or extra fetch")
				}
				if out["dependencies"] != nil || out["trace_linked"] != nil {
					t.Fatal("released invalid performance evidence")
				}
				if (mode == "plan_gated" || mode == "not_retained") && string(out["code"]) != `"native_performance_error"` {
					t.Fatal("native error not preserved")
				}
			}
		})
	}
}

func TestPerformancePromptValidation(t *testing.T) {
	for _, args := range []map[string]string{{"app": "alpha", "since": "bogus"}, {"app": "alpha", "since": "0h"}, {"app": "alpha", "since": "999999999999d"}, {"app": "alpha", "route": "/orders"}, {"app": "alpha", "deployment_id": discoveryDeploymentID}, {"since": "1h"}} {
		if _, err := parsePerformancePrompt(args); err == nil {
			t.Fatalf("accepted invalid performance selector: %v", args)
		}
	}
}
