// adr: 1629
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
	"github.com/onebox-faas/faas/pkg/debugger"
)

func withRequestDependencyComparison(e api.DebugRequestEvidenceResponse) api.DebugRequestEvidenceResponse {
	d := api.DebugDependencyLatencyItem{Type: "binding", Kind: "postgres", Name: "orders " + testToken,
		Calls: 40, ErrorCalls: 6, ErrorRatePct: 15, P50MS: 40, P95MS: 100, P99MS: 110,
		BaselineP95MS: 40, CurrentP95MS: 100, P95DeltaMS: 60, RegressionFactor: 2.5, Regression: true,
		BaselineErrorRatePct: 0, CurrentErrorRatePct: 30, ErrorRateDeltaPct: 30, FailureRegression: true, TopErrorType: "timeout",
		Queries: []api.DebugDependencyQueryItem{{Pattern: "excluded-sql SELECT secret FROM accounts"}}, QueriesTruncated: true}
	e.DependencyComparison = &api.DebugDependencyDeploymentComparison{
		CurrentDeploymentID: e.Request.DeploymentID, PreviousDeploymentID: "release-previous", Route: e.Request.Route,
		CurrentDeploymentTag: "excluded-tag", PreviousCommitSHA: "excluded-commit", Dependencies: []api.DebugDependencyLatencyItem{d}}
	kept := make([]api.DebugEvidenceFinding, 0, len(e.Explanation.Findings))
	for _, f := range e.Explanation.Findings {
		// Keep the combined fixture within native budgets. Both route
		// regression kinds remain independently covered by ADR-1628 tests.
		if f.Code != "dependency_regression" && f.Code != "performance_regression" {
			kept = append(kept, f)
		}
	}
	e.Explanation.Findings = append(kept,
		api.DebugEvidenceFinding{Code: "dependency_regression", Detail: "excluded-prose " + testToken, Confidence: "medium", EvidenceRefs: []string{requestDependencyReference(d)}},
		api.DebugEvidenceFinding{Code: "dependency_failure_regression", Detail: "excluded-query", Confidence: "high", EvidenceRefs: []string{requestDependencyReference(d)}})
	recs := make([]api.DebugEvidenceRecommendation, 0, len(e.Explanation.Recommendations))
	for _, r := range e.Explanation.Recommendations {
		if r.Action != "inspect_query_plan" {
			recs = append(recs, r)
		}
	}
	e.Explanation.Recommendations = append(recs, api.DebugEvidenceRecommendation{Action: "inspect_dependency_change", Detail: "excluded-change"})
	return e
}

func TestRequestDependencyComparisonNativeFindings(t *testing.T) {
	for _, mode := range []string{"both", "latency", "failures", "separate_signals", "redacted_label_collision", "empty_kind", "empty_type_and_kind", "long_text"} {
		t.Run(mode, func(t *testing.T) {
			e := withRequestDependencyComparison(requestFixtureEvidence())
			d := &e.DependencyComparison.Dependencies[0]
			latencyRef, failureRef := "/dependency_comparison/dependencies/0", "/dependency_comparison/dependencies/0"
			switch mode {
			case "latency":
				d.FailureRegression = false
			case "failures":
				d.Regression = false
			case "separate_signals":
				second := *d
				second.Name, second.Regression = "another binding", false
				d.FailureRegression = false
				e.DependencyComparison.Dependencies = append(e.DependencyComparison.Dependencies, second)
				failureRef = "/dependency_comparison/dependencies/1"
			case "redacted_label_collision":
				second := *d
				second.Name = "orders"
				d.Name, d.Regression, d.FailureRegression = "orders\n", false, false
				e.DependencyComparison.Dependencies = append(e.DependencyComparison.Dependencies, second)
				latencyRef, failureRef = "/dependency_comparison/dependencies/1", "/dependency_comparison/dependencies/1"
			case "empty_kind":
				d.Kind = ""
			case "empty_type_and_kind":
				d.Type, d.Kind = "", ""
			case "long_text":
				d.Name = strings.Repeat("x", api.MCPRequestFieldMaxBytes) + testToken
			}
			e.Explanation = debugger.Synthesize(e)
			m := &management{}
			v := m.projectRequestEvidence(m.projectRequest(requestArgs{App: "alpha", RequestID: "row-1"}, "app-1", "staging", e.Request), e, map[string]string{"release-base": "staging", "release-previous": "production"})
			c := v.DependencyComparison
			if (c.Status != "observed" && c.Status != "partial") || c.PreviousDeploymentScope != "production" || c.CurrentDeploymentScope != "staging" || c.SourceCompleteness != "unknown" || c.Route != "/orders" || !c.RouteModified || len(c.Dependencies) == 0 || c.Dependencies[0].Calls != 40 {
				t.Fatal("native comparison lost scope, metrics or uncertainty")
			}
			if mode == "long_text" && (c.Status != "partial" || !c.TextTruncated || !v.TextTruncated || len(c.Dependencies[0].Name) > api.MCPRequestFieldMaxBytes) {
				t.Fatal("comparison text budget or response uncertainty lost")
			}
			refs := map[string]string{}
			for _, f := range v.Explanation.Findings {
				if f.Code == "dependency_regression" || f.Code == "dependency_failure_regression" {
					if !f.Approximate || f.Confidence != "high" {
						t.Fatal("aggregate comparison lost native confidence or approximation")
					}
					refs[f.Code] = f.EvidenceRefs[0]
				}
			}
			if (mode != "failures" && refs["dependency_regression"] != latencyRef) || (mode != "latency" && refs["dependency_failure_regression"] != failureRef) {
				t.Fatalf("native dependency identity did not resolve to its projected row: %+v", refs)
			}
			for _, r := range v.Explanation.Recommendations {
				if r.Action == "inspect_dependency_change" && r.EvidenceRefs[0] != map[bool]string{true: latencyRef, false: failureRef}[mode == "latency"] {
					t.Fatal("dependency investigation did not prefer failure support")
				}
			}
			encoded, _ := json.Marshal(v)
			for _, marker := range []string{"excluded-", "fp_test_", "private-query", `"queries"`, `"pattern"`, `"current_commit_sha"`, `"previous_deployment_tag"`} {
				if strings.Contains(string(encoded), marker) {
					t.Fatalf("comparison leaked excluded data: %s", marker)
				}
			}
			assertExplanationReferences(t, v)
		})
	}
}

func TestRequestDependencyComparisonAvailabilityAndValidation(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "journal", "no_count", "no_identity", "unattributed", "wrong_current", "same_pair", "wrong_route", "empty_route", "invalid_previous", "long_previous", "unverified_previous", "bad_count", "bad_rate", "bad_duration", "bad_factor", "native_truncated", "projection_truncated"} {
		t.Run(mode, func(t *testing.T) {
			e := withRequestDependencyComparison(requestFixtureEvidence())
			n := e.DependencyComparison
			scopes := map[string]string{"release-base": "staging", "release-previous": "production"}
			want := "invalid_native_comparison"
			switch mode {
			case "missing":
				e.DependencyComparison, want = nil, "unavailable"
			case "empty":
				n.Dependencies, want = nil, "no_retained_dependencies"
			case "journal":
				e.Request.EvidenceStatus, want = "request_id_only", "unavailable"
			case "no_count":
				e.Request.Count, want = 0, "unavailable"
			case "no_identity":
				e.Request.ID, want = "", "unavailable"
			case "unattributed":
				e.Request.DeploymentID, want = "", "unavailable"
			case "wrong_current":
				n.CurrentDeploymentID = "other"
			case "same_pair":
				n.PreviousDeploymentID = n.CurrentDeploymentID
			case "wrong_route":
				n.Route = "/orders?other-private-query"
			case "empty_route":
				n.Route, e.Request.Route = "", ""
			case "invalid_previous":
				n.PreviousDeploymentID = "excluded/other"
			case "long_previous":
				n.PreviousDeploymentID = strings.Repeat("x", api.MCPRequestFieldMaxBytes+1)
			case "unverified_previous":
				delete(scopes, "release-previous")
				want = "unavailable"
			case "bad_count":
				n.Dependencies[0].ErrorCalls = n.Dependencies[0].Calls + 1
			case "bad_rate":
				n.Dependencies[0].CurrentErrorRatePct = math.NaN()
			case "bad_duration":
				n.Dependencies[0].CurrentP95MS = -1
			case "bad_factor":
				n.Dependencies[0].RegressionFactor = math.Inf(1)
			case "native_truncated":
				n.Truncated, want = true, "partial"
			case "projection_truncated":
				d := n.Dependencies[0]
				n.Dependencies, want = nil, "partial"
				for i := 0; i <= api.MCPRequestDependencyComparisonMax; i++ {
					n.Dependencies = append(n.Dependencies, d)
				}
			}
			m := &management{}
			v := m.projectRequestEvidence(m.projectRequest(requestArgs{App: "alpha", RequestID: "row-1"}, "app-1", "staging", e.Request), e, scopes)
			c := v.DependencyComparison
			if c.Status != want || v.Request.Status != 503 || c.Dependencies == nil {
				t.Fatalf("comparison state lost or request discarded: %s want %s", c.Status, want)
			}
			if want != "partial" && (len(c.Dependencies) != 0 || (want != "no_retained_dependencies" && c.PreviousDeploymentID != "")) {
				t.Fatal("unverified/invalid comparison leaked identity or rows")
			}
			for _, f := range v.Explanation.Findings {
				if f.Code == "dependency_regression" || f.Code == "dependency_failure_regression" {
					t.Fatal("missing, invalid or truncated comparison supported a finding")
				}
			}
			if mode == "projection_truncated" && (!c.DependenciesTruncated || c.NativeDependencyCount != api.MCPRequestDependencyComparisonMax+1 || c.ReturnedDependencyCount != api.MCPRequestDependencyComparisonMax) {
				t.Fatal("comparison projection budget lost")
			}
			assertExplanationReferences(t, v)
		})
	}
}

func TestRequestDependencyComparisonUnsupportedFindings(t *testing.T) {
	for _, mode := range []string{"ambiguous_label", "missing_native_findings", "wrong_reference", "extra_reference", "missing_reference", "no_calls", "no_latency_flag", "no_latency_increase", "no_factor_increase", "no_failure_flag", "no_error_calls", "no_error_increase", "application_failure"} {
		t.Run(mode, func(t *testing.T) {
			e := withRequestDependencyComparison(requestFixtureEvidence())
			d := &e.DependencyComparison.Dependencies[0]
			code := "dependency_failure_regression"
			if strings.HasPrefix(mode, "no_latency") || mode == "no_factor_increase" {
				code = "dependency_regression"
			}
			e.Explanation.Findings = []api.DebugEvidenceFinding{{Code: code, Confidence: "high", EvidenceRefs: []string{requestDependencyReference(*d)}}}
			switch mode {
			case "ambiguous_label":
				other := *d
				other.Type = "platform"
				e.DependencyComparison.Dependencies = append(e.DependencyComparison.Dependencies, other)
			case "missing_native_findings":
				e.Explanation.Findings = nil
			case "wrong_reference":
				e.Explanation.Findings[0].EvidenceRefs = []string{"dependency:excluded-identity"}
			case "extra_reference":
				e.Explanation.Findings[0].EvidenceRefs = []string{requestDependencyReference(*d), "request"}
			case "missing_reference":
				e.Explanation.Findings[0].EvidenceRefs = nil
			case "no_calls":
				d.Calls, d.ErrorCalls = 0, 0
			case "no_latency_flag":
				d.Regression = false
			case "no_latency_increase":
				d.CurrentP95MS = d.BaselineP95MS
			case "no_factor_increase":
				d.RegressionFactor = 1
			case "no_failure_flag":
				d.FailureRegression = false
			case "no_error_calls":
				d.ErrorCalls = 0
			case "no_error_increase":
				d.CurrentErrorRatePct = d.BaselineErrorRatePct
			case "application_failure":
				d.Type = "application"
			}
			m := &management{}
			v := m.projectRequestEvidence(m.projectRequest(requestArgs{App: "alpha", RequestID: "row-1"}, "app-1", "staging", e.Request), e, map[string]string{"release-base": "staging", "release-previous": "production"})
			if v.DependencyComparison.Status != "observed" || len(v.Explanation.Findings) != 0 || len(v.Explanation.Recommendations) != 0 {
				t.Fatal("ambiguous, absent or contradictory native support created a finding/action")
			}
		})
	}
}

func TestRequestDependencyComparisonProtocolAndOwnership(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "approved", "denied", "replaced", "wrong_previous_app", "previous_disappeared", "previous_app_changed", "previous_scope_changed", "current_scope_changed", "app_replaced", "wrong_route", "invalid_metrics", "missing"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				var reads, previousReads, currentReads atomic.Int32
				var changed atomic.Bool
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Error("dependency inspection wrote native state")
					}
					switch r.URL.Path {
					case "/v1/apps/alpha":
						id := "app-1"
						if changed.Load() {
							id = "replaced"
						}
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
					case "/v1/deployments/release-base":
						scope := "staging"
						if currentReads.Add(1) > 1 && mode == "current_scope_changed" {
							scope = "production"
						}
						_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "release-base", AppID: "app-1", Scope: scope})
					case "/v1/deployments/release-previous":
						count := previousReads.Add(1)
						id, scope := "app-1", "production"
						if mode == "wrong_previous_app" || (mode == "previous_app_changed" && count > 1) {
							id = "excluded-other-app"
						}
						if mode == "previous_disappeared" {
							api.WriteProblem(w, api.NewProblem(404, "not_found", "Unavailable", "missing deployment"))
							return
						}
						if mode == "previous_scope_changed" && count > 1 {
							scope = "staging"
						}
						if mode == "app_replaced" {
							changed.Store(true)
						}
						_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "release-previous", AppID: id, Scope: scope})
					case "/v1/apps/alpha/debug/requests/row-1/evidence":
						reads.Add(1)
						e := withRequestDependencyComparison(requestFixtureEvidence())
						switch mode {
						case "wrong_route":
							e.DependencyComparison.Route = "/orders?different-query"
						case "invalid_metrics":
							e.DependencyComparison.Dependencies[0].Calls = -1
						case "missing":
							e.DependencyComparison = nil
						}
						_ = json.NewEncoder(w).Encode(e)
					default:
						t.Errorf("unexpected native query %s", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer backend.Close()
				opts := Options{APIBase: backend.URL}
				if mode != "local" {
					opts.Environments = &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "app-1"}}
					if mode == "denied" {
						opts.Environments.RequestReadApps = nil
					}
					if mode == "replaced" {
						opts.Environments.RequestReadApps["alpha"] = "old-app"
					}
				}
				denied := mode == "denied" || mode == "replaced"
				blocked := denied || strings.HasPrefix(mode, "previous_") || mode == "wrong_previous_app" || mode == "current_scope_changed" || mode == "app_replaced"
				cs, ctx := connectConfiguredTestServer(t, backend, opts, version)
				out := callTool(t, ctx, cs, "gregale_get_request_evidence", requestArgs{App: "alpha", RequestID: "row-1"}, blocked)
				if denied {
					if reads.Load() != 0 || previousReads.Load() != 0 || currentReads.Load() != 0 || string(out["code"]) != `"request_access_denied"` {
						t.Fatal("comparison evidence accessed before diagnostic consent")
					}
					return
				}
				if blocked {
					if out["request"] != nil || out["dependency_comparison"] != nil {
						t.Fatal("released evidence after deployment ownership, scope or app identity changed")
					}
					return
				}
				var v requestEvidenceView
				encoded, _ := json.Marshal(out)
				_ = json.Unmarshal(encoded, &v)
				want, previous := "observed", int32(2)
				if mode == "wrong_route" || mode == "invalid_metrics" {
					want, previous = "invalid_native_comparison", 0
				}
				if mode == "missing" {
					want, previous = "unavailable", 0
				}
				if reads.Load() != 1 || previousReads.Load() != previous || currentReads.Load() != 2 || v.DependencyComparison.Status != want || v.Request.Status != 503 {
					t.Fatal("lost ownership rechecks, comparison state or introduced evidence queries")
				}
				assertExplanationReferences(t, v)
				before := previousReads.Load()
				prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_request_diagnosis", Arguments: map[string]string{"app": "alpha", "request_id": "row-1"}})
				if err != nil || !strings.Contains(prompt.Messages[0].Content.(*mcp.TextContent).Text, "native-reported current/previous deployment pair") || previousReads.Load() != before || reads.Load() != 1 {
					t.Fatal("prompt omitted comparison guidance or fetched evidence")
				}
			})
		}
	}
}
