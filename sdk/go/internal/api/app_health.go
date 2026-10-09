package api

// AppHealthResponse describes observed default-scope HTTP serving health.
// Unknown evidence is explicit; this is not an uptime or reachability guarantee.
type AppHealthResponse struct {
	AppID                string             `json:"app_id"`
	Status               string             `json:"status"`
	Phase                string             `json:"phase"`
	Summary              string             `json:"summary"`
	Scope                string             `json:"scope"`
	EvaluatedAt          string             `json:"evaluated_at"`
	ValidForSeconds      int                `json:"valid_for_seconds"`
	MetricsAsOf          string             `json:"metrics_as_of,omitempty"`
	ServingDeploymentIDs []string           `json:"serving_deployment_ids"`
	LatestDeploymentID   string             `json:"latest_deployment_id,omitempty"`
	Capacity             AppHealthCapacity  `json:"capacity"`
	Checks               []AppHealthCheck   `json:"checks"`
	Requests             *AppHealthRequests `json:"requests,omitempty"`
}

type AppHealthCapacity struct {
	Known    bool `json:"known"`
	Required int  `json:"required"`
	Ready    int  `json:"ready"`
	Starting int  `json:"starting"`
	Unready  int  `json:"unready"`
	Unknown  int  `json:"unknown"`
}

// Code and Action are stable, safe identifiers for clients. Detail never
// includes deployment errors, probe responses, node addresses or backend errors.
type AppHealthCheck struct {
	Code              string             `json:"code"`
	Status            string             `json:"status"`
	Detail            string             `json:"detail"`
	Reason            string             `json:"reason,omitempty"`
	Action            string             `json:"action,omitempty"`
	DeploymentID      string             `json:"deployment_id,omitempty"`
	Findings          []AppHealthFinding `json:"findings,omitempty"`
	FindingsTruncated bool               `json:"findings_truncated,omitempty"`
}

// Findings retain independent readiness failures. ObservedAt is the recorded
// transition or last node heartbeat, not a claim that a new probe was made.
type AppHealthFinding struct {
	Reason       string `json:"reason"`
	Status       string `json:"status"`
	Detail       string `json:"detail"`
	DeploymentID string `json:"deployment_id"`
	InstanceID   string `json:"instance_id"`
	Source       string `json:"source"`
	ObservedAt   string `json:"observed_at,omitempty"`
}

// Counts are confirmed only when Known is true. Coverage is limited to the
// current serving deployment IDs, never every environment of the app.
type AppHealthRequests struct {
	Known         bool                   `json:"known"`
	Coverage      string                 `json:"coverage"`
	WindowSeconds int                    `json:"window_seconds"`
	DeploymentIDs []string               `json:"deployment_ids"`
	RequestCount  int64                  `json:"request_count"`
	ServerErrors  int64                  `json:"server_errors"`
	ErrorRatePct  float64                `json:"error_rate_pct"`
	Policy        AppHealthRequestPolicy `json:"policy"`
}

type AppHealthRequestPolicy struct {
	MinimumRequests       int64   `json:"minimum_requests"`
	MinimumServerErrors   int64   `json:"minimum_server_errors"`
	WarningErrorRatePct   float64 `json:"warning_error_rate_pct"`
	UnhealthyErrorRatePct float64 `json:"unhealthy_error_rate_pct"`
}

// History contains sampled observations, not exact incident start/end times.
// Latest retains its original timestamp; CollectorFresh never refreshes it.
type AppHealthHistoryPage struct {
	AppID           string                  `json:"app_id"`
	Scope           string                  `json:"scope"`
	Entries         []AppHealthHistoryEntry `json:"entries"`
	NextCursor      string                  `json:"next_cursor,omitempty"`
	Latest          *AppHealthResponse      `json:"latest,omitempty"`
	CollectorFresh  bool                    `json:"collector_fresh"`
	IntervalSeconds int                     `json:"interval_seconds"`
}

type AppHealthHistoryEntry struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	ObservedAt     string            `json:"observed_at"`
	PreviousStatus string            `json:"previous_status,omitempty"`
	Assessment     AppHealthResponse `json:"assessment"`
}

// AppHealthChangedWebhookPayload describes sampled status changes. Unknown
// transitions are confidence changes, never confirmed outages or recoveries.
type AppHealthChangedWebhookPayload struct {
	Version              int      `json:"version"`
	AppID                string   `json:"app_id"`
	Scope                string   `json:"scope"`
	TransitionID         string   `json:"transition_id"`
	TransitionObservedAt string   `json:"transition_observed_at"`
	PreviousStatus       string   `json:"previous_status"`
	Status               string   `json:"status"`
	Change               string   `json:"change"`
	Phase                string   `json:"phase"`
	EvaluatedAt          string   `json:"evaluated_at"`
	QueuedAt             string   `json:"queued_at"`
	Coalesced            bool     `json:"coalesced"`
	CooldownSeconds      int      `json:"cooldown_seconds"`
	LatestDeploymentID   string   `json:"latest_deployment_id,omitempty"`
	ServingDeploymentIDs []string `json:"serving_deployment_ids"`
	HistoryPath          string   `json:"history_path"`
}
