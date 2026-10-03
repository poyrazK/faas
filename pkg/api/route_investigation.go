package api

import "time"

// RouteHealthInvestigationSelection identifies one configured label and signal.
// StatusCode zero selects all 5xx; a nonzero code must be watched on the route.
type RouteHealthInvestigationSelection struct {
	Method          string `json:"method"`
	Path            string `json:"path"`
	StatusCode      int    `json:"status_code"`
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
	Start     time.Time                    `json:"start"`
	End       time.Time                    `json:"end"`
	Candidate RouteHealthInvestigationSide `json:"candidate"`
	Stable    RouteHealthInvestigationSide `json:"stable"`
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
