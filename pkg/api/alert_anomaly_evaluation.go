package api

import "time"

type AlertAnomalyEvaluationReason string

// SupportsEnvironmentAnomalyMetric reports which anomaly metrics have an
// environment-scoped request series.
func SupportsEnvironmentAnomalyMetric(metric string) bool {
	return metric == "request_count" || metric == "error_rate_pct" || metric == "latency_p95_ms"
}

const (
	AlertAnomalyInsufficientTraffic  AlertAnomalyEvaluationReason = "insufficient_traffic"
	AlertAnomalyInsufficientHistory  AlertAnomalyEvaluationReason = "insufficient_history"
	AlertAnomalyTelemetryMissing     AlertAnomalyEvaluationReason = "telemetry_missing_or_stale"
	AlertAnomalyTelemetryUnavailable AlertAnomalyEvaluationReason = "telemetry_unavailable"
	AlertAnomalyThresholdNotReached  AlertAnomalyEvaluationReason = "threshold_not_reached"
	AlertAnomalyMinimumNotReached    AlertAnomalyEvaluationReason = "minimum_impact_not_reached"
	AlertAnomalyZeroBaseline         AlertAnomalyEvaluationReason = "zero_baseline"
	AlertAnomalyNotificationCooldown AlertAnomalyEvaluationReason = "notification_cooldown"
	AlertAnomalyObservedBreach       AlertAnomalyEvaluationReason = "observed_breach"
	AlertAnomalyRecoveryPending      AlertAnomalyEvaluationReason = "recovery_confirmation_pending"
	AlertAnomalyRecoveryConfirmed    AlertAnomalyEvaluationReason = "recovery_confirmed"
)

// AlertObservedIncident records an observation-only breach from open to end.
type AlertObservedIncident struct {
	EnvironmentScope      string                                `json:"environment_scope,omitempty"`
	OpeningEvidence       *AlertObservedIncidentOpeningEvidence `json:"opening_evidence,omitempty"`
	OpenedAt              time.Time                             `json:"opened_at"`
	EndedAt               *time.Time                            `json:"ended_at,omitempty"`
	Outcome               string                                `json:"outcome,omitempty"`
	EligibleNotifications int                                   `json:"eligible_notifications"`
	LastEligibleAt        time.Time                             `json:"last_eligible_at"`
}

// AlertObservedIncidentOpeningEvidence captures the rule and metric values
// at the first breach of a live observed incident.
type AlertObservedIncidentOpeningEvidence struct {
	Metric       string  `json:"metric"`
	Window       string  `json:"window"`
	Comparison   string  `json:"comparison"`
	Observed     float64 `json:"observed"`
	Baseline     float64 `json:"baseline"`
	TriggerValue float64 `json:"trigger_value"`
	BaselineDays int     `json:"baseline_days,omitempty"`
}

const (
	AlertObservationRecovered = "recovered"
	AlertObservationEnded     = "observation_ended"
)

// AlertObservationCoverage summarizes attempted checks from the last 24 hours.
type AlertObservationCoverage struct {
	Attempted     int                         `json:"attempted"`
	Completed     int                         `json:"completed"`
	Skipped       []AlertObservationSkipCount `json:"skipped,omitempty"`
	LastCheckedAt time.Time                   `json:"last_checked_at,omitzero"`
}

type AlertObservationSkipCount struct {
	Reason AlertAnomalyEvaluationReason `json:"reason"`
	Count  int                          `json:"count"`
}

// AlertAnomalyEvaluation explains why the latest check did not trigger a fire.
// Count and RequiredCount describe eligible requests, qualifying historical
// windows, or consecutive healthy checks, depending on Reason.
type AlertAnomalyEvaluation struct {
	Reason                     AlertAnomalyEvaluationReason `json:"reason"`
	EvaluatedAt                time.Time                    `json:"evaluated_at"`
	Count                      int64                        `json:"count"`
	RequiredCount              int64                        `json:"required_count,omitempty"`
	BaselineDays               *int                         `json:"baseline_days,omitempty"`
	Observed                   *float64                     `json:"observed,omitempty"`
	Baseline                   *float64                     `json:"baseline,omitempty"`
	TriggerValue               *float64                     `json:"trigger_value,omitempty"`
	MinimumValue               *float64                     `json:"minimum_value,omitempty"`
	NextNotificationEligibleAt *time.Time                   `json:"next_notification_eligible_at,omitempty"`
}

// AlertAnomalyPreviewResponse summarizes hourly or minute-level historical checks.
type AlertAnomalyPreviewResponse struct {
	EnvironmentScope  string                             `json:"environment_scope,omitempty"`
	EnvironmentScopes []string                           `json:"environment_scopes,omitempty"`
	FirstCheckAt      time.Time                          `json:"first_check_at"`
	LastCheckAt       time.Time                          `json:"last_check_at"`
	StepSeconds       int                                `json:"step_seconds"`
	Evaluated         int                                `json:"evaluated"`
	Skipped           int                                `json:"skipped"`
	SkippedWindows    []AlertAnomalyPreviewSkippedWindow `json:"skipped_windows"`
	Breaches          []AlertAnomalyPreviewBreach        `json:"breaches"`
	Candidate         *AlertAnomalyPreviewCandidate      `json:"candidate,omitempty"`
	IncidentReplay    *AlertAnomalyIncidentReplay        `json:"incident_replay,omitempty"`
}

// AlertAnomalyIncidentReplay groups historical breaches using the rule's incident and cooldown behavior.
type AlertAnomalyIncidentReplay struct {
	CooldownMinutes      int                             `json:"cooldown_minutes"`
	Incidents            []AlertObservedIncident         `json:"incidents"`
	CandidateIncidents   []AlertObservedIncident         `json:"candidate_incidents,omitempty"`
	RecoveryConfirmation *AlertAnomalyRecoveryComparison `json:"recovery_confirmation,omitempty"`
	DeploymentContext    *AlertAnomalyDeploymentContext  `json:"deployment_context,omitempty"`
}

// AlertAnomalyDeploymentContext contains the latest recorded full-traffic
// promotions in the replay window and, when available, one before it.
type AlertAnomalyDeploymentContext struct {
	Scope      string                            `json:"scope"`
	Promotions []AlertAnomalyDeploymentPromotion `json:"promotions"`
	Truncated  bool                              `json:"truncated,omitempty"`
}

type AlertAnomalyDeploymentPromotion struct {
	DeploymentID string    `json:"deployment_id"`
	CommitSHA    string    `json:"commit_sha,omitempty"`
	PromotedAt   time.Time `json:"promoted_at"`
}

// AlertAnomalyRecoveryComparison replays the same checks with consecutive healthy checks required to recover.
type AlertAnomalyRecoveryComparison struct {
	RequiredHealthyChecks int                     `json:"required_healthy_checks"`
	Incidents             []AlertObservedIncident `json:"incidents"`
	CandidateIncidents    []AlertObservedIncident `json:"candidate_incidents,omitempty"`
}

// AlertAnomalyPreviewSkippedWindow identifies a window a preview baseline could not evaluate.
type AlertAnomalyPreviewSkippedWindow struct {
	EvaluatedAt      time.Time                    `json:"evaluated_at"`
	Reason           AlertAnomalyEvaluationReason `json:"reason"`
	EligibleRequests *int64                       `json:"eligible_requests,omitempty"`
	RequiredRequests int64                        `json:"required_requests,omitempty"`
}

// AlertAnomalyPreviewCandidate reports breaches for a second multiplier or baseline on the same windows.
type AlertAnomalyPreviewCandidate struct {
	Threshold            float64                           `json:"threshold"`
	Breaches             []AlertAnomalyPreviewBreach       `json:"breaches"`
	WeekdayBaseline      *AlertAnomalyWeekdayPreview       `json:"weekday_baseline,omitempty"`
	ZeroBaselineFallback *AlertAnomalyZeroBaselineFallback `json:"zero_baseline_fallback,omitempty"`
}

// AlertAnomalyZeroBaselineFallback identifies the absolute thresholds tested by the preview candidate.
type AlertAnomalyZeroBaselineFallback struct {
	MinimumErrorRatePct   float64 `json:"minimum_error_rate_pct"`
	MinimumFailedRequests int64   `json:"minimum_failed_requests"`
}

// AlertAnomalyWeekdayPreview reports coverage and daily-baseline fallback for the same-weekday candidate.
type AlertAnomalyWeekdayPreview struct {
	Evaluated      int                                `json:"evaluated"`
	SkippedWindows []AlertAnomalyPreviewSkippedWindow `json:"skipped_windows"`
	FallbackAt     []time.Time                        `json:"fallback_at"`
}

// AlertAnomalyPreviewBreach is a historical threshold breach for a single window.
type AlertAnomalyPreviewBreach struct {
	EvaluatedAt          time.Time `json:"evaluated_at"`
	Observed             float64   `json:"observed"`
	Baseline             float64   `json:"baseline"`
	TriggerValue         float64   `json:"trigger_value"`
	EligibleRequests     *int64    `json:"eligible_requests,omitempty"`
	FailedRequests       *int64    `json:"failed_requests,omitempty"`
	ZeroBaselineFallback bool      `json:"zero_baseline_fallback,omitempty"`
}
