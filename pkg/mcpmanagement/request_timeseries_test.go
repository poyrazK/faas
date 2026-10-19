// adr: 1632
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

const timeseriesUntil = "2026-10-16T13:15:03Z"

func requestTimeseriesFixture(route, method string, empty bool) api.RequestAnalyticsTimeseriesResponse {
	n := api.RequestAnalyticsTimeseriesResponse{Slug: "alpha", Route: route, Method: method, Since: "3h45m", From: "2026-10-16T09:30:03Z", Until: timeseriesUntil, AsOf: "2026-10-16T13:16:03Z", Bucket: "1h",
		Points: []api.RequestAnalyticsTimeseriesPoint{
			{Start: "2026-10-16T09:00:00Z", Requests: 7, ErrorRequests: 7, ErrorRatePct: 100, ColdBoots: 7, P95MS: 100, P99MS: 150},
			{Start: "2026-10-16T10:00:00Z"},
			{Start: "2026-10-16T11:00:00Z", Requests: 3},
			{Start: "2026-10-16T12:00:00Z"},
			{Start: "2026-10-16T13:00:00Z", Requests: 1, P50MS: 1, P95MS: 1, P99MS: 1},
		}}
	if empty {
		for i, row := range n.Points {
			n.Points[i] = api.RequestAnalyticsTimeseriesPoint{Start: row.Start}
		}
	}
	return n
}

func TestRequestTimeseriesConsentAndProjection(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "approved", "denied", "replaced", "allowlist_denied"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				var reads atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Error("timeseries wrote native state")
						w.WriteHeader(405)
						return
					}
					switch r.URL.Path {
					case "/v1/apps/alpha":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					case "/v1/apps/alpha/analytics/timeseries":
						reads.Add(1)
						q := r.URL.Query()
						if q.Get("since") != "3h45m" || q.Get("until") != timeseriesUntil || q.Get("route") != "GET /orders" || q.Get("method") != "GET" || len(q) != 4 {
							t.Error("changed exact native pair/window or forwarded output/grouping arguments")
						}
						_ = json.NewEncoder(w).Encode(requestTimeseriesFixture("GET /orders", "GET", false))
					default:
						t.Errorf("unexpected timeseries read %s", r.URL.Path)
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
				args := requestTimeseriesArgs{App: "alpha", Since: "3h45m", Until: timeseriesUntil, Route: "GET /orders", Method: "get", Limit: 4}
				out := callTool(t, ctx, cs, "gregale_get_request_analytics_timeseries", args, denied)
				if denied {
					if reads.Load() != 0 || mode != "allowlist_denied" && string(out["code"]) != `"request_access_denied"` {
						t.Fatal("timeseries read before consent validation")
					}
					return
				}
				encoded, _ := json.Marshal(out)
				var v requestTimeseriesView
				_ = json.Unmarshal(encoded, &v)
				if reads.Load() != 1 || v.AppID != "app-1" || v.Scope != "app_wide" || v.SeriesScope != "route_method" || v.Route != args.Route || v.Method != "GET" || v.Until != args.Until || v.SourceCompleteness != "unknown" || v.Freshness != "unknown" || v.ErrorBasis != "http_status_gte_400" || v.Order != "oldest_first" {
					t.Fatal("lost exact pair, native window, scope or uncertainty")
				}
				if len(v.Points) != 4 || v.NativePointCount != 5 || v.ReturnedPointCount != 4 || !v.PointsTruncated || !v.Points[0].Partial || v.Points[0].WindowFrom != v.From || v.Points[0].WindowUntil != "2026-10-16T10:00:00Z" || v.Points[0].P50MS == nil || *v.Points[0].P50MS != 0 || v.Points[1].P50MS != nil || !v.Points[1].ZeroFilled || v.Points[2].ZeroFilled || v.Points[2].P95MS == nil || *v.Points[2].P95MS != 0 {
					t.Fatal("partial hours, prefix limits, empty buckets or observed zero were misrepresented")
				}
				if mode == "approved" {
					args.App = "future"
					callTool(t, ctx, cs, "gregale_get_request_analytics_timeseries", args, true)
					if reads.Load() != 1 {
						t.Fatal("analytics grant widened to a future app")
					}
				}
			})
		}
	}
}

func TestRequestTimeseriesSelections(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       requestTimeseriesArgs
		bad        bool
		wantMethod string
	}{
		{name: "default"},
		{name: "day", args: requestTimeseriesArgs{Since: "3d", Limit: 168}},
		{name: "absolute", args: requestTimeseriesArgs{Since: "2026-10-16T11:30:03+02:00", Until: "2026-10-16T15:15:03+02:00"}},
		{name: "exact_path", args: requestTimeseriesArgs{Route: "/orders", Method: "POST"}, wantMethod: "POST"},
		{name: "native_prefix", args: requestTimeseriesArgs{Route: "GET /orders", Method: "get"}, wantMethod: "GET"},
		{name: "route_only", args: requestTimeseriesArgs{Route: "/orders"}, bad: true},
		{name: "method_only", args: requestTimeseriesArgs{Method: "GET"}, bad: true},
		{name: "unsupported_method", args: requestTimeseriesArgs{Route: "/orders", Method: "TRACE"}, bad: true},
		{name: "query", args: requestTimeseriesArgs{Route: "/orders?private", Method: "GET"}, bad: true},
		{name: "fragment", args: requestTimeseriesArgs{Route: "/orders#private", Method: "GET"}, bad: true},
		{name: "controls", args: requestTimeseriesArgs{Route: "/orders\n", Method: "GET"}, bad: true},
		{name: "invalid_utf8", args: requestTimeseriesArgs{Route: "/\xff", Method: "GET"}, bad: true},
		{name: "blank_route", args: requestTimeseriesArgs{Route: "  ", Method: "GET"}, bad: true},
		{name: "credential", args: requestTimeseriesArgs{Route: "/" + testToken, Method: "GET"}, bad: true},
		{name: "long_route", args: requestTimeseriesArgs{Route: "/" + strings.Repeat("界", 100), Method: "GET"}, bad: true},
		{name: "small_limit", args: requestTimeseriesArgs{Limit: -1}, bad: true},
		{name: "large_limit", args: requestTimeseriesArgs{Limit: api.MCPAnalyticsPointsMax + 1}, bad: true},
		{name: "bad_window", args: requestTimeseriesArgs{Since: "0s"}, bad: true},
		{name: "future_until", args: requestTimeseriesArgs{Until: time.Now().Add(24 * time.Hour).Format(time.RFC3339)}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.args.App = "alpha"
			opts, limit, err := (&management{}).analyticsTimeseriesSelection(tc.args)
			if (err != nil) != tc.bad {
				t.Fatalf("selection=%+v error=%v", opts, err)
			}
			if !tc.bad && (opts.GroupBy != "" || opts.Route != tc.args.Route || opts.Method != tc.wantMethod || limit < 1 || limit > 168 || tc.name == "absolute" && (opts.Since != "2026-10-16T09:30:03Z" || opts.Until != timeseriesUntil)) {
				t.Fatal("selection changed exact pair or historical window")
			}
		})
	}
}

func TestRequestTimeseriesNativeGuards(t *testing.T) {
	for _, mode := range []string{"app_wide", "empty", "clamped", "app_changed", "wrong_app", "wrong_pair", "wrong_group", "grouped_payload", "bad_bucket", "bad_window", "shifted_until", "plan_gated", "native_error", "unknown_argument"} {
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
				if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/alpha/analytics/timeseries" || len(r.URL.Query()) != 2 {
					t.Error("timeseries changed app-wide query or performed extra reads/writes")
				}
				reads.Add(1)
				n := requestTimeseriesFixture("", "", mode == "empty")
				switch mode {
				case "clamped":
					n.From, n.Since, n.WindowClamped, n.Points = "2026-10-16T11:30:03Z", "1h45m", true, n.Points[2:]
				case "wrong_app":
					n.Slug = "other"
				case "wrong_pair":
					n.Route, n.Method = "/orders", "GET"
				case "wrong_group":
					n.GroupBy = "consumer_id"
				case "grouped_payload":
					n.Series = []api.RequestAnalyticsTimeseriesSeries{{Value: "excluded-consumer"}}
				case "bad_bucket":
					n.Bucket = "5m"
				case "bad_window":
					n.AsOf = ""
				case "shifted_until":
					n.Until = "2026-10-16T13:15:04Z"
				case "plan_gated", "native_error":
					status := 403
					if mode == "native_error" {
						status = 503
					}
					api.WriteProblem(w, api.NewProblem(status, "native_timeseries_error", "Unavailable", "analytics unavailable"))
					return
				}
				_ = json.NewEncoder(w).Encode(n)
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			args := map[string]any{"app": "alpha", "since": "3h45m", "until": timeseriesUntil}
			if mode == "unknown_argument" {
				args["group_by"] = "consumer_id"
				result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_get_request_analytics_timeseries", Arguments: args})
				if err != nil || !result.IsError || reads.Load() != 0 {
					t.Fatal("unknown grouping bypassed the schema")
				}
				return
			}
			good := mode == "app_wide" || mode == "empty" || mode == "clamped"
			out := callTool(t, ctx, cs, "gregale_get_request_analytics_timeseries", args, !good)
			if reads.Load() != 1 || !good && out["points"] != nil {
				t.Fatal("invalid native evidence exposed or error triggered fallback")
			}
			if !good {
				return
			}
			var rows []analyticsPointView
			_ = json.Unmarshal(out["points"], &rows)
			last := rows[len(rows)-1]
			if string(out["series_scope"]) != `"app_wide"` || !last.Partial || last.WindowUntil != timeseriesUntil || last.End != "2026-10-16T14:00:00Z" {
				t.Fatal("partial final-hour or app-wide scope lost")
			}
			if mode == "empty" && (string(out["status"]) != `"no_retained_requests"` || len(rows) != 5 || rows[0].P50MS != nil || !rows[0].ZeroFilled) {
				t.Fatal("zero-filled empty grid invented latency or health")
			}
			if mode == "clamped" && (len(rows) != 3 || rows[0].WindowFrom != "2026-10-16T11:30:03Z" || string(out["window_clamped"]) != "true") {
				t.Fatal("lost native retention clamp")
			}
		})
	}
}

func TestRequestTimeseriesGridAndMetrics(t *testing.T) {
	for _, mode := range []string{"valid", "hour_aligned", "empty_prefix", "missing", "extra", "duplicate", "unsorted", "non_hour", "bad_time", "negative_count", "error_count", "cold_count", "bad_rate", "bad_order", "empty_latency", "omitted_invalid"} {
		t.Run(mode, func(t *testing.T) {
			n := requestTimeseriesFixture("", "", false)
			limit := 4
			switch mode {
			case "hour_aligned":
				n.From, n.Until, n.Points = "2026-10-16T10:00:00Z", "2026-10-16T13:00:00Z", n.Points[1:4]
			case "empty_prefix":
				n = requestTimeseriesFixture("", "", true)
				n.Points[4].Requests = 1
			case "missing":
				n.Points = n.Points[:4]
			case "extra":
				n.Points = append(n.Points, api.RequestAnalyticsTimeseriesPoint{Start: "2026-10-16T14:00:00Z"})
			case "duplicate":
				n.Points[1].Start = n.Points[0].Start
			case "unsorted":
				n.Points[0], n.Points[1] = n.Points[1], n.Points[0]
			case "non_hour":
				n.Points[0].Start = "2026-10-16T09:30:00Z"
			case "bad_time":
				n.Points[0].Start = "invalid"
			case "negative_count":
				n.Points[0].Requests = -1
			case "error_count":
				n.Points[0].ErrorRequests = 8
			case "cold_count":
				n.Points[0].ColdBoots = 8
			case "bad_rate":
				n.Points[0].ErrorRatePct = math.NaN()
			case "bad_order":
				n.Points[0].P50MS = 101
			case "empty_latency":
				n.Points[1].P95MS, n.Points[1].P99MS = 1, 1
			case "omitted_invalid":
				n.Points[4].Requests = -1
			}
			good := mode == "valid" || mode == "hour_aligned" || mode == "empty_prefix"
			v, err := projectRequestTimeseries("alpha", "app-1", n, limit)
			if (err == nil) != good {
				t.Fatalf("projection=%+v error=%v", v, err)
			}
			if mode == "hour_aligned" && (len(v.Points) != 3 || v.Points[0].Partial || v.Points[2].Partial) {
				t.Fatal("included an extra bucket at the exclusive hour-aligned end")
			}
			if mode == "empty_prefix" && (v.Status != "observed" || !v.PointsTruncated || v.Points[0].Requests != 0) {
				t.Fatal("projected empty prefix hid evidence in omitted buckets")
			}
		})
	}
}

func TestRequestTimeseriesPrecisionAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, from, until string
		count             int
	}{
		{"exclusive_hour", "2026-10-16T10:00:00Z", "2026-10-16T11:00:00Z", 1},
		{"after_hour_microsecond", "2026-10-16T10:00:00Z", "2026-10-16T11:00:00.000001Z", 2},
		{"after_hour_nanosecond", "2026-10-16T10:00:00Z", "2026-10-16T11:00:00.000000001Z", 1},
		{"sub_microsecond", "2026-10-16T11:00:00Z", "2026-10-16T11:00:00.000000001Z", 0},
		{"default_prefix", "2026-10-09T00:00:00Z", "2026-10-16T01:00:00Z", 169},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := requestTimeseriesFixture("", "", true)
			n.From, n.Until, n.Points = tc.from, tc.until, nil
			from, _ := time.Parse(time.RFC3339Nano, tc.from)
			for i := range tc.count {
				n.Points = append(n.Points, api.RequestAnalyticsTimeseriesPoint{Start: from.Add(time.Duration(i) * time.Hour).Format(time.RFC3339Nano)})
			}
			for _, limit := range []int{api.MCPAnalyticsPointsDefault, api.MCPAnalyticsPointsMax} {
				v, err := projectRequestTimeseries("alpha", "app-1", n, limit)
				if err != nil || v.NativePointCount != tc.count || len(v.Points) != min(tc.count, limit) || v.PointsTruncated != (tc.count > limit) {
					t.Fatalf("precision/bounds projection: %+v %v", v, err)
				}
				if tc.count == 0 && (v.Status != "no_returned_buckets" || len(v.Points) != 0) {
					t.Fatal("invented buckets below native precision")
				}
			}
		})
	}
}
