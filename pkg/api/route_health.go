package api

import "time"

// RouteHealthRoute selects an exact normalized telemetry label, never a raw URL.
type RouteHealthRoute struct {
	// WatchStatuses opts into advisory 401/403/404/422/429 comparisons.
	WatchStatuses []int `json:"watch_statuses,omitempty"`

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
	WatchStatuses []int                         `json:"watch_statuses,omitempty"`
	ClientErrors  *RouteHealthClientErrorReport `json:"client_errors,omitempty"`

	Method        string `json:"method"`
	Path          string `json:"path"`
	CheckLatency  bool   `json:"check_latency,omitempty"`
	MaxP95MS      int64  `json:"max_p95_ms,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason"`
	ErrorStatus   string `json:"error_status,omitempty"`
	ErrorReason   string `json:"error_reason,omitempty"`
	LatencyStatus string `json:"latency_status,omitempty"`
	LatencyReason string `json:"latency_reason,omitempty"`
	// EvidenceWindow is "pooled" when the verdict comes from PooledWindows
	// because the one-minute windows lacked requests (ADR-846).
	EvidenceWindow string                      `json:"evidence_window,omitempty"`
	Windows        []RouteHealthWindowEvidence `json:"windows"`
	// PooledWindows are two halves of the stage so far, read only for routes
	// whose one-minute windows were sparse.
	PooledWindows []RouteHealthWindowEvidence `json:"pooled_windows,omitempty"`
}

// CanaryProfileSignal is a retained, advisory comparison for one canary stage.
// The background worker captures it; report reads never query profile storage.
// It is intentionally absent unless the app's automatic profile policy is
// enabled. An explicitly configured canary gate can consume qualified route evidence.
type CanaryProfileSignal struct {
	Gate                *ProfileCanaryGateState       `json:"gate,omitempty"`
	Attribution         *ProfileAttributionComparison `json:"attribution,omitempty"`
	RouteChecks         []ProfileRouteRegression      `json:"route_checks,omitempty"`
	RequestMix          *ProfileRequestMixSnapshot    `json:"request_mix,omitempty"`
	Mode                string                        `json:"mode"`
	Status              string                        `json:"status"`
	Reason              string                        `json:"reason"`
	CanaryStep          int                           `json:"canary_step"`
	CanaryStepStartedAt time.Time                     `json:"canary_step_started_at"`
	CreatedAt           time.Time                     `json:"created_at"`
	CheckedAt           *time.Time                    `json:"checked_at,omitempty"`
	Attempts            int                           `json:"attempts"`
	NextAttemptAt       *time.Time                    `json:"next_attempt_at,omitempty"`
	CompletedAt         *time.Time                    `json:"completed_at,omitempty"`
	PolicyRevision      int64                         `json:"policy_revision"`
	Metric              string                        `json:"metric"`
	WindowSeconds       int                           `json:"window_seconds"`
	Options             ProfileRegressionOptions      `json:"options"`
	Baseline            *ProfileQuery                 `json:"baseline,omitempty"`
	Candidate           *ProfileQuery                 `json:"candidate,omitempty"`
	BaselineRequests    *int64                        `json:"baseline_requests,omitempty"`
	CandidateRequests   *int64                        `json:"candidate_requests,omitempty"`
	BaselineCoverage    *ProfileCoverage              `json:"baseline_coverage,omitempty"`
	CandidateCoverage   *ProfileCoverage              `json:"candidate_coverage,omitempty"`
	BaselineSource      *ProfileSource                `json:"baseline_source,omitempty"`
	CandidateSource     *ProfileSource                `json:"candidate_source,omitempty"`
	ComparisonURL       string                        `json:"comparison_url,omitempty"`
	Total               *ProfileRegressionMetric      `json:"total,omitempty"`
	Evidence            []ProfileRegressionEvidence   `json:"evidence"`
	UncomparableEntries int                           `json:"uncomparable_entries"`
}

// Coverage refers to stored observations: full capture cannot be established.
type RouteHealthReport struct {
	ClientErrorStatus string `json:"client_error_status,omitempty"`
	ClientErrorReason string `json:"client_error_reason,omitempty"`

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
	ProfileSignal          *CanaryProfileSignal       `json:"profile_signal,omitempty"`
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
