package api

import "time"

// RouteMonitorRoute selects an exact gateway-normalized route and absolute budgets.
// A nil error budget disables that signal; zero permits no sustained 5xx errors.
type RouteMonitorRoute struct {
	Method        string `json:"method"`
	Path          string `json:"path"`
	Max5xxRateBPS *int64 `json:"max_5xx_rate_bps,omitempty"`
	MaxP95MS      int64  `json:"max_p95_ms,omitempty"`
}
type RouteMonitorConfig struct {
	AppID     string              `json:"app_id"`
	Enabled   bool                `json:"enabled"`
	Revision  int64               `json:"revision"`
	Routes    []RouteMonitorRoute `json:"routes"`
	UpdatedAt *time.Time          `json:"updated_at,omitempty"`
}
type SetRouteMonitorRequest struct {
	Enabled          bool                `json:"enabled"`
	ExpectedRevision *int64              `json:"expected_revision"`
	Routes           []RouteMonitorRoute `json:"routes"`
}
type RouteMonitorWindow struct {
	Start         time.Time         `json:"start"`
	End           time.Time         `json:"end"`
	Observed      RouteHealthCounts `json:"observed"`
	ErrorStatus   string            `json:"error_status"`
	ErrorReason   string            `json:"error_reason"`
	LatencyStatus string            `json:"latency_status"`
	LatencyReason string            `json:"latency_reason"`
}
type RouteMonitorFinding struct {
	Route         RouteMonitorRoute    `json:"route"`
	Status        string               `json:"status"`
	Reason        string               `json:"reason"`
	ErrorStatus   string               `json:"error_status"`
	LatencyStatus string               `json:"latency_status"`
	Windows       []RouteMonitorWindow `json:"windows"`
}

// Monitoring describes stored observations, not a complete capture or an SLO.
type RouteMonitorReport struct {
	Version                int                   `json:"version"`
	AppID                  string                `json:"app_id"`
	Enabled                bool                  `json:"enabled"`
	Revision               int64                 `json:"revision"`
	DeploymentID           string                `json:"deployment_id,omitempty"`
	CommitSHA              string                `json:"commit_sha,omitempty"`
	CheckedAt              time.Time             `json:"checked_at"`
	ObservationAnchor      *time.Time            `json:"observation_anchor,omitempty"`
	Coverage               string                `json:"coverage"`
	Status                 string                `json:"status"`
	Reason                 string                `json:"reason"`
	MinimumRequests        int64                 `json:"minimum_requests"`
	MinimumLatencyRequests int64                 `json:"minimum_latency_requests"`
	Routes                 []RouteMonitorFinding `json:"routes"`
}
type RouteMonitorEvidenceWindow struct {
	Start       time.Time                      `json:"start"`
	End         time.Time                      `json:"end"`
	Requests    RouteHealthInvestigationSide   `json:"requests"`
	Diagnostics *RouteHealthLatencyDiagnostics `json:"diagnostics,omitempty"`
}

// Diagnostics reuse the debugger's candidate side for the monitored deployment;
// the stable side is empty and there is no comparative or causal claim.
type RouteMonitorEvidence struct {
	Method  string                       `json:"method"`
	Path    string                       `json:"path"`
	Signal  string                       `json:"signal"`
	Windows []RouteMonitorEvidenceWindow `json:"windows"`
}
type RouteMonitorIncident struct {
	Version           int                    `json:"version"`
	ID                string                 `json:"id"`
	AppID             string                 `json:"app_id"`
	DeploymentID      string                 `json:"deployment_id"`
	Revision          int64                  `json:"revision"`
	Status            string                 `json:"status"`
	OpenedAt          time.Time              `json:"opened_at"`
	ClosedAt          *time.Time             `json:"closed_at,omitempty"`
	OpeningReport     RouteMonitorReport     `json:"opening_report"`
	RecoveryReport    *RouteMonitorReport    `json:"recovery_report,omitempty"`
	Evidence          []RouteMonitorEvidence `json:"evidence"`
	EvidenceTruncated bool                   `json:"evidence_truncated"`
}
type RouteMonitorIncidentPage struct {
	AppID      string                 `json:"app_id"`
	Incidents  []RouteMonitorIncident `json:"incidents"`
	NextBefore string                 `json:"next_before,omitempty"`
}

// Webhooks carry only metadata and an authenticated saved-incident path.
type RouteMonitorWebhookPayload struct {
	Version      int       `json:"version"`
	AppID        string    `json:"app_id"`
	DeploymentID string    `json:"deployment_id"`
	IncidentID   string    `json:"incident_id"`
	Revision     int64     `json:"revision"`
	Status       string    `json:"status"`
	CheckedAt    time.Time `json:"checked_at"`
	IncidentPath string    `json:"incident_path"`
}
