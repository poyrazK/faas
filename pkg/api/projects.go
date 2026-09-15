package api

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
