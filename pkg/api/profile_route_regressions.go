package api

// ProfileRouteRegression is an advisory observation of route-associated CPU.
// Collection coverage cannot establish complete request instrumentation.
type ProfileRouteRegression struct {
	CodeReason        string                                `json:"code_reason,omitempty"`
	CodeEvidence      []ProfileRegressionEvidence           `json:"code_evidence,omitempty"`
	LabelCoverage     *ProfileRouteLabelComparison          `json:"label_coverage,omitempty"`
	Route             string                                `json:"route"`
	Status            string                                `json:"status"`
	Reason            string                                `json:"reason"`
	BaselineRequests  *int64                                `json:"baseline_requests,omitempty"`
	CandidateRequests *int64                                `json:"candidate_requests,omitempty"`
	Metric            *ProfileRegressionCPUPerRequestMetric `json:"metric,omitempty"`
	ComparisonURL     string                                `json:"comparison_url,omitempty"`
}
