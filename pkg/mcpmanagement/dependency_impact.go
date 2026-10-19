package mcpmanagement

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type dependencyImpactExemplarView struct {
	LookupIdentifier string `json:"lookup_identifier"`
	Window           string `json:"window"`
	ReceivedAt       string `json:"received_at"`
	DurationMS       int64  `json:"duration_ms"`
	DurationBasis    string `json:"duration_basis"`
	HTTPStatus       int    `json:"http_status"`
	Error            bool   `json:"error"`
	Count            int64  `json:"count"`
}

// Edges have no native failure-regression flag or per-side call counts. Do
// not fill those missing facts from dependency nodes or manufacture defaults.
type dependencyImpactEdgeView struct {
	From                   criticalPathSegmentView        `json:"from"`
	To                     criticalPathSegmentView        `json:"to"`
	LabelsModified         bool                           `json:"labels_modified"`
	SideSampleAvailability string                         `json:"side_sample_availability"`
	Exemplars              []dependencyImpactExemplarView `json:"exemplars"`
	NativeExemplarCount    int                            `json:"native_returned_exemplar_count"`
	ReturnedExemplarCount  int                            `json:"returned_exemplar_count"`
	ExemplarsTruncated     bool                           `json:"exemplars_truncated"`
	Calls                  int64                          `json:"calls"`
	ErrorCalls             int64                          `json:"error_calls"`
	ErrorRatePct           float64                        `json:"error_rate_pct"`
	P50MS                  int64                          `json:"p50_ms"`
	P95MS                  int64                          `json:"p95_ms"`
	P99MS                  int64                          `json:"p99_ms"`
	ExclusiveP50MS         int64                          `json:"exclusive_p50_ms"`
	ExclusiveP95MS         int64                          `json:"exclusive_p95_ms"`
	ExclusiveP99MS         int64                          `json:"exclusive_p99_ms"`
	BaselineP95MS          int64                          `json:"baseline_p95_ms"`
	CurrentP95MS           int64                          `json:"current_p95_ms"`
	P95DeltaMS             int64                          `json:"p95_delta_ms"`
	BaselineExclusiveP95MS int64                          `json:"baseline_exclusive_p95_ms"`
	CurrentExclusiveP95MS  int64                          `json:"current_exclusive_p95_ms"`
	ExclusiveP95DeltaMS    int64                          `json:"exclusive_p95_delta_ms"`
	RegressionFactor       float64                        `json:"regression_factor"`
	Regression             bool                           `json:"regression"`
	BaselineErrorRatePct   float64                        `json:"baseline_error_rate_pct"`
	CurrentErrorRatePct    float64                        `json:"current_error_rate_pct"`
	ErrorRateDeltaPct      float64                        `json:"error_rate_delta_pct"`
}

func (m *management) projectDependencyImpact(window performanceWindow, n api.DebugDependencyImpactEdge, represented int64, truncated *bool) (dependencyImpactEdgeView, error) {
	v := dependencyImpactEdgeView{SideSampleAvailability: "not_reported", Exemplars: []dependencyImpactExemplarView{}, NativeExemplarCount: len(n.Exemplars), ExemplarsTruncated: len(n.Exemplars) > api.MCPDependencyImpactExemplarsMax,
		Calls: n.Calls, ErrorCalls: n.ErrorCalls, ErrorRatePct: n.ErrorRatePct, P50MS: n.P50MS, P95MS: n.P95MS, P99MS: n.P99MS,
		ExclusiveP50MS: n.ExclusiveP50MS, ExclusiveP95MS: n.ExclusiveP95MS, ExclusiveP99MS: n.ExclusiveP99MS,
		BaselineP95MS: n.BaselineP95MS, CurrentP95MS: n.CurrentP95MS, P95DeltaMS: n.P95DeltaMS,
		BaselineExclusiveP95MS: n.BaselineExclusiveP95MS, CurrentExclusiveP95MS: n.CurrentExclusiveP95MS, ExclusiveP95DeltaMS: n.ExclusiveP95DeltaMS,
		RegressionFactor: n.RegressionFactor, Regression: n.Regression, BaselineErrorRatePct: n.BaselineErrorRatePct, CurrentErrorRatePct: n.CurrentErrorRatePct, ErrorRateDeltaPct: n.ErrorRateDeltaPct}
	metrics := api.DebugDependencyLatencyItem{Calls: n.Calls, ErrorCalls: n.ErrorCalls, ErrorRatePct: n.ErrorRatePct, P50MS: n.P50MS, P95MS: n.P95MS, P99MS: n.P99MS,
		ExclusiveP50MS: n.ExclusiveP50MS, ExclusiveP95MS: n.ExclusiveP95MS, ExclusiveP99MS: n.ExclusiveP99MS,
		BaselineP95MS: n.BaselineP95MS, CurrentP95MS: n.CurrentP95MS, RegressionFactor: n.RegressionFactor,
		BaselineErrorRatePct: n.BaselineErrorRatePct, CurrentErrorRatePct: n.CurrentErrorRatePct, ErrorRateDeltaPct: n.ErrorRateDeltaPct}
	if validateDependencyLatency(metrics) != nil || n.Calls <= 0 || represented <= 0 || n.BaselineExclusiveP95MS < 0 || n.CurrentExclusiveP95MS < 0 {
		return v, invalid("invalid_response", "native dependency edge has invalid counts, rates or duration metrics")
	}
	v.From, v.To = *m.projectCriticalSegment(&n.From, truncated), *m.projectCriticalSegment(&n.To, truncated)
	v.LabelsModified = v.From.Type != n.From.Type || v.From.Kind != n.From.Kind || v.From.Name != n.From.Name || v.To.Type != n.To.Type || v.To.Kind != n.To.Kind || v.To.Name != n.To.Name
	seen := map[string]bool{}
	for _, sample := range n.Exemplars[:min(len(n.Exemplars), api.MCPDependencyImpactExemplarsMax)] {
		if err := m.validateDependencyImpactExemplar(window, sample, n.Calls, represented); err != nil {
			return v, err
		}
		if sample.Error && sample.Count > n.ErrorCalls || !sample.Error && sample.Count > n.Calls-n.ErrorCalls {
			return v, invalid("invalid_response", "native dependency edge exemplar exceeds its outcome's sample count")
		}
		if seen[sample.RequestID] {
			return v, invalid("invalid_response", "native dependency edge repeats an exemplar lookup identity")
		}
		seen[sample.RequestID] = true
		v.Exemplars = append(v.Exemplars, dependencyImpactExemplarView{LookupIdentifier: sample.RequestID, Window: sample.Window,
			ReceivedAt: sample.ReceivedAt, DurationMS: sample.DurationMS, DurationBasis: "child_span_sample", HTTPStatus: sample.HTTPStatus, Error: sample.Error, Count: sample.Count})
	}
	v.ReturnedExemplarCount = len(v.Exemplars)
	return v, nil
}

func (m *management) validateDependencyImpactExemplar(window performanceWindow, sample api.DebugDependencyImpactExemplar, calls, represented int64) error {
	if validateRequestID(sample.RequestID) != nil || m.clean(sample.RequestID) != sample.RequestID || sample.Count < 1 || sample.Count > calls || sample.Count > represented || sample.DurationMS < 0 || len(sample.ReceivedAt) > api.MCPRequestFieldMaxBytes || sample.HTTPStatus != 0 && (sample.HTTPStatus < 100 || sample.HTTPStatus > 599) {
		return invalid("invalid_response", "native dependency edge exemplar has invalid identity, count, status or duration")
	}
	start, startErr := time.Parse(time.RFC3339Nano, window.WindowStart)
	end, endErr := time.Parse(time.RFC3339Nano, window.WindowEnd)
	at, err := time.Parse(time.RFC3339Nano, sample.ReceivedAt)
	cutover := start.Add(end.Sub(start) / 2)
	if startErr != nil || endErr != nil || !start.Before(end) || err != nil || at.Before(start) || !at.Before(end) || sample.Window != "baseline" && sample.Window != "current" || (sample.Window == "current") != !at.Before(cutover) {
		return invalid("invalid_response", "native dependency edge exemplar is outside its labeled half-window")
	}
	return nil
}

const dependencyImpactGuidance = "Inspect edges from gregale_get_dependency_latency as sampled retained parent-to-child span relationships, not a complete service topology or causal critical path. Edge latency and exemplar duration describe child span samples, not full request latency; do not sum overlapping edges or count calls as unique requests. A native application/request parent may be synthetic because the actual parent was absent or unretained. From/to names are bounded display labels, not unique graph IDs, routes or selectors; labels_modified can reflect redaction/truncation and labels can collide. Preserve native edge order, native/source/projection/text/exemplar limits and source_completeness unknown. Native regression and baseline/current metrics compare halves of this dependency window, not deployments; no per-side call counts or native edge failure-regression flag is supplied. side_sample_availability not_reported means zero side metrics cannot distinguish absent samples from observed zero; an exemplar does not establish side totals, complete coverage or a healthy baseline. Empty edges do not prove no dependency or healthy traffic. Use an exemplar lookup_identifier with gregale_get_request or gregale_get_request_evidence for the same app only when drill-down is needed. Preserve its half-window, timestamp and represented count. Duration basis child_span_sample and error describe the selected child span, independent of HTTP status; an exemplar is a selected aggregate representative, not a random independent request. Lookup identifiers may themselves be native trace aliases; do not request separate trace IDs or infer a deployment identity. Follow-up tools recheck consent, app identity and ownership; these independent reads are not an atomic snapshot. Retrieval adds no collection, probes, replay, model requests or writes."
