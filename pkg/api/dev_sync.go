package api

import "time"

// DevSyncPhase is the safe phase-level timing emitted by `gregale dev`.
// Reason is a bounded diagnostic label, never application output.
type DevSyncPhase struct {
	Phase      string `json:"phase"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// RecordDevSyncRequest stores one redacted edit-to-live receipt. The
// deployment ID makes retries idempotent without requiring the CLI to keep a
// second local state file.
type RecordDevSyncRequest struct {
	WorkspaceID  string         `json:"workspace_id"`
	DeploymentID string         `json:"deployment_id"`
	Status       string         `json:"status"`
	EditToLiveMS int64          `json:"edit_to_live_ms"`
	SLOTargetMS  int64          `json:"slo_target_ms"`
	WithinSLO    bool           `json:"within_slo"`
	Phases       []DevSyncPhase `json:"phases"`
}

// DevSyncHistoryItem is one safe edit-to-live receipt returned by the API.
type DevSyncHistoryItem struct {
	DeploymentID string         `json:"deployment_id"`
	Status       string         `json:"status"`
	EditToLiveMS int64          `json:"edit_to_live_ms"`
	SLOTargetMS  int64          `json:"slo_target_ms"`
	WithinSLO    bool           `json:"within_slo"`
	Phases       []DevSyncPhase `json:"phases"`
	CreatedAt    time.Time      `json:"created_at"`
}

// DevSyncHistorySummary makes the trend useful without forcing every
// consumer to calculate percentiles or identify the slowest phase itself.
type DevSyncHistorySummary struct {
	Count           int    `json:"count"`
	WithinSLOCount  int    `json:"within_slo_count"`
	P50EditToLiveMS int64  `json:"p50_edit_to_live_ms"`
	P95EditToLiveMS int64  `json:"p95_edit_to_live_ms"`
	SLOTargetMS     int64  `json:"slo_target_ms"`
	SlowestPhase    string `json:"slowest_phase,omitempty"`
	SlowestPhaseMS  int64  `json:"slowest_phase_ms,omitempty"`
	Guidance        string `json:"guidance,omitempty"`
}

// DevSyncHistoryResponse is the bounded history view for one project and
// workspace. Items are newest first.
type DevSyncHistoryResponse struct {
	Project     string                `json:"project"`
	WorkspaceID string                `json:"workspace_id,omitempty"`
	Items       []DevSyncHistoryItem  `json:"items"`
	Summary     DevSyncHistorySummary `json:"summary"`
}
