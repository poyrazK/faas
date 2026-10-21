package api

import "time"

// Recovery listings assess eligibility now; they grant no retention or execution authority.
type ListEnvironmentGitReleasesRequest struct {
	TargetCommitSHA string `json:"target_commit_sha"`
	Before          string `json:"before,omitempty"`
}

func (r ListEnvironmentGitReleasesRequest) Validate() error {
	id := r.Before
	if id == "" {
		id = "00000000-0000-0000-0000-000000000000"
	}
	return (PreviewEnvironmentGitRollbackRequest{ReleaseSetID: id, TargetCommitSHA: r.TargetCommitSHA}).Validate()
}

type EnvironmentGitRecoveryRelease struct {
	ReleaseSetID string    `json:"release_set_id"`
	Active       bool      `json:"active"`
	CreatedAt    time.Time `json:"created_at"`
	// ExpiresAt is the release routing compatibility deadline, not an artifact guarantee.
	ExpiresAt               *time.Time                        `json:"expires_at,omitempty"`
	RetentionStatus         string                            `json:"retention_status"`
	CommitSHA               string                            `json:"commit_sha"`
	ServedAt                *time.Time                        `json:"served_at,omitempty"`
	Eligibility             string                            `json:"eligibility"`
	BlockingReasons         []string                          `json:"blocking_reasons"`
	Artifacts               []EnvironmentGitPromotionArtifact `json:"artifacts"`
	ProtectionStatus        string                            `json:"protection_status"`
	ArtifactsProtectedUntil *time.Time                        `json:"artifacts_protected_until,omitempty"`
	RetainedBytes           int64                             `json:"retained_bytes"`
	PlanHash                string                            `json:"plan_hash,omitempty"`
}

type ListEnvironmentGitReleasesResponse struct {
	RecoveryRetention           *EnvironmentRecoveryRetention   `json:"recovery_retention,omitempty"`
	RetentionBytes              int64                           `json:"retention_bytes"`
	Project                     string                          `json:"project"`
	Environment                 string                          `json:"environment"`
	SourceID                    string                          `json:"source_id"`
	TargetCommitSHA             string                          `json:"target_commit_sha"`
	ObservedAt                  time.Time                       `json:"observed_at"`
	ReadOnly                    bool                            `json:"read_only"`
	ValuesRedacted              bool                            `json:"values_redacted"`
	RequiresTargetQualification bool                            `json:"requires_target_qualification"`
	Releases                    []EnvironmentGitRecoveryRelease `json:"releases"`
	NextBefore                  string                          `json:"next_before,omitempty"`
}
