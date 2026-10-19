// adr: 1625
package mcpmanagement

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func requestExplanationFixture() api.DebugEvidenceExplanation {
	return api.DebugEvidenceExplanation{Status: "unobserved", GeneratedBy: "rules-v1", Headline: "excluded-headline", Diagnosis: "excluded-diagnosis", Confidence: "excluded-confidence",
		PrimarySpan: &api.DebugTelemetrySpan{Name: "excluded-span", DBStatement: "excluded-sql"},
		Findings: []api.DebugEvidenceFinding{
			{Code: "request_failure", Title: "excluded-title", Detail: "excluded-sql SELECT secret FROM accounts " + testToken, Confidence: "high", EvidenceRefs: []string{"request"}},
			{Code: "evidence_gap", Title: "excluded-title", Detail: "excluded-sql", Confidence: "low", EvidenceRefs: []string{"correlation:guest"}},
			{Code: "dependency_regression", Detail: "excluded-query", Confidence: "high", EvidenceRefs: []string{"dependency:excluded-name"}},
			{Code: "excluded-unknown-code", Detail: "excluded-query", Confidence: "high", EvidenceRefs: []string{"request"}},
		}, Recommendations: []api.DebugEvidenceRecommendation{
			{Action: "inspect_guest_failure", Detail: "excluded-action " + testToken},
			{Action: "improve_telemetry", Detail: "excluded-action"},
			{Action: "inspect_query_plan", Detail: "excluded-query"},
		}, EvidenceRefs: []api.DebugEvidenceRef{{Kind: "request", Label: "excluded-reference", Value: "excluded-raw-value"}}}
}

func explanationFixtureView() requestEvidenceView {
	count, latency, cold := 7, 90, true
	return requestEvidenceView{Request: requestView{ID: "row-1", Status: 503, Count: &count, LatencyMS: &latency, ColdBoot: &cold},
		Timeline: []requestTimelineView{}, DependencyLatency: []api.DebugRequestDependencyLatency{},
		Correlation: api.DebugRequestCorrelation{Complete: false, Stages: []api.DebugRequestCorrelationStage{
			{Phase: "guest", Status: "missing"}, {Phase: "queue", Status: "observed", DurationMS: 50, Approximate: true},
		}}}
}

func TestRequestExplanationProjection(t *testing.T) {
	v := explanationFixtureView()
	v.Explanation = projectRequestExplanation(v, requestExplanationFixture(), requestPathLinks{}, nil)
	x := v.Explanation
	if x.Status != "partial" || x.ConfidenceBasis != "native_rule_label" || x.NativeGenerator != "rules-v1" || x.SourceCompleteness != "unknown" || x.NativeFindingCount != 4 || x.OmittedFindingCount != 2 || len(x.Findings) != 2 || len(x.Recommendations) != 1 || !x.Findings[0].Approximate {
		t.Fatalf("lost explanation provenance or exclusions: %+v", x)
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, omitted := range []string{"excluded-", testToken, "SELECT", "primary_span", "dependency:excluded", "span:0"} {
		if strings.Contains(string(encoded), omitted) {
			t.Errorf("explanation leaked %s", omitted)
		}
	}
	assertExplanationReferences(t, v)
}

func TestRequestExplanationSupportedRules(t *testing.T) {
	for _, test := range []struct {
		code, reference, action string
		status                  int
	}{
		{"request_failure", "request", "inspect_guest_failure", 503},
		{"request_client_error", "request", "", 404},
		{"cold_boot", "request", "inspect_wake_timeline", 200},
		{"evidence_gap", "correlation:guest", "", 200},
		{"longest_stage", "correlation:queue", "inspect_stage_timeline", 200},
	} {
		t.Run(test.code, func(t *testing.T) {
			v := explanationFixtureView()
			v.Request.Status = test.status
			want := "partial"
			if test.code != "evidence_gap" {
				v.Correlation.Complete = true
				v.Correlation.Stages[0].Status, v.Correlation.Stages[0].DurationMS = "observed", 10
				want = "available"
			}
			n := api.DebugEvidenceExplanation{Status: "unobserved", GeneratedBy: "rules-v1", Findings: []api.DebugEvidenceFinding{{Code: test.code, Confidence: "medium", EvidenceRefs: []string{test.reference}}}}
			if test.action != "" {
				n.Recommendations = []api.DebugEvidenceRecommendation{{Action: test.action, Detail: "excluded-prose"}}
			}
			v.Explanation = projectRequestExplanation(v, n, requestPathLinks{}, nil)
			if len(v.Explanation.Findings) != 1 || v.Explanation.Findings[0].Confidence != "medium" || v.Explanation.Status != want {
				t.Fatalf("supported rule lost: %+v", v.Explanation)
			}
			if test.action != "" && len(v.Explanation.Recommendations) != 1 {
				t.Fatal("supported investigation suggestion lost")
			}
			if test.code == "longest_stage" && !v.Explanation.Findings[0].Approximate {
				t.Fatal("stage approximation lost")
			}
			assertExplanationReferences(t, v)
		})
	}
}

func TestRequestExplanationRejectsUnsupportedEvidence(t *testing.T) {
	for _, mode := range []string{"unknown_code", "hidden_span", "extra_reference", "missing_reference", "unknown_phase", "duplicate_phase", "truncated_stage", "wrong_status", "journal_only", "empty_count", "warm_request", "gap_observed", "not_longest", "negative_duration", "edge_longest", "missing_stage"} {
		t.Run(mode, func(t *testing.T) {
			v := explanationFixtureView()
			f := api.DebugEvidenceFinding{Code: "request_failure", Confidence: "high", EvidenceRefs: []string{"request"}}
			switch mode {
			case "unknown_code":
				f.Code = "excluded-unknown"
			case "hidden_span":
				f.EvidenceRefs = []string{"span:excluded-secret"}
			case "extra_reference":
				f.EvidenceRefs = []string{"request", "span:0"}
			case "missing_reference":
				f.EvidenceRefs = nil
			case "unknown_phase":
				f.Code, f.EvidenceRefs = "evidence_gap", []string{"correlation:excluded-phase"}
			case "duplicate_phase":
				f.Code, f.EvidenceRefs = "evidence_gap", []string{"correlation:guest"}
				v.Correlation.Stages = append(v.Correlation.Stages, v.Correlation.Stages[0])
			case "truncated_stage":
				f.Code, f.EvidenceRefs = "evidence_gap", []string{"correlation:guest"}
				v.CorrelationTruncated = true
			case "wrong_status":
				v.Request.Status = 200
			case "journal_only":
				v.Request.Count = nil
			case "empty_count":
				zero := 0
				v.Request.Count = &zero
			case "warm_request":
				f.Code = "cold_boot"
				cold := false
				v.Request.ColdBoot = &cold
			case "gap_observed":
				f.Code, f.EvidenceRefs = "evidence_gap", []string{"correlation:queue"}
			case "not_longest":
				f.Code, f.EvidenceRefs = "longest_stage", []string{"correlation:queue"}
				v.Correlation.Stages[0].Status, v.Correlation.Stages[0].DurationMS = "observed", 100
			case "negative_duration":
				f.Code, f.EvidenceRefs = "longest_stage", []string{"correlation:queue"}
				v.Correlation.Stages[0].DurationMS = -1
			case "edge_longest":
				f.Code, f.EvidenceRefs = "longest_stage", []string{"correlation:edge"}
				v.Correlation.Stages[1].Phase = "edge"
			case "missing_stage":
				f.Code, f.EvidenceRefs = "longest_stage", []string{"correlation:billing"}
			}
			n := requestExplanationFixture()
			n.Findings = []api.DebugEvidenceFinding{f}
			x := projectRequestExplanation(v, n, requestPathLinks{}, nil)
			if x.Status != "no_supported_findings" || len(x.Findings) != 0 || len(x.Recommendations) != 0 || x.OmittedFindingCount != 1 {
				t.Fatalf("released unsupported finding/action: %+v", x)
			}
			if mode == "extra_reference" && !x.ReferencesTruncated {
				t.Fatal("reference budget not reported")
			}
		})
	}
}

func TestRequestExplanationAvailabilityAndBudgets(t *testing.T) {
	for _, mode := range []string{"unavailable", "legacy_status", "empty", "bounded", "prefix", "unknown_confidence", "unknown_metadata", "regression_unavailable", "limited", "dangling_action", "duplicate_action"} {
		t.Run(mode, func(t *testing.T) {
			v := explanationFixtureView()
			n := requestExplanationFixture()
			n.Findings, n.Recommendations = n.Findings[:1], n.Recommendations[:1]
			want := "partial"
			switch mode {
			case "unavailable":
				n = api.DebugEvidenceExplanation{Headline: "excluded-legacy-prose"}
				want = "unavailable"
			case "legacy_status":
				n = api.DebugEvidenceExplanation{Status: "unobserved", Headline: "excluded-legacy-prose"}
				want = "unavailable"
			case "empty":
				n.Findings, n.Recommendations = nil, nil
				want = "no_supported_findings"
			case "bounded":
				n.Findings = make([]api.DebugEvidenceFinding, api.MCPRequestExplanationFindingsMax+1)
				for i := range n.Findings {
					n.Findings[i] = requestExplanationFixture().Findings[0]
				}
				n.Recommendations = make([]api.DebugEvidenceRecommendation, api.MCPRequestExplanationRecommendationsMax+1)
				for i := range n.Recommendations {
					n.Recommendations[i].Action = "inspect_guest_failure"
				}
			case "prefix":
				n.Findings = make([]api.DebugEvidenceFinding, api.MCPRequestExplanationFindingsMax+1)
				n.Findings[api.MCPRequestExplanationFindingsMax] = requestExplanationFixture().Findings[0]
				want = "no_supported_findings"
			case "unknown_confidence":
				n.Findings[0].Confidence = "excluded-confidence"
			case "unknown_metadata":
				n.Status, n.GeneratedBy = "excluded-status", "excluded-generator"
			case "regression_unavailable":
				n.Status = "regression_unavailable"
			case "limited":
				v.SourceSpansTruncated = true
			case "dangling_action":
				n.Recommendations[0].Action = "inspect_wake_timeline"
			case "duplicate_action":
				n.Recommendations = append(n.Recommendations, n.Recommendations[0])
			}
			x := projectRequestExplanation(v, n, requestPathLinks{}, nil)
			if x.Status != want {
				t.Fatalf("status=%s want=%s", x.Status, want)
			}
			if mode == "bounded" && (len(x.Findings) != api.MCPRequestExplanationFindingsMax || !x.FindingsTruncated || !x.RecommendationsTruncated || len(x.Recommendations) != 1) {
				t.Fatal("explanation output budgets or action deduplication lost")
			}
			if mode == "prefix" && (len(x.Findings) != 0 || x.OmittedFindingCount != api.MCPRequestExplanationFindingsMax+1 || !x.FindingsTruncated) {
				t.Fatal("filled the bounded prefix with an unprojected finding")
			}
			if mode == "unknown_confidence" && (x.Findings[0].Confidence != "unknown" || !x.UnknownConfidence) {
				t.Fatal("unknown native confidence upgraded")
			}
			if mode == "unknown_metadata" && (x.NativeStatus != "unknown" || x.NativeGenerator != "unknown") {
				t.Fatal("unreviewed native metadata released")
			}
			if mode == "dangling_action" && len(x.Recommendations) != 0 {
				t.Fatal("recommendation without supporting finding released")
			}
			if (mode == "unavailable" || mode == "legacy_status" || mode == "empty") && (x.Findings == nil || x.Recommendations == nil) {
				t.Fatal("empty explanation lists lost")
			}
		})
	}
}

func assertExplanationReferences(t *testing.T, view requestEvidenceView) {
	t.Helper()
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var root any
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatal(err)
	}
	resolve := func(pointer string) {
		t.Helper()
		if !strings.HasPrefix(pointer, "/") {
			t.Fatalf("reference is not a JSON Pointer: %s", pointer)
		}
		node := root
		for _, key := range strings.Split(pointer[1:], "/") {
			switch value := node.(type) {
			case map[string]any:
				node = value[key]
			case []any:
				i, err := strconv.Atoi(key)
				if err != nil || i < 0 || i >= len(value) {
					t.Fatalf("dangling array reference: %s", pointer)
				}
				node = value[i]
			default:
				t.Fatalf("dangling reference: %s", pointer)
			}
		}
		if node == nil {
			t.Fatalf("missing reference target: %s", pointer)
		}
	}
	for _, f := range view.Explanation.Findings {
		for _, ref := range f.EvidenceRefs {
			resolve(ref)
		}
	}
	for _, rec := range view.Explanation.Recommendations {
		for _, ref := range rec.EvidenceRefs {
			resolve(ref)
		}
	}
}
