// adr: 1623
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

const comparisonTargetID = "22222222-2222-4222-8222-222222222222"
const comparisonUntil = "2026-10-16T11:00:00Z"

func comparisonFixture(appID string) api.DebugCompareResponse {
	clamped := false
	return api.DebugCompareResponse{AppID: appID, Source: discoveryDeploymentID, Mirror: comparisonTargetID, Since: "1h", WindowStart: "2026-10-16T10:00:00Z", WindowEnd: comparisonUntil, RetentionClamped: &clamped,
		Routes: []api.DebugCompareRouteStats{{Route: "/orders", SourceP50: 0, SourceP95: 50, SourceP99: 100, SourceN: 7, MirrorP50: 20, MirrorP95: 100, MirrorP99: 200, MirrorN: 10}, {Route: "/source-only", SourceP50: 10, SourceP95: 20, SourceP99: 30, SourceN: 3}}}
}

func comparisonFixtureArgs() compareDeploymentsArgs {
	return compareDeploymentsArgs{App: "alpha", SourceDeploymentID: discoveryDeploymentID, TargetDeploymentID: comparisonTargetID, Since: "1h", Until: comparisonUntil}
}

func TestDeploymentComparisonConsentAndProjection(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "denied", "approved", "replaced"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				var posts, deployments atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/v1/apps/alpha":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					case "/v1/deployments/" + discoveryDeploymentID, "/v1/deployments/" + comparisonTargetID:
						deployments.Add(1)
						id := strings.TrimPrefix(r.URL.Path, "/v1/deployments/")
						scope := "staging"
						if id == comparisonTargetID {
							scope = "production"
						}
						_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: id, AppID: "app-1", Scope: scope})
					case "/v1/apps/alpha/debug/compare":
						posts.Add(1)
						var req api.DebugCompareRequest
						if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&req) != nil || req.Source != discoveryDeploymentID || req.Mirror != comparisonTargetID || req.Since != "1h" || req.Until != comparisonUntil || r.URL.RawQuery != "" {
							t.Error("read-only native comparison selection changed")
						}
						_ = json.NewEncoder(w).Encode(comparisonFixture("app-1"))
					default:
						t.Errorf("unexpected comparison read %s", r.URL.Path)
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
				out := callTool(t, ctx, cs, "gregale_compare_deployments", comparisonFixtureArgs(), denied)
				if denied {
					if posts.Load() != 0 || deployments.Load() != 0 || string(out["code"]) != `"request_access_denied"` {
						t.Fatal("comparison accessed before consent")
					}
					return
				}
				if posts.Load() != 1 || deployments.Load() != 4 || string(out["source_scope"]) != `"staging"` || string(out["target_scope"]) != `"production"` || string(out["source_completeness"]) != `"unknown"` {
					t.Fatal("consent, deployment pins or source limits lost")
				}
				var rows []compareRouteView
				_ = json.Unmarshal(out["routes"], &rows)
				if len(rows) != 2 || !rows[0].Comparable || rows[0].Source.Count != 7 || rows[0].Source.P50MS == nil || *rows[0].Source.P50MS != 0 || rows[1].Comparable || rows[1].Target.P95MS != nil || rows[1].Target.Evidence != "no_retained_samples" {
					t.Fatal("observed zero or absent side was misrepresented")
				}
				before := posts.Load()
				prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_performance_diagnosis", Arguments: map[string]string{"app": "alpha", "since": "1h"}})
				if err != nil || !strings.Contains(prompt.Messages[0].Content.(*mcp.TextContent).Text, "gregale_compare_deployments") || posts.Load() != before {
					t.Fatal("comparison guidance missing or prompt fetched evidence")
				}
			})
		}
	}
}

func TestDeploymentComparisonEvidenceGuards(t *testing.T) {
	for _, mode := range []string{"empty", "bounded", "redacted", "clamped", "default", "legacy", "wrong_app", "wrong_source", "wrong_target", "wrong_filter", "wrong_until", "wrong_window", "false_clamp", "negative_count", "bad_order", "missing_side_values", "both_missing", "duplicate_route", "filter_mismatch", "foreign_before", "foreign_after", "scope_changed", "app_changed", "plan_gated", "native_error"} {
		t.Run(mode, func(t *testing.T) {
			var posts atomic.Int32
			var reads atomic.Int32
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha" {
					id := "app-1"
					if mode == "app_changed" && posts.Load() > 0 {
						id = "replacement"
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v1/deployments/") {
					reads.Add(1)
					id := strings.TrimPrefix(r.URL.Path, "/v1/deployments/")
					appID, scope := "app-1", "staging"
					if mode == "foreign_before" || mode == "foreign_after" && posts.Load() > 0 {
						appID = "foreign"
					}
					if mode == "scope_changed" && posts.Load() > 0 {
						scope = "production"
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: id, AppID: appID, Scope: scope})
					return
				}
				posts.Add(1)
				if mode == "plan_gated" || mode == "native_error" {
					status := 403
					if mode == "native_error" {
						status = 503
					}
					api.WriteProblem(w, api.NewProblem(status, "native_compare_error", "Unavailable", "comparison unavailable"))
					return
				}
				n := comparisonFixture("app-1")
				switch mode {
				case "empty":
					n.Routes = nil
				case "bounded":
					for len(n.Routes) <= api.MCPCompareRoutesDefault {
						n.Routes = append(n.Routes, api.DebugCompareRouteStats{Route: "/route-" + strings.Repeat("x", len(n.Routes)), SourceN: 1})
					}
				case "redacted":
					n.Routes[0].Route = "/" + testToken + strings.Repeat("界", api.MCPRequestFieldMaxBytes) + "?excluded-query"
				case "clamped":
					*n.RetentionClamped = true
				case "legacy":
					n.AppID = ""
					n.RetentionClamped = nil
				case "wrong_app":
					n.AppID = "foreign"
				case "wrong_source":
					n.Source = comparisonTargetID
				case "wrong_target":
					n.Mirror = discoveryDeploymentID
				case "wrong_filter":
					n.RouteFilter = "/other"
				case "wrong_until":
					n.WindowStart, n.WindowEnd = "2026-10-16T09:00:00Z", "2026-10-16T10:00:00Z"
				case "wrong_window":
					n.WindowStart = "invalid"
				case "false_clamp":
					n.Since, n.WindowStart = "30m", "2026-10-16T10:30:00Z"
				case "negative_count":
					n.Routes[0].SourceN = -1
				case "bad_order":
					n.Routes[0].SourceP50 = 1000
				case "missing_side_values":
					n.Routes[1].MirrorP95 = 20
				case "both_missing":
					n.Routes = append(n.Routes, api.DebugCompareRouteStats{Route: "/absent"})
				case "duplicate_route":
					n.Routes = append(n.Routes, n.Routes[0])
				case "filter_mismatch":
					n.RouteFilter = "/orders"
				}
				_ = json.NewEncoder(w).Encode(n)
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			args := comparisonFixtureArgs()
			if mode == "clamped" {
				args.Since = "3d"
			}
			if mode == "default" {
				args.Since = ""
			}
			if mode == "filter_mismatch" {
				args.Route = "/orders"
			}
			valid := mode == "empty" || mode == "bounded" || mode == "redacted" || mode == "clamped" || mode == "default"
			out := callTool(t, ctx, cs, "gregale_compare_deployments", args, !valid)
			if !valid {
				if out["routes"] != nil || posts.Load() > 1 {
					t.Fatal("invalid evidence released or comparison retried")
				}
				if mode == "foreign_before" && posts.Load() != 0 {
					t.Fatal("foreign deployment queried")
				}
				if mode == "legacy" && string(out["code"]) != `"comparison_metadata_required"` {
					t.Fatal("legacy metadata was invented")
				}
				if (mode == "plan_gated" || mode == "native_error") && string(out["code"]) != `"native_compare_error"` {
					t.Fatal("native error hidden")
				}
				return
			}
			if mode == "empty" && (string(out["routes"]) != "[]" || string(out["status"]) != `"no_retained_route_samples"`) {
				t.Fatal("empty evidence not explicit")
			}
			if mode == "bounded" && (string(out["routes_truncated"]) != "true" || string(out["returned_route_count"]) != "20") {
				t.Fatal("output route bound lost")
			}
			if mode == "redacted" {
				encoded := string(out["routes"])
				if strings.Contains(encoded, testToken) || strings.Contains(encoded, "excluded-query") || !strings.Contains(encoded, "[REDACTED]") || string(out["text_truncated"]) != "true" {
					t.Fatal("route redaction or text bound lost")
				}
			}
		})
	}
}

func TestDeploymentComparisonInvalidSelectors(t *testing.T) {
	for _, mode := range []string{"same", "nil", "malformed", "zero_since", "overflow_since", "invalid_until", "future_until", "route_query", "route_secret", "limit"} {
		t.Run(mode, func(t *testing.T) {
			args := comparisonFixtureArgs()
			switch mode {
			case "same":
				args.TargetDeploymentID = args.SourceDeploymentID
			case "nil":
				args.SourceDeploymentID = "00000000-0000-0000-0000-000000000000"
			case "malformed":
				args.TargetDeploymentID = ".."
			case "zero_since":
				args.Since = "0h"
			case "overflow_since":
				args.Since = "999999999999d"
			case "invalid_until":
				args.Until = "bad"
			case "future_until":
				args.Until = "2099-01-01T00:00:00Z"
			case "route_query":
				args.Route = "/orders?x=1"
			case "route_secret":
				args.Route = "/" + testToken
			case "limit":
				args.Limit = 51
			}
			var posts atomic.Int32
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					posts.Add(1)
				}
				if r.URL.Path == "/v1/apps/alpha" {
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					return
				}
				w.WriteHeader(500)
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			callTool(t, ctx, cs, "gregale_compare_deployments", args, true)
			if posts.Load() != 0 {
				t.Fatal("invalid selector issued native comparison")
			}
		})
	}
}
