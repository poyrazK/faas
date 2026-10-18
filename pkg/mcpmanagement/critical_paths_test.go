// adr: 1622
package mcpmanagement

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func criticalPathsFixture(appID string) api.DebugCriticalPathHistoryResponse {
	segment := api.DebugCriticalPathSegment{Type: "binding", Kind: "postgres", Name: "orders " + testToken}
	return api.DebugCriticalPathHistoryResponse{AppID: appID, Since: "1h", WindowStart: "2026-10-17T10:00:02Z", WindowEnd: "2026-10-17T11:00:02Z", Complete: false, Truncated: true, TelemetryRows: 2, RepresentedRequests: 10, PathSamples: 2,
		CriticalPaths: []api.DebugCriticalPathHistoryItem{{Signature: "application//handler > binding/postgres/orders " + testToken, Segments: []api.DebugCriticalPathSegment{{Type: "application", Name: "handler"}, segment}, DominantSegment: &segment, DominantSegmentExclusiveMS: 300,
			Calls: 10, ErrorCalls: 7, ErrorRatePct: 70, P50MS: 400, P95MS: 400, P99MS: 400, BaselineCalls: 3, CurrentCalls: 7, BaselineP95MS: 100, CurrentP95MS: 400, P95DeltaMS: 300, RegressionFactor: 4, Regression: false, CurrentErrorRatePct: 100, ErrorRateDeltaPct: 100,
			Exemplars: []api.DebugCriticalPathExemplar{{RequestID: "row-1", TraceID: "excluded-trace", Window: "current", ReceivedAt: "2026-10-17T10:59:00Z", DurationMS: 400, HTTPStatus: 503, Error: true, Count: 7, DominantSegment: &segment, DominantSegmentExclusiveMS: 300},
				{RequestID: "row-baseline", TraceID: "excluded-baseline-trace", Window: "baseline", ReceivedAt: "2026-10-17T10:20:00Z", DurationMS: 100, HTTPStatus: 200, Count: 3}}}}}
}

func emptyCriticalPathsFixture(appID string) api.DebugCriticalPathHistoryResponse {
	n := criticalPathsFixture(appID)
	n.TelemetryRows, n.RepresentedRequests, n.PathSamples = 0, 0, 0
	n.CriticalPaths, n.Complete, n.Truncated = nil, true, false
	return n
}

func TestCriticalPathsConsentProjectionAndPrompt(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "denied", "approved", "replaced"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				var reads atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						t.Error("critical-path inspection wrote native state")
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
					case "/v1/apps/alpha/debug/critical-paths":
						reads.Add(1)
						if r.URL.Query().Get("since") != "1h" || len(r.URL.Query()) != 1 {
							t.Error("projection limit changed the native selection")
						}
						_ = json.NewEncoder(w).Encode(criticalPathsFixture("app-1"))
					default:
						t.Errorf("unexpected native read %s", r.URL.Path)
						w.WriteHeader(404)
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
				out := callTool(t, ctx, cs, "gregale_get_critical_paths", criticalPathsArgs{App: "alpha", Since: "1h", Limit: 1}, denied)
				if denied {
					if string(out["code"]) != `"request_access_denied"` || reads.Load() != 0 {
						t.Fatal("critical paths read before diagnostic consent")
					}
				} else {
					encoded, _ := json.Marshal(out)
					for _, marker := range []string{"excluded-", testToken, `"trace_id"`, `"spans"`, `"attributes"`, `"db_statement"`} {
						if strings.Contains(string(encoded), marker) {
							t.Fatalf("excluded critical-path data leaked: %s", marker)
						}
					}
					var paths []criticalPathView
					_ = json.Unmarshal(out["critical_paths"], &paths)
					if len(paths) != 1 || paths[0].Segments[0].Name != "handler" || !strings.Contains(paths[0].Segments[1].Name, "[REDACTED]") || paths[0].Exemplars[0].LookupIdentifier != "row-1" || paths[0].Exemplars[0].Count != 7 || paths[0].Exemplars[1].Window != "baseline" || paths[0].DominantSegmentExclusiveMS != 300 || paths[0].P95DeltaMS != 300 {
						t.Fatal("lost ordered path, weights, dominant sample or representative lookup")
					}
					if string(out["scope"]) != `"app_wide"` || string(out["complete"]) != "false" || string(out["source_complete"]) != "false" || string(out["source_truncated"]) != "true" || string(out["path_samples"]) != "2" || string(out["represented_requests"]) != "10" {
						t.Fatal("lost sampling, scope or source limits")
					}
				}
				before := reads.Load()
				prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_performance_diagnosis", Arguments: map[string]string{"app": "alpha", "since": "1h"}})
				if (err != nil) != denied {
					t.Fatalf("prompt=%+v %v", prompt, err)
				}
				if !denied {
					for _, needle := range []string{"gregale_get_critical_paths", "not full request latency", "maximum observed sample", "selected aggregate representatives", "not establish deployment causality", "never route filters", "gregale_get_request_evidence"} {
						if !strings.Contains(prompt.Messages[0].Content.(*mcp.TextContent).Text, needle) {
							t.Errorf("prompt omitted %s", needle)
						}
					}
				}
				if reads.Load() != before {
					t.Fatal("prompt fetched critical-path evidence")
				}
				if mode == "approved" {
					callTool(t, ctx, cs, "gregale_get_critical_paths", criticalPathsArgs{App: "future", Since: "1h"}, true)
				}
			})
		}
	}
}

func TestCriticalPathsBoundsEmptyAndRetention(t *testing.T) {
	for _, mode := range []string{"empty", "no_paths_with_rows", "clamped", "complete", "paths_bound", "segments_bound", "exemplars_bound", "text_bound", "negative_delta", "default_window", "regression"} {
		t.Run(mode, func(t *testing.T) {
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha" {
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					return
				}
				n := criticalPathsFixture("app-1")
				n.Complete, n.Truncated = true, false
				switch mode {
				case "empty":
					n = emptyCriticalPathsFixture("app-1")
				case "no_paths_with_rows":
					n.PathSamples, n.CriticalPaths, n.Complete = 0, nil, false
				case "clamped":
					n.RetentionClamped = true
				case "paths_bound":
					for len(n.CriticalPaths) <= api.MCPCriticalPathsDefault {
						n.CriticalPaths = append(n.CriticalPaths, n.CriticalPaths[0])
					}
				case "segments_bound":
					for len(n.CriticalPaths[0].Segments) <= api.MCPCriticalPathSegmentsMax {
						n.CriticalPaths[0].Segments = append(n.CriticalPaths[0].Segments, n.CriticalPaths[0].Segments[0])
					}
				case "exemplars_bound":
					for len(n.CriticalPaths[0].Exemplars) <= api.MCPCriticalPathExemplarsMax {
						n.CriticalPaths[0].Exemplars = append(n.CriticalPaths[0].Exemplars, n.CriticalPaths[0].Exemplars[0])
					}
				case "text_bound":
					n.CriticalPaths[0].Signature = strings.Repeat("界", api.MCPRequestFieldMaxBytes) + testToken
				case "negative_delta":
					n.CriticalPaths[0].P95DeltaMS, n.CriticalPaths[0].ErrorRateDeltaPct = -300, -70
				case "regression":
					n.RepresentedRequests, n.CriticalPaths[0].Calls, n.CriticalPaths[0].BaselineCalls = 12, 12, 5
					n.CriticalPaths[0].ErrorRatePct, n.CriticalPaths[0].Regression, n.CriticalPaths[0].Exemplars[1].Count = 58.33, true, 5
				case "default_window":
					if r.URL.RawQuery != "" {
						t.Error("native default lookback not preserved")
					}
					n.Since, n.WindowStart, n.WindowEnd = "24h", "2026-10-17T00:00:00Z", "2026-10-18T00:00:00Z"
					n.CriticalPaths[0].BaselineCalls, n.CriticalPaths[0].CurrentCalls = 10, 0
					n.CriticalPaths[0].BaselineP95MS, n.CriticalPaths[0].CurrentP95MS, n.CriticalPaths[0].P95DeltaMS = 400, 0, -400
					n.CriticalPaths[0].BaselineErrorRatePct, n.CriticalPaths[0].CurrentErrorRatePct, n.CriticalPaths[0].ErrorRateDeltaPct, n.CriticalPaths[0].RegressionFactor = 70, 0, -70, 0
					for i := range n.CriticalPaths[0].Exemplars {
						n.CriticalPaths[0].Exemplars[i].Window = "baseline"
					}
				}
				_ = json.NewEncoder(w).Encode(n)
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			args := criticalPathsArgs{App: "alpha", Since: "1h"}
			if mode == "clamped" {
				args.Since = "3d"
			}
			if mode == "default_window" {
				args.Since = ""
			}
			out := callTool(t, ctx, cs, "gregale_get_critical_paths", args, false)
			var paths []criticalPathView
			_ = json.Unmarshal(out["critical_paths"], &paths)
			switch mode {
			case "empty", "no_paths_with_rows":
				if string(out["status"]) != `"no_returned_paths"` || len(paths) != 0 {
					t.Fatal("empty evidence was presented as observed")
				}
			case "clamped":
				if string(out["retention_clamped"]) != "true" || string(out["retention_clamped_basis"]) != `"native_response"` || string(out["since"]) != `"1h"` {
					t.Fatal("native retention clamp lost")
				}
			case "regression":
				if !paths[0].Regression || paths[0].RegressionFactor != 4 || paths[0].BaselineCalls != 5 {
					t.Fatal("lost native regression signal")
				}
			case "complete", "negative_delta", "default_window":
				if string(out["complete"]) != "true" {
					t.Fatal("native completeness or signed deltas lost")
				}
			case "paths_bound":
				if len(paths) != api.MCPCriticalPathsDefault || string(out["paths_truncated"]) != "true" || string(out["complete"]) != "false" {
					t.Fatal("path bound lost")
				}
			case "segments_bound":
				if len(paths[0].Segments) != api.MCPCriticalPathSegmentsMax || !paths[0].SegmentsTruncated || string(out["complete"]) != "false" {
					t.Fatal("segment bound lost")
				}
			case "exemplars_bound":
				if len(paths[0].Exemplars) != api.MCPCriticalPathExemplarsMax || !paths[0].ExemplarsTruncated || string(out["complete"]) != "false" {
					t.Fatal("exemplar bound lost")
				}
			case "text_bound":
				if string(out["text_truncated"]) != "true" || len(paths[0].Signature) > api.MCPRequestFieldMaxBytes || string(out["complete"]) != "false" {
					t.Fatal("text bound lost")
				}
			}
		})
	}
}

func TestCriticalPathsRejectsNativeErrorsOrChangedEvidence(t *testing.T) {
	for _, mode := range []string{"plan_gated", "not_retained", "replaced", "wrong_app", "bad_window", "widened_window", "false_clamp", "bad_samples", "no_samples", "bad_calls", "bad_halves", "overflow_halves", "bad_rate", "bad_factor", "negative_duration", "bad_lookup", "secret_lookup", "bad_timestamp", "bad_half", "outside_window", "bad_exemplar_count", "excess_exemplar_count", "bad_status", "negative_exemplar_duration", "empty_segments"} {
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
					api.WriteProblem(w, api.NewProblem(status, "native_critical_path_error", "Unavailable", "native evidence denied"))
					return
				}
				n := criticalPathsFixture("app-1")
				path := &n.CriticalPaths[0]
				switch mode {
				case "replaced":
					changed.Store(true)
				case "wrong_app":
					n.AppID = "foreign"
				case "bad_window":
					n.WindowEnd = "invalid"
				case "widened_window":
					n.Since, n.WindowStart = "2h", "2026-10-17T09:00:02Z"
				case "false_clamp":
					n.Since, n.WindowStart = "30m", "2026-10-17T10:30:02Z"
				case "bad_samples":
					n.PathSamples = 3
				case "no_samples":
					n.PathSamples = 0
				case "bad_calls":
					path.ErrorCalls = 11
				case "bad_halves":
					path.CurrentCalls = 8
				case "overflow_halves":
					path.BaselineCalls, path.CurrentCalls = math.MaxInt64, math.MaxInt64
				case "bad_rate":
					path.ErrorRatePct = 101
				case "bad_factor":
					path.RegressionFactor = -1
				case "negative_duration":
					path.P95MS = -1
				case "bad_lookup":
					path.Exemplars[0].RequestID = ".."
				case "secret_lookup":
					path.Exemplars[0].RequestID = testToken
				case "bad_timestamp":
					path.Exemplars[0].ReceivedAt = "invalid"
				case "bad_half":
					path.Exemplars[0].Window = "baseline"
				case "outside_window":
					path.Exemplars[0].ReceivedAt = n.WindowEnd
				case "bad_exemplar_count":
					path.Exemplars[0].Count = 0
				case "excess_exemplar_count":
					path.Exemplars[0].Count = 8
				case "bad_status":
					path.Exemplars[0].HTTPStatus = 700
				case "negative_exemplar_duration":
					path.Exemplars[0].DominantSegmentExclusiveMS = -1
				case "empty_segments":
					path.Segments = nil
				}
				_ = json.NewEncoder(w).Encode(n)
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			out := callTool(t, ctx, cs, "gregale_get_critical_paths", criticalPathsArgs{App: "alpha", Since: "1h"}, true)
			if reads.Load() != 1 || out["critical_paths"] != nil {
				t.Fatal("invalid evidence released or native read retried")
			}
			if (mode == "plan_gated" || mode == "not_retained") && string(out["code"]) != `"native_critical_path_error"` {
				t.Fatal("native error not preserved")
			}
		})
	}
}

func TestCriticalPathsInvalidSelectors(t *testing.T) {
	for _, args := range []criticalPathsArgs{{App: "alpha", Since: "0h"}, {App: "alpha", Since: "999999999999d"}, {App: "alpha", Limit: -1}, {App: "alpha", Limit: 11}, {App: "../alpha"}} {
		t.Run(args.App+args.Since, func(t *testing.T) {
			var reads atomic.Int32
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reads.Add(1); w.WriteHeader(500) }))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			callTool(t, ctx, cs, "gregale_get_critical_paths", args, true)
			if reads.Load() != 0 {
				t.Fatal("invalid selector reached native API")
			}
		})
	}
}
