package api

import "time"

// RouteHealthRoute selects an exact normalized telemetry label, never a raw URL.
type RouteHealthRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	// CheckLatency opts into the relative slowdown check independently of the budget.
	CheckLatency bool `json:"check_latency,omitempty"`
	// MaxP95MS enables an absolute budget when positive; zero disables it.
	MaxP95MS int64 `json:"max_p95_ms,omitempty"`
}
type RouteHealthGate struct {
	OnRegression string             `json:"on_regression,omitempty"`
	AppID        string             `json:"app_id"`
	Mode         string             `json:"mode"`
	Revision     int64              `json:"revision"`
	Routes       []RouteHealthRoute `json:"routes"`
	UpdatedAt    *time.Time         `json:"updated_at,omitempty"`
}
type SetRouteHealthGateRequest struct {
	OnRegression     string             `json:"on_regression,omitempty"`
	Mode             string             `json:"mode"`
	ExpectedRevision *int64             `json:"expected_revision"`
	Routes           []RouteHealthRoute `json:"routes"`
}
type RouteHealthCounts struct {
	Requests     int64   `json:"requests"`
	ServerErrors int64   `json:"server_errors"`
	ErrorRate    float64 `json:"error_rate"`
	// P95LatencyMS estimates weighted telemetry representatives; nil is unavailable.
	P95LatencyMS *float64 `json:"p95_latency_ms,omitempty"`
}
type RouteHealthWindowEvidence struct {
	Start          time.Time         `json:"start"`
	End            time.Time         `json:"end"`
	Candidate      RouteHealthCounts `json:"candidate"`
	Stable         RouteHealthCounts `json:"stable"`
	Status         string            `json:"status"`
	Reason         string            `json:"reason"`
	ErrorStatus    string            `json:"error_status,omitempty"`
	ErrorReason    string            `json:"error_reason,omitempty"`
	LatencyStatus  string            `json:"latency_status,omitempty"`
	LatencyReason  string            `json:"latency_reason,omitempty"`
	LatencyDeltaMS *float64          `json:"latency_delta_ms,omitempty"`
	LatencyFactor  *float64          `json:"latency_factor,omitempty"`
}
type RouteHealthFinding struct {
	Method        string                      `json:"method"`
	Path          string                      `json:"path"`
	CheckLatency  bool                        `json:"check_latency,omitempty"`
	MaxP95MS      int64                       `json:"max_p95_ms,omitempty"`
	Status        string                      `json:"status"`
	Reason        string                      `json:"reason"`
	ErrorStatus   string                      `json:"error_status,omitempty"`
	ErrorReason   string                      `json:"error_reason,omitempty"`
	LatencyStatus string                      `json:"latency_status,omitempty"`
	LatencyReason string                      `json:"latency_reason,omitempty"`
	Windows       []RouteHealthWindowEvidence `json:"windows"`
}

// Coverage refers to stored observations: full capture cannot be established.
type RouteHealthReport struct {
	Customers              *RouteCustomerHealthReport `json:"customers,omitempty"`
	OnRegression           string                     `json:"on_regression,omitempty"`
	AppID                  string                     `json:"app_id"`
	DeploymentID           string                     `json:"deployment_id"`
	CandidateCommitSHA     string                     `json:"candidate_commit_sha"`
	StableDeploymentID     string                     `json:"stable_deployment_id"`
	StableCommitSHA        string                     `json:"stable_commit_sha"`
	CanaryStep             int                        `json:"canary_step"`
	Mode                   string                     `json:"mode"`
	Revision               int64                      `json:"revision"`
	CheckedAt              time.Time                  `json:"checked_at"`
	ObservationAnchor      *time.Time                 `json:"observation_anchor,omitempty"`
	Coverage               string                     `json:"coverage"`
	Status                 string                     `json:"status"`
	Reason                 string                     `json:"reason"`
	MinimumRequests        int64                      `json:"minimum_requests"`
	MinimumLatencyRequests int64                      `json:"minimum_latency_requests,omitempty"`
	Routes                 []RouteHealthFinding       `json:"routes"`
}

// RouteHealthDecision is metadata-only for advancement responses and audits.
type RouteHealthDecision struct {
	OnRegression       string    `json:"on_regression,omitempty"`
	Mode               string    `json:"mode"`
	Revision           int64     `json:"revision"`
	DeploymentID       string    `json:"deployment_id"`
	StableDeploymentID string    `json:"stable_deployment_id"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason"`
	CheckedAt          time.Time `json:"checked_at"`
	HistoryID          string    `json:"history_id,omitempty"`
}
