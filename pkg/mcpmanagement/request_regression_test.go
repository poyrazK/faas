// adr: 1628
package mcpmanagement

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/debugger"
)

func withRequestRegression(e api.DebugRequestEvidenceResponse) api.DebugRequestEvidenceResponse {
	r := regressionsFixture().Regressions[0]
	r.DeploymentID, r.Route = e.Request.DeploymentID, e.Request.Route
	r.Kind, r.Factor, r.P95MS, r.P95BaseMS = "latency_and_errors", "2.50", 250, 100
	r.ErrorRateBasePct = 5
	e.Regression, e.Explanation.Status = &r, "regression_detected"
	// Leave one unsupported native dependency finding, within the native
	// eight-finding budget after path/guest/route findings are all present.
	kept := make([]api.DebugEvidenceFinding, 0, len(e.Explanation.Findings))
	for _, f := range e.Explanation.Findings {
		if f.Code != "excluded-unknown-code" {
			kept = append(kept, f)
		}
	}
	e.Explanation.Findings = append(kept,
		api.DebugEvidenceFinding{Code: "error_regression", Title: "excluded-title", Detail: "excluded-query " + testToken, Confidence: "high", EvidenceRefs: []string{"regression"}},
		api.DebugEvidenceFinding{Code: "performance_regression", Detail: "excluded-query", Confidence: "medium", EvidenceRefs: []string{"regression"}})
	e.Explanation.Recommendations = append(e.Explanation.Recommendations, api.DebugEvidenceRecommendation{Action: "compare_deployments", Detail: "excluded-action"})
	return e
}

func TestRequestRegressionProjectionAndFindings(t *testing.T) {
	for _, kind := range []string{"latency", "errors", "latency_and_errors"} {
		for _, state := range []string{"active", "acknowledged", "dismissed"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				e := withRequestRegression(requestFixtureEvidence())
				e.Regression.Kind, e.Regression.State = kind, state
				// Visibility remains native-authoritative even with a future
				// dismissal timestamp; MCP does not run a second visibility rule.
				e.Regression.DismissedUntil = "2099-10-17T10:59:00Z"
				e.Request.Status = 200
				e.Explanation = debugger.Synthesize(e)
				m := &management{}
				v := m.projectRequestEvidence(m.projectRequest(requestArgs{App: "alpha", RequestID: "row-1"}, "app-1", "staging", e.Request), e, nil)
				r := v.Regression
				if r.Status != "observed" || r.Basis != "native_route_deployment_observation" || r.SourceCompleteness != "unknown" || r.Observation == nil || r.Observation.DeploymentScope != "staging" || r.Observation.Route != "/orders" || !r.Observation.RouteModified || r.Observation.Factor != "2.50" || r.Observation.State != state || r.Observation.SuspectedDependency.Basis != "hypothesis" {
					t.Fatal("matched regression lost metrics, scope, visibility or uncertainty")
				}
				codes := map[string]bool{}
				for _, f := range v.Explanation.Findings {
					codes[f.Code] = true
					if f.Code == "error_regression" || f.Code == "performance_regression" {
						if !f.Approximate || f.Confidence != "high" || len(f.EvidenceRefs) != 1 || f.EvidenceRefs[0] != "/regression/observation" {
							t.Fatal("aggregate finding lost native confidence or local citation")
						}
					}
				}
				if codes["error_regression"] != (kind != "latency") || codes["performance_regression"] != (kind != "errors") {
					t.Fatal("regression kind inferred from HTTP status or hidden evidence")
				}
				if len(v.Explanation.Recommendations) == 0 || v.Explanation.Recommendations[0].Action != "compare_deployments" || v.Explanation.Recommendations[0].EvidenceRefs[0] != "/regression/observation" {
					t.Fatal("comparison investigation suggestion lost supported reference")
				}
				encoded, _ := json.Marshal(v)
				for _, marker := range []string{"excluded-", "private-query", "fp_test_", `"query"`, `"baseline_deployment_id"`} {
					if strings.Contains(string(encoded), marker) {
						t.Fatalf("unexposed native regression data leaked: %s", marker)
					}
				}
				assertExplanationReferences(t, v)
			})
		}
	}
}

func TestRequestRegressionUnavailableAndInvalidEvidence(t *testing.T) {
	for _, mode := range []string{"none", "unavailable", "legacy", "detected_without_row", "wrong_route", "redacted_route_collision", "truncated_route_collision", "wrong_deployment", "missing_route", "wrong_status", "unknown_kind", "unknown_state", "resolved", "bad_factor", "bad_time", "reversed_time", "bad_count", "negative_p95", "bad_rate", "bad_suspect", "journal", "no_identity", "no_deployment", "no_count"} {
		t.Run(mode, func(t *testing.T) {
			e := withRequestRegression(requestFixtureEvidence())
			want := "invalid_native_regression"
			switch mode {
			case "none", "unavailable", "legacy", "detected_without_row":
				e.Regression = nil
				switch mode {
				case "none":
					e.Explanation.Status, want = "unobserved", "no_matching_observation"
				case "unavailable":
					e.Explanation.Status, want = "regression_unavailable", "unavailable"
				case "legacy":
					e.Explanation.Status, want = "", "unknown"
				}
			case "wrong_route":
				e.Regression.Route = "/other"
			case "redacted_route_collision":
				e.Regression.Route = "/orders?other-secret"
			case "truncated_route_collision":
				e.Request.Route = "/" + strings.Repeat("x", api.MCPRequestFieldMaxBytes) + "a"
				e.Regression.Route = strings.TrimSuffix(e.Request.Route, "a") + "b"
			case "wrong_deployment":
				e.Regression.DeploymentID = "excluded-other-app-deployment"
			case "missing_route":
				e.Request.Route, e.Regression.Route = "", ""
			case "wrong_status":
				e.Explanation.Status = "regression_unavailable"
			case "unknown_kind":
				e.Regression.Kind = "excluded-kind"
			case "unknown_state":
				e.Regression.State = "excluded-state"
			case "resolved":
				e.Regression.State = "resolved"
			case "bad_factor":
				e.Regression.Factor = "NaN"
			case "bad_time":
				e.Regression.LastDetectedAt = "excluded-time"
			case "reversed_time":
				e.Regression.FirstDetectedAt = "2099-10-17T10:00:00Z"
			case "bad_count":
				e.Regression.AffectedCount = -1
			case "negative_p95":
				e.Regression.P95MS = -1
			case "bad_rate":
				e.Regression.ErrorRatePct = math.NaN()
			case "bad_suspect":
				e.Regression.SuspectedDependency.RegressionFactor = math.Inf(1)
			case "journal":
				e.Request.EvidenceStatus, want = "request_id_only", "unavailable"
			case "no_identity":
				e.Request.ID, want = "", "unavailable"
			case "no_deployment":
				e.Request.DeploymentID, want = "", "unavailable"
			case "no_count":
				e.Request.Count, want = 0, "unavailable"
			}
			m := &management{}
			v := m.projectRequestEvidence(m.projectRequest(requestArgs{App: "alpha", RequestID: "row-1"}, "app-1", "staging", e.Request), e, nil)
			if v.Regression.Status != want || v.Regression.Observation != nil || v.Request.Status != 503 {
				t.Fatalf("unavailable regression discarded request or exposed observation: %s", v.Regression.Status)
			}
			for _, f := range v.Explanation.Findings {
				if f.Code == "error_regression" || f.Code == "performance_regression" {
					t.Fatal("unsupported regression became a finding")
				}
			}
			for _, r := range v.Explanation.Recommendations {
				if r.Action == "compare_deployments" {
					t.Fatal("unsupported regression became a comparison suggestion")
				}
			}
			assertExplanationReferences(t, v)
		})
	}
}

func TestRequestRegressionContradictoryFindings(t *testing.T) {
	for _, mode := range []string{"missing_native_findings", "wrong_reference", "extra_reference", "missing_reference", "no_latency_increase", "no_affected_count", "no_error_increase", "no_error_count"} {
		t.Run(mode, func(t *testing.T) {
			e := withRequestRegression(requestFixtureEvidence())
			code := "error_regression"
			if mode == "no_latency_increase" || mode == "no_affected_count" {
				code = "performance_regression"
			}
			e.Explanation.Findings = []api.DebugEvidenceFinding{{Code: code, Confidence: "high", EvidenceRefs: []string{"regression"}}}
			switch mode {
			case "missing_native_findings":
				e.Explanation.Findings = nil
			case "wrong_reference":
				e.Explanation.Findings[0].EvidenceRefs = []string{"regression:excluded-id"}
			case "extra_reference":
				e.Explanation.Findings[0].EvidenceRefs = []string{"regression", "request"}
			case "missing_reference":
				e.Explanation.Findings[0].EvidenceRefs = nil
			case "no_latency_increase":
				e.Regression.P95MS = e.Regression.P95BaseMS
			case "no_affected_count":
				e.Regression.AffectedCount = 0
			case "no_error_increase":
				e.Regression.ErrorRatePct = e.Regression.ErrorRateBasePct
			case "no_error_count":
				e.Regression.ErrorCount = 0
			}
			m := &management{}
			v := m.projectRequestEvidence(m.projectRequest(requestArgs{App: "alpha", RequestID: "row-1"}, "app-1", "default", e.Request), e, nil)
			if v.Regression.Status != "observed" || len(v.Explanation.Findings) != 0 || len(v.Explanation.Recommendations) != 0 {
				t.Fatal("contradictory or absent native support invented a finding or action")
			}
		})
	}
}

func TestRequestRegressionTextBounds(t *testing.T) {
	for _, field := range []string{"suspect", "route", "deployment"} {
		t.Run(field, func(t *testing.T) {
			e := withRequestRegression(requestFixtureEvidence())
			long := strings.Repeat("x", api.MCPRequestFieldMaxBytes) + testToken
			switch field {
			case "suspect":
				e.Regression.SuspectedDependency.Name = long
			case "route":
				e.Regression.Route, e.Request.Route = "/"+long, "/"+long
			case "deployment":
				e.Regression.DeploymentID, e.Request.DeploymentID = long, long
			}
			m := &management{}
			v := m.projectRequestEvidence(m.projectRequest(requestArgs{App: "alpha", RequestID: "row-1"}, "app-1", "staging", e.Request), e, nil)
			r := v.Regression
			if r.Status != "observed" || !r.TextTruncated || !v.TextTruncated || !v.Explanation.EvidenceLimited {
				t.Fatal("regression truncation or explanation uncertainty lost")
			}
			if len(r.Observation.Route) > api.MCPRequestFieldMaxBytes || len(r.Observation.DeploymentID) > api.MCPRequestFieldMaxBytes || len(r.Observation.SuspectedDependency.Name) > api.MCPRequestFieldMaxBytes {
				t.Fatal("regression field exceeded its output budget")
			}
			encoded, _ := json.Marshal(r)
			if strings.Contains(string(encoded), "fp_test_") {
				t.Fatal("truncation boundary exposed credential text")
			}
		})
	}
}
