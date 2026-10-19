package mcpmanagement

import (
	"math"

	"github.com/onebox-faas/faas/pkg/api"
)

// Explicit projection excludes query fingerprints and native deployment
// comparison payloads even when future native DTO fields are added.
type dependencyLatencyView struct {
	Type                 string  `json:"type"`
	Kind                 string  `json:"kind,omitempty"`
	Name                 string  `json:"name"`
	Calls                int64   `json:"calls"`
	ErrorCalls           int64   `json:"error_calls"`
	ErrorRatePct         float64 `json:"error_rate_pct"`
	P50MS                int64   `json:"p50_ms"`
	P95MS                int64   `json:"p95_ms"`
	P99MS                int64   `json:"p99_ms"`
	ExclusiveP50MS       int64   `json:"exclusive_p50_ms"`
	ExclusiveP95MS       int64   `json:"exclusive_p95_ms"`
	ExclusiveP99MS       int64   `json:"exclusive_p99_ms"`
	BaselineP95MS        int64   `json:"baseline_p95_ms"`
	CurrentP95MS         int64   `json:"current_p95_ms"`
	P95DeltaMS           int64   `json:"p95_delta_ms"`
	RegressionFactor     float64 `json:"regression_factor"`
	Regression           bool    `json:"regression"`
	BaselineErrorRatePct float64 `json:"baseline_error_rate_pct"`
	CurrentErrorRatePct  float64 `json:"current_error_rate_pct"`
	ErrorRateDeltaPct    float64 `json:"error_rate_delta_pct"`
	FailureRegression    bool    `json:"failure_regression"`
	TopErrorType         string  `json:"top_error_type,omitempty"`
}

type dependenciesView struct {
	performanceWindow
	Status                  string                     `json:"status"`
	TelemetryRows           int64                      `json:"telemetry_rows"`
	RepresentedRequests     int64                      `json:"represented_requests"`
	SpanSamples             int64                      `json:"span_samples"`
	Dependencies            []dependencyLatencyView    `json:"dependencies"`
	ReturnedDependencyCount int                        `json:"returned_dependency_count"`
	NativeDependencyCount   int                        `json:"native_returned_dependency_count"`
	Edges                   []dependencyImpactEdgeView `json:"edges"`
	NativeEdgeCount         int                        `json:"native_returned_edge_count"`
	ReturnedEdgeCount       int                        `json:"returned_edge_count"`
	EdgesTruncated          bool                       `json:"edges_truncated"`
	ExemplarsTruncated      bool                       `json:"exemplars_truncated"`
	SourceCompleteness      string                     `json:"source_completeness"`
	SourceComplete          bool                       `json:"source_complete"`
	SourceTruncated         bool                       `json:"source_truncated"`
	DependenciesTruncated   bool                       `json:"dependencies_truncated"`
	TextTruncated           bool                       `json:"text_truncated"`
	Complete                bool                       `json:"complete"`
	Basis                   string                     `json:"basis"`
	Limitations             string                     `json:"limitations"`
}

func (m *management) projectDependencyLatency(window performanceWindow, n api.DebugDependencyLatencyResponse) (dependenciesView, error) {
	view := dependenciesView{performanceWindow: window, Status: "observed", TelemetryRows: n.TelemetryRows, RepresentedRequests: n.RepresentedRequests, SpanSamples: n.SpanSamples,
		Dependencies: []dependencyLatencyView{}, NativeDependencyCount: len(n.Dependencies), Edges: []dependencyImpactEdgeView{}, NativeEdgeCount: len(n.Edges), EdgesTruncated: len(n.Edges) > api.MCPDependencyImpactEdgesMax, SourceCompleteness: "unknown", SourceComplete: n.Complete, SourceTruncated: n.Truncated, DependenciesTruncated: len(n.Dependencies) > api.MCPPerformanceDependenciesMax,
		Basis:       "sampled_retained_spans_weighted_by_request_count",
		Limitations: "Span evidence is sampled and aggregate rows are weighted by represented request count. Side sample availability is not reported; zero baseline/current scalars can mean absent side samples or observed zero. Native regression flags compare halves of this read's window; they establish neither cause nor a deployment regression. Complete refers to the bounded retained read/projection, not full traffic capture. Missing, truncated or empty evidence does not prove health. Edges are retained parent-child relationships; application/request parents may be synthetic when the real parent is missing. Edge calls count weighted child span samples, not unique requests; edge/exemplar durations are child span time, not full request latency. Calls and edges can overlap; do not sum them into a critical path. Exemplars are selected aggregate representatives, not random independent requests. Display labels may be modified or collide; they are not graph IDs or route filters. Source, edge, exemplar and text truncation remain separate. No separate trace IDs, raw spans, attributes, SQL/query patterns or deployment comparisons are exposed. Lookup identifiers may be native trace aliases. No exemplar is automatically fetched; follow-up reads recheck consent/ownership and are not an atomic snapshot. Coverage and dependency reads have independent clocks/windows."}
	if n.TelemetryRows < 0 || n.RepresentedRequests < n.TelemetryRows || n.SpanSamples < 0 || n.SpanSamples == 0 && (len(n.Dependencies) > 0 || len(n.Edges) > 0) {
		return view, invalid("invalid_response", "native dependency sample counts are inconsistent")
	}
	for _, dep := range n.Dependencies[:min(len(n.Dependencies), api.MCPPerformanceDependenciesMax)] {
		if err := validateDependencyLatency(dep); err != nil {
			return view, err
		}
		view.Dependencies = append(view.Dependencies, m.projectDependencyLatencyItem(dep, &view.TextTruncated))
	}
	for _, edge := range n.Edges[:min(len(n.Edges), api.MCPDependencyImpactEdgesMax)] {
		row, err := m.projectDependencyImpact(window, edge, n.RepresentedRequests, &view.TextTruncated)
		if err != nil {
			return view, err
		}
		view.ExemplarsTruncated = view.ExemplarsTruncated || row.ExemplarsTruncated
		view.Edges = append(view.Edges, row)
	}
	view.ReturnedDependencyCount = len(view.Dependencies)
	view.ReturnedEdgeCount = len(view.Edges)
	view.Complete = n.Complete && !n.Truncated && !view.DependenciesTruncated && !view.EdgesTruncated && !view.ExemplarsTruncated && !view.TextTruncated
	if n.SpanSamples == 0 {
		view.Status = "no_retained_spans"
	}
	return view, nil
}

func (m *management) projectDependencyLatencyItem(dep api.DebugDependencyLatencyItem, truncated *bool) dependencyLatencyView {
	text := func(value string) string { return m.requestText(value, truncated) }
	return dependencyLatencyView{
		Type: text(dep.Type), Kind: text(dep.Kind), Name: text(dep.Name), Calls: dep.Calls, ErrorCalls: dep.ErrorCalls, ErrorRatePct: dep.ErrorRatePct,
		P50MS: dep.P50MS, P95MS: dep.P95MS, P99MS: dep.P99MS, ExclusiveP50MS: dep.ExclusiveP50MS, ExclusiveP95MS: dep.ExclusiveP95MS, ExclusiveP99MS: dep.ExclusiveP99MS,
		BaselineP95MS: dep.BaselineP95MS, CurrentP95MS: dep.CurrentP95MS, P95DeltaMS: dep.P95DeltaMS, RegressionFactor: dep.RegressionFactor, Regression: dep.Regression,
		BaselineErrorRatePct: dep.BaselineErrorRatePct, CurrentErrorRatePct: dep.CurrentErrorRatePct, ErrorRateDeltaPct: dep.ErrorRateDeltaPct, FailureRegression: dep.FailureRegression, TopErrorType: text(dep.TopErrorType)}
}

func validateDependencyLatency(dep api.DebugDependencyLatencyItem) error {
	if dep.Calls < 0 || dep.ErrorCalls < 0 || dep.ErrorCalls > dep.Calls || !performanceRate(dep.ErrorRatePct) || !performanceRate(dep.BaselineErrorRatePct) || !performanceRate(dep.CurrentErrorRatePct) || !performanceRate(math.Abs(dep.ErrorRateDeltaPct)) || math.IsNaN(dep.RegressionFactor) || math.IsInf(dep.RegressionFactor, 0) || dep.RegressionFactor < 0 {
		return invalid("invalid_response", "native dependency counts or regression rates are invalid")
	}
	for _, latency := range []int64{dep.P50MS, dep.P95MS, dep.P99MS, dep.ExclusiveP50MS, dep.ExclusiveP95MS, dep.ExclusiveP99MS, dep.BaselineP95MS, dep.CurrentP95MS} {
		if latency < 0 {
			return invalid("invalid_response", "native dependency latency is negative")
		}
	}
	return nil
}
