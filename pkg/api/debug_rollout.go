package api

// Rollout explanations and the app debug status (ADR-127 amendment,
// following ADR-1447). ADR-1447 records why a canary advanced, held, or
// aborted with aggregates only; these responses add the request evidence for
// the same deployment and window, read at request time from request
// telemetry under the debugger's plan gate and retention. Nothing here is
// stored, so the decision record stays aggregate-only.

// DebugRolloutState values: the explained deployment's rollout as the
// customer sees it.
const (
	DebugRolloutAborted    = "aborted"
	DebugRolloutHeld       = "held"
	DebugRolloutRollingOut = "rolling_out"
	DebugRolloutComplete   = "complete"
	DebugRolloutNone       = "none"
)

// DebugStatusVerdict values for GET /v1/apps/{slug}/debug/status, worst
// first.
const (
	DebugStatusFailing   = "failing"
	DebugStatusRollback  = "rolled_back"
	DebugStatusDegraded  = "degraded"
	DebugStatusReleasing = "releasing"
	DebugStatusHealthy   = "healthy"
	DebugStatusIdle      = "idle"
)

// Response bounds for the rollout explanation and debug status.
const (
	DebugRolloutMaxRoutes    = 5
	DebugRolloutMaxExamples  = 3
	DebugStatusMaxFailures   = 5
	DebugStatusMaxRegression = 5
	DebugStatusMaxOOMKills   = 5
	// DebugRolloutSearchDeployments is how many of the app's newest
	// deployments are searched for one with a canary rollout when no
	// deployment_id is given.
	DebugRolloutSearchDeployments = 20
)

// DebugRolloutExplanation answers GET /v1/apps/{slug}/debug/rollout: why a
// release was rolled back or is held, with the routes and requests behind it.
type DebugRolloutExplanation struct {
	DeploymentID       string `json:"deployment_id,omitempty"`
	StableDeploymentID string `json:"stable_deployment_id,omitempty"`
	// State is one of aborted, held, rolling_out, complete, or none (no
	// canary rollout among the app's recent deployments).
	State          string `json:"state"`
	TrafficPercent int    `json:"traffic_percent"`
	// Decision is the ADR-1447 record that explains State: the abort record,
	// the newest hold, or the latest advance. Absent when the rollout was
	// aborted by another path (route monitor, alert rollback, profile gate);
	// AbortedReason then carries that path's reason.
	Decision      *RolloutDecision `json:"decision,omitempty"`
	AbortedReason string           `json:"aborted_reason,omitempty"`
	// Headline is a deterministic one-line explanation built from Decision
	// and the worst route.
	Headline    string `json:"headline"`
	WindowStart string `json:"window_start,omitempty"`
	WindowEnd   string `json:"window_end,omitempty"`
	// RetentionClamped is true when the window started before the plan's
	// debugger retention and was shortened.
	RetentionClamped bool `json:"retention_clamped,omitempty"`
	// Routes compare the candidate with the stable deployment over the
	// window, worst first; at most DebugRolloutMaxRoutes.
	Routes []DebugRolloutRoute `json:"routes"`
	// Examples are candidate requests from the worst route that show the
	// problem (server errors, or the slowest requests for a latency rule);
	// at most DebugRolloutMaxExamples. Each links to its request evidence.
	Examples []DebugTelemetryRequestItem `json:"examples"`
	// Regressions are the candidate's open debugger regressions.
	Regressions []DebugRegressionItem `json:"regressions"`
	GeneratedAt string                `json:"generated_at"`
}

// DebugRolloutRoute is one route's candidate-vs-stable comparison. Counts are
// represented requests (collapsed telemetry rows are weighted).
type DebugRolloutRoute struct {
	Route                 string `json:"route"`
	CandidateRequests     int64  `json:"candidate_requests"`
	CandidateServerErrors int64  `json:"candidate_server_errors"`
	CandidateP95MS        int    `json:"candidate_p95_ms"`
	StableRequests        int64  `json:"stable_requests"`
	StableServerErrors    int64  `json:"stable_server_errors"`
	StableP95MS           int    `json:"stable_p95_ms"`
}

// DebugStatusResponse answers GET /v1/apps/{slug}/debug/status: one summary
// of whether the app is healthy and, if not, where to look next.
type DebugStatusResponse struct {
	// Verdict is one of failing, rolled_back, degraded, releasing, healthy,
	// or idle; Headline explains it in one line.
	Verdict     string `json:"verdict"`
	Headline    string `json:"headline"`
	Since       string `json:"since"`
	WindowStart string `json:"window_start"`
	WindowEnd   string `json:"window_end"`
	// Health covers the serving deployments over the window.
	Health      DebugStatusHealth       `json:"health"`
	Deployments []DebugStatusDeployment `json:"deployments"`
	// Rollout is present while a canary rollout is in flight, or when one was
	// rolled back inside the window.
	Rollout *DebugRolloutExplanation `json:"rollout,omitempty"`
	// Regressions are the app's open debugger regressions, newest first; at
	// most DebugStatusMaxRegression, with RegressionsTotal the full count.
	Regressions      []DebugRegressionItem `json:"regressions"`
	RegressionsTotal int                   `json:"regressions_total"`
	// RecentFailures are the newest server-error requests in the window; at
	// most DebugStatusMaxFailures.
	RecentFailures []DebugTelemetryRequestItem `json:"recent_failures"`
	// OOMKills are the window's out-of-memory kills of the app's instances,
	// newest first; at most DebugStatusMaxOOMKills, with OOMKillsTotal the
	// full count. OOMFix is the remedy for the newest kill.
	OOMKills      []DebugStatusOOMKill `json:"oom_kills"`
	OOMKillsTotal int                  `json:"oom_kills_total"`
	OOMFix        string               `json:"oom_fix,omitempty"`
	// TracingEnabled reports whether in-guest tracing (spans and request
	// logs) is on for the app.
	TracingEnabled bool   `json:"tracing_enabled"`
	GeneratedAt    string `json:"generated_at"`
}

// DebugStatusHealth is the serving deployments' traffic over the window.
type DebugStatusHealth struct {
	Requests     int64 `json:"requests"`
	ServerErrors int64 `json:"server_errors"`
	P95MS        int   `json:"p95_ms"`
}

// DebugStatusOOMKill is one out-of-memory kill of an app instance (ADR-121).
// DeploymentFailed is true when the kill failed a deployment that was not yet
// live; a live release keeps serving and its instance is replaced.
type DebugStatusOOMKill struct {
	At               string `json:"at"`
	DeploymentID     string `json:"deployment_id"`
	PeakMB           int    `json:"peak_mb"`
	PlanMB           int    `json:"plan_mb"`
	DeploymentFailed bool   `json:"deployment_failed"`
}

// DebugStatusDeployment is one deployment currently serving traffic.
type DebugStatusDeployment struct {
	ID             string `json:"id"`
	TrafficPercent int    `json:"traffic_percent"`
	RolloutState   string `json:"rollout_state,omitempty"`
	CommitSHA      string `json:"commit_sha,omitempty"`
	CreatedAt      string `json:"created_at"`
}
