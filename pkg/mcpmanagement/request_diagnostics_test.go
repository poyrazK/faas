// adr: 1618
package mcpmanagement

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-1618: request diagnostics are exact, app-wide reads with independent consent.
func requestFixtureEvidence() api.DebugRequestEvidenceResponse {
	return api.DebugRequestEvidenceResponse{
		Request:           api.DebugTelemetryRequestItem{ID: "row-1", DeploymentID: "release-base", Route: "/orders?private-query=excluded", Method: "GET", Status: 503, LatencyMS: 90, Count: 1, ReceivedAt: "2026-10-17T10:00:00Z", NodeID: "excluded-node", ConsumerID: "excluded-consumer"},
		Timeline:          []api.DebugTimelineEvent{{Phase: "request", Kind: "telemetry", Summary: "request " + testToken, Actor: "excluded-actor", DurationMS: 90, Approximate: true}},
		Correlation:       api.DebugRequestCorrelation{Complete: false, Stages: []api.DebugRequestCorrelationStage{{Phase: "guest", Status: "missing", Reason: "no retained guest evidence"}}},
		DependencyLatency: []api.DebugRequestDependencyLatency{{Type: "binding", Kind: "postgres", Name: "orders", Calls: 2, TotalDurationMS: 80, MaxDurationMS: 50}},
		Spans:             []api.DebugTelemetrySpan{{DBStatement: "excluded-sql", Name: "excluded-span"}},
		Explanation:       requestExplanationFixture(), GeneratedAt: "2026-10-17T10:01:00Z",
	}
}

func requestFixtureHook(w http.ResponseWriter, req *http.Request, account string) bool {
	if !strings.HasPrefix(req.URL.Path, "/v1/apps/alpha/debug/requests/") {
		return false
	}
	if req.Method != http.MethodGet {
		w.WriteHeader(405)
		return true
	}
	e := withRequestRegression(withRequestGuest(requestFixtureEvidence()))
	if strings.HasSuffix(req.URL.Path, "/evidence") {
		_ = json.NewEncoder(w).Encode(withRequestPath(e))
	} else {
		e.Request.RequestID = "public-request"
		_ = json.NewEncoder(w).Encode(e.Request)
	}
	return true
}

func TestRequestDiagnosticsConsentAndProjection(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, test := range []struct {
			name    string
			consent *EnvironmentConsent
			denied  bool
		}{
			{"local", nil, false},
			{"legacy_remote", &EnvironmentConsent{}, true},
			{"explicit", &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "app-1"}}, false},
			{"replaced", &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "old-app"}}, true},
		} {
			t.Run(version+"/"+test.name, func(t *testing.T) {
				var reads atomic.Int32
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.Method != http.MethodGet {
						t.Error("diagnosis performed a native write")
					}
					switch req.URL.Path {
					case "/v1/apps":
						_ = json.NewEncoder(w).Encode([]api.AppResponse{{ID: "app-1", Slug: "alpha"}, {ID: "future-app", Slug: "future"}})
					case "/v1/apps/alpha":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					case "/v1/apps/future":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "future-app", Slug: "future"})
					case "/v1/deployments/release-base":
						_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "release-base", AppID: "app-1", Scope: "staging"})
					default:
						reads.Add(1)
						if !requestFixtureHook(w, req, "") {
							t.Errorf("unexpected path %s", req.URL.Path)
							w.WriteHeader(404)
						}
					}
				}))
				defer backend.Close()
				cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, Environments: test.consent}, version)
				for _, tool := range []string{"gregale_get_request", "gregale_get_request_evidence"} {
					out := callTool(t, ctx, cs, tool, requestArgs{App: "alpha", RequestID: "public-request"}, test.denied)
					if test.denied {
						if string(out["code"]) != `"request_access_denied"` {
							t.Fatalf("denial=%s", out)
						}
						continue
					}
					request := out
					if tool == "gregale_get_request_evidence" {
						request = map[string]json.RawMessage{}
						_ = json.Unmarshal(out["request"], &request)
					}
					var guest requestGuestView
					_ = json.Unmarshal(request["guest"], &guest)
					if guest.Status != "observed" || guest.Runtime != "node22" || guest.Outcome != "timeout" || guest.ErrorClass != "timeout" || guest.DurationMS == nil || *guest.DurationMS != 70 {
						t.Fatal("guest evidence missing from request protocol response")
					}
					encoded, _ := json.Marshal(out)
					for _, omitted := range []string{"excluded-", "private-query", "db_statement", "\"spans\"", "\"actor\""} {
						if strings.Contains(string(encoded), omitted) {
							t.Fatalf("leaked %s: %s", omitted, encoded)
						}
					}
					if !strings.Contains(string(encoded), `staging`) {
						t.Fatalf("missing deployment attribution: %s", encoded)
					}
					if tool == "gregale_get_request_evidence" && (!strings.Contains(string(encoded), "missing") || !strings.Contains(string(encoded), "[REDACTED]")) {
						t.Fatalf("evidence=%s", encoded)
					}
					if tool == "gregale_get_request_evidence" {
						var path requestPathView
						_ = json.Unmarshal(out["critical_path"], &path)
						if path.Status != "observed" || len(path.Segments) != 3 || !path.Complete {
							t.Fatal("selected request path missing from protocol response")
						}
						var regression requestRegressionView
						_ = json.Unmarshal(out["regression"], &regression)
						if regression.Status != "observed" || regression.Observation == nil || regression.Observation.DeploymentScope != "staging" || regression.Observation.Route != "/orders" || regression.Observation.Factor != "2.50" || regression.Observation.SuspectedDependency.Basis != "hypothesis" {
							t.Fatal("matching regression missing from protocol response")
						}
						var explanation requestExplanationView
						_ = json.Unmarshal(out["explanation"], &explanation)
						if len(explanation.Findings) != 7 || explanation.OmittedFindingCount != 1 || explanation.Findings[0].Confidence != "high" || explanation.Findings[0].EvidenceRefs[0] != "/request" || explanation.Findings[1].EvidenceRefs[0] != "/correlation/stages/0" || len(explanation.Recommendations) != 3 {
							t.Fatal("request explanation missing or unsupported findings exposed")
						}
					}
				}
				if !test.denied && reads.Load() != 2 {
					t.Fatal("explanation performed extra native diagnostic reads")
				}
				prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_request_diagnosis", Arguments: map[string]string{"app": "alpha", "request_id": "public-request"}})
				if (err != nil) != test.denied {
					t.Fatalf("prompt=%+v error=%v", prompt, err)
				}
				if !test.denied && !strings.Contains(prompt.Messages[0].Content.(*mcp.TextContent).Text, "gregale_get_request_evidence") {
					t.Fatal("missing diagnosis recipe")
				}
				if !test.denied {
					for _, needle := range []string{"JSON Pointers", "uncalibrated label", "Unsupported findings", "Recommendations are investigation suggestions only", "selected span-chain", "not full request latency", "native_retained_may_be_bucketed", "guest_failure", "/regression/observation", "no_matching_observation", "two explicit deployment IDs", "dependency_comparison", "per-side sample counts", "inspect_dependency_change"} {
						if !strings.Contains(prompt.Messages[0].Content.(*mcp.TextContent).Text, needle) {
							t.Errorf("request prompt omitted %s", needle)
						}
					}
				}
				if test.denied && reads.Load() != 0 {
					t.Fatal("diagnostics read before consent")
				}
				p := completionParams("app", "", nil)
				p.Ref.Name = "gregale_request_diagnosis"
				completion, err := cs.Complete(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				want := 1
				if test.consent == nil {
					want = 2
				}
				if test.denied {
					want = 0
				}
				if len(completion.Completion.Values) != want {
					t.Fatalf("completion widened consent: %+v", completion)
				}
				if !test.denied {
					p = completionParams("request_id", "opaque:id/", map[string]string{"app": "alpha"})
					p.Ref.Name = "gregale_request_diagnosis"
					completion, err = cs.Complete(ctx, p)
					if err != nil || len(completion.Completion.Values) != 0 {
						t.Fatalf("request IDs enumerated: %+v %v", completion, err)
					}
				}
				if test.consent != nil && !test.denied {
					out := callTool(t, ctx, cs, "gregale_get_deployment", deploymentArgs{DeploymentID: "release-base"}, true)
					if string(out["code"]) != `"environment_access_denied"` {
						t.Fatalf("diagnostic grant expanded environment access: %s", out)
					}
					callTool(t, ctx, cs, "gregale_get_request", requestArgs{App: "future", RequestID: "public-request"}, true)
				}
			})
		}
	}
}

func TestRequestDiagnosticsBoundsAndNativeErrors(t *testing.T) {
	for _, test := range []struct {
		name            string
		status          int
		swap            bool
		wrongDeployment bool
		journal         bool
	}{
		{"bounded", 200, false, false, false}, {"plan_gated", 403, false, false, false}, {"expired", 404, false, false, false},
		{"replacement", 200, true, false, false}, {"wrong_deployment", 200, false, true, false}, {"journal_only", 200, false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var changed atomic.Bool
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/v1/apps/alpha":
					id := "app-1"
					if changed.Load() {
						id = "replacement"
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
				case "/v1/deployments/release-base":
					id := "app-1"
					if test.wrongDeployment {
						id = "other-app"
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "release-base", AppID: id, Scope: "default"})
				default:
					if test.status != 200 {
						api.WriteProblem(w, api.NewProblem(test.status, "native_diagnostic_error", "Unavailable", "native plan or retention denied"))
						return
					}
					e := withRequestRegression(requestFixtureEvidence())
					if test.swap {
						changed.Store(true)
					}
					if test.journal {
						_ = json.NewEncoder(w).Encode(api.DebugTelemetryRequestItem{RequestID: "public-request", EvidenceStatus: "request_id_only"})
						return
					}
					e.Timeline = make([]api.DebugTimelineEvent, api.MCPRequestTimelineMax+1)
					for i := range e.Timeline {
						e.Timeline[i].Summary = strings.Repeat("x", api.MCPRequestFieldMaxBytes-4) + testToken + strings.Repeat("y", api.MCPRequestFieldMaxBytes)
					}
					e.Correlation.Stages = make([]api.DebugRequestCorrelationStage, api.MCPRequestCorrelationMax+1)
					e.Correlation.Complete = true
					e.DependencyLatency = make([]api.DebugRequestDependencyLatency, api.MCPRequestDependenciesMax+1)
					e.SpansTruncated = true
					_ = json.NewEncoder(w).Encode(e)
				}
			}))
			defer backend.Close()
			cs, ctx := connectTestServer(t, backend, 0, "2026-07-28")
			tool := "gregale_get_request_evidence"
			if test.journal {
				tool = "gregale_get_request"
			}
			out := callTool(t, ctx, cs, tool, requestArgs{App: "alpha", RequestID: "public-request"}, test.status != 200 || test.swap || test.wrongDeployment)
			switch {
			case test.status != 200:
				if string(out["code"]) != `"native_diagnostic_error"` {
					t.Fatalf("native error lost: %s", out)
				}
			case test.swap || test.wrongDeployment:
				if _, ok := out["request"]; ok {
					t.Fatal("released evidence after identity changed")
				}
			case test.journal:
				if string(out["scope_attribution"]) != `"unavailable"` || string(out["evidence_status"]) != `"request_id_only"` || out["status"] != nil || out["deployment_id"] != nil {
					t.Fatalf("invented journal evidence: %s", out)
				}
			default:
				for _, flag := range []string{"timeline_truncated", "correlation_truncated", "dependency_latency_truncated", "source_spans_truncated", "text_truncated"} {
					if string(out[flag]) != "true" {
						t.Errorf("missing %s", flag)
					}
				}
				var timeline []requestTimelineView
				_ = json.Unmarshal(out["timeline"], &timeline)
				if len(timeline) != api.MCPRequestTimelineMax || strings.Contains(timeline[0].Summary, "fp_test") || len(timeline[0].Summary) > api.MCPRequestFieldMaxBytes {
					t.Fatal("unsafe truncation")
				}
				var correlation api.DebugRequestCorrelation
				_ = json.Unmarshal(out["correlation"], &correlation)
				if correlation.Complete || len(correlation.Stages) != api.MCPRequestCorrelationMax {
					t.Fatal("invented complete correlation")
				}
			}
		})
	}
}

func TestRequestDiagnosticConsentSelection(t *testing.T) {
	available := []api.AppResponse{{ID: "app-1", Slug: "alpha"}, {ID: "app-2", Slug: "beta"}}
	for _, test := range []struct {
		name     string
		selected []string
		allowed  map[string]string
		denied   bool
	}{
		{"legacy", nil, nil, false}, {"explicit", []string{"alpha"}, nil, false}, {"selected", []string{"alpha"}, map[string]string{"alpha": "app-1"}, false},
		{"future", []string{"future"}, nil, true}, {"excluded", []string{"beta"}, map[string]string{"alpha": "app-1"}, true}, {"replaced", []string{"alpha"}, map[string]string{"alpha": "old"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", nil)
			req.PostForm = url.Values{"request_read_apps": test.selected}
			selected, err := selectedRequestApps(req, available, test.allowed)
			if (err != nil) != test.denied {
				t.Fatalf("selection=%v err=%v", selected, err)
			}
			if !test.denied && len(selected) != len(test.selected) {
				t.Fatal("consent widened")
			}
		})
	}
	for _, id := range []string{"", " ", ".", "..", "a\nb", strings.Repeat("a", api.MCPRequestIdentifierMaxBytes+1)} {
		if validateRequestID(id) == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	for _, id := range []string{"public-request", "opaque:id/with?chars", strings.Repeat("a", api.MCPRequestIdentifierMaxBytes)} {
		if validateRequestID(id) != nil {
			t.Fatalf("rejected valid native identifier %q", id)
		}
	}
}

func TestRequestDiagnosticOAuthReview(t *testing.T) {
	f := newRemoteFixture(t, false)
	f.mu.Lock()
	f.apiHook = requestFixtureHook
	f.mu.Unlock()
	flow := f.begin(scopeRead)
	response := f.consent(flow, "verify", "fp_test_alice_remote_key", f.server.URL, flow.csrf, false)
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("review=%d %v", response.StatusCode, err)
	}
	if !strings.Contains(string(page), `type="checkbox" name="request_read_apps" value="alpha">`) {
		t.Fatal("diagnostic consent missing or automatically selected")
	}
	for _, category := range []string{"App diagnostics (optional)", "performance evidence", "retained wake observations", "sampled CPU profile function labels and collection/attribution summaries", "scheduler residency causes"} {
		if !strings.Contains(string(page), category) {
			t.Fatalf("diagnostic consent omitted reviewed evidence category %q", category)
		}
	}
	form := url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"approve"}, "app_access": {"selected"}, "apps": {"alpha"}, "request_read_apps": {"future"}}
	response = f.submitConsent(flow, form, f.server.URL)
	_ = response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("unreviewed diagnostic app approved")
	}
	form["request_read_apps"] = []string{"alpha"}
	response = f.submitConsent(flow, form, f.server.URL)
	_ = response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatalf("approval=%d", response.StatusCode)
	}
	redirect, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	response = f.exchange(redirect.Query().Get("code"), testVerifier, f.resource, f.clientID)
	var tokens tokenResponse
	decodeResponse(t, response, &tokens)
	_ = response.Body.Close()
	cs, ctx := f.connect(tokens, "2026-07-28")
	callTool(t, ctx, cs, "gregale_get_request_evidence", requestArgs{App: "alpha", RequestID: "public-request"}, false)
	meta := callTool(t, ctx, cs, "gregale_context", noArgs{}, false)
	if !strings.Contains(string(meta["request_access"]), "selected_apps") {
		t.Fatalf("missing consent metadata: %s", meta)
	}
	// Refresh parameters cannot approve more apps or turn read consent into writes.
	response = f.request("POST", "/oauth/token", strings.NewReader(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {f.clientID}, "resource": {f.resource}, "request_read_apps": {"future"}, "allow_writes": {"yes"}}.Encode()), "application/x-www-form-urlencoded", "")
	decodeResponse(t, response, &tokens)
	_ = response.Body.Close()
	cs, ctx = f.connect(tokens, "2026-07-28")
	callTool(t, ctx, cs, "gregale_get_request_evidence", requestArgs{App: "alpha", RequestID: "public-request"}, false)
	result := callTool(t, ctx, cs, "gregale_get_request", requestArgs{App: "future", RequestID: "public-request"}, true)
	if string(result["code"]) != `"app_access_denied"` {
		t.Fatalf("refresh grew app access: %s", result)
	}
	catalog, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range catalog.Tools {
		if !tool.Annotations.ReadOnlyHint {
			t.Fatal("refresh enabled writes")
		}
	}
}
