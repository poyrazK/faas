// adr: 1624
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

func regressionsFixture() api.DebugRegressionsResponse {
	return api.DebugRegressionsResponse{Since: "1h", Regressions: []api.DebugRegressionItem{
		{DeploymentID: discoveryDeploymentID, Route: "/orders?excluded-query#excluded-fragment", Kind: "errors", State: "active", Factor: "1.00", P95MS: 50, P95BaseMS: 40, AffectedCount: 7, ErrorCount: 7, ErrorRatePct: 20,
			FirstDetectedAt: "2026-10-01T10:00:00Z", LastDetectedAt: "2026-10-17T10:59:00Z",
			SuspectedDependency: &api.DebugSuspectedDependency{Type: "binding", Kind: "postgres", Name: "orders " + testToken, P95BaseMS: 20, P95MS: 100, RegressionFactor: 5, Reason: "failures", ErrorRatePct: 20, ErrorType: "timeout", Query: "excluded-sql-query"}},
		{DeploymentID: comparisonTargetID, Route: "/latency", Kind: "latency", State: "acknowledged", Factor: "2.50", P95MS: 250, P95BaseMS: 100, AffectedCount: 9,
			FirstDetectedAt: "2026-10-17T10:20:00Z", LastDetectedAt: "2026-10-17T10:58:00Z", AcknowledgedAt: "2026-10-17T10:30:00Z"},
		{DeploymentID: discoveryDeploymentID, Route: "/native-visible", Kind: "latency", State: "dismissed", Factor: "1.20", P95MS: 120, P95BaseMS: 100, AffectedCount: 3,
			FirstDetectedAt: "2026-10-17T10:20:00Z", LastDetectedAt: "2026-10-17T10:57:00Z"},
	}}
}

func TestRegressionsConsentProjectionAndPrompt(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "denied", "approved", "replaced", "app_denied"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				var reads, deployments atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Error("regression inbox wrote native state")
						w.WriteHeader(405)
						return
					}
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
					case "/v1/apps/alpha/debug/regressions":
						reads.Add(1)
						if r.URL.Query().Get("since") != "1h" || len(r.URL.Query()) != 1 {
							t.Error("regression lookback changed or projection limit sent upstream")
						}
						_ = json.NewEncoder(w).Encode(regressionsFixture())
					default:
						t.Errorf("unexpected regression read %s", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer b.Close()
				opts := Options{APIBase: b.URL}
				if mode != "local" {
					opts.Environments = &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "app-1"}}
					if mode == "denied" {
						opts.Environments.RequestReadApps = nil
					}
					if mode == "replaced" {
						opts.Environments.RequestReadApps["alpha"] = "old-app"
					}
					if mode == "app_denied" {
						opts.AllowedApps = map[string]string{}
						opts.Environments.RequestReadApps = nil
					}
				}
				denied := mode == "denied" || mode == "replaced" || mode == "app_denied"
				cs, ctx := connectConfiguredTestServer(t, b, opts, version)
				out := callTool(t, ctx, cs, "gregale_list_regressions", regressionsArgs{App: "alpha", Since: "1h", Limit: 2}, denied)
				if denied {
					want := `"request_access_denied"`
					if mode == "app_denied" {
						want = `"app_access_denied"`
					}
					if reads.Load() != 0 || deployments.Load() != 0 || string(out["code"]) != want {
						t.Fatal("regression evidence accessed before consent")
					}
					return
				}
				if reads.Load() != 1 || deployments.Load() != 4 || string(out["scope"]) != `"app_wide"` || string(out["lookback_basis"]) != `"last_detected_at"` || string(out["source_completeness"]) != `"unknown"` || out["window_start"] != nil || out["window_end"] != nil {
					t.Fatal("lost deployment pins or invented window/completeness metadata")
				}
				var rows []regressionView
				_ = json.Unmarshal(out["regressions"], &rows)
				if len(rows) != 2 || rows[0].Kind != "errors" || rows[0].Factor != "1.00" || rows[0].Route != "/orders" || !rows[0].RouteModified || rows[0].DeploymentScope != "staging" || rows[1].DeploymentScope != "production" || rows[1].State != "acknowledged" || rows[1].AcknowledgedAt == "" || rows[0].SuspectedDependency.Basis != "hypothesis" {
					t.Fatal("regression order, metrics, triage or hypothesis lost")
				}
				encoded, _ := json.Marshal(out)
				if strings.Contains(string(encoded), "excluded-") || strings.Contains(string(encoded), "fp_test_") || strings.Contains(string(encoded), `"query"`) || string(out["regressions_truncated"]) != "true" || string(out["native_returned_regression_count"]) != "3" {
					t.Fatal("regression projection leaked data or lost limits")
				}
				before := reads.Load()
				prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_performance_diagnosis", Arguments: map[string]string{"app": "alpha", "since": "1h"}})
				if err != nil {
					t.Fatal(err)
				}
				for _, needle := range []string{"gregale_list_regressions", "last_detected_at", "hypotheses", "no baseline deployment ID", "route_modified", "Do not acknowledge", "gregale_compare_deployments"} {
					if !strings.Contains(prompt.Messages[0].Content.(*mcp.TextContent).Text, needle) {
						t.Errorf("prompt omitted %s", needle)
					}
				}
				if reads.Load() != before {
					t.Fatal("prompt fetched regression evidence")
				}
			})
		}
	}
}

func TestRegressionEvidenceGuards(t *testing.T) {
	good := map[string]bool{"empty": true, "default": true, "clamped": true, "bounded": true, "text": true, "native_visibility": true, "legacy_reason": true}
	for _, mode := range []string{"empty", "default", "clamped", "bounded", "text", "native_visibility", "legacy_reason", "widened", "missing_since", "bad_since", "bad_id", "foreign_before", "foreign_after", "scope_changed", "app_changed", "bad_factor", "rounded_factor", "nan_factor", "exponent_factor", "negative_count", "negative_latency", "bad_rate", "reversed_time", "bad_triage_time", "long_detection_time", "long_triage_time", "plan_gated", "native_error"} {
		t.Run(mode, func(t *testing.T) {
			var reads, deployments atomic.Int32
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("unexpected native mutation")
					w.WriteHeader(405)
					return
				}
				if r.URL.Path == "/v1/apps/alpha" {
					id := "app-1"
					if mode == "app_changed" && reads.Load() > 0 {
						id = "replacement"
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v1/deployments/") {
					n := deployments.Add(1)
					id := strings.TrimPrefix(r.URL.Path, "/v1/deployments/")
					appID, scope := "app-1", "staging"
					if mode == "foreign_before" || mode == "foreign_after" && n > 2 {
						appID = "other-app"
					}
					if mode == "scope_changed" && n > 2 {
						scope = "production"
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: id, AppID: appID, Scope: scope})
					return
				}
				if r.URL.Path != "/v1/apps/alpha/debug/regressions" {
					t.Errorf("unexpected read %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				reads.Add(1)
				if status := map[string]int{"plan_gated": 403, "native_error": 503}[mode]; status != 0 {
					api.WriteProblem(w, api.NewProblem(status, "native_regression_error", "Unavailable", "native evidence denied"))
					return
				}
				n := regressionsFixture()
				switch mode {
				case "empty":
					n.Regressions = nil
				case "default":
					if r.URL.RawQuery != "" {
						t.Error("changed native default")
					}
				case "clamped":
					n.Since = "30m"
				case "bounded":
					row := n.Regressions[0]
					n.Regressions = make([]api.DebugRegressionItem, api.MCPRegressionsDefault+1)
					for i := range n.Regressions {
						n.Regressions[i] = row
					}
					// Do not dereference or release an unprojected deployment.
					n.Regressions[api.MCPRegressionsDefault].DeploymentID = "excluded-unprojected-deployment"
				case "text":
					n.Regressions[0].Route = "/" + strings.Repeat("x", api.MCPRequestFieldMaxBytes-5) + testToken + strings.Repeat("界", 80)
					n.Regressions[0].SuspectedDependency.Name = strings.Repeat("界", api.MCPRequestFieldMaxBytes)
				case "native_visibility":
					// Native selection/clock remains authoritative; do not apply
					// local filtering or infer health from lifecycle timestamps.
					n.Regressions[0].State = "active"
					n.Regressions[0].DismissedUntil = "2026-10-16T12:00:00Z"
					n.Regressions[1].State = "future_triage_state"
				case "legacy_reason":
					n.Regressions[0].SuspectedDependency.Reason = ""
				case "widened":
					n.Since = "2h"
				case "missing_since":
					n.Since = ""
				case "bad_since":
					n.Since = "0h"
				case "bad_id":
					n.Regressions[0].DeploymentID = "00000000-0000-0000-0000-000000000000"
				case "bad_factor":
					n.Regressions[0].Factor = "0.50"
				case "rounded_factor":
					n.Regressions[0].Factor = "0.999999999999999999999"
				case "nan_factor":
					n.Regressions[0].Factor = "NaN"
				case "exponent_factor":
					n.Regressions[0].Factor = "2e1"
				case "negative_count":
					n.Regressions[0].AffectedCount = -1
				case "negative_latency":
					n.Regressions[0].P95MS = -1
				case "bad_rate":
					n.Regressions[0].ErrorRatePct = 101
				case "reversed_time":
					n.Regressions[0].LastDetectedAt = "2026-09-01T00:00:00Z"
				case "bad_triage_time":
					n.Regressions[0].AcknowledgedAt = "invalid"
				case "long_detection_time":
					n.Regressions[0].LastDetectedAt = "2026-10-17T10:59:00." + strings.Repeat("1", api.MCPRequestFieldMaxBytes) + "Z"
				case "long_triage_time":
					n.Regressions[0].AcknowledgedAt = "2026-10-17T10:59:00." + strings.Repeat("1", api.MCPRequestFieldMaxBytes) + "Z"
				}
				_ = json.NewEncoder(w).Encode(n)
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			args := regressionsArgs{App: "alpha", Since: "1h"}
			if mode == "default" {
				args.Since = ""
			}
			out := callTool(t, ctx, cs, "gregale_list_regressions", args, !good[mode])
			if reads.Load() != 1 {
				t.Fatal("regression error triggered fallback or extra fetch")
			}
			if !good[mode] {
				if out["regressions"] != nil {
					t.Fatal("released invalid or cross-app regression evidence")
				}
				if (mode == "native_error" || mode == "plan_gated") && string(out["code"]) != `"native_regression_error"` {
					t.Fatal("native regression error lost")
				}
				return
			}
			var rows []regressionView
			_ = json.Unmarshal(out["regressions"], &rows)
			if mode == "empty" && (string(out["status"]) != `"no_visible_regressions"` || string(out["regressions"]) != "[]" || deployments.Load() != 0) {
				t.Fatal("empty regression inbox invented evidence")
			}
			if mode == "clamped" && (string(out["retention_clamped"]) != "true" || string(out["retention_clamped_basis"]) != `"requested_and_effective_lookback"`) {
				t.Fatal("lost detection lookback clamp provenance")
			}
			if mode == "bounded" && (len(rows) != api.MCPRegressionsDefault || string(out["regressions_truncated"]) != "true" || deployments.Load() != 2) {
				t.Fatal("projection budget or deployment deduplication lost")
			}
			if mode == "text" && (string(out["text_truncated"]) != "true" || len(rows[0].Route) > api.MCPRequestFieldMaxBytes || len(rows[0].SuspectedDependency.Name) > api.MCPRequestFieldMaxBytes || strings.Contains(rows[0].Route, "fp_test")) {
				t.Fatal("text was not redacted before UTF-8 bounding")
			}
			if mode == "native_visibility" && (len(rows) != 3 || rows[0].DismissedUntil == "" || rows[1].State != "future_triage_state" || rows[2].State != "dismissed") {
				t.Fatal("bridge reinterpreted native visibility or workflow")
			}
			if mode == "legacy_reason" && rows[0].SuspectedDependency.Reason != "latency" {
				t.Fatal("legacy hypothesis reason lost")
			}
		})
	}
}

func TestRegressionsRejectInvalidArgumentsAndHypotheses(t *testing.T) {
	for _, args := range []regressionsArgs{{App: "../alpha"}, {App: "alpha", Since: "invalid"}, {App: "alpha", Since: "-1h"}, {App: "alpha", Since: "0h"}, {App: "alpha", Since: "999999999999d"}, {App: "alpha", Limit: -1}, {App: "alpha", Limit: api.MCPRegressionsMax + 1}} {
		if _, err := regressionSelection(args); err == nil {
			t.Fatalf("accepted invalid regression selection: %+v", args)
		}
	}
	for _, value := range []float64{-1, math.NaN(), math.Inf(1)} {
		m := &management{}
		truncated := false
		if _, err := m.projectRegressionSuspect(&api.DebugSuspectedDependency{RegressionFactor: value}, &truncated); err == nil {
			t.Fatal("accepted invalid hypothesis factor")
		}
	}
}
