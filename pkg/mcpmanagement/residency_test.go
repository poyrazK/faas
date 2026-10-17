// adr: 1621
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

func residencyFixture(appID string) api.DebugRunningResponse {
	trace := "excluded-trace"
	causes := []api.DebugRunningCause{{Code: api.DebugRunningReasonRequestActivity, Summary: "Observed activity " + testToken, InstanceCount: 1, LastActivityAt: "2026-10-17T10:29:00Z", IdleDeadline: "2026-10-17T10:34:00Z",
		Request: &api.DebugRunningRequestAttribution{TelemetryID: "row-1", DeploymentID: discoveryDeploymentID, Route: "/orders?excluded-query#excluded-fragment", Method: "POST", ReceivedAt: "2026-10-17T10:28:59Z", Count: 7, MatchDeltaMS: 1000, TraceID: &trace, WakeID: "excluded-wake", InstanceID: "excluded-instance"}},
		{Code: api.DebugRunningReasonOpenConnection, Summary: "Open connections observed", InstanceCount: 1, OpenConnections: 2, FlowTopologyDegraded: true, FlowTopology: []api.DebugRunningFlowSummary{{InstanceID: "excluded-instance", RemoteIP: "192.0.2.123", RemotePort: 8443, Protocol: "excluded-protocol", Count: 2}}}}
	return api.DebugRunningResponse{AppID: appID, Since: "1h", WindowStart: "2026-10-17T10:00:00Z", WindowEnd: "2026-10-17T11:00:00Z", Current: causes, CurrentObservedAt: "2026-10-17T10:30:00Z", Config: api.DebugRunningConfig{ConfiguredMinInstances: 1, EffectiveMinInstances: 2, PrewarmMinInstances: 2, IdleTimeoutSeconds: 300},
		History: []api.DebugRunningObservation{{EventID: "excluded-event", ObservedAt: "2026-10-17T10:30:00Z", RunningInstances: 2, ConfiguredMinInstances: 1, EffectiveMinInstances: 2, PrewarmMinInstances: 2, IdleTimeoutSeconds: 300, Degraded: true, Causes: causes}}, HistoryTruncated: true}
}

func emptyResidencyFixture(appID string) api.DebugRunningResponse {
	n := residencyFixture(appID)
	n.Current, n.History, n.CurrentObservedAt, n.HistoryTruncated = nil, nil, "", false
	return n
}

func TestResidencyConsentProjectionAndPrompt(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "denied", "approved", "replaced"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				var reads, deploymentReads atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						t.Error("residency diagnosis wrote native state")
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
					case "/v1/deployments/" + discoveryDeploymentID:
						deploymentReads.Add(1)
						_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: discoveryDeploymentID, AppID: "app-1", Scope: "staging"})
					case "/v1/apps/alpha/debug/running":
						reads.Add(1)
						if r.URL.Query().Get("since") != "1h" || r.URL.Query().Get("limit") != "10" {
							t.Error("residency selection not forwarded")
						}
						_ = json.NewEncoder(w).Encode(residencyFixture("app-1"))
					default:
						t.Errorf("unexpected residency read %s", r.URL.Path)
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
				out := callTool(t, ctx, cs, "gregale_get_running_causes", residencyArgs{App: "alpha", Since: "1h"}, denied)
				if denied {
					if string(out["code"]) != `"request_access_denied"` || reads.Load() != 0 || deploymentReads.Load() != 0 {
						t.Fatal("residency evidence read before consent")
					}
				} else {
					encoded, _ := json.Marshal(out)
					for _, excluded := range []string{"excluded-", "192.0.2.123", "8443", testToken, `"flow_topology"`, `"instance_id"`, `"trace_id"`} {
						if strings.Contains(string(encoded), excluded) {
							t.Fatalf("residency projection leaked %s", excluded)
						}
					}
					if !strings.Contains(string(encoded), "[REDACTED]") || !strings.Contains(string(out["current"]), `"lookup_identifier":"row-1"`) || !strings.Contains(string(out["current"]), `"route":"/orders"`) || !strings.Contains(string(out["current"]), `"scope":"staging"`) || !strings.Contains(string(out["current"]), `"count":7`) || !strings.Contains(string(out["current"]), `"match_delta_ms":1000`) {
						t.Fatal("lost redaction or approximate request evidence")
					}
					if string(out["freshness"]) != `"unknown"` || string(out["current_observation_age_seconds"]) != "1800" || string(out["current_degraded"]) != "true" || string(out["history_truncated"]) != "true" || string(out["scope"]) != `"app_wide"` {
						t.Fatal("lost observation age, scope or degraded/truncation flags")
					}
					if deploymentReads.Load() != 2 {
						t.Fatal("attributed deployments not deduplicated and rechecked")
					}
				}
				before := reads.Load()
				prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_residency_diagnosis", Arguments: map[string]string{"app": "alpha", "since": "1h"}})
				if (err != nil) != denied {
					t.Fatalf("prompt=%+v err=%v", prompt, err)
				}
				if !denied {
					for _, needle := range []string{"gregale_get_running_causes", "freshness is unknown", "config_basis", "nearest retained aggregate", "gregale_get_request_evidence", "Do not estimate hypothetical savings"} {
						if !strings.Contains(prompt.Messages[0].Content.(*mcp.TextContent).Text, needle) {
							t.Errorf("prompt omitted %s", needle)
						}
					}
				}
				if reads.Load() != before {
					t.Fatal("prompt fetched scheduler evidence")
				}
				p := completionParams("app", "", nil)
				p.Ref.Name = "gregale_residency_diagnosis"
				res, err := cs.Complete(ctx, p)
				want := 1
				if mode == "local" {
					want = 2
				}
				if denied {
					want = 0
				}
				if err != nil || len(res.Completion.Values) != want {
					t.Fatalf("completion grew consent: %+v %v", res, err)
				}
				if mode == "approved" {
					callTool(t, ctx, cs, "gregale_get_running_causes", residencyArgs{App: "future", Since: "1h"}, true)
				}
			})
		}
	}
}

func TestResidencyHistoryAndBounds(t *testing.T) {
	for _, mode := range []string{"empty", "historical_only", "clamped", "bounded", "text_bound", "no_blocker", "historical_settings"} {
		t.Run(mode, func(t *testing.T) {
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha" {
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					return
				}
				n := residencyFixture("app-1")
				// This test isolates observation semantics from request attribution.
				n.Current[0].Request = nil
				switch mode {
				case "empty":
					n = emptyResidencyFixture("app-1")
				case "historical_only":
					n.Current, n.CurrentObservedAt = nil, ""
				case "clamped":
					n.Since, n.WindowStart, n.RetentionClamped = "30m", "2026-10-17T10:30:00Z", true
				case "bounded":
					for len(n.Current) <= api.MCPResidencyCausesMax {
						n.Current = append(n.Current, api.DebugRunningCause{Code: "future_reason"})
					}
					n.History[0].Causes = n.Current
				case "text_bound":
					n.Current[0].Summary = strings.Repeat("界", api.MCPRequestFieldMaxBytes) + testToken
				case "no_blocker":
					n.Current[0].Code = api.DebugRunningReasonNoBlockerObserved
				case "historical_settings":
					old := n.History[0]
					old.ObservedAt, old.ConfiguredMinInstances, old.EffectiveMinInstances, old.PrewarmMinInstances = "2026-10-17T10:15:00Z", 0, 0, 0
					n.History = append(n.History, old)
				}
				_ = json.NewEncoder(w).Encode(n)
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			limit := 1
			if mode == "historical_settings" {
				limit = 2
			}
			out := callTool(t, ctx, cs, "gregale_get_running_causes", residencyArgs{App: "alpha", Since: "1h", Limit: limit}, false)
			if string(out["freshness"]) != `"unknown"` {
				t.Fatal("invented freshness")
			}
			if mode == "empty" || mode == "historical_only" {
				if string(out["current"]) != "[]" || out["current_observed_at"] != nil || out["current_observation_age_seconds"] != nil || string(out["config_basis"]) != `"native_app_configuration"` {
					t.Fatal("invented current evidence")
				}
			}
			if mode == "empty" && (string(out["status"]) != `"no_retained_observations"` || string(out["history"]) != "[]") {
				t.Fatal("empty history presented as observed")
			}
			if mode == "clamped" && (string(out["retention_clamped"]) != "true" || string(out["since"]) != `"30m"`) {
				t.Fatal("lost native retention clamp")
			}
			if mode == "bounded" {
				var causes []residencyCauseView
				_ = json.Unmarshal(out["current"], &causes)
				if len(causes) != api.MCPResidencyCausesMax || string(out["current_causes_truncated"]) != "true" || !strings.Contains(string(out["history"]), `"causes_truncated":true`) {
					t.Fatal("lost projection bounds")
				}
			}
			if mode == "historical_settings" {
				var history []residencyObservationView
				_ = json.Unmarshal(out["history"], &history)
				if len(history) != 2 || history[0].EffectiveMinInstances != 2 || history[1].EffectiveMinInstances != 0 {
					t.Fatal("historical settings overwritten by current configuration")
				}
			}
			if mode == "text_bound" && string(out["text_truncated"]) != "true" {
				t.Fatal("lost text truncation")
			}
		})
	}
}

func TestResidencyRejectsInvalidOrChangedEvidence(t *testing.T) {
	for _, mode := range []string{"plan_gated", "not_retained", "replaced", "wrong_app", "bad_window", "false_clamp", "bad_config", "bad_count", "bad_time", "future_observation", "missing_current_time", "missing_current_history", "oversized_history", "bad_request_id", "bad_request_count", "bad_request_time", "negative_delta", "foreign_deployment", "scope_changed"} {
		t.Run(mode, func(t *testing.T) {
			var reads, deploymentReads atomic.Int32
			var changed atomic.Bool
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha" {
					id := "app-1"
					if changed.Load() {
						id = "replacement"
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v1/deployments/") {
					count := deploymentReads.Add(1)
					appID, scope := "app-1", "staging"
					if mode == "foreign_deployment" {
						appID = "foreign"
					}
					if mode == "scope_changed" && count > 1 {
						scope = "production"
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: discoveryDeploymentID, AppID: appID, Scope: scope})
					return
				}
				reads.Add(1)
				if status := map[string]int{"plan_gated": 403, "not_retained": 404}[mode]; status != 0 {
					api.WriteProblem(w, api.NewProblem(status, "native_residency_error", "Unavailable", "native evidence denied"))
					return
				}
				n := residencyFixture("app-1")
				switch mode {
				case "replaced":
					changed.Store(true)
				case "wrong_app":
					n.AppID = "foreign"
				case "bad_window":
					n.WindowEnd = "invalid"
				case "false_clamp":
					n.Since, n.WindowStart = "30m", "2026-10-17T10:30:00Z"
				case "bad_config":
					n.Config.EffectiveMinInstances = -1
				case "bad_count":
					n.Current[0].InstanceCount = -1
				case "bad_time":
					n.Current[0].IdleDeadline = "invalid"
				case "future_observation":
					n.History[0].ObservedAt = "2026-10-17T12:00:00Z"
				case "missing_current_time":
					n.CurrentObservedAt = ""
				case "missing_current_history":
					n.History = nil
				case "oversized_history":
					for len(n.History) <= api.MCPResidencyHistoryDefault {
						n.History = append(n.History, n.History[0])
					}
				case "bad_request_id":
					n.Current[0].Request.TelemetryID = ".."
				case "bad_request_count":
					n.Current[0].Request.Count = 0
				case "bad_request_time":
					n.Current[0].Request.ReceivedAt = "invalid"
				case "negative_delta":
					n.Current[0].Request.MatchDeltaMS = -1
				}
				_ = json.NewEncoder(w).Encode(n)
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			out := callTool(t, ctx, cs, "gregale_get_running_causes", residencyArgs{App: "alpha", Since: "1h"}, true)
			if reads.Load() != 1 || out["current"] != nil || out["history"] != nil {
				t.Fatal("invalid evidence released or native read retried")
			}
			if (mode == "plan_gated" || mode == "not_retained") && string(out["code"]) != `"native_residency_error"` {
				t.Fatal("native error not preserved")
			}
		})
	}
}

func TestResidencyRejectsInvalidSelectorsBeforeNativeRead(t *testing.T) {
	for _, args := range []residencyArgs{{App: "alpha", Limit: -1}, {App: "alpha", Limit: 21}, {App: "alpha", Since: "0h"}, {App: "alpha", Since: "999999999999d"}, {App: "../alpha"}} {
		t.Run(args.App+args.Since, func(t *testing.T) {
			var reads atomic.Int32
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reads.Add(1); w.WriteHeader(500) }))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			callTool(t, ctx, cs, "gregale_get_running_causes", args, true)
			if reads.Load() != 0 {
				t.Fatal("invalid selector reached native API")
			}
		})
	}
}
