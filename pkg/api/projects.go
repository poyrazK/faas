package api

import (
	"encoding/json"
	"time"
)

// ProjectSummaryResponse is the stable account-scoped project list shape.
type ProjectSummaryResponse struct {
	ID               string `json:"id"`
	Slug             string `json:"slug"`
	RepoFullName     string `json:"repo_full_name,omitempty"`
	ProductionBranch string `json:"production_branch,omitempty"`
	ScanSource       string `json:"scan_source"`
	WorkloadCount    int    `json:"workload_count"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

// ProjectWorkloadResponse describes one app still attached to a project.
type ProjectWorkloadResponse struct {
	Slug             string `json:"slug"`
	WorkloadName     string `json:"workload_name"`
	Status           string `json:"status"`
	DeploymentStatus string `json:"deployment_status,omitempty"`
	RolloutState     string `json:"rollout_state,omitempty"`
	BuildStatus      string `json:"build_status,omitempty"`
}

// ProjectResponse contains durable project metadata and recovery context.
type ProjectResponse struct {
	ProjectSummaryResponse
	Workloads                []ProjectWorkloadResponse `json:"workloads"`
	Exclusions               []string                  `json:"exclusions"`
	LastReconciliationStatus string                    `json:"last_reconciliation_status,omitempty"`
	LastBuildStatus          string                    `json:"last_build_status,omitempty"`
}

// UpdateProjectRequest changes the customer-managed source binding. Nil fields
// preserve their prior values; an explicitly empty repository unbinds it.
type UpdateProjectRequest struct {
	RepoFullName     *string `json:"repo_full_name,omitempty"`
	ProductionBranch *string `json:"production_branch,omitempty"`
}

// ProjectDeletePreviewResponse explains the state affected by deleting the
// project row. Member apps remain live and are detached from the project.
type ProjectDeletePreviewResponse struct {
	Project     ProjectSummaryResponse    `json:"project"`
	Workloads   []ProjectWorkloadResponse `json:"workloads"`
	DomainCount int                       `json:"domain_count"`
	EnvCount    int                       `json:"env_count"`
	CronCount   int                       `json:"cron_count"`
}

// ProjectEnvironmentResponse is one durable environment registry entry.
type ProjectEnvironmentResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Slug      string `json:"slug"`
	Protected bool   `json:"protected"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// CreateProjectEnvironmentRequest registers a named project environment.
type CreateProjectEnvironmentRequest struct {
	Slug      string `json:"slug"`
	Protected *bool  `json:"protected,omitempty"`
}

// UpdateProjectEnvironmentRequest changes only environment protection.
type UpdateProjectEnvironmentRequest struct {
	Protected *bool `json:"protected,omitempty"`
}

// CreateProjectEnvironmentApprovalRequest approves one exact plan for a
// protected project environment. Exactly one token field must be supplied:
// plan_token for a source upload apply, or promotion_token for an environment
// promotion.
type CreateProjectEnvironmentApprovalRequest struct {
	PlanToken      string `json:"plan_token,omitempty"`
	PromotionToken string `json:"promotion_token,omitempty"`
}

// ProjectEnvironmentApprovalResponse contains a short-lived credential that
// may be used only with the approved plan and environment.
type ProjectEnvironmentApprovalResponse struct {
	ApprovalID    string `json:"approval_id"`
	ApprovalToken string `json:"approval_token"`
	Environment   string `json:"environment"`
	TokenKind     string `json:"token_kind"`
	Status        string `json:"status"`
	ExpiresAt     string `json:"expires_at"`
}

// ProjectEnvironmentApprovalStatusResponse is the durable, non-secret view
// of one approval. The raw approval token is intentionally never returned by
// this endpoint.
type ProjectEnvironmentApprovalStatusResponse struct {
	ApprovalID  string `json:"approval_id"`
	Environment string `json:"environment"`
	TokenKind   string `json:"token_kind"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	ExpiresAt   string `json:"expires_at"`
	ConsumedAt  string `json:"consumed_at,omitempty"`
}

// DeriveProjectEnvironmentApprovalStatus computes the approval lifecycle
// state from its durable timestamps. Consumption takes precedence over expiry
// so operators can distinguish a used credential from an unused timeout.
func DeriveProjectEnvironmentApprovalStatus(consumedAt *time.Time, expiresAt, now time.Time) string {
	if consumedAt != nil {
		return "consumed"
	}
	if !expiresAt.IsZero() && now.After(expiresAt) {
		return "expired"
	}
	return "pending"
}

// UpdateProjectEnvironmentConfigRequest replaces the non-secret configuration
// for one project environment. Values must be a JSON object; the server
// canonicalizes it and returns the resulting version and hash.
type UpdateProjectEnvironmentConfigRequest struct {
	Values json.RawMessage `json:"values"`
}

// ProjectEnvironmentConfigResponse is the latest immutable configuration
// version for one project environment. Values never contain secret material.
type ProjectEnvironmentConfigResponse struct {
	ProjectSlug string          `json:"project_slug"`
	Environment string          `json:"environment"`
	Version     int64           `json:"version"`
	ConfigHash  string          `json:"config_hash"`
	Values      json.RawMessage `json:"values"`
	UpdatedAt   string          `json:"updated_at,omitempty"`
}

// ProjectEnvironmentConfigChange is one key-level difference between two
// environment configuration snapshots.
type ProjectEnvironmentConfigChange struct {
	Key    string          `json:"key"`
	Kind   string          `json:"kind"`
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}

// ProjectEnvironmentConfigDiffResponse compares the latest snapshots for two
// environments in the same project.
type ProjectEnvironmentConfigDiffResponse struct {
	ProjectSlug     string                           `json:"project_slug"`
	FromEnvironment string                           `json:"from_environment"`
	ToEnvironment   string                           `json:"to_environment"`
	FromVersion     int64                            `json:"from_version"`
	ToVersion       int64                            `json:"to_version"`
	FromHash        string                           `json:"from_hash"`
	ToHash          string                           `json:"to_hash"`
	Changes         []ProjectEnvironmentConfigChange `json:"changes"`
}

// ProjectEnvironmentPromotionChange describes one workload's source and
// target environment release identity. Values are deployment metadata only;
// the preview never exposes source bytes or secrets.
type ProjectEnvironmentPromotionChange struct {
	WorkloadSlug       string `json:"workload_slug"`
	WorkloadName       string `json:"workload_name"`
	Kind               string `json:"kind"`
	SourceDeploymentID string `json:"source_deployment_id,omitempty"`
	TargetDeploymentID string `json:"target_deployment_id,omitempty"`
	SourceBuildID      string `json:"source_build_id,omitempty"`
	TargetBuildID      string `json:"target_build_id,omitempty"`
	SourceRevision     string `json:"source_revision,omitempty"`
	TargetRevision     string `json:"target_revision,omitempty"`
	SourceRevisionKind string `json:"source_revision_kind,omitempty"`
	TargetRevisionKind string `json:"target_revision_kind,omitempty"`
}

// ProjectEnvironmentPromotionPreviewResponse is a read-only promotion plan
// between two registered environments in one project.
type ProjectEnvironmentPromotionPreviewResponse struct {
	ProjectSlug            string                               `json:"project_slug"`
	FromEnvironment        string                               `json:"from_environment"`
	ToEnvironment          string                               `json:"to_environment"`
	ToEnvironmentProtected bool                                 `json:"to_environment_protected"`
	ApprovalRequired       bool                                 `json:"approval_required"`
	CanPromote             bool                                 `json:"can_promote"`
	BlockingReasons        []string                             `json:"blocking_reasons,omitempty"`
	ConfigDiff             ProjectEnvironmentConfigDiffResponse `json:"config_diff"`
	Changes                []ProjectEnvironmentPromotionChange  `json:"changes"`
	PromotionHash          string                               `json:"promotion_hash"`
	PromotionToken         string                               `json:"promotion_token"`
}

// PromoteProjectEnvironmentRequest executes a previously previewed
// promotion. The promotion token is revalidated against current live
// deployments and environment configuration before any deployment changes.
type PromoteProjectEnvironmentRequest struct {
	FromEnvironment string `json:"from_environment"`
	PromotionToken  string `json:"promotion_token"`
	ApprovalToken   string `json:"approval_token,omitempty"`
}

// ProjectEnvironmentPromotionWorkloadResponse reports one workload's
// promotion result. Promoted deployments reuse the source rootfs artifact;
// environment configuration and secrets remain target-scoped.
type ProjectEnvironmentPromotionWorkloadResponse struct {
	WorkloadSlug       string `json:"workload_slug"`
	WorkloadName       string `json:"workload_name"`
	Status             string `json:"status"`
	SourceDeploymentID string `json:"source_deployment_id,omitempty"`
	TargetDeploymentID string `json:"target_deployment_id,omitempty"`
}

// ProjectEnvironmentPromotionResponse is returned after a guarded promotion
// has applied all changed workloads in the current preview.
type ProjectEnvironmentPromotionResponse struct {
	PromotionID     string                                        `json:"promotion_id"`
	ProjectSlug     string                                        `json:"project_slug"`
	FromEnvironment string                                        `json:"from_environment"`
	ToEnvironment   string                                        `json:"to_environment"`
	PromotionHash   string                                        `json:"promotion_hash"`
	Workloads       []ProjectEnvironmentPromotionWorkloadResponse `json:"workloads"`
}

// ProjectEnvironmentPromotionStatusWorkloadResponse is one durable
// promotion checkpoint, including any retryable failure detail.
type ProjectEnvironmentPromotionStatusWorkloadResponse struct {
	WorkloadSlug               string `json:"workload_slug"`
	WorkloadName               string `json:"workload_name"`
	Status                     string `json:"status"`
	SourceDeploymentID         string `json:"source_deployment_id,omitempty"`
	PreviousTargetDeploymentID string `json:"previous_target_deployment_id,omitempty"`
	TargetDeploymentID         string `json:"target_deployment_id,omitempty"`
	Error                      string `json:"error,omitempty"`
	RollbackStatus             string `json:"rollback_status,omitempty"`
	RestoredTargetDeploymentID string `json:"restored_target_deployment_id,omitempty"`
	RollbackError              string `json:"rollback_error,omitempty"`
	VerificationStatus         string `json:"verification_status,omitempty"`
	VerificationError          string `json:"verification_error,omitempty"`
}

// ProjectEnvironmentPromotionStatusResponse is the durable status view for
// one promotion operation.
type ProjectEnvironmentPromotionStatusResponse struct {
	PromotionID             string                                              `json:"promotion_id"`
	ProjectSlug             string                                              `json:"project_slug"`
	FromEnvironment         string                                              `json:"from_environment"`
	ToEnvironment           string                                              `json:"to_environment"`
	PromotionHash           string                                              `json:"promotion_hash"`
	Status                  string                                              `json:"status"`
	Error                   string                                              `json:"error,omitempty"`
	CreatedAt               string                                              `json:"created_at"`
	UpdatedAt               string                                              `json:"updated_at"`
	CompletedAt             string                                              `json:"completed_at,omitempty"`
	RollbackStatus          string                                              `json:"rollback_status,omitempty"`
	RollbackError           string                                              `json:"rollback_error,omitempty"`
	RollbackStartedAt       string                                              `json:"rollback_started_at,omitempty"`
	RollbackCompletedAt     string                                              `json:"rollback_completed_at,omitempty"`
	VerificationStatus      string                                              `json:"verification_status,omitempty"`
	VerificationError       string                                              `json:"verification_error,omitempty"`
	VerificationStartedAt   string                                              `json:"verification_started_at,omitempty"`
	VerificationCompletedAt string                                              `json:"verification_completed_at,omitempty"`
	Workloads               []ProjectEnvironmentPromotionStatusWorkloadResponse `json:"workloads"`
}
