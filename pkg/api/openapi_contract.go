package api

import "time"

// GitHubInstallMutationRequest is the CSRF envelope used by customer-facing
// GitHub connection mutations. The concrete handlers live in cmd/apid, while
// this wire DTO keeps the public OpenAPI schema tied to the Go API package.
type GitHubInstallMutationRequest struct {
	CSRFToken string `json:"csrf_token"`
}

// GitHubInstallStatus is the safe, account-scoped GitHub connection snapshot
// returned to the dashboard. It deliberately contains no installation
// credentials; the nested sync result is kept anonymous because it is an
// inline object in the public OpenAPI document.
type GitHubInstallStatus struct {
	State                        string     `json:"state"`
	Health                       string     `json:"health"`
	Connected                    bool       `json:"connected"`
	InstallationID               int64      `json:"installation_id,omitempty"`
	GitHubLogin                  string     `json:"github_login,omitempty"`
	DefaultBranch                string     `json:"default_branch,omitempty"`
	RepoFullName                 string     `json:"repo_full_name,omitempty"`
	ProductionBranch             string     `json:"production_branch,omitempty"`
	BindingID                    string     `json:"binding_id,omitempty"`
	LinkedAt                     *time.Time `json:"linked_at,omitempty"`
	LastReconciledAt             *time.Time `json:"last_reconciled_at,omitempty"`
	LastReconcileError           string     `json:"last_reconcile_error,omitempty"`
	LastReconcileRepositoryCount int        `json:"last_reconcile_repository_count"`
	LastReconcileDetachedCount   int        `json:"last_reconcile_detached_count"`
	CSRFToken                    string     `json:"csrf_token,omitempty"`
	SyncResult                   *struct {
		Detached              bool      `json:"detached"`
		RemoteRepositoryCount int       `json:"remote_repository_count"`
		SyncedAt              time.Time `json:"synced_at"`
	} `json:"sync_result,omitempty"`
}

// GitHubDeploymentPolicy is the customer-owned project-level policy applied
// by githubd to source staging and PR-preview leases.
type GitHubDeploymentPolicy struct {
	ProjectID       string   `json:"project_id"`
	RootDir         string   `json:"root_dir"`
	IgnoredPaths    []string `json:"ignored_paths"`
	PreviewEnabled  bool     `json:"preview_enabled"`
	PreviewTTLHours int      `json:"preview_ttl_hours"`
}

// GitHubDeploymentPolicyPatch is the partial update shape for the policy.
type GitHubDeploymentPolicyPatch struct {
	RootDir         *string   `json:"root_dir,omitempty"`
	IgnoredPaths    *[]string `json:"ignored_paths,omitempty"`
	PreviewEnabled  *bool     `json:"preview_enabled,omitempty"`
	PreviewTTLHours *int      `json:"preview_ttl_hours,omitempty"`
}

// OpenAPIContractDiffResponse is the read-only production contract check.
// It is intentionally separate from DiffResponse: deploydiff describes
// application configuration changes, while this response describes the
// customer-facing OpenAPI surface.
type OpenAPIContractDiffResponse struct {
	AppID                string                    `json:"app_id"`
	Scope                string                    `json:"scope"`
	Source               string                    `json:"source"`
	BaselineDeploymentID string                    `json:"baseline_deployment_id,omitempty"`
	BaselineSHA256       string                    `json:"baseline_sha256,omitempty"`
	ProposedSHA256       string                    `json:"proposed_sha256"`
	BaselineCapturedAt   *time.Time                `json:"baseline_captured_at,omitempty"`
	Blocking             bool                      `json:"blocking"`
	Breaks               []OpenAPIContractBreak    `json:"breaks"`
	Additions            []OpenAPIContractAddition `json:"additions"`
}

type OpenAPIContractBreak struct {
	Path         string      `json:"path"`
	Method       string      `json:"method"`
	Status       string      `json:"status,omitempty"`
	Kind         string      `json:"kind"`
	PathInSchema string      `json:"path_in_schema,omitempty"`
	Before       interface{} `json:"before,omitempty"`
	After        interface{} `json:"after,omitempty"`
}

type OpenAPIContractAddition struct {
	Kind         string `json:"kind"`
	Path         string `json:"path"`
	Method       string `json:"method,omitempty"`
	Status       string `json:"status,omitempty"`
	PathInSchema string `json:"path_in_schema,omitempty"`
	Field        string `json:"field"`
}
