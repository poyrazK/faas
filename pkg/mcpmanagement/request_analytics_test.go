// adr: 1631
package mcpmanagement

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const analyticsUntil = "2026-10-16T11:00:03Z"

func requestAnalyticsFixture() api.RequestAnalyticsResponse {
	zero, twenty, hundred, eight := 0, 20, 100, 8
	return api.RequestAnalyticsResponse{Slug: "alpha", Since: "1h", From: "2026-10-16T10:00:03Z", Until: analyticsUntil, AsOf: "2026-10-16T11:00:04Z", GroupBy: "route",
		Requests: 10, ErrorRequests: 7, ErrorRatePct: 70, ColdBoots: 7, P95MS: 100, P99MS: 150,
		RoutesLimit: 50, RoutesTruncated: true, GroupsTruncated: true,
		Groups:          []api.RequestAnalyticsGroup{{Value: "excluded-consumer-identity"}},
		ComputeCost:     &api.RequestAnalyticsComputeCost{Basis: "excluded-billing"},
		DeploymentCosts: &api.RequestAnalyticsDeploymentCostBreakdown{Deployments: []api.RequestAnalyticsDeploymentCost{{DeploymentID: "excluded-deployment"}}},
		Routes: []api.RequestAnalyticsRoute{
			{Route: "/orders?excluded-query", Method: "GET", Requests: 7, ErrorRequests: 7, ErrorRatePct: 100, ColdBoots: 7, P95MS: 100, P99MS: 150,
				ColdRequestP95MS: &hundred, GuestExecutionP50MS: &zero, GuestExecutionP95MS: &twenty, GuestCPUAvgMS: &zero, GuestCPUP95MS: &zero, GuestPeakRSSMaxMB: &eight,
				Dependencies: []api.RequestAnalyticsDependency{{Name: "excluded-dependency"}}, DeploymentObservations: []api.RequestAnalyticsRouteDeploymentObservation{{DeploymentID: "excluded-deployment"}}, EstimatedComputeCostMillicents: 99},
			{Route: "/zero", Method: "POST", Requests: 3},
			{Route: "/omitted", Method: "GET", Requests: 1},
		}}
}

func emptyRequestAnalyticsFixture() api.RequestAnalyticsResponse {
	n := requestAnalyticsFixture()
	n.Requests, n.ErrorRequests, n.ColdBoots, n.ErrorRatePct, n.P95MS, n.P99MS = 0, 0, 0, 0, 0, 0
	n.Routes, n.Groups, n.RoutesTruncated, n.GroupsTruncated = nil, nil, false, false
	return n
}

func TestRequestAnalyticsConsentAndProjection(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "approved", "denied", "replaced", "allowlist_denied"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				var reads atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Error("analytics wrote native state")
						w.WriteHeader(405)
						return
					}
					switch r.URL.Path {
					case "/v1/apps/alpha":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					case "/v1/apps/alpha/analytics":
						reads.Add(1)
						q := r.URL.Query()
						if q.Get("group_by") != "route" || q.Get("since") != "1h" || q.Get("until") != analyticsUntil || len(q) != 3 {
							t.Error("native analytics selection changed or projection limit forwarded")
						}
						_ = json.NewEncoder(w).Encode(requestAnalyticsFixture())
					default:
						t.Errorf("unexpected analytics read %s", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer b.Close()
				opts := Options{APIBase: b.URL}
				if mode != "local" && mode != "allowlist_denied" {
					opts.Environments = &EnvironmentConsent{RequestReadApps: map[string]string{}}
					if mode == "approved" {
						opts.Environments.RequestReadApps["alpha"] = "app-1"
					} else if mode == "replaced" {
						opts.Environments.RequestReadApps["alpha"] = "old-app"
					}
				}
				if mode == "allowlist_denied" {
					opts.AllowedApps = map[string]string{"other": "other-app"}
				}
				denied := mode == "denied" || mode == "replaced" || mode == "allowlist_denied"
				cs, ctx := connectConfiguredTestServer(t, b, opts, version)
				out := callTool(t, ctx, cs, "gregale_get_request_analytics", requestAnalyticsArgs{App: "alpha", Since: "1h", Until: analyticsUntil, Limit: 2}, denied)
				if denied {
					if reads.Load() != 0 || mode != "allowlist_denied" && string(out["code"]) != `"request_access_denied"` {
						t.Fatal("analytics read before per-app consent/allowlist validation")
					}
					return
				}
				encoded, _ := json.Marshal(out)
				for _, marker := range []string{"excluded-", `"groups"`, `"compute_cost"`, `"deployment_costs"`, `"deployment_observations"`, `"dependencies"`, `"estimated_compute_cost_millicents"`} {
					if strings.Contains(string(encoded), marker) {
						t.Errorf("excluded analytics data leaked: %s", marker)
					}
				}
				var v requestAnalyticsView
				_ = json.Unmarshal(encoded, &v)
				if reads.Load() != 1 || v.AppID != "app-1" || v.Scope != "app_wide" || v.SourceCompleteness != "unknown" || v.Freshness != "unknown" || v.Until != analyticsUntil || v.ErrorBasis != "http_status_gte_400" || v.Basis != "retained_requests_weighted_by_collapse_count" {
					t.Fatal("analytics scope, windows or measurement basis lost")
				}
				if v.Summary.P50MS == nil || *v.Summary.P50MS != 0 || v.Summary.ErrorRequests != 7 || len(v.Routes) != 2 || v.NativeRouteCount != 3 || v.ReturnedRouteCount != 2 || v.NativeRoutesLimit != 50 || !v.NativeRoutesTruncated || !v.NativeGroupsTruncated || !v.RoutesTruncated {
					t.Fatal("observed zero or independent native/projection limits lost")
				}
				a, z := v.Routes[0], v.Routes[1]
				if a.Route != "/orders" || !a.RouteModified || a.RouteFilterUsable || a.TimeseriesFilterUsable || a.Measurements.MeasurementCoverage != "not_reported" || a.Measurements.GuestCPUAvgMS == nil || *a.Measurements.GuestCPUAvgMS != 0 || a.Measurements.WakeBootP95MS != nil || !z.RouteFilterUsable || !z.TimeseriesFilterUsable || z.Measurements.GuestCPUAvgMS != nil || z.P95MS == nil || *z.P95MS != 0 {
					t.Fatal("safe selectors, observed zero and missing measurement semantics lost")
				}
			})
		}
	}
}

func TestRequestAnalyticsSelection(t *testing.T) {
	for _, tc := range []struct {
		name, since, until, wantSince string
		limit                         int
		bad                           bool
	}{
		{name: "default"}, {name: "duration", since: "1h", wantSince: "1h"}, {name: "days", since: "3d", wantSince: "3d", limit: 50},
		{name: "absolute", since: "2026-10-16T12:00:03+02:00", until: "2026-10-16T13:00:03+02:00", wantSince: "2026-10-16T10:00:03Z"},
		{name: "negative", since: "-1h", bad: true}, {name: "zero", since: "0s", bad: true}, {name: "overflow", since: "9223372036854775807d", bad: true},
		{name: "bad_time", since: "2026-99-16T10:00:00Z", bad: true}, {name: "bad_until", until: "1h", bad: true},
		{name: "future", until: time.Now().Add(24 * time.Hour).Format(time.RFC3339), bad: true},
		{name: "reversed", since: analyticsUntil, until: "2026-10-16T10:00:03Z", bad: true}, {name: "equal", since: analyticsUntil, until: analyticsUntil, bad: true},
		{name: "long", since: strings.Repeat("1", api.MCPAnalyticsTimeMaxBytes+1), bad: true}, {name: "controls", since: "1h\n", bad: true},
		{name: "small_limit", limit: -1, bad: true}, {name: "large_limit", limit: api.MCPAnalyticsRoutesMax + 1, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, limit, err := analyticsSelection(requestAnalyticsArgs{App: "alpha", Since: tc.since, Until: tc.until, Limit: tc.limit})
			if (err != nil) != tc.bad {
				t.Fatalf("selection: %v", err)
			}
			if !tc.bad && (opts.GroupBy != "route" || opts.Since != tc.wantSince || limit < 1 || limit > 50 || tc.until != "" && opts.Until != analyticsUntil) {
				t.Fatalf("selection changed: %+v limit=%d", opts, limit)
			}
		})
	}
}

func TestRequestAnalyticsWindowValidation(t *testing.T) {
	for _, mode := range []string{"valid", "absolute", "default", "days", "clamped", "wrong_app", "wrong_group", "missing", "bad_since", "bad_as_of", "widened", "shifted_until", "false_clamp", "hidden_clamp"} {
		t.Run(mode, func(t *testing.T) {
			n := requestAnalyticsFixture()
			opts := api.AppRequestAnalyticsOptions{Since: "1h", Until: analyticsUntil, GroupBy: "route"}
			switch mode {
			case "absolute":
				opts.Since = "2026-10-16T10:00:03Z"
			case "default", "days":
				opts.Since, n.Since, n.From = "", "1d", "2026-10-15T11:00:03Z"
				if mode == "days" {
					opts.Since = "1d"
				}
			case "clamped", "hidden_clamp":
				n.Since, n.From, n.WindowClamped = "30m", "2026-10-16T10:30:03Z", mode == "clamped"
			case "wrong_app":
				n.Slug = "other"
			case "wrong_group":
				n.GroupBy = "consumer_id"
			case "missing":
				n.AsOf = ""
			case "bad_since":
				n.Since = "2h"
			case "bad_as_of":
				n.AsOf = "2026-10-16T10:00:03Z"
			case "widened":
				n.Since, n.From = "2h", "2026-10-16T09:00:03Z"
			case "shifted_until":
				n.From, n.Until = "2026-10-16T09:00:03Z", "2026-10-16T10:00:03Z"
			case "false_clamp":
				n.WindowClamped = true
			}
			good := mode == "valid" || mode == "absolute" || mode == "default" || mode == "days" || mode == "clamped"
			if err := validateAnalyticsWindow("alpha", opts, n); (err == nil) != good {
				t.Fatalf("window validation: %v", err)
			}
		})
	}
}

func TestRequestAnalyticsEvidenceGuards(t *testing.T) {
	for _, mode := range []string{"empty", "redacted", "method_prefix", "app_changed", "plan_gated", "native_error", "bad_metadata", "unknown_argument"} {
		t.Run(mode, func(t *testing.T) {
			var reads atomic.Int32
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha" {
					id := "app-1"
					if mode == "app_changed" && reads.Load() > 0 {
						id = "replacement"
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
					return
				}
				if r.URL.Path != "/v1/apps/alpha/analytics" || r.Method != http.MethodGet {
					t.Error("unexpected native call")
				}
				reads.Add(1)
				n := requestAnalyticsFixture()
				switch mode {
				case "empty":
					n = emptyRequestAnalyticsFixture()
				case "redacted":
					n.Routes[0].Route = "/" + testToken + strings.Repeat("界", api.MCPRequestFieldMaxBytes) + "?excluded-query"
					n.Routes[0].Method = "GET " + testToken
				case "method_prefix":
					n.Routes[0].Route = "GET /orders"
				case "bad_metadata":
					n.From = ""
				case "plan_gated", "native_error":
					status := 403
					if mode == "native_error" {
						status = 503
					}
					api.WriteProblem(w, api.NewProblem(status, "native_analytics_error", "Unavailable", "analytics unavailable"))
					return
				}
				_ = json.NewEncoder(w).Encode(n)
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			args := map[string]any{"app": "alpha", "since": "1h", "until": analyticsUntil}
			if mode == "unknown_argument" {
				args["group_by"] = "consumer_id"
				result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_get_request_analytics", Arguments: args})
				if err != nil || !result.IsError || reads.Load() != 0 {
					t.Fatal("unknown grouping bypassed the input schema")
				}
				return
			}
			bad := mode != "empty" && mode != "redacted" && mode != "method_prefix"
			out := callTool(t, ctx, cs, "gregale_get_request_analytics", args, bad)
			if bad {
				if reads.Load() != 1 || out["routes"] != nil {
					t.Fatal("invalid evidence exposed or native error triggered a fallback")
				}
				return
			}
			if mode == "empty" {
				var summary map[string]json.RawMessage
				_ = json.Unmarshal(out["summary"], &summary)
				if string(out["status"]) != `"no_retained_requests"` || string(out["routes"]) != "[]" || summary["p50_ms"] != nil || string(summary["latency_status"]) != `"no_retained_requests"` {
					t.Fatal("empty analytics invented measured zero")
				}
				return
			}
			var rows []analyticsRouteView
			_ = json.Unmarshal(out["routes"], &rows)
			if rows[0].RouteFilterUsable {
				t.Fatal("manufactured a discovery selector")
			}
			if mode == "method_prefix" && (rows[0].Route != "GET /orders" || rows[0].RouteModified || !rows[0].TimeseriesFilterUsable) {
				t.Fatal("rewrote native method-prefixed route")
			}
			if mode == "redacted" && (!rows[0].RouteModified || !rows[0].MethodModified || rows[0].TimeseriesFilterUsable || len(rows[0].Route) > api.MCPRequestFieldMaxBytes || string(out["text_truncated"]) != "true" || strings.Contains(string(out["routes"]), testToken)) {
				t.Fatal("unbounded or unredacted telemetry label")
			}
		})
	}
}

func TestRequestAnalyticsInvalidMetrics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*api.RequestAnalyticsResponse)
	}{
		{"negative_count", func(n *api.RequestAnalyticsResponse) { n.Requests = -1 }},
		{"errors_exceed_count", func(n *api.RequestAnalyticsResponse) { n.ErrorRequests = 11 }},
		{"cold_exceeds_count", func(n *api.RequestAnalyticsResponse) { n.ColdBoots = 11 }},
		{"rate_nan", func(n *api.RequestAnalyticsResponse) { n.ErrorRatePct = math.NaN() }},
		{"rate_range", func(n *api.RequestAnalyticsResponse) { n.ErrorRatePct = 101 }},
		{"percentiles", func(n *api.RequestAnalyticsResponse) { n.P50MS = 101 }},
		{"empty_latency", func(n *api.RequestAnalyticsResponse) { *n = emptyRequestAnalyticsFixture(); n.P99MS = 1 }},
		{"missing_limit", func(n *api.RequestAnalyticsResponse) { n.RoutesLimit = 0 }},
		{"exceeded_limit", func(n *api.RequestAnalyticsResponse) { n.RoutesLimit = 2 }},
		{"row_count", func(n *api.RequestAnalyticsResponse) { n.Routes[0].Requests = -1 }},
		{"negative_measurement", func(n *api.RequestAnalyticsResponse) { *n.Routes[0].GuestCPUAvgMS = -1 }},
		{"guest_order", func(n *api.RequestAnalyticsResponse) { *n.Routes[0].GuestExecutionP50MS = 21 }},
		{"measurement_without_count", func(n *api.RequestAnalyticsResponse) {
			n.Routes[0] = api.RequestAnalyticsRoute{GuestCPUAvgMS: new(int)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := requestAnalyticsFixture()
			tc.mutate(&n)
			if _, err := (&management{}).projectRequestAnalytics("alpha", "app-1", n, 2); err == nil {
				t.Fatal("accepted invalid exposed metrics")
			}
		})
	}
}
