package api

import (
	"encoding/json"
	"time"
)

type CreateProjectEnvironmentApprovalRequest struct {
	PlanToken      string `json:"plan_token,omitempty"`
	PromotionToken string `json:"promotion_token,omitempty"`
}

type ProjectEnvironmentApprovalResponse struct {
	ApprovalID    string `json:"approval_id"`
	ApprovalToken string `json:"approval_token"`
	Environment   string `json:"environment"`
	TokenKind     string `json:"token_kind"`
	Status        string `json:"status"`
	ExpiresAt     string `json:"expires_at"`
}

type ProjectEnvironmentConfigChange struct {
	Key    string          `json:"key"`
	Kind   string          `json:"kind"`
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}

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

type ProjectEnvironmentPromotionChange struct {
	SourceWorkloadConfigHash   string `json:"source_workload_config_hash,omitempty"`
	TargetWorkloadConfigHash   string `json:"target_workload_config_hash,omitempty"`
	PromotedWorkloadConfigHash string `json:"promoted_workload_config_hash,omitempty"`
	WorkloadSlug               string `json:"workload_slug"`
	WorkloadName               string `json:"workload_name"`
	Kind                       string `json:"kind"`
	SourceDeploymentID         string `json:"source_deployment_id,omitempty"`
	TargetDeploymentID         string `json:"target_deployment_id,omitempty"`
	SourceBuildID              string `json:"source_build_id,omitempty"`
	TargetBuildID              string `json:"target_build_id,omitempty"`
	SourceRevision             string `json:"source_revision,omitempty"`
	TargetRevision             string `json:"target_revision,omitempty"`
	SourceRevisionKind         string `json:"source_revision_kind,omitempty"`
	TargetRevisionKind         string `json:"target_revision_kind,omitempty"`
}

type ProjectEnvironmentPromotionPreviewResponse struct {
	ProjectSlug            string                                   `json:"project_slug"`
	FromEnvironment        string                                   `json:"from_environment"`
	ToEnvironment          string                                   `json:"to_environment"`
	SyncConfig             bool                                     `json:"sync_config,omitempty"`
	ToEnvironmentProtected bool                                     `json:"to_environment_protected"`
	ApprovalRequired       bool                                     `json:"approval_required"`
	CanPromote             bool                                     `json:"can_promote"`
	BlockingReasons        []string                                 `json:"blocking_reasons,omitempty"`
	ConfigDiff             ProjectEnvironmentConfigDiffResponse     `json:"config_diff"`
	Changes                []ProjectEnvironmentPromotionChange      `json:"changes"`
	FromReleaseSet         *ProjectReleaseSetResponse               `json:"from_release_set,omitempty"`
	ToReleaseSet           *ProjectReleaseSetResponse               `json:"to_release_set,omitempty"`
	ReleaseGraphMode       bool                                     `json:"release_graph_mode"`
	ReleaseTTLSeconds      int                                      `json:"release_ttl_seconds,omitempty"`
	QualificationRequired  bool                                     `json:"qualification_required,omitempty"`
	Qualification          *ProjectEnvironmentQualificationResponse `json:"qualification,omitempty"`
	PromotionHash          string                                   `json:"promotion_hash"`
	PromotionToken         string                                   `json:"promotion_token"`
}

type ProjectEnvironmentPromotionReleaseGraphResponse struct {
	SourceReleaseSetID         string `json:"source_release_set_id,omitempty"`
	PreviousTargetReleaseSetID string `json:"previous_target_release_set_id,omitempty"`
	TargetReleaseSetID         string `json:"target_release_set_id,omitempty"`
	RestoredTargetReleaseSetID string `json:"restored_target_release_set_id,omitempty"`
	TTLSeconds                 int    `json:"ttl_seconds"`
}

type ProjectEnvironmentPromotionResponse struct {
	Status           string                                           `json:"status,omitempty"`
	BindingsRequired bool                                             `json:"bindings_required,omitempty"`
	BindingsCheck    *ProjectReleaseCheckResponse                     `json:"bindings_check,omitempty"`
	PromotionID      string                                           `json:"promotion_id"`
	ProjectSlug      string                                           `json:"project_slug"`
	FromEnvironment  string                                           `json:"from_environment"`
	ToEnvironment    string                                           `json:"to_environment"`
	SyncConfig       bool                                             `json:"sync_config,omitempty"`
	PromotionHash    string                                           `json:"promotion_hash"`
	ReleaseGraph     *ProjectEnvironmentPromotionReleaseGraphResponse `json:"release_graph,omitempty"`
	Workloads        []ProjectEnvironmentPromotionWorkloadResponse    `json:"workloads"`
}

type ProjectEnvironmentPromotionStatusResponse struct {
	BindingsRequired        bool                                                `json:"bindings_required,omitempty"`
	BindingsCheck           *ProjectReleaseCheckResponse                        `json:"bindings_check,omitempty"`
	PromotionID             string                                              `json:"promotion_id"`
	ProjectSlug             string                                              `json:"project_slug"`
	FromEnvironment         string                                              `json:"from_environment"`
	ToEnvironment           string                                              `json:"to_environment"`
	SyncConfig              bool                                                `json:"sync_config,omitempty"`
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
	ReleaseGraph            *ProjectEnvironmentPromotionReleaseGraphResponse    `json:"release_graph,omitempty"`
	Workloads               []ProjectEnvironmentPromotionStatusWorkloadResponse `json:"workloads"`
}

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

type ProjectEnvironmentPromotionWorkloadResponse struct {
	WorkloadSlug       string `json:"workload_slug"`
	WorkloadName       string `json:"workload_name"`
	Status             string `json:"status"`
	SourceDeploymentID string `json:"source_deployment_id,omitempty"`
	TargetDeploymentID string `json:"target_deployment_id,omitempty"`
}

type ProjectEnvironmentQualificationCheck struct {
	Name    string                                  `json:"name"`
	Status  string                                  `json:"status"`
	Results []ProjectEnvironmentQualificationResult `json:"results"`
}

type ProjectEnvironmentQualificationResponse struct {
	WorkloadConfigHashes map[string]string                      `json:"workload_config_hashes,omitempty"`
	ID                   string                                 `json:"id"`
	Environment          string                                 `json:"environment"`
	ReleaseSetID         string                                 `json:"release_set_id"`
	ConfigurationVersion int64                                  `json:"configuration_version"`
	ConfigurationHash    string                                 `json:"configuration_hash"`
	SecretRevisionHashes map[string]string                      `json:"secret_revision_hashes"`
	Status               string                                 `json:"status"`
	Checks               []ProjectEnvironmentQualificationCheck `json:"checks"`
	CreatedAt            time.Time                              `json:"created_at"`
	ExpiresAt            time.Time                              `json:"expires_at"`
}

type ProjectEnvironmentQualificationResult struct {
	WorkloadSlug string `json:"workload_slug"`
	DeploymentID string `json:"deployment_id"`
	Status       string `json:"status"`
	HTTPStatus   *int   `json:"http_status,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
}

type PromoteProjectEnvironmentRequest struct {
	FromEnvironment string `json:"from_environment"`
	PromotionToken  string `json:"promotion_token"`
	ApprovalToken   string `json:"approval_token,omitempty"`
	RequireBindings bool   `json:"require_bindings,omitempty"`
}
