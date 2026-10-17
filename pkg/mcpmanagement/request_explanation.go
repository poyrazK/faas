package mcpmanagement

import "github.com/onebox-faas/faas/pkg/api"

type requestFindingView struct {
	Code         string   `json:"code"`
	Title        string   `json:"title"`
	Detail       string   `json:"detail"`
	Confidence   string   `json:"confidence"`
	EvidenceRefs []string `json:"evidence_refs"`
	Approximate  bool     `json:"approximate"`
}

type requestRecommendationView struct {
	Action       string            `json:"action"`
	Detail       string            `json:"detail"`
	EvidenceRefs []string          `json:"evidence_refs"`
	Tool         string            `json:"tool,omitempty"`
	Arguments    *wakeTimelineArgs `json:"arguments,omitempty"`
}

type requestExplanationView struct {
	Status                     string                      `json:"status"`
	Basis                      string                      `json:"basis"`
	NativeStatus               string                      `json:"native_status"`
	NativeGenerator            string                      `json:"native_generator"`
	ConfidenceBasis            string                      `json:"confidence_basis"`
	UnknownConfidence          bool                        `json:"unknown_confidence"`
	Findings                   []requestFindingView        `json:"findings"`
	Recommendations            []requestRecommendationView `json:"recommendations"`
	NativeFindingCount         int                         `json:"native_returned_finding_count"`
	NativeRecommendationCount  int                         `json:"native_returned_recommendation_count"`
	OmittedFindingCount        int                         `json:"omitted_finding_count"`
	OmittedRecommendationCount int                         `json:"omitted_recommendation_count"`
	FindingsTruncated          bool                        `json:"findings_truncated"`
	RecommendationsTruncated   bool                        `json:"recommendations_truncated"`
	ReferencesTruncated        bool                        `json:"references_truncated"`
	EvidenceLimited            bool                        `json:"evidence_limited"`
	SourceCompleteness         string                      `json:"source_completeness"`
	Limitations                string                      `json:"limitations"`
}

// Native prose can contain SQL and references to intentionally omitted spans,
// unexposed guest outcomes or regression fields. Only approved codes and fixed text
// supported by fields in the serialized MCP response cross this projection.
func projectRequestExplanation(e requestEvidenceView, n api.DebugEvidenceExplanation, links requestPathLinks, dependencyLinks map[string]int) requestExplanationView {
	v := requestExplanationView{Status: "available", Basis: "native_finding_codes_and_exposed_evidence", NativeStatus: "unknown", NativeGenerator: "unknown", ConfidenceBasis: "native_rule_label", Findings: []requestFindingView{}, Recommendations: []requestRecommendationView{},
		NativeFindingCount: len(n.Findings), NativeRecommendationCount: len(n.Recommendations), FindingsTruncated: len(n.Findings) > api.MCPRequestExplanationFindingsMax, RecommendationsTruncated: len(n.Recommendations) > api.MCPRequestExplanationRecommendationsMax,
		EvidenceLimited: e.TimelineTruncated || e.CorrelationTruncated || !e.Correlation.Complete || e.DependencyLatencyTruncated || e.SourceSpansTruncated || e.TextTruncated || e.CriticalPath.Status == "partial" || e.CriticalPath.Status == "invalid_native_path" || e.Request.Guest.Status == "partial" || e.Request.Guest.Status == "invalid_native_guest" || e.Regression.Status == "unavailable" || e.Regression.Status == "unknown" || e.Regression.Status == "invalid_native_regression" || e.DependencyComparison.Status == "unavailable" || e.DependencyComparison.Status == "invalid_native_comparison" || e.DependencyComparison.Status == "partial", SourceCompleteness: "unknown",
		Limitations: "Descriptions are fixed templates from approved native finding codes and exposed structured facts. Native headline, diagnosis, finding/recommendation prose, reference labels/values and primary spans are omitted. Unsupported, contradictory, ambiguous or hidden-evidence findings and actions are omitted, including those relying on hidden spans, unexposed guest outcomes, unexposed regression fields or unexposed deployment comparisons. Verified selected path segments can support span findings without exporting raw spans. References are JSON Pointers into this tool response, not URLs, IDs or permission grants. Confidence is an uncalibrated native rule label, not causal proof or a probability. Findings may share evidence and are not independent samples. Source completeness is unknown; unavailable, partial or empty explanations do not prove health. Recommendations are investigation suggestions only; no additional reads, collection, replay or changes occur."}
	switch n.Status {
	case "unobserved", "regression_detected", "regression_unavailable":
		v.NativeStatus = n.Status
	}
	if n.GeneratedBy == "rules-v1" {
		v.NativeGenerator = "rules-v1"
	}
	for _, f := range n.Findings[:min(len(n.Findings), api.MCPRequestExplanationFindingsMax)] {
		v.ReferencesTruncated = v.ReferencesTruncated || len(f.EvidenceRefs) > api.MCPRequestExplanationRefsMax
		finding, ok := projectRequestFinding(e, f)
		if !ok {
			finding, ok = projectRequestPathFinding(e, f, links)
		}
		if !ok {
			finding, ok = projectRequestDependencyFinding(e, f, dependencyLinks)
		}
		if ok {
			v.UnknownConfidence = v.UnknownConfidence || finding.Confidence == "unknown"
			v.Findings = append(v.Findings, finding)
		}
	}
	seen := map[string]bool{}
	for _, rec := range n.Recommendations[:min(len(n.Recommendations), api.MCPRequestExplanationRecommendationsMax)] {
		if projected, ok := projectRequestRecommendation(rec.Action, v.Findings); ok && !seen[rec.Action] {
			seen[rec.Action] = true
			v.Recommendations = append(v.Recommendations, linkRequestWakeRecommendation(e, projected))
		}
	}
	v.OmittedFindingCount = len(n.Findings) - len(v.Findings)
	v.OmittedRecommendationCount = len(n.Recommendations) - len(v.Recommendations)
	switch {
	case (n.Status == "" || n.GeneratedBy == "") && len(n.Findings) == 0 && len(n.Recommendations) == 0:
		v.Status = "unavailable"
	case len(v.Findings) == 0:
		v.Status = "no_supported_findings"
	case v.OmittedFindingCount > 0 || v.OmittedRecommendationCount > 0 || v.EvidenceLimited || v.UnknownConfidence || v.NativeStatus == "unknown" || v.NativeStatus == "regression_unavailable" || v.NativeGenerator == "unknown":
		v.Status = "partial"
	}
	return v
}

func projectRequestRecommendation(action string, findings []requestFindingView) (requestRecommendationView, bool) {
	v := requestRecommendationView{Action: action}
	code := ""
	switch action {
	case "inspect_dependency_change":
		for _, preferred := range []string{"dependency_failure_regression", "dependency_regression"} {
			for _, f := range findings {
				if f.Code == preferred {
					v.Detail = "Review the returned dependency metrics and native deployment pair. These observations do not prove root cause; additional logs or service access require separate authorization. No collection, comparison, configuration change or rollback executes automatically."
					v.EvidenceRefs = append([]string{}, f.EvidenceRefs...)
					return v, true
				}
			}
		}
		return v, false
	case "compare_deployments":
		for _, f := range findings {
			if f.Code == "error_regression" || f.Code == "performance_regression" {
				v.Detail = "Review the matched detector observation. Compare deployments only when the user supplies two exact deployment IDs; no baseline or healthy release is inferred, and no comparison or rollback executes automatically."
				v.EvidenceRefs = append([]string{}, f.EvidenceRefs...)
				return v, true
			}
		}
		return v, false
	case "inspect_guest_failure":
		for _, f := range findings {
			if f.Code == "guest_failure" {
				v.Detail = "Review the returned runner outcome and error class. Application logs require separate authorization."
				v.EvidenceRefs = append([]string{}, f.EvidenceRefs...)
				return v, true
			}
		}
		code, v.Detail = "request_failure", "Review the returned request outcome and correlation gaps. Application logs require separate authorization."
	case "inspect_wake_timeline":
		code, v.Detail = "cold_boot", "Review the returned timeline and correlation stages for startup timing; missing wake evidence remains unknown."
	case "inspect_stage_timeline":
		code, v.Detail = "longest_stage", "Review the returned correlation stages. Overlapping stage durations do not establish a causal critical path."
	case "inspect_dependency_failure":
		code, v.Detail = "dependency_failure", "Review the returned dependency category and error status. Additional logs or service reads require separate authorization."
	default:
		return v, false
	}
	for _, f := range findings {
		if f.Code == code {
			v.EvidenceRefs = append([]string{}, f.EvidenceRefs...)
			return v, true
		}
	}
	return v, false
}

const requestExplanationGuidance = "Use the returned explanation as a bounded projection of native finding codes and exposed facts. Its evidence_refs are JSON Pointers into the same tool response, not URLs or new tool selectors. Report native rule confidence as an uncalibrated label, not a probability or proven root cause. Preserve approximation, represented request counts, omissions, output budgets and evidence_limited. Unsupported findings, native prose and raw spans, unexposed guest outcomes and unexposed regression evidence are omitted; do not reconstruct them from missing references. An unavailable or empty explanation does not prove health, and native_status unobserved refers to regression availability, not healthy traffic. Recommendations are investigation suggestions only: retrieving them executes no actions and grants no additional environment/log access. Do not automatically collect, probe, replay, change configuration or follow recommendations into writes."
