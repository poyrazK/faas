package api

import "encoding/json"

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
// protected project environment.
type CreateProjectEnvironmentApprovalRequest struct {
	PlanToken string `json:"plan_token"`
}

// ProjectEnvironmentApprovalResponse contains a short-lived credential that
// may be used only with the approved plan and environment.
type ProjectEnvironmentApprovalResponse struct {
	ApprovalToken string `json:"approval_token"`
	Environment   string `json:"environment"`
	ExpiresAt     string `json:"expires_at"`
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
