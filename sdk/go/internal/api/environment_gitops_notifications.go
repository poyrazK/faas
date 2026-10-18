package api

import "time"

// EnvironmentGitOpsChangedWebhookPayload identifies a status transition without
// copying environment values, secret references, or source credentials.
type EnvironmentGitOpsChangedWebhookPayload struct {
	Version          int                       `json:"version"`
	TransitionID     string                    `json:"transition_id"`
	SourceID         string                    `json:"source_id"`
	Project          string                    `json:"project"`
	Environment      string                    `json:"environment"`
	Axis             string                    `json:"axis"`
	PreviousStatus   string                    `json:"previous_status"`
	Status           string                    `json:"status"`
	Generation       int64                     `json:"generation"`
	ObservedVersion  int64                     `json:"observed_version"`
	ApprovedRevision string                    `json:"approved_revision_id,omitempty"`
	CommitSHA        string                    `json:"commit_sha,omitempty"`
	RunID            string                    `json:"run_id,omitempty"`
	ErrorCode        string                    `json:"error_code,omitempty"`
	ObservedAt       time.Time                 `json:"observed_at"`
	DashboardPath    string                    `json:"dashboard_path"`
	Changes          []EnvironmentGitOpsChange `json:"changes"`
	BlockingReasons  []string                  `json:"blocking_reasons"`
	DetailsTruncated bool                      `json:"details_truncated"`
}
