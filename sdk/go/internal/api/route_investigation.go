package api

import "time"

// RouteHealthInvestigationSelection identifies one configured label and signal.
// The default error signal selects all 5xx or a watched code. Latency includes
// all statuses and requires a configured latency check and StatusCode zero.
type RouteHealthInvestigationSelection struct {
	Method          string `json:"method"`
	Path            string `json:"path"`
	StatusCode      int    `json:"status_code"`
	Signal          string `json:"signal,omitempty"`
	CustomerGroupBy string `json:"customer_group_by,omitempty"`
	CustomerID      string `json:"customer_id,omitempty"`
}

// Examples are retained telemetry rows. RepresentedRequests can exceed one;
// a trace link does not establish an individual trace for every represented request.
type RouteHealthInvestigationExample struct {
	TelemetryID         string    `json:"telemetry_id"`
	ReceivedAt          time.Time `json:"received_at"`
	Status              int       `json:"status"`
	LatencyMS           int64     `json:"latency_ms"`
	RepresentedRequests int64     `json:"represented_requests"`
	TraceID             string    `json:"trace_id,omitempty"`
	EvidencePath        string    `json:"evidence_path"`
}

type RouteHealthInvestigationSide struct {
	MatchingRequests  int64                             `json:"matching_requests"`
	ObservedRows      int64                             `json:"observed_rows"`
	ExamplesTruncated bool                              `json:"examples_truncated"`
	Examples          []RouteHealthInvestigationExample `json:"examples"`
}

type RouteHealthInvestigationWindow struct {
	Start       time.Time                      `json:"start"`
	End         time.Time                      `json:"end"`
	Candidate   RouteHealthInvestigationSide   `json:"candidate"`
	Stable      RouteHealthInvestigationSide   `json:"stable"`
	Diagnostics *RouteHealthLatencyDiagnostics `json:"diagnostics,omitempty"`
}

// Diagnostics describe bounded retained samples, separately from the full
// weighted route p95. Dependency percentiles and stages are not additive.
type RouteHealthLatencyDiagnostics struct {
	Coverage              string                            `json:"coverage"`
	RowsLimit             int                               `json:"rows_limit"`
	DependenciesTruncated bool                              `json:"dependencies_truncated"`
	Candidate             RouteHealthLatencySample          `json:"candidate"`
	Stable                RouteHealthLatencySample          `json:"stable"`
	Dependencies          []RouteHealthDependencyComparison `json:"dependencies"`
}

type RouteHealthLatencySample struct {
	SampledRows      int64  `json:"sampled_rows"`
	SampledRequests  int64  `json:"sampled_requests"`
	SamplesTruncated bool   `json:"samples_truncated"`
	SpanRows         int64  `json:"span_rows"`
	MissingSpanRows  int64  `json:"missing_span_rows"`
	SpanSamples      int64  `json:"span_samples"`
	SpansTruncated   bool   `json:"spans_truncated"`
	TimingIncomplete bool   `json:"timing_incomplete"`
	GuestRows        int64  `json:"guest_rows"`
	GuestRequests    int64  `json:"guest_requests"`
	GuestP95MS       *int64 `json:"guest_p95_ms,omitempty"`
	ColdBootRequests int64  `json:"cold_boot_requests"`
	WakeSamples      int64  `json:"wake_samples"`
	WakeBootP95MS    *int64 `json:"wake_boot_p95_ms,omitempty"`
}

type RouteHealthDependencyTiming struct {
	SpanSamples      int64                             `json:"span_samples"`
	RepresentedCalls int64                             `json:"represented_calls"`
	ErrorCalls       int64                             `json:"error_calls"`
	P95MS            *int64                            `json:"p95_ms,omitempty"`
	ExclusiveP95MS   *int64                            `json:"exclusive_p95_ms,omitempty"`
	Examples         []RouteHealthInvestigationExample `json:"examples"`
}

type RouteHealthDependencyComparison struct {
	Type                string                      `json:"type"`
	Kind                string                      `json:"kind,omitempty"`
	Status              string                      `json:"status"`
	Candidate           RouteHealthDependencyTiming `json:"candidate"`
	Stable              RouteHealthDependencyTiming `json:"stable"`
	P95DeltaMS          *int64                      `json:"p95_delta_ms,omitempty"`
	ExclusiveP95DeltaMS *int64                      `json:"exclusive_p95_delta_ms,omitempty"`
}

// The report, selected (possibly customer-specific) finding and examples share
// one snapshot. Evidence paths require normal debugger authorization on access.
type RouteHealthInvestigation struct {
	Version        int                               `json:"version"`
	Selection      RouteHealthInvestigationSelection `json:"selection"`
	Report         RouteHealthReport                 `json:"report"`
	Finding        RouteHealthFinding                `json:"finding"`
	Status         string                            `json:"status"`
	Reason         string                            `json:"reason"`
	Coverage       string                            `json:"coverage"`
	EvidenceStatus string                            `json:"evidence_status"`
	ExamplesLimit  int                               `json:"examples_limit"`
	Windows        []RouteHealthInvestigationWindow  `json:"windows"`
}
