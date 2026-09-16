package api

import (
	"strings"
	"time"
)

// OpenAPI contract policies control how a production deployment reacts to
// breaking changes in its customer-facing OpenAPI surface. Observe is the
// compatibility-safe default; warn records telemetry without rejecting a
// deployment; block rejects breaking promotions.
const (
	OpenAPIContractPolicyObserve = "observe"
	OpenAPIContractPolicyWarn    = "warn"
	OpenAPIContractPolicyBlock   = "block"
)

// NormalizeOpenAPIContractPolicy returns the canonical policy. Empty values
// intentionally resolve to observe so legacy rows and hand-built fixtures
// retain the pre-policy deployment behaviour.
func NormalizeOpenAPIContractPolicy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case OpenAPIContractPolicyWarn:
		return OpenAPIContractPolicyWarn
	case OpenAPIContractPolicyBlock:
		return OpenAPIContractPolicyBlock
	default:
		return OpenAPIContractPolicyObserve
	}
}

// IsValidOpenAPIContractPolicy reports whether raw is one of the explicit
// policy values. The empty string is accepted by callers as "observe" for
// backwards-compatible defaults, but is not an explicit wire value.
func IsValidOpenAPIContractPolicy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case OpenAPIContractPolicyObserve, OpenAPIContractPolicyWarn, OpenAPIContractPolicyBlock:
		return true
	default:
		return false
	}
}

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

// GitHubInstallActivity is the redacted activity projection returned with a
// GitHub connection status. It intentionally contains no payloads, queue
// identifiers, retry controls, or worker error text.
type GitHubInstallActivity struct {
	WebhookDeliveries []GitHubWebhookActivity `json:"webhook_deliveries"`
	CheckUpdates      []GitHubCheckActivity   `json:"check_updates"`
}

// GitHubWebhookActivity is a customer-safe projection of webhook processing.
type GitHubWebhookActivity struct {
	EventType   string     `json:"event_type"`
	Status      string     `json:"status"`
	CommitSHA   string     `json:"commit_sha,omitempty"`
	ReceivedAt  time.Time  `json:"received_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// GitHubCheckActivity is a customer-safe projection of Check Run syncing.
type GitHubCheckActivity struct {
	DeploymentID string     `json:"deployment_id"`
	Status       string     `json:"status"`
	CommitSHA    string     `json:"commit_sha,omitempty"`
	ProcessedAt  *time.Time `json:"processed_at,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
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
	Policy               string                    `json:"policy"`
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
