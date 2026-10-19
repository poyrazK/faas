// adr: 1636
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

func TestRequestWakeProjection(t *testing.T) {
	for _, mode := range []string{"recorded", "warm", "aggregate", "absent", "cold_without_id", "journal", "no_row", "zero_count", "negative_count", "unsafe", "credential", "long", "control", "unicode", "invalid_utf8"} {
		t.Run(mode, func(t *testing.T) {
			r := requestFixtureEvidence().Request
			r.WakeID, r.ColdBoot = "wake-1", true
			want := "observed"
			switch mode {
			case "warm":
				r.ColdBoot = false
			case "aggregate":
				r.Count = 7
			case "absent", "cold_without_id":
				r.WakeID, want = "", "not_reported"
			case "journal":
				r.EvidenceStatus, want = "request_id_only", "unavailable"
			case "no_row":
				r.ID, want = "", "unavailable"
			case "zero_count":
				r.Count, want = 0, "unavailable"
			case "negative_count":
				r.Count, want = -1, "unavailable"
			case "unsafe":
				r.WakeID, want = "../private", "invalid_native_wake"
			case "credential":
				r.WakeID, want = testToken, "invalid_native_wake"
			case "long":
				r.WakeID, want = strings.Repeat("a", 129), "invalid_native_wake"
			case "control":
				r.WakeID, want = "wake-1\n", "invalid_native_wake"
			case "unicode":
				r.WakeID, want = "wake-界", "invalid_native_wake"
			case "invalid_utf8":
				r.WakeID, want = "wake-\xff", "invalid_native_wake"
			}
			m := &management{opts: Options{Token: testToken}}
			v := m.projectRequest(requestArgs{App: "alpha", RequestID: "row-1"}, "app-1", "staging", r)
			w := v.Wake
			if w.Status != want || w.Basis != "native_retained_request_wake_id" || w.AssociationScope != "retained_row" || w.TimelineAvailability != "not_checked" || v.Status != 503 || v.LookupIdentifier != "row-1" {
				t.Fatal("association lost provenance/uncertainty or discarded other evidence")
			}
			if want == "observed" {
				if w.WakeID != r.WakeID || w.TimelineTool != "gregale_get_wake_timeline" || w.TimelineArguments == nil || w.TimelineArguments.App != "alpha" || w.TimelineArguments.WakeID != r.WakeID || w.TimelineArguments.Since != "" || w.TimelineArguments.Limit != api.MCPWakeTimelineDefault || w.RepresentsMultipleRequests != (r.Count > 1) {
					t.Fatal("handoff changed identity, inferred a time filter or lost aggregation")
				}
			} else if w.WakeID != "" || w.TimelineTool != "" || w.TimelineArguments != nil {
				t.Fatal("missing/unavailable/invalid metadata exposed a rewritten selector")
			}
			encoded, _ := json.Marshal(w)
			for _, marker := range []string{testToken, "../private", `"instance_id"`, `"trace_id"`, `"since"`, `"cursor"`} {
				if strings.Contains(string(encoded), marker) {
					t.Errorf("wake association leaked/invented %s", marker)
				}
			}
		})
	}
}

func TestRequestWakeConsentAndExplicitDrillDown(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "approved", "denied", "replaced", "allowlist_denied", "timeline_missing"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				var reads, timelines atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Error("wake association wrote native state")
						w.WriteHeader(405)
						return
					}
					switch r.URL.Path {
					case "/v1/apps/alpha":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					case "/v1/apps/alpha/debug/requests", "/v1/apps/alpha/debug/requests/row-1", "/v1/apps/alpha/debug/requests/row-1/evidence":
						reads.Add(1)
						if r.URL.Path == "/v1/apps/alpha/debug/requests" {
							page := discoveryFixturePage(r.URL.Query())
							page.Requests[0].WakeID = "wake-1"
							_ = json.NewEncoder(w).Encode(page)
							return
						}
						e := requestFixtureEvidence()
						e.Request.DeploymentID, e.Request.WakeID, e.Request.Count = "", "wake-1", 7
						if strings.HasSuffix(r.URL.Path, "/evidence") {
							_ = json.NewEncoder(w).Encode(e)
						} else {
							_ = json.NewEncoder(w).Encode(e.Request)
						}
					case "/v1/apps/alpha/wakes/wake-1/timeline":
						timelines.Add(1)
						if r.URL.Query().Get("limit") != "50" || r.URL.Query().Get("pagination") != "keyset_v1" || len(r.URL.Query()) != 2 {
							t.Error("request association inferred unsupported timeline filters")
						}
						if mode == "timeline_missing" {
							api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "no retained events"))
							return
						}
						_ = json.NewEncoder(w).Encode(wakeTimelineFixture("app-1", 50, false))
					default:
						t.Errorf("unexpected automatic enrichment %s", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer b.Close()
				opts := Options{APIBase: b.URL}
				if mode != "local" && mode != "allowlist_denied" {
					opts.Environments = &EnvironmentConsent{RequestReadApps: map[string]string{}}
					if mode == "approved" || mode == "timeline_missing" {
						opts.Environments.RequestReadApps["alpha"] = "app-1"
					}
					if mode == "replaced" {
						opts.Environments.RequestReadApps["alpha"] = "old-app"
					}
				}
				if mode == "allowlist_denied" {
					opts.AllowedApps = map[string]string{"other": "other-app"}
				}
				denied := mode == "denied" || mode == "replaced" || mode == "allowlist_denied"
				cs, ctx := connectConfiguredTestServer(t, b, opts, version)
				for _, tool := range []string{"gregale_list_requests", "gregale_get_request", "gregale_get_request_evidence"} {
					args := map[string]any{"app": "alpha", "request_id": "row-1"}
					if tool == "gregale_list_requests" {
						args = map[string]any{"app": "alpha", "since": "1h", "limit": 1}
					}
					out := callTool(t, ctx, cs, tool, args, denied)
					if denied {
						if reads.Load() != 0 || timelines.Load() != 0 {
							t.Fatal("association bypassed diagnostic consent")
						}
						continue
					}
					var request requestView
					raw, _ := json.Marshal(out)
					if tool == "gregale_get_request_evidence" {
						raw = out["request"]
					}
					if tool == "gregale_list_requests" {
						var rows []requestView
						_ = json.Unmarshal(out["requests"], &rows)
						request = rows[0]
					} else {
						_ = json.Unmarshal(raw, &request)
					}
					if request.Wake.Status != "observed" || request.Wake.TimelineArguments == nil || timelines.Load() != 0 {
						t.Fatal("association was unavailable or automatically fetched stage data")
					}
					if tool == "gregale_get_request_evidence" {
						result := callTool(t, ctx, cs, request.Wake.TimelineTool, *request.Wake.TimelineArguments, mode == "timeline_missing")
						if timelines.Load() != 1 || mode == "timeline_missing" && string(result["code"]) != `"not_found"` {
							t.Fatal("independent event retention/error or explicit drill-down was lost")
						}
					}
				}
				if mode == "approved" {
					callTool(t, ctx, cs, "gregale_get_wake_timeline", wakeTimelineArgs{App: "future", WakeID: "wake-1"}, true)
					if timelines.Load() != 1 {
						t.Fatal("request association authorized a future app")
					}
				}
			})
		}
	}
}

func TestRequestWakeIdentityChangesWithholdAssociations(t *testing.T) {
	for _, tool := range []string{"gregale_list_requests", "gregale_get_request", "gregale_get_request_evidence"} {
		t.Run(tool, func(t *testing.T) {
			var read atomic.Bool
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha" {
					id := "app-1"
					if read.Load() {
						id = "replacement"
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
					return
				}
				read.Store(true)
				if r.URL.Path == "/v1/apps/alpha/debug/requests" {
					page := discoveryFixturePage(r.URL.Query())
					page.Requests[0].WakeID = "wake-1"
					_ = json.NewEncoder(w).Encode(page)
					return
				}
				e := requestFixtureEvidence()
				e.Request.DeploymentID, e.Request.WakeID = "", "wake-1"
				if strings.HasSuffix(r.URL.Path, "/evidence") {
					_ = json.NewEncoder(w).Encode(e)
				} else {
					_ = json.NewEncoder(w).Encode(e.Request)
				}
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			args := map[string]any{"app": "alpha", "request_id": "row-1"}
			if tool == "gregale_list_requests" {
				args = map[string]any{"app": "alpha", "since": "1h", "limit": 1}
			}
			out := callTool(t, ctx, cs, tool, args, true)
			if out["wake"] != nil || out["request"] != nil || out["requests"] != nil || !read.Load() {
				t.Fatal("identity replacement exposed a wake association")
			}
		})
	}
}

func TestRequestWakeRecommendations(t *testing.T) {
	for _, mode := range []string{"recorded", "warm", "missing", "invalid", "aggregate", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			e := requestFixtureEvidence()
			e.Request.WakeID, e.Request.ColdBoot = "wake-1", true
			switch mode {
			case "warm":
				e.Request.ColdBoot = false
			case "missing":
				e.Request.WakeID = ""
			case "invalid":
				e.Request.WakeID = "../private"
			case "aggregate":
				e.Request.Count = 7
			}
			e.Explanation = api.DebugEvidenceExplanation{Status: "unobserved", GeneratedBy: "rules-v1", Findings: []api.DebugEvidenceFinding{{Code: "cold_boot", Confidence: "medium", EvidenceRefs: []string{"request"}}}, Recommendations: []api.DebugEvidenceRecommendation{{Action: "inspect_wake_timeline"}}}
			if mode == "unsupported" {
				e.Explanation.Findings = nil
			}
			m := &management{}
			v := m.projectRequestEvidence(m.projectRequest(requestArgs{App: "alpha", RequestID: "row-1"}, "app-1", "default", e.Request), e, nil)
			if mode == "warm" || mode == "unsupported" {
				if len(v.Explanation.Recommendations) != 0 {
					t.Fatal("association invented a cold-boot recommendation")
				}
				return
			}
			if len(v.Explanation.Recommendations) != 1 {
				t.Fatal("supported suggestion lost")
			}
			r := v.Explanation.Recommendations[0]
			linked := mode == "recorded" || mode == "aggregate"
			if (r.Arguments != nil) != linked || linked && (r.Tool != "gregale_get_wake_timeline" || r.Arguments.WakeID != "wake-1" || r.EvidenceRefs[0] != "/request/wake") || !linked && (r.Tool != "" || r.EvidenceRefs[0] != "/request") {
				t.Fatal("recommendation exposed unsafe arguments or wrong supporting facts")
			}
			assertExplanationReferences(t, v)
		})
	}
}

func TestRequestWakePromptGuidance(t *testing.T) {
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/alpha" || r.Method != http.MethodGet {
			t.Error("prompt fetched diagnostic data")
		}
		_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
	}))
	defer b.Close()
	cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
	for _, name := range []string{"gregale_request_diagnosis", "gregale_performance_diagnosis"} {
		prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: name, Arguments: map[string]string{"app": "alpha"}})
		if err != nil {
			t.Fatal(err)
		}
		text := prompt.Messages[0].Content.(*mcp.TextContent).Text
		for _, needle := range []string{"native_retained_request_wake_id", "timeline_availability not_checked", "invalid_native_wake", "represents_multiple_requests", "Do not derive a wake ID", "timestamp_only_not_lossless"} {
			if !strings.Contains(text, needle) {
				t.Errorf("%s omitted %s", name, needle)
			}
		}
	}
}
