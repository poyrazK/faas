package mcpmanagement

import "github.com/onebox-faas/faas/pkg/api"

type requestDependencyComparisonView struct {
	Status                  string                  `json:"status"`
	Basis                   string                  `json:"basis"`
	CurrentDeploymentID     string                  `json:"current_deployment_id,omitempty"`
	CurrentDeploymentScope  string                  `json:"current_deployment_scope,omitempty"`
	PreviousDeploymentID    string                  `json:"previous_deployment_id,omitempty"`
	PreviousDeploymentScope string                  `json:"previous_deployment_scope,omitempty"`
	Route                   string                  `json:"route,omitempty"`
	RouteModified           bool                    `json:"route_modified"`
	Dependencies            []dependencyLatencyView `json:"dependencies"`
	NativeDependencyCount   int                     `json:"native_returned_dependency_count"`
	ReturnedDependencyCount int                     `json:"returned_dependency_count"`
	NativeTruncated         bool                    `json:"native_truncated"`
	DependenciesTruncated   bool                    `json:"dependencies_truncated"`
	TextTruncated           bool                    `json:"text_truncated"`
	SourceCompleteness      string                  `json:"source_completeness"`
	Limitations             string                  `json:"limitations"`
}

func retainedRequestComparison(e api.DebugRequestEvidenceResponse) bool {
	r := e.Request
	return r.ID != "" && r.EvidenceStatus != "request_id_only" && r.Count > 0 && r.DeploymentID != ""
}

func validRequestDependencyComparison(e api.DebugRequestEvidenceResponse) bool {
	n := e.DependencyComparison
	if n == nil || !retainedRequestComparison(e) || n.Route == "" || n.Route != e.Request.Route || n.CurrentDeploymentID != e.Request.DeploymentID || n.PreviousDeploymentID == n.CurrentDeploymentID {
		return false
	}
	for _, id := range []string{n.CurrentDeploymentID, n.PreviousDeploymentID} {
		if len(id) > api.MCPRequestFieldMaxBytes || validateSegment(id, "deployment_id") != nil {
			return false
		}
	}
	for _, dep := range n.Dependencies[:min(len(n.Dependencies), api.MCPRequestDependencyComparisonMax)] {
		if validateDependencyLatency(dep) != nil {
			return false
		}
	}
	return true
}

func (m *management) projectRequestDependencyComparison(e api.DebugRequestEvidenceResponse, scopes map[string]string) requestDependencyComparisonView {
	v := requestDependencyComparisonView{Status: "unavailable", Basis: "native_route_deployment_dependency_comparison", Dependencies: []dependencyLatencyView{}, SourceCompleteness: "unknown",
		Limitations: "Sampled retained dependency aggregates weighted by represented request count, not measurements of this individual request or causal proof. The deployment pair is selected by the native API from retained evidence; previous does not mean healthy, immediately preceding release, or a baseline chosen by MCP. No exact comparison window, per-side sample counts or collection-completeness proof is supplied. Missing enrichment cannot distinguish absent history from a failed native read. Native and projection truncation remain separate; false truncation flags do not prove full capture. Dependency durations can overlap and must not be summed. SQL/query patterns, native tags/commits and raw spans are omitted. App-wide diagnostic consent grants no other environment/log access or writes. Reads and ownership rechecks are sequential, not an atomic snapshot; no new comparison, replay, collection, triage change or rollback executes."}
	if e.DependencyComparison == nil || !retainedRequestComparison(e) {
		return v
	}
	if !validRequestDependencyComparison(e) {
		v.Status = "invalid_native_comparison"
		return v
	}
	n := e.DependencyComparison
	currentScope, currentOK := scopes[n.CurrentDeploymentID]
	previousScope, previousOK := scopes[n.PreviousDeploymentID]
	if !currentOK || !previousOK {
		return v // Never export a pair whose deployment scopes were not checked.
	}
	text := func(s string) string { return m.requestText(s, &v.TextTruncated) }
	v.CurrentDeploymentID, v.PreviousDeploymentID = text(n.CurrentDeploymentID), text(n.PreviousDeploymentID)
	v.CurrentDeploymentScope, v.PreviousDeploymentScope = text(currentScope), text(previousScope)
	v.Route = requestRoute(n.Route, text)
	v.RouteModified = v.Route != n.Route
	v.NativeDependencyCount, v.NativeTruncated = len(n.Dependencies), n.Truncated
	v.DependenciesTruncated = len(n.Dependencies) > api.MCPRequestDependencyComparisonMax
	for _, dep := range n.Dependencies[:min(len(n.Dependencies), api.MCPRequestDependencyComparisonMax)] {
		v.Dependencies = append(v.Dependencies, m.projectDependencyLatencyItem(dep, &v.TextTruncated))
	}
	v.ReturnedDependencyCount, v.Status = len(v.Dependencies), "observed"
	switch {
	case v.NativeTruncated || v.DependenciesTruncated || v.TextTruncated:
		v.Status = "partial"
	case len(v.Dependencies) == 0:
		v.Status = "no_retained_dependencies"
	}
	return v
}

const requestDependencyComparisonGuidance = "Inspect dependency_comparison separately from the route regression and this request's path. It is sampled aggregate dependency evidence across the native-reported current/previous deployment pair, not a time-split trend or an individual request measurement. Both deployments are ownership/scope checked. Previous is native selection from retained observations, not proof of an immediately preceding or healthy release; do not infer a new baseline or automatically run gregale_compare_deployments. No exact comparison window, per-side sample counts or capture completeness is reported. Preserve native_truncated, dependencies_truncated, text_truncated and source_completeness unknown; unavailable cannot distinguish missing history from failed native enrichment. Missing/empty/invalid evidence does not prove health. An inspect_dependency_change suggestion is investigation only and executes nothing automatically. Findings cite response-local /dependency_comparison/dependencies/N rows only when the native reference resolves uniquely without collection truncation. Changes are observations, not proven causes or statistical significance. Do not sum overlapping dependency durations, request SQL/query patterns or follow suggestions into logs, collection, probes, replay, triage updates or writes without separate authorization."
