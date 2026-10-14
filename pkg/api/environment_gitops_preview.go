package api

import "time"

// PreviewEnvironmentGitPlanResponse is a read-only candidate assessment. Its
// plan hash cannot authorize adoption, approval, or reconciliation. Values are
// omitted so the result can be retained in a PR/MR's CI artifacts.
type PreviewEnvironmentGitPlanResponse struct {
	SourceID       string                `json:"source_id"`
	Provider       string                `json:"provider"`
	Project        string                `json:"project"`
	Environment    string                `json:"environment"`
	ObservedAt     time.Time             `json:"observed_at"`
	ReadOnly       bool                  `json:"read_only"`
	ValuesRedacted bool                  `json:"values_redacted"`
	Plan           EnvironmentGitOpsPlan `json:"plan"`
}
