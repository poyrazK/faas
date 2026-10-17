package mcpmanagement

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// Match the native synthesizer's canonical identity before redaction. These
// labels stay internal; only response-local JSON Pointers cross MCP.
func requestDependencyReference(dep api.DebugDependencyLatencyItem) string {
	kind := dep.Kind
	if kind == "" {
		kind = dep.Type
	}
	if kind == "" {
		return fmt.Sprintf("dependency:%q", dep.Name)
	}
	return fmt.Sprintf("dependency:%s %q", kind, dep.Name)
}

func linkRequestDependencyComparison(e api.DebugRequestEvidenceResponse, v requestDependencyComparisonView) map[string]int {
	links := map[string]int{}
	if (v.Status != "observed" && v.Status != "partial") || e.DependencyComparison == nil || v.NativeTruncated || v.DependenciesTruncated || len(v.Dependencies) != len(e.DependencyComparison.Dependencies) {
		return links
	}
	for i, dep := range e.DependencyComparison.Dependencies {
		ref := requestDependencyReference(dep)
		if _, exists := links[ref]; exists {
			links[ref] = -1 // Distinct type/kind/name tuples can share a native label.
		} else {
			links[ref] = i
		}
	}
	return links
}

func projectRequestDependencyFinding(e requestEvidenceView, f api.DebugEvidenceFinding, links map[string]int) (requestFindingView, bool) {
	v := requestFindingView{Code: f.Code, Confidence: "unknown", EvidenceRefs: []string{}}
	if len(f.EvidenceRefs) != api.MCPRequestExplanationRefsMax {
		return v, false
	}
	i, ok := links[f.EvidenceRefs[0]]
	if !ok || i < 0 || i >= len(e.DependencyComparison.Dependencies) {
		return v, false
	}
	d := e.DependencyComparison.Dependencies[i]
	if d.Calls <= 0 {
		return v, false
	}
	switch f.Code {
	case "dependency_regression":
		if !d.Regression || d.CurrentP95MS <= d.BaselineP95MS || d.RegressionFactor <= 1 {
			return v, false
		}
		v.Title = "Native comparison observed higher dependency latency"
		v.Detail = fmt.Sprintf("The dependency comparison reports current p95 %dms versus %dms baseline, factor %.2f. These sampled deployment aggregates do not establish a cause or an individual request measurement.", d.CurrentP95MS, d.BaselineP95MS, d.RegressionFactor)
	case "dependency_failure_regression":
		if !d.FailureRegression || d.Type == "application" || d.ErrorCalls <= 0 || d.CurrentErrorRatePct <= d.BaselineErrorRatePct {
			return v, false
		}
		v.Title = "Native comparison observed a higher dependency error rate"
		v.Detail = fmt.Sprintf("The dependency comparison reports current error rate %.1f%% versus %.1f%% baseline. These sampled deployment aggregates do not establish a cause or an individual request measurement.", d.CurrentErrorRatePct, d.BaselineErrorRatePct)
	default:
		return v, false
	}
	switch f.Confidence {
	case "high", "medium", "low":
		v.Confidence = f.Confidence
	}
	v.EvidenceRefs, v.Approximate = []string{fmt.Sprintf("/dependency_comparison/dependencies/%d", i)}, true
	return v, true
}
